package typecheck

import (
	"strings"
	"testing"
)

// Four Repo operations address a row by its primary key, and the runtime
// refuses each by name when the entity has none. Measured: an app with a
// serial-less entity boots clean, creates the table, and answers 500 to every
// Repo.create — which is nearly every app — with
//
//	Repo.create: entity rows has no Entity.serial primary key
//
// reaching only the operator's log, once per request.

const keylessEntity = `module M exposing (..)

type alias Row =
    { code : String
    , note : String
    }


rows : Entity Row
rows =
    Entity.define
        { name = "rows"
        , columns =
            { code = Entity.text Entity.notNull
            , note = Entity.text Entity.notNull
            }
        , uniques = []
        }

`

func TestEveryKeyedOperationOnAKeylessEntityIsRefused(t *testing.T) {
	for _, call := range []string{
		`made = Repo.create rows { code = "a", note = "n" }`,
		`found = Repo.findById rows 1`,
		`gone = Repo.deleteById rows 1`,
	} {
		issues := lintWithTypes(t, keylessEntity+call+"\n")
		if len(issues) == 0 {
			t.Errorf("%s can only ever fail", call)
			continue
		}
		for _, want := range []string{"Entity.serial", "rows"} {
			if !strings.Contains(issues[0].Message, want) {
				t.Errorf("%s: the message must mention %q, got: %s", call, want, issues[0].Message)
			}
		}
	}
}

func TestReadsThatNeedNoKeyAreLeftAlone(t *testing.T) {
	// An app that only reads a keyless entity works today, and must keep
	// working: the entity is not the problem, the call is.
	for _, call := range []string{
		`all = Repo.all rows`,
		`some = Repo.findBy rows { code = "a" }`,
	} {
		if issues := lintWithTypes(t, keylessEntity+call+"\n"); len(issues) > 0 {
			t.Errorf("%s needs no key: %s", call, issues[0].Message)
		}
	}
}

func TestAnEntityWithAKeyIsUntouched(t *testing.T) {
	src := `module M exposing (..)

type alias Row =
    { id : Int
    , code : String
    }


rows : Entity Row
rows =
    Entity.define
        { name = "rows"
        , columns =
            { id   = Entity.serial
            , code = Entity.text Entity.notNull
            }
        , uniques = []
        }


made = Repo.create rows { code = "a" }
found = Repo.findById rows 1
`
	if issues := lintWithTypes(t, src); len(issues) > 0 {
		t.Fatalf("this is the shape every example ships: %s", issues[0].Message)
	}
}
