package runtime

import (
	"fmt"
	"testing"
)

// applyN curries a builtin over several args (Random.int 1 6, step gen seed…).
func applyN(t *testing.T, f Value, args ...Value) Value {
	t.Helper()
	cur := f
	for _, a := range args {
		var err error
		cur, err = apply(cur, a)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
	return cur
}

// TestRandomPCGDeterministic pins the raw PCG output sequence from a fixed
// state. These exact numbers are the conformance target the JS and Swift
// runtimes must reproduce bit-for-bit.
func TestRandomPCGDeterministic(t *testing.T) {
	state := scramble(42)
	s := state
	var outs []uint32
	for i := 0; i < 8; i++ {
		var o uint32
		s, o = pcgStep(s)
		outs = append(outs, o)
	}
	// Conformance anchor: runtime.js (BigInt) and Swift (UInt64) must produce
	// these EXACT numbers for the same seed, or a cross-runtime replay diverges.
	if state != 4159066171780167020 {
		t.Fatalf("scramble(42) = %d, want 4159066171780167020", state)
	}
	want := []uint32{242089394, 3457789919, 3637502659, 19596830, 3604887170, 2990774977, 1309574617, 1245861208}
	for i := range want {
		if outs[i] != want[i] {
			t.Fatalf("pcg out[%d] = %d, want %d", i, outs[i], want[i])
		}
	}
}

// TestRandomStepThreadsSeed exercises the builtins the way Mar code does:
// initialSeed → step a `Random.int 1 6` generator, threading the seed.
func TestRandomStepThreadsSeed(t *testing.T) {
	b := randomBuiltins()
	seed0 := applyN(t, b["randomInitialSeed"], VInt{V: 42})
	die := applyN(t, b["randomInt"], VInt{V: 1}, VInt{V: 6}) // Generator Int

	var rolls []int64
	seed := seed0
	for i := 0; i < 12; i++ {
		res := applyN(t, b["randomStep"], die, seed).(VTuple)
		roll := res.Members[0].(VInt).V
		if roll < 1 || roll > 6 {
			t.Fatalf("die out of range: %d", roll)
		}
		rolls = append(rolls, roll)
		seed = res.Members[1]
	}
	// Conformance anchor for the int-range mapping (JS/Swift must match).
	wantDice := []int64{3, 6, 2, 3, 3, 2, 2, 5, 2, 2, 3, 4}
	for i := range wantDice {
		if rolls[i] != wantDice[i] {
			t.Fatalf("die[%d] = %d, want %d", i, rolls[i], wantDice[i])
		}
	}

	// step is pure: the SAME seed always yields the same (value, nextSeed).
	r1 := applyN(t, b["randomStep"], die, seed0)
	r2 := applyN(t, b["randomStep"], die, seed0)
	if fmt.Sprint(r1) != fmt.Sprint(r2) {
		t.Fatal("Random.step is not pure")
	}
}
