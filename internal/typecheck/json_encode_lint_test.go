package typecheck

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
)

// `JSON.encode : a -> String` accepts anything, and the encoder meets a Page or
// an Entity at RUNTIME. The runtime's own comment said moving it earlier "would
// need the API to stop being polymorphic, which is a language decision rather
// than a fix", and I repeated that to Marcio as the one thing left that was his
// call.
//
// It is only true of changing the TYPE. A call site whose argument is
// definitely one of the values a runtime builds for itself is a mistake nobody
// can talk out of, and refusing it needs no change to the signature -- the same
// shape as the Repo lint, which refuses a non-record while Repo's parameter
// stays open.
//
// The seventh time this session that "this needs a decision" was an assumption
// rather than a property of the code.
func TestEncodingAValueWithNoJSONFormIsRefusedAtCompileTime(t *testing.T) {
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
			"an Entity, which is the app's own table description",
			"dump : String\ndump =\n    JSON.encode items\n",
			"an Entity has no JSON form",
		},
		{
			"a function, which has no wire form on any runtime",
			"dumpFn : String\ndumpFn =\n    JSON.encode (\\x -> x + 1)\n",
			"a function has no JSON form",
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
				t.Fatalf("this checks clean and fails when the code runs:\n%s", c.decl)
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

// And the half that decides whether the lint is usable. A record encodes, a
// list of them encodes, and a type variable is not a definite answer -- the
// same discipline as the rest of this pass, because a false positive here
// refuses a program that works.
func TestTheEncodeLintStaysSilentOnAnythingThatEncodes(t *testing.T) {
	const src = `module Main exposing (..)


type alias Item =
    { id : Int, name : String }


one : Item -> String
one it =
    JSON.encode it


many : List Item -> String
many xs =
    JSON.encode xs


plain : String
plain =
    JSON.encode { a = 1, b = "two", c = True }


-- Nothing pins this, so nothing is said about it.
whatever : a -> String
whatever x =
    JSON.encode x
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
		t.Fatalf("a false positive refuses a program that works: %s", issues[0].Message)
	}
}
