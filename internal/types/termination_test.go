package types

import (
	"strings"
	"testing"

	"mar/internal/model"
)

// CheckTermination tests.

func mustFunction(name string, params []string, body string) model.Function {
	return model.Function{Name: name, Parameters: params, Expression: body}
}

func TestTerminationRejectsTrivialLoop(t *testing.T) {
	app := &model.App{
		Functions: []model.Function{
			mustFunction("loop", []string{"n"}, "(loop n)"),
		},
	}
	err := CheckTermination(app)
	if err == nil {
		t.Fatal("expected diverging-recursion error")
	}
	if !strings.Contains(err.Error(), "loop") || !strings.Contains(err.Error(), "terminate") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTerminationAcceptsBoundedRecursion(t *testing.T) {
	// (count n) recurses on (- n 1), which differs from n verbatim.
	app := &model.App{
		Functions: []model.Function{
			mustFunction("count", []string{"n"},
				"(if (= n 0) 0 (count (- n 1)))"),
		},
	}
	if err := CheckTermination(app); err != nil {
		t.Fatalf("expected acceptance, got: %v", err)
	}
}

func TestTerminationAcceptsConditionalRecursion(t *testing.T) {
	// Recursive call with same args, but inside a conditional that has a
	// non-recursive branch — accept (we're conservative).
	app := &model.App{
		Functions: []model.Function{
			mustFunction("maybe-loop", []string{"n"},
				"(if (= n 0) 0 (maybe-loop n))"),
		},
	}
	// This is technically diverging if n != 0 ever, but our heuristic is
	// conservative and only flags when ALL branches diverge.
	if err := CheckTermination(app); err != nil {
		t.Fatalf("conservative heuristic should accept conditional with base case: %v", err)
	}
}

func TestTerminationRejectsLoopInBothBranches(t *testing.T) {
	// Both branches recurse with same args — definitely diverges.
	app := &model.App{
		Functions: []model.Function{
			mustFunction("loop", []string{"n"},
				"(if (= n 0) (loop n) (loop n))"),
		},
	}
	if err := CheckTermination(app); err == nil {
		t.Fatal("expected error for diverging-in-both-branches")
	}
}

func TestTerminationAcceptsNoRecursion(t *testing.T) {
	app := &model.App{
		Functions: []model.Function{
			mustFunction("identity", []string{"x"}, "x"),
		},
	}
	if err := CheckTermination(app); err != nil {
		t.Fatalf("identity should pass: %v", err)
	}
}

func TestTerminationRejectsMultiArgVerbatim(t *testing.T) {
	// (f x y) calls (f x y) — same args verbatim.
	app := &model.App{
		Functions: []model.Function{
			mustFunction("f", []string{"x", "y"}, "(f x y)"),
		},
	}
	if err := CheckTermination(app); err == nil {
		t.Fatal("expected error for f(x,y) = f(x,y)")
	}
}

func TestTerminationAcceptsArgPermutation(t *testing.T) {
	// (f x y) calls (f y x) — args swapped, not verbatim. Conservative
	// heuristic accepts (could still loop, but caught at runtime via fuel).
	app := &model.App{
		Functions: []model.Function{
			mustFunction("f", []string{"x", "y"}, "(f y x)"),
		},
	}
	if err := CheckTermination(app); err != nil {
		t.Fatalf("permuted-args recursion should pass conservative check: %v", err)
	}
}
