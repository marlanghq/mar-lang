package parser

import (
	"strings"
	"testing"
)

// The grammar reads a full pattern in a parameter position, and nothing
// downstream binds one: every evaluator accepts a name or `_` and refuses the
// rest. That left the language answering the same mistake two different ways,
// depending on whether the body happened to READ the names.
//
//	\(a, b) -> a + b     the typechecker says "unknown identifier: a"
//	                     — an error about the body, for a problem in the head
//	\(a, b) -> 42        `mar check` passes, and the program dies at LOAD with
//	                     "lambda params must be names or _ (got *ast.PTuple)"
//
// The second is the one that matters: a checked program failing at run time,
// with a message quoting a Go type name at someone who wrote Mar. Both are now
// the same parse error, which is where a purely syntactic rule belongs.
func TestParameterMustBeANameOrWildcard(t *testing.T) {
	refused := []struct{ name, src string }{
		{"lambda tuple, names read", `module M exposing (..)

out : Int
out =
    (\(a, b) -> a + b) (1, 2)
`},
		{"lambda tuple, names unread", `module M exposing (..)

out : Int
out =
    (\(a, b) -> 42) (1, 2)
`},
		{"lambda record", `module M exposing (..)

out : Int
out =
    (\{ n } -> 42) { n = 1 }
`},
		{"lambda constructor", `module M exposing (..)

type Box = Box Int


out : Int
out =
    (\(Box n) -> 42) (Box 3)
`},
		{"declaration tuple", `module M exposing (..)

pair : (Int, Int) -> Int
pair (a, b) =
    42
`},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.src)
			if err == nil {
				t.Fatal("this parsed; nothing downstream can bind these names, so it would have failed later")
			}
			if !strings.Contains(err.Error(), "a parameter has to be a name or `_`") {
				t.Fatalf("refused for the wrong reason: %v", err)
			}
		})
	}

	// What a parameter IS allowed to be has to keep parsing, or the rule is a
	// grammar change rather than a guard: every program in the repo takes its
	// arguments this way.
	accepted := []struct{ name, src string }{
		{"names", `module M exposing (..)

add : Int -> Int -> Int
add a b =
    a + b
`},
		{"wildcards", `module M exposing (..)

const : Int -> Int -> Int
const a _ =
    a
`},
		{"lambda names and wildcards", `module M exposing (..)

out : Int
out =
    (\x _ -> x) 1 2
`},
		{"patterns still work where they bind", `module M exposing (..)

first : (Int, Int) -> Int
first pair =
    case pair of
        (a, _) ->
            a
`},
	}

	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.src); err != nil {
				t.Fatalf("this is how arguments are taken in every Mar program: %v", err)
			}
		})
	}
}
