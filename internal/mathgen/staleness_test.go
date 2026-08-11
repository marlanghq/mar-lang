package mathgen

import (
	"math"
	"os"
	"testing"
)

// TestGeneratedTablesAreCurrent is the lock on the whole scheme: it regenerates
// all three outputs in memory and fails if a committed copy disagrees. Without
// it, editing the table and regenerating two of the three runtimes would be a
// silent divergence of exactly the kind the module exists to prevent — a game
// that aims one degree differently on the phone than in the browser, with a
// green suite.
func TestGeneratedTablesAreCurrent(t *testing.T) {
	js, err := os.ReadFile("../jsserve/runtime.js")
	if err != nil {
		t.Fatalf("reading runtime.js: %v", err)
	}
	spliced, err := SpliceJS(string(js))
	if err != nil {
		t.Fatal(err)
	}
	if spliced != string(js) {
		t.Errorf("the generated sine table in internal/jsserve/runtime.js is stale.\nFix: go generate ./internal/mathgen")
	}

	goSrc, err := os.ReadFile("../runtime/math_table_gen.go")
	if err != nil {
		t.Fatalf("reading math_table_gen.go (run `go generate ./internal/mathgen` if it does not exist): %v", err)
	}
	if string(goSrc) != GoFile() {
		t.Errorf("internal/runtime/math_table_gen.go is stale.\nFix: go generate ./internal/mathgen")
	}

	swift, err := os.ReadFile("../iosbundle/template/Sources/MarMathTable.swift")
	if err != nil {
		t.Fatalf("reading MarMathTable.swift (run `go generate ./internal/mathgen` if it does not exist): %v", err)
	}
	if string(swift) != SwiftFile() {
		t.Errorf("internal/iosbundle/template/Sources/MarMathTable.swift is stale.\nFix: go generate ./internal/mathgen")
	}
}

// TestSinQuarterIsTheSpec pins the properties the kernels rely on, so a change
// to the generator that still produces 901 plausible numbers cannot pass. The
// endpoints are the ones a fold depends on: sinQuarter[0] must be exactly 0 and
// sinQuarter[900] exactly 1000, or sin(180°) is not 0 and cos(0°) is not 1000.
func TestSinQuarterIsTheSpec(t *testing.T) {
	table := SinQuarter()
	if len(table) != Quarter+1 {
		t.Fatalf("table has %d entries, want %d", len(table), Quarter+1)
	}
	if table[0] != 0 {
		t.Errorf("sinQuarter[0] = %d, want 0", table[0])
	}
	if table[Quarter] != Scale {
		t.Errorf("sinQuarter[%d] = %d, want %d", Quarter, table[Quarter], Scale)
	}
	for i, v := range table {
		if v < 0 || v > Scale {
			t.Fatalf("sinQuarter[%d] = %d, outside 0..%d", i, v, Scale)
		}
		if i > 0 && v < table[i-1] {
			t.Fatalf("sinQuarter is not monotonic at %d: %d after %d", i, v, table[i-1])
		}
		// Each entry is within half a unit of the real sine — the check that
		// the rounding is a rounding and not, say, a truncation.
		exact := math.Sin(float64(i)/10*math.Pi/180) * Scale
		if math.Abs(exact-float64(v)) > 0.5 {
			t.Fatalf("sinQuarter[%d] = %d, but sin is %.4f", i, v, exact)
		}
	}
	// The named angles a reader would check by hand.
	for _, c := range []struct{ deci, want int }{
		{300, 500},  // 30°
		{450, 707},  // 45°
		{600, 866},  // 60°
		{900, 1000}, // 90°
	} {
		if table[c.deci] != c.want {
			t.Errorf("sin(%.1f°) = %d, want %d", float64(c.deci)/10, table[c.deci], c.want)
		}
	}
}
