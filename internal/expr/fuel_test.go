package expr

import (
	"strings"
	"testing"
)

// TestFuelStopsInfiniteRecursion verifies that an unbounded recursive call
// terminates with a structured RaisedError instead of crashing the Go
// process via stack overflow.
func TestFuelStopsInfiniteRecursion(t *testing.T) {
	// (define (loop n) (loop n)) — pure recursion with no decreasing arg.
	loopExpr := Call{Name: "loop", Args: []Expr{Variable{Name: "n"}}}
	loopFn := UserFunction{Params: []string{"n"}, Body: loopExpr}

	ctx := map[string]any{
		"n":              int64(1),
		"__functions":    map[string]UserFunction{"loop": loopFn},
	}
	SetFuel(ctx, 100) // small budget for fast test

	_, err := loopExpr.Eval(ctx)
	if err == nil {
		t.Fatal("expected fuel-exceeded error, got nil")
	}
	if !strings.Contains(err.Error(), "execution budget exceeded") {
		t.Fatalf("expected budget-exceeded error, got: %v", err)
	}
	// Must be a RaisedError so the runtime can convert it to a clean HTTP error.
	if _, ok := err.(RaisedError); !ok {
		t.Errorf("expected RaisedError type, got %T", err)
	}
}

// TestFuelAllowsBoundedRecursion verifies that legitimate bounded recursion
// completes within the budget.
func TestFuelAllowsBoundedRecursion(t *testing.T) {
	// (define (count n) (if (= n 0) 0 (count (- n 1))))
	countBody := If{
		Condition: Binary{Op: "==", Left: Variable{Name: "n"}, Right: Literal{Value: int64(0)}},
		Then:      Literal{Value: int64(0)},
		Else: Call{
			Name: "count",
			Args: []Expr{Binary{Op: "-", Left: Variable{Name: "n"}, Right: Literal{Value: int64(1)}}},
		},
	}
	countFn := UserFunction{Params: []string{"n"}, Body: countBody}

	ctx := map[string]any{
		"n":              int64(50),
		"__functions":    map[string]UserFunction{"count": countFn},
	}
	SetFuel(ctx, 1000)

	_, err := countBody.Eval(ctx)
	if err != nil {
		t.Fatalf("expected success for bounded recursion, got: %v", err)
	}
}

// TestNoFuelMeansNoLimit verifies that contexts without fuel are unconstrained
// (existing test behavior preserved).
func TestNoFuelMeansNoLimit(t *testing.T) {
	body := Literal{Value: int64(42)}
	ctx := map[string]any{}
	val, err := body.Eval(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != int64(42) {
		t.Errorf("got %v, want 42", val)
	}
}
