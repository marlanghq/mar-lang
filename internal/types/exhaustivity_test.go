package types

import (
	"strings"
	"testing"

	"mar/internal/expr"
)

// Match exhaustivity tests.

func TestExhaustivityRejectsMissingMaybeNothing(t *testing.T) {
	resetVarIDsForTesting()
	// (match (just 1) ((just v) v)) — missing nothing branch.
	src := `(match (just 1) ((just v) v))`
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{}),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := InferExpr(parsed, BaseEnv()); err == nil {
		t.Fatal("expected exhaustivity error")
	} else if !strings.Contains(err.Error(), "not exhaustive") || !strings.Contains(err.Error(), "nothing") {
		t.Fatalf("expected 'not exhaustive ... nothing', got: %v", err)
	}
}

func TestExhaustivityAcceptsMaybeWithBothBranches(t *testing.T) {
	resetVarIDsForTesting()
	src := `(match (just 1) ((just v) v) ((nothing) 0))`
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{}),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := InferExpr(parsed, BaseEnv()); err != nil {
		t.Fatalf("expected success: %v", err)
	}
}

func TestExhaustivityRejectsMissingResultErr(t *testing.T) {
	resetVarIDsForTesting()
	src := `(match (ok 1) ((ok v) v))`
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{}),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := InferExpr(parsed, BaseEnv()); err == nil {
		t.Fatal("expected exhaustivity error")
	} else if !strings.Contains(err.Error(), "err") {
		t.Fatalf("expected 'missing err', got: %v", err)
	}
}

func TestExhaustivityRejectsMissingTUnionVariant(t *testing.T) {
	resetVarIDsForTesting()
	color := TUnion{
		Name:         "color",
		Variants:     map[string][]Type{"red": nil, "green": nil, "blue": nil},
		VariantOrder: []string{"red", "green", "blue"},
	}
	env := BaseEnv().Bind("c", color)

	// (match c ((red) 1) ((green) 2)) — missing blue.
	src := `(match c ((red) 1) ((green) 2))`
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{"c": {}}),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := InferExpr(parsed, env); err == nil {
		t.Fatal("expected exhaustivity error")
	} else if !strings.Contains(err.Error(), "blue") {
		t.Fatalf("expected 'missing blue', got: %v", err)
	}
}

func TestExhaustivityAcceptsTUnionWithAllVariants(t *testing.T) {
	resetVarIDsForTesting()
	color := TUnion{
		Name:         "color",
		Variants:     map[string][]Type{"red": nil, "green": nil, "blue": nil},
		VariantOrder: []string{"red", "green", "blue"},
	}
	env := BaseEnv().Bind("c", color)
	src := `(match c ((red) 1) ((green) 2) ((blue) 3))`
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{"c": {}}),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := InferExpr(parsed, env); err != nil {
		t.Fatalf("expected success: %v", err)
	}
}

func TestExhaustivitySkipsUnknownSubject(t *testing.T) {
	resetVarIDsForTesting()
	// Match on a TVar (parametric) — can't enumerate variants. Accept.
	v := FreshVar()
	env := BaseEnv().Bind("x", v)
	src := `(match x ((foo a) a))`
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{"x": {}}),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := InferExpr(parsed, env); err != nil {
		t.Fatalf("parametric subject should pass exhaustivity check: %v", err)
	}
}
