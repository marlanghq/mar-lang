package typecheck

import (
	"strings"
	"testing"

	"mar/internal/parser"
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

// A row field's type can have constructors its `Entity.enum` column does not
// accept. Measured on a running app, `status : Status` with three
// constructors over `Entity.enum [ Open, Closed ]`:
//
//	POST {"status":{"__ctor":"Open"}}      200, stored
//	POST {"status":{"__ctor":"Pending"}}   500 Internal Server Error
//
// `Pending` is a perfectly good `Status`, so the wire guard accepts it — the
// contract says `Status` — and the write fails on a CHECK the source never
// mentions. `mar check` passed.

const statusUnion = `
type Status
    = Open
    | Closed
    | Pending

`

func TestAConstructorTheColumnRefusesIsCaught(t *testing.T) {
	src := `module M exposing (..)
` + statusUnion + `
type alias Row =
    { id : Int
    , status : Status
    }


rows : Entity Row
rows =
    Entity.define
        { name = "rows"
        , columns =
            { id     = Entity.serial
            , status = Entity.enum [ Open, Closed ] Entity.notNull
            }
        , uniques = []
        }
`
	issues := lintWithTypes(t, src)
	if len(issues) == 0 {
		t.Fatal("the program can build `Pending` and the column refuses it")
	}
	for _, want := range []string{"status", "Pending", "Entity.enum"} {
		if !strings.Contains(issues[0].Message, want) {
			t.Errorf("the message must mention %q, got: %s", want, issues[0].Message)
		}
	}
}

func TestAMatchingEnumSetIsAccepted(t *testing.T) {
	src := `module M exposing (..)
` + statusUnion + `
type alias Row =
    { id : Int
    , status : Status
    }


rows : Entity Row
rows =
    Entity.define
        { name = "rows"
        , columns =
            { id     = Entity.serial
            , status = Entity.enum [ Open, Closed, Pending ] Entity.notNull
            }
        , uniques = []
        }
`
	if issues := lintWithTypes(t, src); len(issues) > 0 {
		t.Fatalf("every constructor is accepted: %s", issues[0].Message)
	}
}

// The other direction cannot be written. `Entity.enum`'s list is typed, so
// every name in it has to be a constructor of ONE union: an invented name is
// "unknown identifier", and mixing two unions is "cannot unify Status with
// Colour". So a column can only ever accept a SUBSET of the type's
// constructors, which is the direction that bites — the check needs no
// opinion about the other one.
func TestAColumnCannotAcceptMoreThanTheTypeHas(t *testing.T) {
	for _, extra := range []string{"Archived", "Blue"} {
		src := `module M exposing (..)

type Status
    = Open
    | Closed


type Colour
    = Blue


type alias Row =
    { id : Int
    , status : Status
    }


rows : Entity Row
rows =
    Entity.define
        { name = "rows"
        , columns =
            { id     = Entity.serial
            , status = Entity.enum [ Open, Closed, ` + extra + ` ] Entity.notNull
            }
        , uniques = []
        }
`
		resetVarIDsForTesting()
		mod, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if _, err := CheckModule(mod); err == nil {
			t.Fatalf("`%s` in the list should not check", extra)
		}
	}
}

// `Auth.config`'s signup hook returns the row for a new user, and a column it
// does not fill has nothing else to fill it. Measured on hello-auth with a
// required `name` added to the entity and not to the hook: `mar check` passed,
// the app booted clean, and the FIRST sign-in answered
// `500 {"error":"signup_failed"}` — the first user, with nothing naming the
// column.

const authEntity = `module M exposing (..)

type alias User =
    { id : Int
    , email : String
    , name : String
    }


users : Entity User
users =
    Entity.define
        { name = "users"
        , columns =
            { id    = Entity.serial
            , email = Entity.text Entity.notNull
            , name  = Entity.text Entity.notNull
            }
        , uniques = []
        }

`

func TestASignupHookThatSkipsAColumnIsCaught(t *testing.T) {
	issues := lintWithTypes(t, authEntity+`
auth =
    Auth.config
        { entity = users
        , identify = \u -> u.email
        , signup = \e -> { email = e }
        , email = { subject = "Sign in" }
        , sessionDuration = Time.days 30
        }
`)
	if len(issues) == 0 {
		t.Fatal("nothing can fill `name`, so the first sign-in fails")
	}
	for _, want := range []string{"name", "signup"} {
		if !strings.Contains(issues[0].Message, want) {
			t.Errorf("the message must mention %q, got: %s", want, issues[0].Message)
		}
	}
}

func TestASignupHookThatFillsEveryColumnIsAccepted(t *testing.T) {
	if issues := lintWithTypes(t, authEntity+`
auth =
    Auth.config
        { entity = users
        , identify = \u -> u.email
        , signup = \e -> { email = e, name = "new" }
        , email = { subject = "Sign in" }
        , sessionDuration = Time.days 30
        }
`); len(issues) > 0 {
		t.Fatalf("every column is given: %s", issues[0].Message)
	}
}

func TestTheSerialAndTimestampExemptionsHold(t *testing.T) {
	// The two the database and the framework fill: `id` is serial, and a
	// timestamp comes from fillTimestampsForSignup. These are the same
	// exemptions Repo.create makes.
	src := `module M exposing (..)

type alias User =
    { id : Int
    , email : String
    , joinedAt : Time
    }


users : Entity User
users =
    Entity.define
        { name = "users"
        , columns =
            { id       = Entity.serial
            , email    = Entity.text Entity.notNull
            , joinedAt = Entity.timestamp Entity.notNull
            }
        , uniques = []
        }


auth =
    Auth.config
        { entity = users
        , identify = \u -> u.email
        , signup = \e -> { email = e }
        , email = { subject = "Sign in" }
        , sessionDuration = Time.days 30
        }
`
	if issues := lintWithTypes(t, src); len(issues) > 0 {
		t.Fatalf("id and a timestamp are filled for you: %s", issues[0].Message)
	}
}

// Three things about a `uniques` list are facts about the literal it sits in,
// and all three were caught only at boot — the columns are right there in the
// same expression. Measured: `uniques = [ [ "emial" ] ]` checked clean and the
// app refused to start with
// `uniques[0] references column "emial" which is not declared on this entity`.

const uniqueEntity = `module M exposing (..)

type alias Row =
    { id : Int
    , email : String
    , org : String
    }


rows : Entity Row
rows =
    Entity.define
        { name = "rows"
        , columns =
            { id    = Entity.serial
            , email = Entity.text Entity.notNull
            , org   = Entity.text Entity.notNull
            }
        , uniques = `

func TestUniquesIsCheckedAgainstTheColumnsBesideIt(t *testing.T) {
	for _, tc := range []struct{ uniques, want string }{
		{`[ [ "emial" ] ]`, "not a column"},
		{`[ [] ]`, "is empty"},
		{`[ [ "org", "org" ] ]`, "twice"},
	} {
		issues := lintWithTypes(t, uniqueEntity+tc.uniques+"\n        }\n")
		if len(issues) == 0 {
			t.Errorf("%s: the boot refuses this; the check should too", tc.uniques)
			continue
		}
		if !strings.Contains(issues[0].Message, tc.want) {
			t.Errorf("%s: the message must say %q, got: %s", tc.uniques, tc.want, issues[0].Message)
		}
	}
}

func TestOrdinaryUniquesAreAccepted(t *testing.T) {
	for _, u := range []string{
		`[]`,
		`[ [ "email" ] ]`,
		`[ [ "email" ], [ "org" ] ]`,
		`[ [ "email", "org" ] ]`, // a composite index over two DIFFERENT columns
	} {
		if issues := lintWithTypes(t, uniqueEntity+u+"\n        }\n"); len(issues) > 0 {
			t.Errorf("%s is an ordinary index: %s", u, issues[0].Message)
		}
	}
}

// A column holds one scalar. The comparison between a row type and its
// columns only ever looked at scalar-versus-scalar, and skipped everything
// else with a rationale that does not hold: "the column might genuinely take a
// scalar that's typed as a record alias somewhere" — an alias to a scalar
// resolves to the scalar, so nothing legitimate landed in that branch.
//
// What landed there instead, all measured passing `mar check` before this:
//
//	art : { w : Int }   against an enum column and against a text column
//	art : List String   against a text column
//	art : (Int, Int)    against a text column
//	art : Art           against a TEXT column (a union outside Entity.enum)
//
// Each is a row type that lies about what the database hands back. The write
// path refuses a constructor where TEXT wants a string, and the read hands
// back the string while the row type promises the union — so a `case` over it
// reaches "no case branch matched" on a program the checker proved total.
func TestARowTypeCannotPromiseWhatAColumnCannotHold(t *testing.T) {
	entity := func(rowType, column string) string {
		return `module M exposing (..)

type Art
    = Small
    | Big


type alias Email = String


type alias Row =
    { id : Int
    , art : ` + rowType + `
    }


rows : Entity Row
rows =
    Entity.define
        { name = "rows"
        , columns =
            { id = Entity.serial
            , art = ` + column + `
            }
        , uniques = []
        }
`
	}

	const enumCol = `Entity.enum [ Small, Big ] Entity.notNull`
	const textCol = `Entity.text Entity.notNull`

	refused := []struct{ name, row, col, want string }{
		{"a record against an enum column", "{ w : Int }", enumCol, "is a record"},
		{"a record against a text column", "{ w : Int }", textCol, "is a record"},
		{"a List against a text column", "List String", textCol, "is a List"},
		{"a tuple against a text column", "(Int, Int)", textCol, "is a tuple"},
		{"a union against a TEXT column", "Art", textCol, "is M.Art"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			issues := lintWithTypes(t, entity(tc.row, tc.col))
			if len(issues) == 0 {
				t.Fatal("this passed; every read would come back as something the row type does not describe")
			}
			if !strings.Contains(issues[0].Message, tc.want) {
				t.Fatalf("reported the wrong thing: %s", issues[0].Message)
			}
		})
	}

	// And what a column CAN hold has to keep passing, including the alias
	// that the old rationale was written for.
	accepted := []struct{ name, row, col string }{
		{"a union against its enum column", "Art", enumCol},
		{"an alias to String against text", "Email", textCol},
		{"String against text", "String", textCol},
		{"Int against int", "Int", "Entity.int Entity.notNull"},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			if issues := lintWithTypes(t, entity(tc.row, tc.col)); len(issues) > 0 {
				t.Fatalf("this is an ordinary entity: %s", issues[0].Message)
			}
		})
	}
}
