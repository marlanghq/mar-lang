package runtime

import (
	"math"
	"testing"
)

// The conformance corpus pins a few dozen answers and holds all three runtimes
// to them. These tests do the other half: they sweep the whole input range,
// which is what catches a fold that is right at the named angles and wrong
// between them.

func TestSinCosAgreeWithRealTrigEverywhere(t *testing.T) {
	for a := int64(0); a < deciFull; a++ {
		rad := float64(a) / 10 * math.Pi / 180
		if got, want := sinDeci(a), math.Sin(rad)*1000; math.Abs(float64(got)-want) > 0.5 {
			t.Fatalf("sin(%.1f°) = %d, want %.4f", float64(a)/10, got, want)
		}
		if got, want := cosDeci(a), math.Cos(rad)*1000; math.Abs(float64(got)-want) > 0.5 {
			t.Fatalf("cos(%.1f°) = %d, want %.4f", float64(a)/10, got, want)
		}
	}
}

// The quadrant fold has to land the axes exactly, because a game that turns to
// face north and gets 999 instead of 1000 drifts a little further every tick.
func TestSinCosAreExactOnTheAxes(t *testing.T) {
	for _, c := range []struct{ a, sin, cos int64 }{
		{0, 0, 1000},
		{900, 1000, 0},
		{1800, 0, -1000},
		{2700, -1000, 0},
	} {
		if got := sinDeci(c.a); got != c.sin {
			t.Errorf("sin(%d) = %d, want %d", c.a, got, c.sin)
		}
		if got := cosDeci(c.a); got != c.cos {
			t.Errorf("cos(%d) = %d, want %d", c.a, got, c.cos)
		}
	}
}

func TestIsqrtIsTheFlooredRoot(t *testing.T) {
	for n := int64(0); n < 20000; n++ {
		r := isqrtInt(n)
		if r*r > n || (r+1)*(r+1) <= n {
			t.Fatalf("isqrt(%d) = %d, which is not the floored root", n, r)
		}
	}
	// Perfect squares and their neighbours, up the range: the boundary
	// Newton's stopping rule is most likely to get wrong.
	for _, k := range []int64{1, 2, 3, 1000, 65536, 3037000499} {
		sq := k * k
		for _, c := range []struct{ n, want int64 }{{sq - 1, k - 1}, {sq, k}, {sq + 1, k}} {
			if got := isqrtInt(c.n); got != c.want {
				t.Errorf("isqrt(%d) = %d, want %d", c.n, got, c.want)
			}
		}
	}
	// Negative and zero are defined, not an error: every function in Math is
	// total, so there is no input a caller has to guard.
	for _, n := range []int64{0, -1, -1 << 40} {
		if got := isqrtInt(n); got != 0 {
			t.Errorf("isqrt(%d) = %d, want 0", n, got)
		}
	}
}

// atan2 has to be the inverse of sin/cos at every representable angle: take a
// point on the unit circle at angle a, ask for its angle back, get a. This is
// the property that makes `Math.atan2` and `Math.sin` usable in the same
// expression, and it only holds because both read the same table.
func TestAtan2InvertsTheCircle(t *testing.T) {
	for a := int64(0); a < deciFull; a++ {
		// Scale up so the rounded table values still point accurately enough
		// to name the deci-degree they came from.
		x := cosDeci(a) * 1000
		y := sinDeci(a) * 1000
		got := atan2Deci(y, x)
		diff := posMod(got-a+1800, deciFull) - 1800
		if diff < -1 || diff > 1 {
			t.Fatalf("atan2 of the point at %d deci-degrees came back %d (off by %d)", a, got, diff)
		}
	}
}

func TestAtan2OnTheAxesAndOrigin(t *testing.T) {
	for _, c := range []struct{ y, x, want int64 }{
		{0, 0, 0}, // defined, not an error
		{0, 1, 0},
		{1, 1, 450},
		{1, 0, 900},
		{1, -1, 1350},
		{0, -1, 1800},
		{-1, -1, 2250},
		{-1, 0, 2700},
		{-1, 1, 3150},
	} {
		if got := atan2Deci(c.y, c.x); got != c.want {
			t.Errorf("atan2(%d, %d) = %d, want %d", c.y, c.x, got, c.want)
		}
	}
}

// Legs above 2^40 are halved before the search so a product with a table entry
// stays inside 53 bits. The halving is exact integer arithmetic: the point of
// this test is that the answer is still right, not that no halving happened.
func TestAtan2SurvivesHugeLegs(t *testing.T) {
	const big = int64(1) << 50
	for _, c := range []struct {
		y, x, want int64
	}{
		{big, big, 450},
		{big, 0, 900},
		{0, big, 0},
		{-big, big, 3150},
	} {
		if got := atan2Deci(c.y, c.x); got != c.want {
			t.Errorf("atan2(%d, %d) = %d, want %d", c.y, c.x, got, c.want)
		}
	}
}

// Every constructor wraps, so any Int is a valid argument: including the ones
// that would overflow if the scaling happened before the reduction.
func TestAngleConstructorsWrapWithoutOverflow(t *testing.T) {
	env := mathBuiltins()
	call := func(name string, args ...Value) Value {
		fn := env[name].(VFn)
		v, err := fn.Native(args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return v
	}
	deci := func(v Value) int64 { return v.(VAngle).Deci }

	if got := deci(call("mathDegrees", VInt{V: 1 << 52})); got < 0 || got >= deciFull {
		t.Errorf("Math.degrees of a huge Int produced %d, outside 0..3599", got)
	}
	for _, c := range []struct {
		name string
		in   int64
		want int64
	}{
		{"mathDegrees", 45, 450},
		{"mathDegrees", 360, 0},
		{"mathDegrees", -90, 2700},
		{"mathDeciDegrees", 3600, 0},
		{"mathDeciDegrees", -1, 3599},
		{"mathTurns", 64, 900},   // a quarter turn in brads
		{"mathTurns", 256, 0},    // a full turn is exact
		{"mathTurns", 1, 14},     // one brad floors: 3600/256 is 14.0625
		{"mathTurns", -64, 2700}, // and negatives wrap the same way
	} {
		if got := deci(call(c.name, VInt{V: c.in})); got != c.want {
			t.Errorf("%s %d = %d deci-degrees, want %d", c.name, c.in, got, c.want)
		}
	}
}
