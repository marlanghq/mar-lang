package typecheck

import (
	"strings"
	"testing"
)

// `++` is constrained to Elm's `appendable`: String and List only.
// Before the constraint it was `forall a. a -> a -> a`, so `1 ++ 2`
// typechecked even though the runtime append can never honor it.

func TestAppendStringsOK(t *testing.T) {
	got, err := inferExprSrc(t, `"a" ++ "b"`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "String" {
		t.Fatalf("want String, got %s", got)
	}
}

func TestAppendListsOK(t *testing.T) {
	got, err := inferExprSrc(t, `[1, 2] ++ [3]`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "List Int" {
		t.Fatalf("want List Int, got %s", got)
	}
}

func TestAppendIntRejected(t *testing.T) {
	_, err := inferExprSrc(t, `1 ++ 2`)
	if err == nil {
		t.Fatal("expected type error for `1 ++ 2`, got clean inference")
	}
	if !strings.Contains(err.Error(), "appendable") {
		t.Fatalf("expected mention of 'appendable', got: %v", err)
	}
}

func TestAppendBoolRejected(t *testing.T) {
	_, err := inferExprSrc(t, `True ++ False`)
	if err == nil {
		t.Fatal("expected type error for `True ++ False`, got clean inference")
	}
	if !strings.Contains(err.Error(), "appendable") {
		t.Fatalf("expected mention of 'appendable', got: %v", err)
	}
}

func TestAppendStringWithListRejected(t *testing.T) {
	// Both operands share the one appendable var, so a String on the
	// left pins it and a List on the right is a plain type mismatch.
	_, err := inferExprSrc(t, `"a" ++ [1]`)
	if err == nil {
		t.Fatal("expected type error for `\"a\" ++ [1]`, got clean inference")
	}
}

func TestAppendListElementMismatchRejected(t *testing.T) {
	// List Int on the left pins the element type; List String on the
	// right fails to unify element-wise.
	_, err := inferExprSrc(t, `[1] ++ ["a"]`)
	if err == nil {
		t.Fatal("expected type error for `[1] ++ [\"a\"]`, got clean inference")
	}
}

func TestAppendConstraintPropagatesThroughFunction(t *testing.T) {
	// `join` generalizes to `forall a:appendable. a -> a -> a`. The
	// String call site is clean.
	src := `module M exposing (..)
join a b = a ++ b
useStr = join "x" "y"
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("expected clean check for String use, got: %v", err)
	}
}

func TestAppendEmptyListsOK(t *testing.T) {
	// `[] ++ []` keeps the element polymorphic — the appendable var
	// binds to `List a`, which satisfies the constraint.
	got, err := inferExprSrc(t, `[] ++ []`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "List ") {
		t.Fatalf("want a List type, got %s", got)
	}
}

func TestAppendComparableConflictRejected(t *testing.T) {
	// A value used as BOTH appendable (`++`) and comparable (`<`) has
	// no representable type — we don't model Elm's `compappend`, so we
	// reject it with a clear message rather than silently picking one.
	src := `module M exposing (..)
both x = if x < x then x ++ x else x
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected conflict error for a comparable+appendable value, got clean check")
	}
	if !strings.Contains(err.Error(), "both") {
		t.Fatalf("expected mention of 'both', got: %v", err)
	}
}

func TestAppendConstraintFiresAtCallSite(t *testing.T) {
	// The constraint rides through let-generalization and fires when a
	// caller pins the appendable var to a non-appendable type.
	src := `module M exposing (..)
join a b = a ++ b
useInt = join 1 2
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected type error for `join 1 2`, got clean check")
	}
	if !strings.Contains(err.Error(), "appendable") {
		t.Fatalf("expected mention of 'appendable', got: %v", err)
	}
}
