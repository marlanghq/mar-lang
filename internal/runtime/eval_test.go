package runtime

import (
	"strings"
	"testing"

	"mar/internal/parser"
)

// runValue parses src as a module, loads it, evaluates the value named "x",
// and returns its display string.
func runValue(t *testing.T, src string) string {
	t.Helper()
	full := "module M exposing (..)\nx = " + src + "\n"
	mod, err := parser.Parse(full)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	loaded, err := LoadModule(mod)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	v, err := loaded.Get("x")
	if err != nil {
		t.Fatal(err)
	}
	return v.Display()
}

func runModule(t *testing.T, src, name string) string {
	t.Helper()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	loaded, err := LoadModule(mod)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	v, err := loaded.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return v.Display()
}

func TestEvalLiterals(t *testing.T) {
	if got := runValue(t, "42"); got != "42" {
		t.Fatalf("int: got %s", got)
	}
	if got := runValue(t, `"hi"`); got != `"hi"` {
		t.Fatalf("string: got %s", got)
	}
	if got := runValue(t, "True"); got != "True" {
		t.Fatalf("bool: got %s", got)
	}
	if got := runValue(t, "()"); got != "()" {
		t.Fatalf("unit: got %s", got)
	}
}

func TestEvalArith(t *testing.T) {
	if got := runValue(t, "1 + 2 * 3"); got != "7" {
		t.Fatalf("got %s", got)
	}
	if got := runValue(t, "10 - 4"); got != "6" {
		t.Fatalf("got %s", got)
	}
	if got := runValue(t, "10 / 3"); got != "3" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalIf(t *testing.T) {
	if got := runValue(t, "if True then 1 else 2"); got != "1" {
		t.Fatalf("got %s", got)
	}
	if got := runValue(t, "if False then 1 else 2"); got != "2" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalLambda(t *testing.T) {
	if got := runValue(t, `(\n -> n + 1) 5`); got != "6" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalLet(t *testing.T) {
	src := `let
        y = 10
    in
    y * 2`
	if got := runValue(t, src); got != "20" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalList(t *testing.T) {
	if got := runValue(t, "[1, 2, 3]"); got != "[1, 2, 3]" {
		t.Fatalf("got %s", got)
	}
	if got := runValue(t, `[1, 2] ++ [3, 4]`); got != "[1, 2, 3, 4]" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalRecord(t *testing.T) {
	got := runValue(t, `{ name = "Alice", age = 30 }`)
	if !strings.Contains(got, "Alice") || !strings.Contains(got, "30") {
		t.Fatalf("got %s", got)
	}
}

func TestEvalFieldAccess(t *testing.T) {
	if got := runValue(t, `{ x = 7 }.x`); got != "7" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalRecordUpdate(t *testing.T) {
	src := `let
        r = { count = 1, name = "a" }
    in
    { r | count = 99 }`
	got := runValue(t, src)
	if !strings.Contains(got, "99") {
		t.Fatalf("got %s", got)
	}
}

func TestEvalPipeline(t *testing.T) {
	if got := runValue(t, `5 |> (\n -> n + 1) |> (\n -> n * 2)`); got != "12" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalStringAppend(t *testing.T) {
	if got := runValue(t, `"Hello, " ++ "World"`); got != `"Hello, World"` {
		t.Fatalf("got %s", got)
	}
}

func TestEvalMaybe(t *testing.T) {
	if got := runValue(t, "Just 42"); got != "Just 42" {
		t.Fatalf("got %s", got)
	}
	if got := runValue(t, "Nothing"); got != "Nothing" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalCaseMaybe(t *testing.T) {
	src := `case Just 5 of
        Just n -> n + 1
        Nothing -> 0`
	if got := runValue(t, src); got != "6" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalCaseNothing(t *testing.T) {
	src := `case Nothing of
        Just n -> n
        Nothing -> 99`
	if got := runValue(t, src); got != "99" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalRecursiveFactorial(t *testing.T) {
	src := `module M exposing (..)
fact n =
    if n <= 1 then 1 else n * fact (n - 1)
result = fact 5
`
	if got := runModule(t, src, "result"); got != "120" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalMutualRecursion(t *testing.T) {
	src := `module M exposing (..)
even n = if n == 0 then True else odd (n - 1)
odd n = if n == 0 then False else even (n - 1)
result = even 10
`
	if got := runModule(t, src, "result"); got != "True" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalCustomType(t *testing.T) {
	src := `module M exposing (..)
type Color = Red | Green | Blue
toInt c =
    case c of
        Red -> 1
        Green -> 2
        Blue -> 3
result = toInt Green
`
	if got := runModule(t, src, "result"); got != "2" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalGenericCustomType(t *testing.T) {
	src := `module M exposing (..)
type Box a = Box a
unbox b = case b of Box x -> x
result = unbox (Box 42)
`
	if got := runModule(t, src, "result"); got != "42" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalCurriedFunction(t *testing.T) {
	src := `module M exposing (..)
add x y = x + y
addOne = add 1
result = addOne 41
`
	if got := runModule(t, src, "result"); got != "42" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalFieldAccessor(t *testing.T) {
	src := `module M exposing (..)
result = .name { name = "Alice", age = 30 }
`
	if got := runModule(t, src, "result"); got != `"Alice"` {
		t.Fatalf("got %s", got)
	}
}

func TestEvalEquality(t *testing.T) {
	if got := runValue(t, "1 == 1"); got != "True" {
		t.Fatalf("got %s", got)
	}
	if got := runValue(t, `"a" == "b"`); got != "False" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalLetWithFunction(t *testing.T) {
	src := `let
        double = \n -> n * 2
    in
    double 21`
	if got := runValue(t, src); got != "42" {
		t.Fatalf("got %s", got)
	}
}

// Record-pattern destructuring — runtime side. Typechecker tests cover
// type inference + scope; these tests cover that values actually get
// bound to the right fields at eval time.

func TestEvalRecordPatternInCase(t *testing.T) {
	src := `case { name = "Alice", age = 30 } of
        { name, age } -> name`
	if got := runValue(t, src); got != `"Alice"` {
		t.Fatalf("got %s", got)
	}
}

func TestEvalRecordPatternBindsByFieldName(t *testing.T) {
	// Order in the pattern shouldn't matter — bindings are by name,
	// not position.
	src := `case { name = "Alice", age = 30 } of
        { age, name } -> age`
	if got := runValue(t, src); got != "30" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalRecordPatternInLet(t *testing.T) {
	src := `let
        person = { first = "Ada", last = "Lovelace" }
        { first, last } = person
    in
    first ++ " " ++ last`
	if got := runValue(t, src); got != `"Ada Lovelace"` {
		t.Fatalf("got %s", got)
	}
}

func TestEvalRecordPatternNestedInCtor(t *testing.T) {
	src := `case Just { email = "a@b.com" } of
        Just { email } -> email
        Nothing -> ""`
	if got := runValue(t, src); got != `"a@b.com"` {
		t.Fatalf("got %s", got)
	}
}

func TestEvalRecordPatternPartialMatch(t *testing.T) {
	// Pattern lists fewer fields than the record has — the extras
	// are silently passed over.
	src := `case { a = 1, b = 2, c = 3, d = 4 } of
        { b, d } -> b + d`
	if got := runValue(t, src); got != "6" {
		t.Fatalf("got %s", got)
	}
}
