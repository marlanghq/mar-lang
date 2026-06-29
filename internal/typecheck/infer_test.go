package typecheck

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
)

// inferExprSrc wraps the given expression in a minimal module
// ("module M exposing (..)\nx = <expr>"), parses, and infers the type.
func inferExprSrc(t *testing.T, exprSrc string) (string, error) {
	t.Helper()
	resetVarIDsForTesting()
	src := "module M exposing (..)\nx = " + exprSrc + "\n"
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v\nsource: %s", err, src)
	}
	if len(mod.Decls) != 1 {
		t.Fatalf("expected 1 decl, got %d", len(mod.Decls))
	}
	vd, ok := mod.Decls[0].(*ast.ValueDecl)
	if !ok {
		t.Fatalf("expected ValueDecl, got %T", mod.Decls[0])
	}
	env := BaseEnv()
	tp, err := InferExpr(vd.Body, env)
	if err != nil {
		return "", err
	}
	return tp.String(), nil
}

func TestLitInt(t *testing.T) {
	got, err := inferExprSrc(t, "42")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Int" {
		t.Fatalf("want Int, got %s", got)
	}
}

func TestLitString(t *testing.T) {
	got, err := inferExprSrc(t, `"hello"`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "String" {
		t.Fatalf("want String, got %s", got)
	}
}

func TestLitBoolTrue(t *testing.T) {
	got, err := inferExprSrc(t, "True")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bool" {
		t.Fatalf("want Bool, got %s", got)
	}
}

func TestArith(t *testing.T) {
	got, err := inferExprSrc(t, "1 + 2 * 3")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Int" {
		t.Fatalf("want Int, got %s", got)
	}
}

func TestComparison(t *testing.T) {
	got, err := inferExprSrc(t, "1 == 2")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bool" {
		t.Fatalf("want Bool, got %s", got)
	}
}

func TestComparisonStrings(t *testing.T) {
	got, err := inferExprSrc(t, `"a" == "b"`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bool" {
		t.Fatalf("want Bool, got %s", got)
	}
}

func TestComparisonMixedFails(t *testing.T) {
	_, err := inferExprSrc(t, `1 == "two"`)
	if err == nil {
		t.Fatal("expected type error")
	}
}

func TestIf(t *testing.T) {
	got, err := inferExprSrc(t, "if True then 1 else 2")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Int" {
		t.Fatalf("want Int, got %s", got)
	}
}

func TestIfBranchMismatch(t *testing.T) {
	_, err := inferExprSrc(t, `if True then 1 else "two"`)
	if err == nil {
		t.Fatal("expected branch mismatch")
	}
	if !strings.Contains(err.Error(), "branches") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIfCondNotBool(t *testing.T) {
	_, err := inferExprSrc(t, "if 1 then 2 else 3")
	if err == nil {
		t.Fatal("expected non-bool condition error")
	}
}

func TestLambda(t *testing.T) {
	got, err := inferExprSrc(t, `\x -> x`)
	if err != nil {
		t.Fatal(err)
	}
	// Identity: a -> a. Var IDs vary, but must contain "->".
	if !strings.Contains(got, "->") {
		t.Fatalf("want arrow type, got %s", got)
	}
}

func TestLambdaApplication(t *testing.T) {
	got, err := inferExprSrc(t, `(\x -> x + 1) 5`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Int" {
		t.Fatalf("want Int, got %s", got)
	}
}

// A wrong-typed argument names the callee + the argument position and
// points the caret at the ARGUMENT (with a real multi-column span),
// not at the function head. This is the regression guard for the
// "caret on `text`, message about the argument" mismatch.
func TestApp_ArgErrorPointsAtArgument(t *testing.T) {
	// `x = UI.text [] [ "oops" ]`: UI.text spans cols 5-11; the bad
	// 2nd argument `[ "oops" ]` starts at col 16. UI.text wants a
	// String there, gets a List.
	_, err := inferExprSrc(t, `UI.text [] [ "oops" ]`)
	ie, ok := err.(*InferError)
	if !ok {
		t.Fatalf("want *InferError, got %T: %v", err, err)
	}
	if !strings.Contains(ie.Message, "2nd argument to `UI.text`") {
		t.Errorf("message should name the 2nd argument to UI.text; got %q", ie.Message)
	}
	if !strings.Contains(ie.Message, "expected String") {
		t.Errorf("message should state the expected type; got %q", ie.Message)
	}
	if ie.Pos.Column <= 11 {
		t.Errorf("caret should land on the argument (past `UI.text` at col 11), got col %d", ie.Pos.Column)
	}
	if ie.End.Line == 0 || ie.End.Column <= ie.Pos.Column {
		t.Errorf("error should carry a multi-column span; Pos=%+v End=%+v", ie.Pos, ie.End)
	}
}

// Applying a saturated function to an extra argument names the callee
// and reports "too many arguments" rather than the cryptic "not a
// function".
func TestApp_TooManyArgumentsNamesCallee(t *testing.T) {
	_, err := inferExprSrc(t, `String.length "hi" 5`)
	ie, ok := err.(*InferError)
	if !ok {
		t.Fatalf("want *InferError, got %T: %v", err, err)
	}
	if !strings.Contains(ie.Message, "too many arguments") {
		t.Errorf("message should say too many arguments; got %q", ie.Message)
	}
	if !strings.Contains(ie.Message, "String.length") {
		t.Errorf("message should name the callee; got %q", ie.Message)
	}
}

func TestLetSimple(t *testing.T) {
	src := `let
        y = 1
    in
    y + 2`
	got, err := inferExprSrc(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Int" {
		t.Fatalf("want Int, got %s", got)
	}
}

func TestLetPolymorphism(t *testing.T) {
	// Classic let-poly: id used at two different types.
	// Since == is polymorphic, this should type as Bool.
	src := `let
        id = \z -> z
    in
    id 1 == 2`
	got, err := inferExprSrc(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Bool" {
		t.Fatalf("want Bool, got %s", got)
	}
}

func TestList(t *testing.T) {
	got, err := inferExprSrc(t, "[1, 2, 3]")
	if err != nil {
		t.Fatal(err)
	}
	if got != "List Int" {
		t.Fatalf("want List Int, got %s", got)
	}
}

func TestListMixedFails(t *testing.T) {
	_, err := inferExprSrc(t, `[1, "two"]`)
	if err == nil {
		t.Fatal("expected list element mismatch")
	}
}

func TestRecord(t *testing.T) {
	got, err := inferExprSrc(t, `{ name = "Alice", age = 30 }`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "name : String") || !strings.Contains(got, "age : Int") {
		t.Fatalf("want record with name and age, got %s", got)
	}
}

func TestFieldAccess(t *testing.T) {
	got, err := inferExprSrc(t, `{ name = "Alice", age = 30 }.name`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "String" {
		t.Fatalf("want String, got %s", got)
	}
}

func TestFieldAccessor(t *testing.T) {
	got, err := inferExprSrc(t, `.name { name = "Alice" }`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "String" {
		t.Fatalf("want String, got %s", got)
	}
}

func TestUnit(t *testing.T) {
	got, err := inferExprSrc(t, "()")
	if err != nil {
		t.Fatal(err)
	}
	if got != "()" {
		t.Fatalf("want (), got %s", got)
	}
}

func TestTuple(t *testing.T) {
	got, err := inferExprSrc(t, `(1, "two")`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "(Int, String)" {
		t.Fatalf("want (Int, String), got %s", got)
	}
}

func TestPipe(t *testing.T) {
	src := `5 |> (\x -> x + 1)`
	got, err := inferExprSrc(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Int" {
		t.Fatalf("want Int, got %s", got)
	}
}

func TestStringAppend(t *testing.T) {
	got, err := inferExprSrc(t, `"Hello, " ++ "world"`)
	if err != nil {
		t.Fatal(err)
	}
	if got != "String" {
		t.Fatalf("want String, got %s", got)
	}
}

func TestMaybeJust(t *testing.T) {
	got, err := inferExprSrc(t, "Just 42")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Maybe Int" {
		t.Fatalf("want Maybe Int, got %s", got)
	}
}

func TestMaybeNothing(t *testing.T) {
	got, err := inferExprSrc(t, "Nothing")
	if err != nil {
		t.Fatal(err)
	}
	// "Maybe t<n>" — type of Nothing is polymorphic, var ID varies.
	if !strings.HasPrefix(got, "Maybe ") {
		t.Fatalf("want Maybe ..., got %s", got)
	}
}

func TestCaseMaybe(t *testing.T) {
	src := `case Just 5 of
        Just x -> x + 1
        Nothing -> 0`
	got, err := inferExprSrc(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Int" {
		t.Fatalf("want Int, got %s", got)
	}
}

func TestUnknownIdentifier(t *testing.T) {
	_, err := inferExprSrc(t, "nonexistent")
	if err == nil {
		t.Fatal("expected unknown identifier error")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOccursCheck(t *testing.T) {
	// (\x -> x x) — applying x to itself — needs x : a -> b AND x : a, occurs.
	_, err := inferExprSrc(t, `\x -> x x`)
	if err == nil {
		t.Fatal("expected occurs check failure")
	}
}
