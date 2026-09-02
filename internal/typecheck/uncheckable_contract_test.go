package typecheck

import (
	"strings"
	"testing"

	"mar/internal/parser"
)

// A contract exists to make two sides of a wire agree, and the guard is what
// enforces it. A side the guard cannot check is not a contract — it accepts
// any shape at all and hands it to the handler.
//
// A function was already refused here. What was not: `View`, `Shape`, `Sound`,
// `Attr`, `Page` — values a runtime builds for itself, holding closures, with
// no JSON form. `Service () Shape` type-checked. And the three runtimes do not
// even agree on the tag for a canvas text shape (`text` on the server and the
// web, `canvasText` on the phone), so what crossed would have drawn nothing on
// one of them.
//
// The rule is not a list of type names, which would go stale the day someone
// adds another opaque type. It asks the wire reduction the question the guard
// will ask at run time — "can this be checked?" — and refuses the side that
// answers no.
func TestAContractCannotCarryWhatTheGuardCannotCheck(t *testing.T) {
	refused := []struct{ name, src string }{
		{"a Shape response", `module M exposing (..)


draw : Service () Shape
draw = Service.declare GET "/api/draw"
`},
		{"a View response", `module M exposing (..)


v : Service () (View Int)
v = Service.declare GET "/api/v"
`},
		{"a Sound response", `module M exposing (..)


s : Service () Sound
s = Service.declare GET "/api/s"
`},
		{"a Shape in a record field", `module M exposing (..)


type alias Box = { name : String, art : Shape }


b : Service () Box
b = Service.declare GET "/api/b"
`},
		{"a Shape inside a List", `module M exposing (..)


l : Service () (List Shape)
l = Service.declare GET "/api/l"
`},
		{"a Shape on the REQUEST side", `module M exposing (..)


r : Service Shape Int
r = Service.declare POST "/api/r"
`},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			mod, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			_, err = CheckModule(mod)
			if err == nil {
				t.Fatal("this type-checked; the request guard would accept any body at all here")
			}
			if !strings.Contains(err.Error(), "no wire form") {
				t.Fatalf("refused for the wrong reason: %v", err)
			}
		})
	}

	// The rule has to leave ordinary contracts alone, including the shapes
	// that took work to make checkable: parameterised unions and Maybe.
	accepted := []struct{ name, src string }{
		{"records and lists", `module M exposing (..)


type alias Note = { id : Int, body : String }


n : Service () (List Note)
n = Service.declare GET "/api/n"
`},
		{"Maybe and Result", `module M exposing (..)


m : Service Int (Maybe (Result String Int))
m = Service.declare POST "/api/m"
`},
		{"a union the program declares", `module M exposing (..)


type Status
    = Draft
    | Sent Int


s : Service () (List Status)
s = Service.declare GET "/api/s"
`},
	}

	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			mod, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := CheckModule(mod); err != nil {
				t.Fatalf("this is an ordinary contract: %v", err)
			}
		})
	}
}
