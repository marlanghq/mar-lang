package typecheck

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
)

// Top-level values are evaluated eagerly, in declaration order, by all three
// runtimes. That made source order load-bearing: a value reading a value
// declared below it saw the pre-bind placeholder and produced garbage, on a
// program that typechecks clean. The checker now hands the runtimes a list
// that is already in dependency order, so these tests are about the order of
// mod.Decls after checking, not about types.

func valueOrder(t *testing.T, src string) []string {
	t.Helper()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}
	var out []string
	for _, d := range mod.Decls {
		if v, ok := d.(*ast.ValueDecl); ok {
			out = append(out, v.Name)
		}
	}
	return out
}

func indexOf(names []string, want string) int {
	for i, n := range names {
		if n == want {
			return i
		}
	}
	return -1
}

func TestForwardReferenceIsReordered(t *testing.T) {
	order := valueOrder(t, `module M exposing (..)


total : Int
total =
    List.sum nums


nums : List Int
nums =
    [ 1, 2, 3 ]
`)
	if indexOf(order, "nums") > indexOf(order, "total") {
		t.Fatalf("nums must be evaluated before total, got %v", order)
	}
}

// A value that calls a function declared below it needs that function's
// closure to already exist. Functions read nothing when they are evaluated,
// so they all go first.
func TestValueCanCallFunctionDeclaredBelow(t *testing.T) {
	order := valueOrder(t, `module M exposing (..)


greeting : String
greeting =
    shout "hello"


shout : String -> String
shout s =
    String.toUpper s
`)
	if indexOf(order, "shout") > indexOf(order, "greeting") {
		t.Fatalf("shout must be evaluated before greeting, got %v", order)
	}
}

func TestTransitiveChainIsOrdered(t *testing.T) {
	order := valueOrder(t, `module M exposing (..)


third : Int
third =
    second + 1


second : Int
second =
    first + 1


first : Int
first =
    1
`)
	if indexOf(order, "first") > indexOf(order, "second") || indexOf(order, "second") > indexOf(order, "third") {
		t.Fatalf("expected first, second, third; got %v", order)
	}
}

// Independent values keep source order, so the reordering does not churn
// diffs or make output depend on map iteration.
func TestIndependentValuesKeepSourceOrder(t *testing.T) {
	src := `module M exposing (..)


alpha : Int
alpha =
    1


beta : Int
beta =
    2


gamma : Int
gamma =
    3
`
	want := []string{"alpha", "beta", "gamma"}
	for i := 0; i < 20; i++ {
		got := valueOrder(t, src)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("run %d: got %v, want %v", i, got, want)
		}
	}
}

// Reordering must not weaken the cycle check that made it safe.
func TestCycleStillRejected(t *testing.T) {
	mod, err := parser.Parse(`module M exposing (..)


a : Int
a =
    b + 1


b : Int
b =
    a + 1
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = CheckModule(mod)
	if err == nil {
		t.Fatal("a mutually dependent pair of values should be rejected")
	}
	if !strings.Contains(err.Error(), "depends on itself") {
		t.Errorf("expected a cycle error, got: %v", err)
	}
}

// Mutually recursive functions have no topological order and must stay legal.
func TestMutuallyRecursiveFunctionsStillCheck(t *testing.T) {
	valueOrder(t, `module M exposing (..)


isEven : Int -> Bool
isEven n =
    if n == 0 then
        True

    else
        isOdd (n - 1)


isOdd : Int -> Bool
isOdd n =
    if n == 0 then
        False

    else
        isEven (n - 1)
`)
}
