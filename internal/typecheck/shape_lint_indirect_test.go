package typecheck

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
)

// The shape lint read the record as SYNTAX, so writing it at the call site was
// checked and binding it to a name one line above was not. Measured on the
// same mistake:
//
//	Repo.findBy users { emial = "ada" }   Shape error, naming the column list
//	payload = { emial = "ada" }
//	Repo.findBy users payload             clean, and 500 on every request

// lintWithTypes runs the lint the way the loaders do: check first, then hand
// the lint the expression types the check produced.
func lintWithTypes(t *testing.T, src string) []ShapeIssue {
	t.Helper()
	resetVarIDsForTesting()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	res, err := CheckModule(mod)
	if err != nil {
		t.Fatalf("type error: %v", err)
	}
	types := map[ast.Expr]Type{}
	for e, ty := range res.ExprTypes {
		types[e] = ty
	}
	return RunShapeLint([]*ast.Module{mod}, types)
}

const usersEntity = `module M exposing (..)

type alias User =
    { id : Int
    , name : String
    , age : Int
    }


users : Entity User
users =
    Entity.define
        { name = "users"
        , columns =
            { id   = Entity.serial
            , name = Entity.text Entity.notNull
            , age  = Entity.int Entity.notNull
            }
        , uniques = []
        }

`

func onlyIssue(t *testing.T, issues []ShapeIssue, want ...string) {
	t.Helper()
	if len(issues) == 0 {
		t.Fatal("the record reaches the database without matching any column of it")
	}
	msg := issues[0].Message
	for _, w := range want {
		if !strings.Contains(msg, w) {
			t.Errorf("the message must mention %q, got: %s", w, msg)
		}
	}
}

func TestABoundRecordWithAnUnknownColumnIsCaught(t *testing.T) {
	issues := lintWithTypes(t, usersEntity+`
payload =
    { emial = "ada" }


found =
    Repo.findBy users payload
`)
	onlyIssue(t, issues, "emial", "not a column", "id, name, age")
}

func TestABoundRecordWithTheWrongColumnTypeIsCaught(t *testing.T) {
	issues := lintWithTypes(t, usersEntity+`
payload : { age : String }
payload =
    { age = "old" }


found =
    Repo.findBy users payload
`)
	onlyIssue(t, issues, "age", "expects Int")
}

func TestABoundRecordMissingAColumnIsCaughtOnCreate(t *testing.T) {
	// Only `Repo.create` requires every column; `id` is serial and exempt.
	issues := lintWithTypes(t, usersEntity+`
payload =
    { name = "ada" }


made =
    Repo.create users payload
`)
	onlyIssue(t, issues, "missing column", "age")
}

func TestAPartialRecordIsFineForFindBy(t *testing.T) {
	// `findBy` takes a subset by design, so a missing column is not an issue.
	if issues := lintWithTypes(t, usersEntity+`
payload =
    { name = "ada" }


found =
    Repo.findBy users payload
`); len(issues) > 0 {
		t.Fatalf("findBy matches on a subset: %s", issues[0].Message)
	}
}

func TestAnOpenRecordSaysNothingAndIsPassedOver(t *testing.T) {
	// `{ r | name : String }` constrains the field it names and says nothing
	// about the rest, so a missing column there is not a missing column.
	if issues := lintWithTypes(t, usersEntity+`
pick : { r | name : String } -> String
pick r =
    r.name


made rec =
    ( pick rec, Repo.create users rec )
`); len(issues) > 0 {
		t.Fatalf("an open record is not something to refuse on: %s", issues[0].Message)
	}
}

func TestTheRowTypeItselfIsAccepted(t *testing.T) {
	// The ordinary shape: a value of the entity's own row type.
	if issues := lintWithTypes(t, usersEntity+`
payload : User
payload =
    { id = 0, name = "ada", age = 36 }


made =
    Repo.create users payload
`); len(issues) > 0 {
		t.Fatalf("this is the row type the entity was defined with: %s", issues[0].Message)
	}
}
