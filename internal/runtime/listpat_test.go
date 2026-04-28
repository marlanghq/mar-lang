package runtime

import "testing"

func TestEvalEmptyListPattern(t *testing.T) {
	src := `module M exposing (..)
isEmpty xs =
    case xs of
        [] -> True
        x :: rest -> False
result = isEmpty []
`
	if got := runModule(t, src, "result"); got != "True" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalConsPattern(t *testing.T) {
	src := `module M exposing (..)
firstOrZero xs =
    case xs of
        [] -> 0
        x :: rest -> x
result = firstOrZero [42, 1, 2]
`
	if got := runModule(t, src, "result"); got != "42" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalRecursiveLength(t *testing.T) {
	src := `module M exposing (..)
length xs =
    case xs of
        [] -> 0
        x :: rest -> 1 + length rest
result = length [10, 20, 30, 40]
`
	if got := runModule(t, src, "result"); got != "4" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalNestedConsPattern(t *testing.T) {
	src := `module M exposing (..)
first2 xs =
    case xs of
        a :: b :: rest -> a + b
        _ -> 0
result = first2 [10, 20, 30]
`
	if got := runModule(t, src, "result"); got != "30" {
		t.Fatalf("got %s", got)
	}
}

func TestEvalLiteralListPattern(t *testing.T) {
	src := `module M exposing (..)
classify xs =
    case xs of
        [] -> "empty"
        [x] -> "one"
        [x, y] -> "two"
        _ -> "many"
ones = classify [1]
twos = classify [1, 2]
manys = classify [1, 2, 3]
empties = classify []
`
	if got := runModule(t, src, "ones"); got != `"one"` {
		t.Fatalf("ones: %s", got)
	}
	if got := runModule(t, src, "twos"); got != `"two"` {
		t.Fatalf("twos: %s", got)
	}
	if got := runModule(t, src, "manys"); got != `"many"` {
		t.Fatalf("manys: %s", got)
	}
	if got := runModule(t, src, "empties"); got != `"empty"` {
		t.Fatalf("empties: %s", got)
	}
}
