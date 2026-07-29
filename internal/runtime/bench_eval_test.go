package runtime

import (
	"testing"

	"mar/internal/parser"
	"mar/internal/typecheck"
)

const benchSrc = `module B exposing (work)


step : Int -> Int -> Int
step acc x =
    acc + x * 2 - 1


work : Int
work =
    List.foldl step 0 (List.range 1 20000)
`

func BenchmarkEvalHotLoop(b *testing.B) {
	mod, err := parser.Parse(benchSrc)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loaded, err := LoadModule(mod)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := loaded.Get("work"); err != nil {
			b.Fatal(err)
		}
	}
}

// A draw-list shaped workload: build records in a list, the way a canvas frame
// does. Less native-call-dense than pure arithmetic, so closer to a real frame.
const benchDrawSrc = `module B exposing (frame)


type alias Shape =
    { x : Int, y : Int, w : Int, h : Int, color : String }


mk : Int -> Shape
mk i =
    { x = i * 3, y = i * 7, w = 16, h = 16, color = "#ff0000" }


frame : Int
frame =
    List.length (List.map mk (List.range 1 20000))
`

func BenchmarkEvalDrawList(b *testing.B) {
	mod, err := parser.Parse(benchDrawSrc)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		loaded, err := LoadModule(mod)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := loaded.Get("frame"); err != nil {
			b.Fatal(err)
		}
	}
}
