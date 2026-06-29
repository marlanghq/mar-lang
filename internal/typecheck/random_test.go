package typecheck

import "testing"

// Random is Elm-style: build a Generator with the combinators, run it with
// Random.generate (a Cmd). uniform/map2 compose; generate bridges to the loop.
func TestRandomGeneratorChecks(t *testing.T) {
	src := `module M exposing (..)
type Shape = Circle | Square
type alias Shapes = { left : Shape, right : Shape }
type Msg = Got Shapes
shapeGen : Random.Generator Shape
shapeGen = Random.uniform Circle [ Square ]
shapesGen : Random.Generator Shapes
shapesGen = Random.map2 (\l r -> { left = l, right = r }) shapeGen shapeGen
cmd : Cmd Msg
cmd = Random.generate Got shapesGen
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("Random generators + generate should typecheck; got: %v", err)
	}
}

// A Generator is not a Cmd: it can only reach the MVU loop through
// Random.generate. Returning a bare Generator where a Cmd is expected is a type
// error — the same discipline that keeps Task/Cmd/Sub distinct.
func TestRandomGeneratorIsNotCmd(t *testing.T) {
	src := `module M exposing (..)
type Msg = Got Int
bad : Cmd Msg
bad = Random.uniform 1 [ 2, 3 ]
`
	if _, err := checkSource(t, src); err == nil {
		t.Fatal("expected a type error: a Generator is not a Cmd (must go through Random.generate)")
	}
}
