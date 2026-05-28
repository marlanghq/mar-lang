package runtime

import (
	"strings"
	"testing"
)

func TestEntityDefine(t *testing.T) {
	src := `module M exposing (..)
ent =
    Entity.define
        { name = "users"
        , columns =
            { id    = Entity.serial
            , email = Entity.text Entity.notNull
            }
        , uniques = []
        }
`
	got := runModule(t, src, "ent")
	if !strings.Contains(got, "users") {
		t.Fatalf("expected entity:users in display, got %s", got)
	}
}

func TestEffectForEach(t *testing.T) {
	src := `module M exposing (..)
xs = [1, 2, 3]
go = Effect.forEach (\_ -> Effect.succeed ()) xs
`
	got := runModule(t, src, "go")
	// go is an Effect, just confirm the value exists.
	if !strings.Contains(got, "effect") {
		t.Fatalf("got %s", got)
	}
}
