package types

import (
	"strings"
	"testing"

	"mar/internal/expr"
)

// Row polymorphism tests.

func TestRowGetOnUnknownTargetGeneratesConstraint(t *testing.T) {
	// (lambda (x) x.name) — should infer (∀α ρ. {name: α | ρ} → α).
	resetVarIDsForTesting()
	src := `(lambda (x) x.name)`
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{}),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := InferExpr(parsed, BaseEnv())
	if err != nil {
		t.Fatalf("infer: %v", err)
	}
	// Type should be an arrow whose argument is an open record with `name`.
	str := got.String()
	if !strings.Contains(str, "name:") || !strings.Contains(str, "->") {
		t.Errorf("expected arrow with open-record argument, got %q", str)
	}
}

func TestRowGetMissingFieldOnClosedRecordRejected(t *testing.T) {
	// Closed record {a: int} — accessing .b should fail.
	resetVarIDsForTesting()
	rec := TRecord{
		Name:   "Foo",
		Fields: map[string]Type{"a": TInt()},
		Order:  []string{"a"},
	}
	env := BaseEnv().Bind("foo", rec)
	src := `foo.b`
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{"foo": {}}),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := InferExpr(parsed, env); err == nil {
		t.Fatal("expected error accessing missing field on closed record")
	}
}

func TestRowOpenRecordAcceptsExtraFields(t *testing.T) {
	// (lambda (x) x.name) applied to a closed record {name: string, age: int}
	// — the open-record requirement (just .name) is satisfied even though
	// the actual record has extra fields.
	resetVarIDsForTesting()
	closedRec := TRecord{
		Name:   "Person",
		Fields: map[string]Type{"name": TString(), "age": TInt()},
		Order:  []string{"name", "age"},
	}

	// Build the lambda type via inference.
	lambdaSrc := `(lambda (x) x.name)`
	parsed, err := expr.Parse(lambdaSrc, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{}),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := NewSubst()
	lambdaT, err := Infer(parsed, BaseEnv(), s)
	if err != nil {
		t.Fatalf("infer lambda: %v", err)
	}
	// Apply to a closed record. Build (lambda closedRec) -> result.
	resultVar := FreshVar()
	expected := TArrow([]Type{closedRec}, resultVar)
	if err := Unify(lambdaT, expected, s); err != nil {
		t.Fatalf("unify with closed record: %v", err)
	}
	got := s.Apply(resultVar).String()
	if got != "string" {
		t.Errorf("result type = %q, want string", got)
	}
}

func TestRowOpenRecordRejectsRecordWithoutField(t *testing.T) {
	// Lambda needs .name; pass a record without name → fail.
	resetVarIDsForTesting()
	noName := TRecord{
		Name:   "Empty",
		Fields: map[string]Type{"age": TInt()},
		Order:  []string{"age"},
	}
	lambdaSrc := `(lambda (x) x.name)`
	parsed, err := expr.Parse(lambdaSrc, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{}),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := NewSubst()
	lambdaT, _ := Infer(parsed, BaseEnv(), s)
	resultVar := FreshVar()
	expected := TArrow([]Type{noName}, resultVar)
	if err := Unify(lambdaT, expected, s); err == nil {
		t.Fatal("expected error: record Empty has no field 'name'")
	}
}

func TestRowMultipleAccessesAccumulate(t *testing.T) {
	// (lambda (x) (+ x.age 1)) — should infer x as {age: int | ρ}.
	resetVarIDsForTesting()
	src := `(lambda (x) (+ x.age 1))`
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{}),
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := InferExpr(parsed, BaseEnv())
	if err != nil {
		t.Fatalf("infer: %v", err)
	}
	str := got.String()
	if !strings.Contains(str, "age:") {
		t.Errorf("expected open record with age, got %q", str)
	}
}
