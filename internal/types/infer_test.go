package types

import (
	"strings"
	"testing"

	"mar/internal/expr"
)

// helper to parse and infer a mar-lang expression source string in BaseEnv.
func inferSource(t *testing.T, src string) Type {
	t.Helper()
	resetVarIDsForTesting()
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{}),
	})
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	got, err := InferExpr(parsed, BaseEnv())
	if err != nil {
		t.Fatalf("infer %q: %v", src, err)
	}
	return got
}

func inferSourceErr(t *testing.T, src string) error {
	t.Helper()
	resetVarIDsForTesting()
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{}),
	})
	if err != nil {
		// Parse error counts as an inference failure for these tests.
		return err
	}
	_, err = InferExpr(parsed, BaseEnv())
	return err
}

func TestInferLiteralInt(t *testing.T) {
	if got := inferSource(t, "42").String(); got != "int" {
		t.Errorf("got %q, want int", got)
	}
}

func TestInferLiteralString(t *testing.T) {
	if got := inferSource(t, `"hello"`).String(); got != "string" {
		t.Errorf("got %q, want string", got)
	}
}

func TestInferLiteralBool(t *testing.T) {
	if got := inferSource(t, "true").String(); got != "bool" {
		t.Errorf("got %q, want bool", got)
	}
}

func TestInferIfWellTyped(t *testing.T) {
	src := `(if true 1 2)`
	if got := inferSource(t, src).String(); got != "int" {
		t.Errorf("got %q, want int", got)
	}
}

func TestInferIfBranchMismatch(t *testing.T) {
	err := inferSourceErr(t, `(if true 1 "two")`)
	if err == nil || !strings.Contains(err.Error(), "branches") {
		t.Errorf("want branch mismatch, got: %v", err)
	}
}

func TestInferIfConditionMustBeBool(t *testing.T) {
	err := inferSourceErr(t, `(if 1 2 3)`)
	if err == nil {
		t.Fatal("expected error for non-bool condition")
	}
}

func TestInferLetSimple(t *testing.T) {
	src := `(let ((x 1)) x)`
	if got := inferSource(t, src).String(); got != "int" {
		t.Errorf("got %q, want int", got)
	}
}

func TestInferLetPolymorphic(t *testing.T) {
	// Classic let-poly: id used at two types.
	src := `(let* ((id (lambda (x) x))) (= (id 1) (id 2)))`
	if got := inferSource(t, src).String(); got != "bool" {
		t.Errorf("got %q, want bool", got)
	}
}

func TestInferLambdaIdentity(t *testing.T) {
	src := `(lambda (x) x)`
	got := inferSource(t, src).String()
	// Should be (α -> α) or similar — fresh var same on both sides.
	if !strings.Contains(got, "->") {
		t.Errorf("got %q, want arrow type", got)
	}
}

func TestInferArithmetic(t *testing.T) {
	if got := inferSource(t, "(+ 1 2)").String(); got != "int" {
		t.Errorf("got %q, want int", got)
	}
}

func TestInferArithmeticDecimal(t *testing.T) {
	if got := inferSource(t, "(+ 1.5 2.5)").String(); got != "decimal" {
		t.Errorf("got %q, want decimal", got)
	}
}

func TestInferArithmeticTypeError(t *testing.T) {
	err := inferSourceErr(t, `(+ 1 "two")`)
	if err == nil {
		t.Fatal("expected type error")
	}
}

func TestInferEqualityPolymorphic(t *testing.T) {
	for _, src := range []string{`(= 1 2)`, `(= "a" "b")`, `(= true false)`} {
		got := inferSource(t, src).String()
		if got != "bool" {
			t.Errorf("%s = %q, want bool", src, got)
		}
	}
}

func TestInferEqualityRejectsMixed(t *testing.T) {
	if err := inferSourceErr(t, `(= 1 "two")`); err == nil {
		t.Fatal("expected error for (= 1 \"two\")")
	}
}

func TestInferAndOrBool(t *testing.T) {
	if got := inferSource(t, `(and true false)`).String(); got != "bool" {
		t.Errorf("got %q, want bool", got)
	}
}

func TestInferNot(t *testing.T) {
	if got := inferSource(t, `(not true)`).String(); got != "bool" {
		t.Errorf("got %q, want bool", got)
	}
}

func TestInferContains(t *testing.T) {
	if got := inferSource(t, `(contains "hello" "world")`).String(); got != "bool" {
		t.Errorf("got %q, want bool", got)
	}
}

func TestInferLengthString(t *testing.T) {
	if got := inferSource(t, `(length "hello")`).String(); got != "int" {
		t.Errorf("got %q, want int", got)
	}
}

func TestInferUnknownIdentifier(t *testing.T) {
	if err := inferSourceErr(t, `nonexistent`); err == nil {
		t.Fatal("expected unknown identifier error")
	}
}

func TestInferRecursionViaUserFunction(t *testing.T) {
	// Simulate a top-level (define (fact n) ...): pre-bind fact with a fresh
	// var, infer the body assuming that type, then unify.
	resetVarIDsForTesting()
	env := BaseEnv()

	factVar := FreshVar()
	env = env.Bind("fact", factVar)

	src := `(lambda (n) (if (= n 0) 1 (* n (fact (- n 1)))))`
	parsed, err := expr.Parse(src, expr.ParserOptions{
		AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{}),
		AllowedFunctions: map[string]int{"fact": 1},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s := NewSubst()
	bodyT, err := Infer(parsed, env, s)
	if err != nil {
		t.Fatalf("infer recursive body: %v", err)
	}
	if err := Unify(factVar, bodyT, s); err != nil {
		t.Fatalf("unify recursive: %v", err)
	}
	got := s.Apply(factVar).String()
	if got != "(int -> int)" {
		t.Errorf("fact type = %q, want (int -> int)", got)
	}
}

func TestInferMutualRecursion(t *testing.T) {
	resetVarIDsForTesting()
	env := BaseEnv()

	evenVar := FreshVar()
	oddVar := FreshVar()
	env = env.Bind("even?", evenVar).Bind("odd?", oddVar)

	parseBody := func(src string) expr.Expr {
		t.Helper()
		parsed, err := expr.Parse(src, expr.ParserOptions{
			AllowedVariables: expr.AllowedVariablesWithBuiltins(map[string]struct{}{}),
			AllowedFunctions: map[string]int{"even?": 1, "odd?": 1},
		})
		if err != nil {
			t.Fatalf("parse %s: %v", src, err)
		}
		return parsed
	}

	evenBody := parseBody(`(lambda (n) (if (= n 0) true (odd? (- n 1))))`)
	oddBody := parseBody(`(lambda (n) (if (= n 0) false (even? (- n 1))))`)

	s := NewSubst()
	tEven, err := Infer(evenBody, env, s)
	if err != nil {
		t.Fatalf("infer even: %v", err)
	}
	if err := Unify(evenVar, tEven, s); err != nil {
		t.Fatalf("unify even: %v", err)
	}
	tOdd, err := Infer(oddBody, env, s)
	if err != nil {
		t.Fatalf("infer odd: %v", err)
	}
	if err := Unify(oddVar, tOdd, s); err != nil {
		t.Fatalf("unify odd: %v", err)
	}

	gotEven := s.Apply(evenVar).String()
	gotOdd := s.Apply(oddVar).String()
	if gotEven != "(int -> bool)" {
		t.Errorf("even? = %q, want (int -> bool)", gotEven)
	}
	if gotOdd != "(int -> bool)" {
		t.Errorf("odd? = %q, want (int -> bool)", gotOdd)
	}
}

func TestInferHigherOrder(t *testing.T) {
	src := `(lambda (f x) (f x))`
	got := inferSource(t, src).String()
	if !strings.Contains(got, "->") {
		t.Errorf("got %q, want arrow type", got)
	}
}

func TestInferConstK(t *testing.T) {
	// K = λx. λy. x
	src := `(lambda (x) (lambda (y) x))`
	got := inferSource(t, src).String()
	// (α -> (β -> α))
	if !strings.Contains(got, "->") {
		t.Errorf("got %q, want arrow type", got)
	}
}

func TestInferFunctionStoredInVariable(t *testing.T) {
	src := `(let* ((double (lambda (x) (* x 2)))) (double 21))`
	if got := inferSource(t, src).String(); got != "int" {
		t.Errorf("got %q, want int", got)
	}
}

func TestInferOccursCheck(t *testing.T) {
	if err := inferSourceErr(t, `(lambda (x) (x x))`); err == nil {
		t.Fatal("expected occurs check failure")
	}
}
