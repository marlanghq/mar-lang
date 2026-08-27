package typecheck

import (
	"strings"
	"testing"
)

// `users : Entity User` names two things that were never compared: the row
// type, which every read is typed as, and the columns, which decide what a
// read produces.
//
// The sharpest disagreement breaks the totality guarantee. Measured end to
// end on a running server:
//
//	tag : Maybe String over a NOT NULL TEXT column   mar check passes
//	Repo.create ... { tag = Just "verde" }           the Just is unwrapped
//	Repo.all                                         hands back a bare String
//	case tag of Just t -> ... ; Nothing -> ...       no case branch matched
//
// on a program whose `case` the checker had PROVED total (ADR 0022).

func entitySrc(rowFields, columns string) string {
	return `module M exposing (..)

type alias Row =
    { ` + rowFields + `
    }


rows : Entity Row
rows =
    Entity.define
        { name = "rows"
        , columns =
            { ` + columns + `
            }
        , uniques = []
        }
`
}

func TestAnOptionalRowFieldOverAFilledColumnIsRefused(t *testing.T) {
	issues := lintWithTypes(t, entitySrc(
		`id : Int
    , tag : Maybe String`,
		`id  = Entity.serial
            , tag = Entity.text Entity.notNull`))
	if len(issues) == 0 {
		t.Fatal("a `case` over this reaches a branch that cannot match")
	}
	for _, want := range []string{"tag", "optional", "no case branch matched", "Declare it as `String`"} {
		if !strings.Contains(issues[0].Message, want) {
			t.Errorf("the message must mention %q, got: %s", want, issues[0].Message)
		}
	}
}

func TestARowFieldWithNoColumnIsRefused(t *testing.T) {
	issues := lintWithTypes(t, entitySrc(
		`id : Int
    , missing : String`,
		`id = Entity.serial`))
	if len(issues) == 0 {
		t.Fatal("that field is absent from every record the database hands back")
	}
	for _, want := range []string{"missing", "no such column"} {
		if !strings.Contains(issues[0].Message, want) {
			t.Errorf("the message must mention %q, got: %s", want, issues[0].Message)
		}
	}
}

func TestARowFieldOfTheWrongTypeIsRefused(t *testing.T) {
	issues := lintWithTypes(t, entitySrc(
		`id : Int
    , age : String`,
		`id  = Entity.serial
            , age = Entity.int Entity.notNull`))
	if len(issues) == 0 {
		t.Fatal("every row comes back as something its own type does not describe")
	}
	for _, want := range []string{"age", "String", "Int"} {
		if !strings.Contains(issues[0].Message, want) {
			t.Errorf("the message must mention %q, got: %s", want, issues[0].Message)
		}
	}
}

func TestAColumnWithNoRowFieldIsLeftAlone(t *testing.T) {
	// Measured: it reads back fine. The record carries a field the type does
	// not mention, and naming it would not type-check, so no program can
	// observe it. Where it bites is `Repo.create`, and the call site already
	// says so.
	if issues := lintWithTypes(t, entitySrc(
		`id : Int`,
		`id    = Entity.serial
            , extra = Entity.int Entity.notNull`)); len(issues) > 0 {
		t.Fatalf("this is not something to refuse: %s", issues[0].Message)
	}
}

func TestAMatchingRowTypeIsAccepted(t *testing.T) {
	// Every column kind lined up with the field that reads it back.
	if issues := lintWithTypes(t, entitySrc(
		`id : Int
    , name : String
    , done : Bool
    , at : Time
    , cost : Decimal`,
		`id   = Entity.serial
            , name = Entity.text Entity.notNull
            , done = Entity.bool Entity.notNull
            , at   = Entity.timestamp Entity.notNull
            , cost = Entity.decimal 2 Entity.notNull`)); len(issues) > 0 {
		t.Fatalf("this is the shape every example ships: %s", issues[0].Message)
	}
}
