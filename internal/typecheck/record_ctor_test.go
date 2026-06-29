package typecheck

import (
	"strings"
	"testing"
)

// A record type alias is also a positional constructor, the same as Elm:
// `type alias Point = { x : Int, y : Int }` introduces
// `Point : Int -> Int -> Point`. It resolves as an ordinary value, so it can
// be applied directly or passed bare to a higher-order function like
// Random.map2 — the shape a faithful "Touch me When" port needs.
func TestRecordAliasConstructorResolves(t *testing.T) {
	src := `module M exposing (..)
type Shape = Circle | Square
type alias Shapes = { left : Shape, right : Shape }
type Msg = Got Shapes
shapeGen : Random.Generator Shape
shapeGen = Random.uniform Circle [ Square ]
shapesGen : Random.Generator Shapes
shapesGen = Random.map2 Shapes shapeGen shapeGen
cmd : Cmd Msg
cmd = Random.generate Got shapesGen
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("record alias as constructor should typecheck; got: %v", err)
	}
}

// Because the constructor is a named value (not a desugared anonymous
// lambda), a misapplication is reported against the alias name with the
// offending argument pointed at — the same diagnostic quality as any named
// function, instead of a generic "cannot unify" anchored at the binding.
// This is the whole reason for registering it in the typechecker rather than
// desugaring before inference.
func TestRecordAliasConstructorNamesErrors(t *testing.T) {
	src := `module M exposing (..)
type Color = Pink | Blue
type alias ColoredShape = { color : Color, big : Bool }
type alias Shapes = { left : ColoredShape, right : ColoredShape }
bad : Shapes
bad = Shapes (ColoredShape Pink True) Pink
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected a type error for the misapplied constructor")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Shapes") || !strings.Contains(msg, "argument") {
		t.Errorf("error should name `Shapes` and point at the argument; got: %s", msg)
	}
}

// Only closed record aliases become constructors. A non-record alias like
// `type alias Id = Int` is transparent and introduces no value, so using it
// in expression position stays an unknown-identifier error.
func TestNonRecordAliasIsNotAConstructor(t *testing.T) {
	src := `module M exposing (..)
type alias Id = Int
bad : Id
bad = Id 5
`
	if _, err := checkSource(t, src); err == nil {
		t.Fatal("expected an error: a non-record alias is not a constructor")
	}
}
