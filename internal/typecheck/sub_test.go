package typecheck

import "testing"

// A page may declare `subscriptions : Model -> Sub Msg`. Time.every yields a
// Sub; Sub.batch / Sub.none compose them. This is the v1 subscription surface.
func TestSubscriptionsFieldChecks(t *testing.T) {
	src := `module M exposing (..)
type Msg = Tick Time
page =
    Page.create
        { path = "/"
        , init = (0, Cmd.none)
        , update = \_ m -> (m, Cmd.none)
        , view = \_ -> UI.text [] "x"
        , subscriptions = \_ -> Sub.batch [ Time.every (Time.seconds 1) Tick, Sub.none ]
        }
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("a page with a subscriptions field should typecheck; got: %v", err)
	}
}

// Sub and Cmd are distinct types: returning a Sub where `update` expects a Cmd
// is a compile error. This is the footgun the Effect → Task/Cmd/Sub split kills
// — a subscription can never be silently handed to the command runner (which
// would no-op it). The type system forces `Cmd.perform` / the subscriptions
// field, not an accidental return.
func TestSubIsNotCmd(t *testing.T) {
	src := `module M exposing (..)
type Msg = Tick Time
page =
    Page.create
        { path = "/"
        , init = (0, Cmd.none)
        , update = \_ m -> (m, Time.every (Time.seconds 1) Tick)
        , view = \_ -> UI.text [] "x"
        , subscriptions = \_ -> Sub.none
        }
`
	if _, err := checkSource(t, src); err == nil {
		t.Fatal("expected a type error: a Sub (Time.every) cannot be returned where update expects a Cmd")
	}
}
