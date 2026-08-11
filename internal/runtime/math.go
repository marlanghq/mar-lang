package runtime

import "fmt"

// Math — deterministic integer trigonometry and roots
// (docs/proposals/math.md). Every function here is total and every answer
// comes from the one generated quarter-wave table in math_table_gen.go, so
// Go, the browser and iOS return the same integer for the same input. That
// is not a nicety: lendas replays one rules engine on both sides,
// pulse-runner ships a bot that proves its level is beatable, and time
// travel re-runs old messages against the current code. A libm call would
// put three different implementations of sine between a program and its
// answer.
//
// The JS mirror lives in the "Math (integer trigonometry)" section of
// internal/jsserve/runtime.js; the Swift one in MarMath.swift. All three are
// line-for-line ports of each other and math_conformance_test.go holds them
// to it.

const (
	deciFull    = 3600 // deci-degrees in a full turn
	deciQuarter = 900  // deci-degrees in a quarter turn
	// brads — the 256-step turn seasons-gp and vortex already count in.
	bradsPerTurn = 256
	// atan2 halves its legs below this so a product with a table entry
	// (< 2^10) stays inside the 53 bits an Int has (ADR 0021).
	atanLegLimit = 1 << 40
)

// posMod is the positive modulo every Angle constructor wraps through, which
// is what lets any Int be a valid argument and makes a negative angle mean
// what it should.
func posMod(n, m int64) int64 { return ((n % m) + m) % m }

// sinDeci folds the whole circle onto the quarter-wave table: quadrants 1
// and 3 read it backwards, 2 and 3 negate. No interpolation, because the
// table already holds every representable input.
func sinDeci(a int64) int64 {
	q := a / deciQuarter
	r := a - q*deciQuarter
	v := sinQuarter[r]
	if q&1 == 1 {
		v = sinQuarter[deciQuarter-r]
	}
	if q&2 == 0 {
		return v
	}
	return -v
}

func cosDeci(a int64) int64 { return sinDeci(posMod(a+deciQuarter, deciFull)) }

// isqrtInt is integer Newton, floored: it converges from above and stops the
// moment it would climb back, which is exactly floor(sqrt(n)) for n >= 1.
func isqrtInt(n int64) int64 {
	if n <= 0 {
		return 0
	}
	x := n
	y := (x + 1) / 2
	for y < x {
		x = y
		y = (x + n/x) / 2
	}
	return x
}

// atan2Deci returns the angle of the vector (x, y) in deci-degrees, 0..3599,
// searching the SAME table sin reads — which is what makes the two
// structurally unable to disagree about where 45.0° is.
//
// The angle is chosen as the nearer of the two table entries the direction
// falls between, measured by cross product: for a candidate k, the quantity
// cos(k)·ay − sin(k)·ax is |v| times the sine of the angular error, so
// comparing it against the same quantity for k+1 compares the two errors
// directly. A tie keeps the lower angle. This IS the specification: the true
// arctangent is irrational, so "round the real answer" is not something an
// integer implementation can promise, and three runtimes agreeing matters
// more than a definition none of them can meet.
func atan2Deci(y, x int64) int64 {
	if x == 0 && y == 0 {
		return 0
	}
	ax, ay := x, y
	if ax < 0 {
		ax = -ax
	}
	if ay < 0 {
		ay = -ay
	}
	// Fold into the first octant so the search range is 0..450.
	swapped := ay > ax
	if swapped {
		ax, ay = ay, ax
	}
	// Halving is exact integer arithmetic, so all three runtimes drop the
	// same bits. Only reachable for legs above 2^40, where the discarded
	// precision is many orders below one deci-degree.
	for ax >= atanLegLimit {
		ax /= 2
		ay /= 2
	}
	// Largest k with tan(k) <= ay/ax, i.e. sin(k)·ax <= cos(k)·ay.
	lo, hi := int64(0), int64(450)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if sinQuarter[mid]*ax <= sinQuarter[deciQuarter-mid]*ay {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	inner := lo
	if inner < 450 {
		d0 := sinQuarter[deciQuarter-inner]*ay - sinQuarter[inner]*ax
		d1 := sinQuarter[inner+1]*ax - sinQuarter[deciQuarter-inner-1]*ay
		if d1 < d0 {
			inner++
		}
	}
	if swapped {
		inner = deciQuarter - inner
	}
	var a int64
	switch {
	case x >= 0 && y >= 0:
		a = inner
	case x >= 0:
		a = deciFull - inner
	case y >= 0:
		a = 1800 - inner
	default:
		a = 1800 + inner
	}
	return posMod(a, deciFull)
}

func mathBuiltins() map[string]Value {
	return map[string]Value{
		// Angle constructors. Each names its unit and wraps, so any Int is
		// valid; each also reduces BEFORE multiplying, so `Math.degrees` of
		// a huge Int cannot overflow on the way to a value that was always
		// going to land in 0..3599.
		"mathDegrees":     nativeFn(1, angleFrom("Math.degrees", 360, 10)),
		"mathDeciDegrees": nativeFn(1, angleFrom("Math.deciDegrees", deciFull, 1)),
		// turns counts in brads — 256 to the turn, the unit seasons-gp and
		// vortex already use. 3600/256 is not a whole number of
		// deci-degrees, so a single brad floors; a full turn is exact.
		"mathTurns": nativeFn(1, func(args []Value) (Value, error) {
			n, err := wantInt("Math.turns", args[0])
			if err != nil {
				return nil, err
			}
			return VAngle{Deci: posMod(n, bradsPerTurn) * deciFull / bradsPerTurn}, nil
		}),

		// The algebra ships with the type on purpose: an angle nobody can
		// add is not a safer Int, it is one every caller converts out of,
		// adds, and converts back. Wrapping is built in, so nothing in a
		// game ever writes `modBy 3600 (h + d + 3600)` again.
		"mathAdd":      nativeFn(2, angleBinop("Math.add", func(a, b int64) int64 { return a + b })),
		"mathSubtract": nativeFn(2, angleBinop("Math.subtract", func(a, b int64) int64 { return a - b })),
		"mathOpposite": nativeFn(1, func(args []Value) (Value, error) {
			a, err := wantAngle("Math.opposite", args[0])
			if err != nil {
				return nil, err
			}
			return VAngle{Deci: posMod(a+1800, deciFull)}, nil
		}),

		"mathSin": nativeFn(1, angleToInt("Math.sin", sinDeci)),
		"mathCos": nativeFn(1, angleToInt("Math.cos", cosDeci)),

		"mathAtan2": nativeFn(2, func(args []Value) (Value, error) {
			y, err := wantInt("Math.atan2", args[0])
			if err != nil {
				return nil, err
			}
			x, err := wantInt("Math.atan2", args[1])
			if err != nil {
				return nil, err
			}
			return VAngle{Deci: atan2Deci(y, x)}, nil
		}),

		"mathIsqrt": nativeFn(1, func(args []Value) (Value, error) {
			n, err := wantInt("Math.isqrt", args[0])
			if err != nil {
				return nil, err
			}
			return VInt{V: isqrtInt(n)}, nil
		}),
	}
}

// angleFrom builds a unit-named Angle constructor: reduce into one turn of
// the caller's unit, then scale to deci-degrees.
func angleFrom(name string, perTurn, scale int64) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		n, err := wantInt(name, args[0])
		if err != nil {
			return nil, err
		}
		return VAngle{Deci: posMod(n, perTurn) * scale}, nil
	}
}

func angleBinop(name string, op func(a, b int64) int64) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		a, err := wantAngle(name, args[0])
		if err != nil {
			return nil, err
		}
		b, err := wantAngle(name, args[1])
		if err != nil {
			return nil, err
		}
		return VAngle{Deci: posMod(op(a, b), deciFull)}, nil
	}
}

func angleToInt(name string, f func(int64) int64) func([]Value) (Value, error) {
	return func(args []Value) (Value, error) {
		a, err := wantAngle(name, args[0])
		if err != nil {
			return nil, err
		}
		return VInt{V: f(a)}, nil
	}
}

func wantInt(name string, v Value) (int64, error) {
	n, ok := v.(VInt)
	if !ok {
		return 0, fmt.Errorf("%s: expected Int (got %T)", name, v)
	}
	return n.V, nil
}

func wantAngle(name string, v Value) (int64, error) {
	a, ok := v.(VAngle)
	if !ok {
		return 0, fmt.Errorf("%s: expected Angle (got %T)", name, v)
	}
	return a.Deci, nil
}
