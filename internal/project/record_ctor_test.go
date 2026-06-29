package project

import (
	"os"
	"path/filepath"
	"testing"

	"mar/internal/runtime"
)

// A record type alias doubles as a positional constructor (Elm-style). The
// loader desugars every such use into a record-building lambda before eval,
// so the constructor builds the fields in declaration order — including when
// partially applied or passed bare to a higher-order function (the
// `Random.map2 Shapes g g` shape). This drives the desugar end to end:
// parse -> desugar -> eval, then reads back the resulting records.
func TestRecordAliasConstructorRuntime(t *testing.T) {
	src := `module Main exposing (main)

type alias Point = { x : Int, y : Int }
type alias Pair a = { first : a, second : a }

-- full application
p : Point
p = Point 3 4
full : Int
full = p.x * 100 + p.y

-- partial application: the curried desugared lambda
mk : Int -> Point
mk = Point 2
partial : Int
partial = (mk 6).x * 10 + (mk 6).y

-- generic record alias as a constructor
pr : Pair Int
pr = Pair 5 9
generic : Int
generic = pr.first * 10 + pr.second

-- the bare, unapplied constructor passed as a value (Random.map2 Shapes ...)
build2 : (Int -> Int -> Point) -> Point
build2 f = f 8 9
hof : Int
hof =
    let built = build2 Point
    in built.x * 100 + built.y

main : Int
main = 0
`
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	rEnv, _, err := LoadIntoEnvForRuntime(entry, nil)
	if err != nil {
		t.Fatalf("load+eval failed: %v", err)
	}

	cases := map[string]int64{
		"Main.full":    304, // {x=3,y=4} -> 3*100+4
		"Main.partial": 26,  // (Point 2) 6 = {x=2,y=6} -> 2*10+6
		"Main.generic": 59,  // {first=5,second=9} -> 5*10+9
		"Main.hof":     809, // build2 Point = Point 8 9 = {x=8,y=9} -> 8*100+9
	}
	for name, want := range cases {
		v, ok := rEnv.Lookup(name)
		if !ok {
			t.Fatalf("%s not found in env", name)
		}
		got, ok := v.(runtime.VInt)
		if !ok {
			t.Fatalf("%s: expected VInt, got %T (%v)", name, v, v)
		}
		if got.V != want {
			t.Errorf("%s = %d, want %d (record built with wrong field order?)", name, got.V, want)
		}
	}
}
