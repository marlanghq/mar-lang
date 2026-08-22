package typecheck

import (
	"strings"
	"testing"
)

// Every shape a name can be declared twice in. Before checkDuplicateDecls
// exactly one of these produced a diagnostic; of the rest, one panicked the
// compiler (and with it the language server and `mar dev`), two produced
// programs that checked clean and then went wrong at run time, and the others
// silently dropped whichever declaration came first.
//
// The pairing an annotation makes with its value is the one case where a name
// legitimately appears twice, so TestAnnotationAndValuePairingStillWorks
// guards the floor beneath all of this.

func checkDuplicate(t *testing.T, src, wantName string) string {
	t.Helper()
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected a duplicate-declaration error, got a clean check")
	}
	msg := err.Error()
	if !strings.Contains(msg, wantName) {
		t.Fatalf("expected the message to name %q, got: %v", wantName, msg)
	}
	return msg
}

func TestDuplicateValuePanicsNoMore(t *testing.T) {
	// The panic case: two zero-parameter values. orderValueDecls emitted one
	// entry for the pair and then indexed the result by declaration slot.
	src := `module M exposing (..)
greeting = "hi"
greeting = "bye"
main = greeting
`
	msg := checkDuplicate(t, src, "greeting")
	if !strings.Contains(msg, "two definitions") {
		t.Fatalf("expected a two-definitions message, got: %v", msg)
	}
}

func TestDuplicateFunctionRejected(t *testing.T) {
	// Never panicked, because functions keep both slots — it just silently
	// ran the second one.
	src := `module M exposing (..)
double n = n * 2
double n = n * 3
result = double 10
`
	checkDuplicate(t, src, "double")
}

func TestDuplicateCustomTypeRejected(t *testing.T) {
	// The worst of the family: this checked clean and then reached
	// "no case branch matched" at run time. Both declarations canonicalise
	// to M.Color, so `describe` was proven total against Blue | Yellow
	// while `Red` was still a live value of the same canonical type.
	src := `module M exposing (..)
type Color = Red | Green
type Color = Blue | Yellow
describe c =
    case c of
        Blue -> "b"
        Yellow -> "y"
result = describe Red
`
	msg := checkDuplicate(t, src, "Color")
	if !strings.Contains(msg, "two custom types") {
		t.Fatalf("expected a two-custom-types message, got: %v", msg)
	}
}

func TestDuplicateTypeAliasRejected(t *testing.T) {
	src := `module M exposing (..)
type alias P = { x : Int }
type alias P = { y : String }
v : P
v = { y = "hi" }
`
	checkDuplicate(t, src, "P")
}

func TestAliasAndCustomTypeShareNameRejected(t *testing.T) {
	// The custom type won regardless of source order, so the alias became
	// unreachable as a type while still claiming its name as a value.
	src := `module M exposing (..)
type alias Point = { x : Int }
type Point = Origin
v : Point
v = Origin
`
	msg := checkDuplicate(t, src, "Point")
	if !strings.Contains(msg, "type alias") || !strings.Contains(msg, "custom type") {
		t.Fatalf("expected the message to name both kinds, got: %v", msg)
	}
}

func TestDuplicateConstructorInOneTypeRejected(t *testing.T) {
	src := `module M exposing (..)
type Color = Red | Red
`
	checkDuplicate(t, src, "Red")
}

func TestConstructorNameInTwoTypesRejected(t *testing.T) {
	// This one used to surface as "cannot unify Fruit with Color" at the
	// USE site, with nothing anywhere saying that Red was declared twice.
	src := `module M exposing (..)
type Color = Red | Green
type Fruit = Apple | Red
pick : Color
pick = Red
`
	msg := checkDuplicate(t, src, "Red")
	if !strings.Contains(msg, "Color") || !strings.Contains(msg, "Fruit") {
		t.Fatalf("expected the message to name both types, got: %v", msg)
	}
}

func TestRecordAliasConstructorClashRejected(t *testing.T) {
	// `type alias Wrapper = { n : Int }` claims the VALUE name Wrapper too,
	// so a constructor of the same name silently made it unreachable.
	src := `module M exposing (..)
type alias Wrapper = { n : Int }
type Thing = Wrapper String
v = Wrapper "s"
`
	checkDuplicate(t, src, "Wrapper")
}

func TestDuplicateAnnotationRejected(t *testing.T) {
	src := `module M exposing (..)
foo : String
foo : Int
foo = 1
`
	checkDuplicate(t, src, "foo")
}

func TestDuplicateRecordFieldInAliasRejected(t *testing.T) {
	// Checked clean and built a record carrying `x` twice, with the declared
	// `x : Int` gone: `P "first" "second"` gave { x = "second", x = "second" }.
	src := `module M exposing (..)
type alias P = { x : Int, x : String }
built = P "first" "second"
`
	msg := checkDuplicate(t, src, "x")
	if !strings.Contains(msg, "twice") {
		t.Fatalf("expected a repeated-field message, got: %v", msg)
	}
}

func TestDuplicateRecordFieldInAnnotationRejected(t *testing.T) {
	// The same defect reached through an annotation rather than an alias.
	src := `module M exposing (..)
f : { x : Int, x : String } -> Int
f r = r.x
`
	checkDuplicate(t, src, "x")
}

func TestDuplicateRecordFieldNestedRejected(t *testing.T) {
	// Records nest inside arrows, tuples, type applications and each other;
	// the walk has to reach all of them.
	src := `module M exposing (..)
type alias Box = { items : List { id : Int, id : String } }
`
	checkDuplicate(t, src, "id")
}

func TestAnnotationAndValuePairingStillWorks(t *testing.T) {
	// The floor: an annotation and its value share a name on essentially
	// every declaration in every example, and must stay legal.
	src := `module M exposing (..)
greeting : String
greeting = "hi"

count : Int -> Int
count n = n + 1

type alias Point = { x : Int, y : Int }

origin : Point
origin = Point 0 0

type Shape = Circle Int | Square Int

area : Shape -> Int
area s =
    case s of
        Circle r -> r * r
        Square w -> w * w
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("this module declares nothing twice and must check: %v", err)
	}
}

func TestOpenRowAliasClaimsNoConstructor(t *testing.T) {
	// An open row builds no constructor, so it does not claim the value
	// name and a constructor may legitimately share it.
	src := `module M exposing (..)
type alias Named r = { r | name : String }
type Tag = Named Int
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("an open row claims no value name: %v", err)
	}
}

// The same rule one level down: a group of patterns binds each name once.

func TestDuplicateFunctionParameterRejected(t *testing.T) {
	src := `module M exposing (..)
f x x = x
result = f 1 2
`
	checkDuplicate(t, src, "x")
}

func TestDuplicateNameInCaseBranchRejected(t *testing.T) {
	// `( x, x )` reads like a claim that the two halves are equal. It was
	// not one: it bound the second and dropped the first.
	src := `module M exposing (..)
f p =
    case p of
        ( x, x ) -> x
result = f ( 1, 2 )
`
	checkDuplicate(t, src, "x")
}

func TestDuplicateLetBindingRejected(t *testing.T) {
	src := `module M exposing (..)
main =
    let
        a = 1
        a = 2
    in
    a
`
	checkDuplicate(t, src, "a")
}

func TestDuplicateNameInConstructorPatternRejected(t *testing.T) {
	src := `module M exposing (..)
type Pair = Pair Int Int
f p =
    case p of
        Pair n n -> n
result = f (Pair 1 2)
`
	checkDuplicate(t, src, "n")
}

func TestWildcardsMayRepeat(t *testing.T) {
	// `_` binds nothing, which is the whole point of it.
	src := `module M exposing (..)
f _ _ = 1
g p =
    case p of
        ( _, _ ) -> 2
result = f 1 2 + g ( 1, 2 )
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("wildcards bind nothing and may repeat: %v", err)
	}
}

func TestShadowingAcrossScopesStillWorks(t *testing.T) {
	// Shadowing an OUTER name is a different thing from binding one name
	// twice in the same group, and stays legal.
	src := `module M exposing (..)
outer : Int
outer = 1

f : Int -> Int
f outer =
    let
        inner = outer + 1
    in
    let
        inner2 = inner + 1
    in
    inner2

g : Int -> Int
g n =
    let
        n2 = n + 1
    in
    case n2 of
        other -> other
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("shadowing across scopes must stay legal: %v", err)
	}
}
