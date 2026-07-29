package runtime

import "testing"

// TestNumericKit locks the numeric builtins' contract, with the negative cases
// front and center: modBy and remainderBy DISAGREE there on purpose, and eleven
// apps migrated onto them, so a silent flip would corrupt every wrapped
// coordinate and animation phase in the games.
func TestNumericKit(t *testing.T) {
	call := func(name string, args ...Value) Value {
		fn, ok := stdlib()[name]
		if !ok {
			t.Fatalf("%s is not in stdlib()", name)
		}
		nf, ok := fn.(VFn)
		if !ok || nf.Native == nil {
			t.Fatalf("%s is not a native fn", name)
		}
		out, err := nf.Native(args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return out
	}
	i := func(n int64) Value { return VInt{V: n} }
	wantInt := func(label string, got Value, want int64) {
		iv, ok := got.(VInt)
		if !ok {
			t.Errorf("%s: got %T, want VInt", label, got)
			return
		}
		if iv.V != want {
			t.Errorf("%s = %d, want %d", label, iv.V, want)
		}
	}

	wantInt("max 3 7", call("max", i(3), i(7)), 7)
	wantInt("max 7 3", call("max", i(7), i(3)), 7)
	wantInt("min 3 7", call("min", i(3), i(7)), 3)
	wantInt("clamp 0 10 42", call("clamp", i(0), i(10), i(42)), 10)
	wantInt("clamp 0 10 -5", call("clamp", i(0), i(10), i(-5)), 0)
	wantInt("clamp 0 10 5", call("clamp", i(0), i(10), i(5)), 5)
	wantInt("abs -5", call("abs", i(-5)), 5)
	wantInt("abs 5", call("abs", i(5)), 5)

	// The divisor comes first, like Elm. `modBy 3 10` is "10 mod 3".
	wantInt("modBy 3 10", call("modBy", i(3), i(10)), 1)
	wantInt("remainderBy 3 10", call("remainderBy", i(3), i(10)), 1)

	// Negatives: modBy follows the DIVISOR's sign (what wrapping wants),
	// remainderBy the DIVIDEND's (what `//` produces).
	wantInt("modBy 8 -1", call("modBy", i(8), i(-1)), 7)
	wantInt("remainderBy 8 -1", call("remainderBy", i(8), i(-1)), -1)
	wantInt("modBy 3 -4", call("modBy", i(3), i(-4)), 2)
	wantInt("remainderBy 3 -4", call("remainderBy", i(3), i(-4)), -1)

	// remainderBy is the one that keeps `(n // d) * d + r == n`.
	for _, n := range []int64{-9, -4, -1, 0, 1, 7, 13} {
		for _, d := range []int64{3, 8, -5} {
			r := call("remainderBy", i(d), i(n)).(VInt).V
			q := n / d // Go truncates toward zero, same as Mar's //
			if q*d+r != n {
				t.Errorf("remainderBy %d %d: %d*%d+%d != %d", d, n, q, d, r, n)
			}
		}
	}

	// Total at zero, matching how `//` refuses to trap.
	wantInt("modBy 0 5", call("modBy", i(0), i(5)), 0)
	wantInt("remainderBy 0 5", call("remainderBy", i(0), i(5)), 0)
}
