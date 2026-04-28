package typecheck

import (
	"strings"
	"testing"

	"mar/internal/parser"
)

func checkSource(t *testing.T, src string) (*CheckResult, error) {
	t.Helper()
	resetVarIDsForTesting()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return CheckModule(mod)
}

func TestCheckSingleValueDecl(t *testing.T) {
	src := `module M exposing (..)
foo = 42
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["foo"].String(); got != "Int" {
		t.Fatalf("want Int, got %s", got)
	}
}

func TestCheckSeveralValueDecls(t *testing.T) {
	src := `module M exposing (..)
greeting = "hello"
count = 1 + 2
flag = True
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["greeting"].String(); got != "String" {
		t.Fatalf("greeting: %s", got)
	}
	if got := res.ValueTypes["count"].String(); got != "Int" {
		t.Fatalf("count: %s", got)
	}
	if got := res.ValueTypes["flag"].String(); got != "Bool" {
		t.Fatalf("flag: %s", got)
	}
}

func TestCheckCustomTypeAndCtor(t *testing.T) {
	src := `module M exposing (..)
type Status = Active | Inactive
foo = Active
bar = Inactive
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["foo"].String(); got != "Status" {
		t.Fatalf("foo: want Status, got %s", got)
	}
	if got := res.ValueTypes["bar"].String(); got != "Status" {
		t.Fatalf("bar: want Status, got %s", got)
	}
}

func TestCheckCustomTypeWithPayload(t *testing.T) {
	src := `module M exposing (..)
type UserId = UserId Int
mkId = UserId 42
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["mkId"].String(); got != "UserId" {
		t.Fatalf("mkId: want UserId, got %s", got)
	}
}

func TestCheckGenericCustomType(t *testing.T) {
	src := `module M exposing (..)
type Box a = Box a
fooInt = Box 1
fooStr = Box "x"
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["fooInt"].String(); got != "Box Int" {
		t.Fatalf("fooInt: %s", got)
	}
	if got := res.ValueTypes["fooStr"].String(); got != "Box String" {
		t.Fatalf("fooStr: %s", got)
	}
}

func TestCheckAnnotation(t *testing.T) {
	src := `module M exposing (..)
foo : Int
foo = 42
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["foo"].String(); got != "Int" {
		t.Fatalf("foo: %s", got)
	}
}

func TestCheckAnnotationMismatch(t *testing.T) {
	src := `module M exposing (..)
foo : String
foo = 42
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected annotation mismatch error")
	}
	if !strings.Contains(err.Error(), "Int") || !strings.Contains(err.Error(), "String") {
		t.Fatalf("expected mismatch between Int and String, got: %v", err)
	}
}

func TestCheckFunctionDecl(t *testing.T) {
	src := `module M exposing (..)
add x y = x + y
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["add"].String()
	if got != "Int -> Int -> Int" && got != "Int -> (Int -> Int)" {
		t.Fatalf("add: want Int -> Int -> Int, got %s", got)
	}
}

func TestCheckRecursiveFunction(t *testing.T) {
	src := `module M exposing (..)
fact n =
    if n == 0 then 1 else n * fact (n - 1)
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["fact"].String()
	if got != "Int -> Int" {
		t.Fatalf("fact: want Int -> Int, got %s", got)
	}
}

func TestCheckMutualRecursion(t *testing.T) {
	src := `module M exposing (..)
even n = if n == 0 then True else odd (n - 1)
odd n = if n == 0 then False else even (n - 1)
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["even"].String(); got != "Int -> Bool" {
		t.Fatalf("even: %s", got)
	}
	if got := res.ValueTypes["odd"].String(); got != "Int -> Bool" {
		t.Fatalf("odd: %s", got)
	}
}

func TestCheckCustomTypeUsedInRecord(t *testing.T) {
	src := `module M exposing (..)
type UserId = UserId Int
mkUser = { id = UserId 1, name = "Alice" }
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	got := res.ValueTypes["mkUser"].String()
	if !strings.Contains(got, "UserId") || !strings.Contains(got, "String") {
		t.Fatalf("mkUser: %s", got)
	}
}

func TestCheckCaseOnCustomType(t *testing.T) {
	src := `module M exposing (..)
type Color = Red | Green | Blue
toInt c =
    case c of
        Red -> 1
        Green -> 2
        Blue -> 3
`
	res, err := checkSource(t, src)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.ValueTypes["toInt"].String(); got != "Color -> Int" {
		t.Fatalf("toInt: %s", got)
	}
}
