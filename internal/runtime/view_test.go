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
go = Task.forEach (\_ -> Task.succeed ()) xs
`
	got := runModule(t, src, "go")
	// go is an Effect, just confirm the value exists.
	if !strings.Contains(got, "effect") {
		t.Fatalf("got %s", got)
	}
}

// `width fill` is the explicit "claim the free space" sizing attr
// that replaced the expand wrapper — `text [width fill]` is the
// equal-columns idiom. Pins (a) text's leading attrs list and (b)
// that the shape typechecks + evaluates against the real BaseEnv,
// so the web/iOS renderers (flex classes / .frame(maxWidth:
// .infinity)) keep receiving what they dispatch on.
func TestUITextWidthFillAttr(t *testing.T) {
	src := `module M exposing (..)
v = UI.text [ UI.width UI.fill ] "hi"
`
	if got := runModule(t, src, "v"); got != "<view:text>" {
		t.Fatalf("UI.text with attrs should produce <view:text>, got %s", got)
	}
}

// UI.fill is the axis-polymorphic Size value — same __unit-tagged
// record shape as chars/lines so every renderer dispatches on one
// field. Pins the tag.
func TestUIFillValue(t *testing.T) {
	src := `module M exposing (..)
v = UI.fill
`
	got := runModule(t, src, "v")
	if !strings.Contains(got, "fill") {
		t.Fatalf("UI.fill should carry the fill unit tag, got %s", got)
	}
}

// `align` is the cross-axis position attr for stacks (vstack:
// leading/center/trailing). Pins that an aligned stack typechecks
// against the real BaseEnv and still produces the stack tag.
func TestUIAlignOnStack(t *testing.T) {
	src := `module M exposing (..)
v = UI.vstack [ UI.align UI.trailing ] [ UI.text [] "x" ]
`
	if got := runModule(t, src, "v"); got != "<view:vstack>" {
		t.Fatalf("aligned vstack should produce <view:vstack>, got %s", got)
	}
}
