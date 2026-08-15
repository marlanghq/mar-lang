package typecheck

import (
	"strings"
	"testing"

	"mar/internal/parser"
)

func TestGeneralizeIdentity(t *testing.T) {
	src := `module M exposing (..)
identity x = x
`
	resetVarIDsForTesting()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	res, err := CheckModule(mod)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["identity"].String()
	t.Logf("identity : %s", got)
	if !strings.Contains(got, "forall") {
		t.Fatalf("expected forall in type, got %s", got)
	}
}

// checkSrcErr parses and checks one module and RETURNS the error rather
// than failing on it, so a test can pin rejection as well as acceptance.
// (checkSrc, in polymorphic_sum_test.go, fatals — it is for the happy
// path only.)
func checkSrcErr(t *testing.T, src string) error {
	t.Helper()
	resetVarIDsForTesting()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, cerr := CheckModule(mod)
	return cerr
}

// A local name for an outer parameter must NOT be generalized over that
// parameter's own type variable. envFreeVars used to read the single
// frame it was handed, so with two parameters the binder for `x` sat one
// frame out of view and `g` was quantified over it: `g` could then be a
// number in one component and appendable in the other. The module checked
// clean and printed `f : Bool -> Int -> (Int, String)` — a signature that
// adds True to 1 — and the program died in the evaluator.
//
// Each case is a shape that pushes the binder a different distance from
// the frame the generalizer sees.
func TestLetDoesNotGeneralizeOverEnclosingBinders(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"two parameters", `module M exposing (..)
f x y =
    let
        g =
            x
    in
    ( g + y, g ++ "!" )
`},
		{"one parameter, one earlier let", `module M exposing (..)
f x =
    let
        z =
            1

        g =
            x
    in
    ( g + z, g ++ "!" )
`},
		{"binder two lambdas out", `module M exposing (..)
f x =
    \y ->
        let
            g =
                x
        in
        ( g + y, g ++ "!" )
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSrcErr(t, c.src); err == nil {
				t.Fatal("checked clean: a let generalized over a variable still bound outside it")
			}
		})
	}
}

// The other half of the same property: closing the hole must not cost
// let-polymorphism. A local helper whose type is genuinely its own stays
// usable at two types, including inside a function that has parameters —
// which is exactly the case a too-eager fix would break.
func TestLetPolymorphismSurvives(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"helper used at two types", `module M exposing (..)
usesBoth : (Int, String)
usesBoth =
    let
        pick =
            \a -> a
    in
    ( pick 1, pick "x" )
`},
		{"helper inside a function with parameters", `module M exposing (..)
nested : Int -> (List Int, List String)
nested n =
    let
        twice =
            \a -> [ a, a ]
    in
    ( twice n, twice "s" )
`},
		{"monomorphic local over a parameter is still fine", `module M exposing (..)
shadowed : Int -> Int
shadowed x =
    let
        g =
            x
    in
    g + 1
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := checkSrcErr(t, c.src); err != nil {
				t.Fatalf("rejected a valid program: %v", err)
			}
		})
	}
}

// The environment's free-variable set is carried per frame and shared
// with the parent whenever a binding adds nothing. If that sharing ever
// breaks, generalization goes quadratic and `mar check` slows to a crawl
// on a real project, silently. The base environment is 900-odd frames of
// closed builtin schemes, so it must come out with an empty set and no
// allocation of its own.
func TestBaseEnvCarriesNoFreeVars(t *testing.T) {
	base := BaseEnv()
	if n := len(base.free); n != 0 {
		t.Fatalf("BaseEnv carries %d free type variables; every builtin scheme should be closed", n)
	}
	// Sharing check: a closed binding must hand back the very same map,
	// not a copy.
	closed := base.Bind("someName", TCon{Name: "Int"})
	if len(closed.free) != 0 {
		t.Fatalf("binding a closed type introduced %d free vars", len(closed.free))
	}
	open := base.Bind("other", TVar{ID: 999999})
	if !open.free[999999] {
		t.Fatal("binding an open type did not record its variable")
	}
	if len(closed.free) != 0 {
		t.Fatal("recording on a child leaked into a sibling: the shared map was mutated")
	}
}

func TestGeneralizeUnbox(t *testing.T) {
	src := `module M exposing (..)
type Box a = Box a
unbox b = case b of Box x -> x
`
	resetVarIDsForTesting()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	res, err := CheckModule(mod)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["unbox"].String()
	t.Logf("unbox : %s", got)
	if !strings.Contains(got, "forall") {
		t.Fatalf("expected forall in type, got %s", got)
	}
}
