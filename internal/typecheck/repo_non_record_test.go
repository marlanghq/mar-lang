package typecheck

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
)

// `Repo.create : Entity a -> b -> Task a`. The second argument is a bare type
// variable, so the checker asserts NOTHING about it: `Repo.create items n`
// with `n : Int` type-checks, and the failure arrives when a request does.
//
// The runtime's own guard says the right sentence — "expected input record" —
// at the wrong time, and with a Go type name in it. This is the compile-time
// half, and it is a compile error rather than a boot one because the call can
// sit inside a function body that boot never evaluates. Measured before the
// fix: with the call at the top level `mar check` refused it (boot evaluates
// top-level values); moved one line into a function, `mar check` said OK.
//
// The lint stays silent whenever the type is not a definite answer, which is
// the same discipline as the rest of the pass: a type variable might still
// become a record, and `Int` cannot.
func TestARepoArgumentThatCannotBeARecordIsRefusedAtCompileTime(t *testing.T) {
	const entity = `module Main exposing (..)


type alias Item =
    { id : Int, name : String }


items : Entity Item
items =
    Entity.define
        { name = "items"
        , columns =
            { id = Entity.serial
            , name = Entity.text Entity.notNull
            }
        , uniques = []
        }


`
	for _, c := range []struct{ name, decl, want string }{
		{
			"create with an Int where the row belongs",
			"addOne : Int -> Task Item\naddOne n =\n    Repo.create items n\n",
			`Repo.create: expected a record of the columns of "items", got Int`,
		},
		{
			"update with a String patch",
			"patchOne : Task (Maybe Item)\npatchOne =\n    Repo.update items 1 \"hello\"\n",
			`Repo.update: expected a record of the columns of "items", got String`,
		},
		{
			"findBy with a Bool filter",
			"findOne : Task (List Item)\nfindOne =\n    Repo.findBy items True\n",
			`Repo.findBy: expected a record of the columns of "items", got Bool`,
		},
		{
			"a list, which groups values and cannot be one row",
			"listOne : Task Item\nlistOne =\n    Repo.create items [ 1, 2 ]\n",
			`Repo.create: expected a record of the columns of "items", got List`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			mod, err := parser.Parse(entity + c.decl)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			types, err := CheckModule(mod)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			issues := RunShapeLint([]*ast.Module{mod}, types.ExprTypes)
			if len(issues) == 0 {
				t.Fatalf("this checks clean, and fails when a request arrives:\n%s", c.decl)
			}
			var got []string
			for _, i := range issues {
				got = append(got, i.Message)
			}
			if !strings.Contains(strings.Join(got, "\n"), c.want) {
				t.Errorf("want a message containing %q\ngot:\n  %s", c.want, strings.Join(got, "\n  "))
			}
		})
	}
}

// The other half, and the one that decides whether the lint is usable: a real
// record still passes, and so does anything the pass cannot answer for.
func TestTheRepoLintStaysSilentWhenItCannotBeSure(t *testing.T) {
	const src = `module Main exposing (..)


type alias Item =
    { id : Int, name : String }


items : Entity Item
items =
    Entity.define
        { name = "items"
        , columns =
            { id = Entity.serial
            , name = Entity.text Entity.notNull
            }
        , uniques = []
        }


okLiteral : String -> Task Item
okLiteral s =
    Repo.create items { name = s }


okBound : String -> Task Item
okBound s =
    let
        row =
            { name = s }
    in
    Repo.create items row


-- The entity is not an Entity.define this pass can resolve, so nothing is
-- known about the columns and nothing is said.
viaArgument : Entity Item -> Int -> Task Item
viaArgument ent n =
    Repo.create ent n
`
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	types, err := CheckModule(mod)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if issues := RunShapeLint([]*ast.Module{mod}, types.ExprTypes); len(issues) > 0 {
		t.Fatalf("a false positive is worse than the hole it closes: %s", issues[0].Message)
	}
}
