package runtime

import (
	"testing"

	"mar/internal/parser"
	"mar/internal/typecheck"
)

// Top-level values evaluate as the module loads, in declaration order, and
// every name is pre-bound to a placeholder so mutual recursion works. That
// makes the ORDER load-bearing, and `orderValueDecls` is what stops source
// order from deciding whether a program is right.
//
// Ordering the values by their own dependencies is not enough. A function
// declaration only builds a closure, but CALLING one runs its body, and the
// body reads its free names at that moment. So a value that calls a function
// declared above it still depends on everything that function reaches.
//
// Before the ordering followed those edges, each module below type-checked,
// loaded without error, and left `out` holding `()` while its own type said
// String or Int. Nothing reported it: not the checker, not the loader, not
// the first use. The assertion here is the VALUE rather than the order,
// because the order test that already covered "a value calls a function
// declared below" used a function whose body reads nothing from the module —
// it passed the whole time this was broken.
func TestValueGetsWhatAFunctionItCallsReads(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want Value
	}{
		{
			// out -> render -> suffix, with `suffix` last in the file.
			name: "through one function",
			want: VString{V: "there"},
			src: `module M exposing (..)

out : String
out =
    render ()


render : () -> String
render _ =
    suffix


suffix : String
suffix =
    "there"
`,
		},
		{
			// The chain is longer than one hop: out -> a -> b -> tail.
			name: "through a chain of functions",
			want: VString{V: "deep"},
			src: `module M exposing (..)

out : String
out =
    a ()


a : () -> String
a _ =
    b ()


b : () -> String
b _ =
    tail


tail : String
tail =
    "deep"
`,
		},
		{
			// Mutually recursive functions are ordinary in Mar, and the
			// walk that finds what they read has to stop rather than spin.
			name: "through mutually recursive functions",
			want: VInt{V: 7},
			src: `module M exposing (..)

out : Int
out =
    even 4


even : Int -> Int
even n =
    if n <= 0 then
        seedVal

    else
        odd (n - 1)


odd : Int -> Int
odd n =
    even (n - 1)


seedVal : Int
seedVal =
    7
`,
		},
		{
			// A local binding shadows the top-level name inside `out`, and
			// the function still reads the top-level one.
			name: "with a local shadowing the same name",
			want: VString{V: "localtop"},
			src: `module M exposing (..)

out : String
out =
    let
        tail =
            "local"
    in
    tail ++ grab ()


grab : () -> String
grab _ =
    tail


tail : String
tail =
    "top"
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mod, err := parser.Parse(tc.src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := typecheck.CheckModule(mod); err != nil {
				t.Fatalf("this module is supposed to type-check: %v", err)
			}
			m, err := LoadModule(mod)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			got, ok := m.Env.Lookup("out")
			if !ok {
				t.Fatal("out is not bound")
			}
			if got != tc.want {
				t.Fatalf("out = %#v, want %#v — a value that calls a function "+
					"must be evaluated after everything that function reads", got, tc.want)
			}
		})
	}
}
