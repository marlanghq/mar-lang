package typecheck

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
)

// `Repo.create : Entity a -> b -> Task a`. The second argument was a bare type
// variable, so the checker asserted NOTHING about it: `Repo.create items n`
// with `n : Int` type-checked, and the failure arrived when a request did.
//
// It is refused at compile time twice over now, and the two are worth keeping
// apart. The TYPE says the argument is a record -- an open row with no
// required fields, which Mar already had and which other builtins already use
// -- so a non-record is a unification error from the checker itself. The LINT
// then answers the narrower question the type cannot: whether those fields are
// columns of THIS entity.
//
// This test drives the checker, so it accepts either voice: what it refuses to
// accept is the call passing.
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
			"cannot unify { … } with Int",
		},
		{
			"update with a String patch",
			"patchOne : Task (Maybe Item)\npatchOne =\n    Repo.update items 1 \"hello\"\n",
			"the 3rd argument to `Repo.update` has the wrong type: expected { … }, got String",
		},
		{
			"findBy with a Bool filter",
			"findOne : Task (List Item)\nfindOne =\n    Repo.findBy items True\n",
			"the 2nd argument to `Repo.findBy` has the wrong type: expected { … }, got Bool",
		},
		{
			"a list, which groups values and cannot be one row",
			"listOne : Task Item\nlistOne =\n    Repo.create items [ 1, 2 ]\n",
			"the 2nd argument to `Repo.create` has the wrong type: expected { … }, got List",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			mod, err := parser.Parse(entity + c.decl)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			var said []string
			types, err := CheckModule(mod)
			if err != nil {
				said = append(said, err.Error())
			} else {
				for _, i := range RunShapeLint([]*ast.Module{mod}, types.ExprTypes) {
					said = append(said, i.Message)
				}
			}
			if len(said) == 0 {
				t.Fatalf("this checks clean, and fails when a request arrives:\n%s", c.decl)
			}
			joined := strings.Join(said, "\n")
			if !strings.Contains(joined, c.want) {
				t.Errorf("want a message containing %q\ngot:\n  %s", c.want, strings.Join(said, "\n  "))
			}
		})
	}
}

// The other half, and the one that decides whether any of this is usable: a
// real record still passes.
//
// The `viaArgument` case used to live here, as the thing the LINT cannot judge:
// an entity arriving as a parameter, whose columns the pass cannot resolve. It
// moved out because the TYPE now catches it -- `Repo.create ent n` with an Int
// is a unification error whether or not anyone knows which entity `ent` is.
// That is the better place for it, and it is why the type was worth
// strengthening rather than leaving the lint to carry the whole load.
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


-- The entity is not an Entity.define this pass can resolve, so the lint says
-- nothing about the columns. The record still has to be a record, and it is.
viaArgument : Entity Item -> String -> Task Item
viaArgument ent s =
    Repo.create ent { name = s }
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
