package runtime

import "fmt"

// Int is 53 bits wide, and leaving that range is an error rather than a
// number nobody asked for.
//
// The width is not arbitrary and not a Go decision: it is the widest integer
// all three runtimes can agree on. JavaScript has no integers: `Int` is a
// double there, so above 2^53 the browser stops being able to tell
// 9007199254740993 from 9007199254740992 and says nothing about it. Before
// this bound, `Int` meant three different things: Go wrapped around, Swift
// trapped, and JS quietly lost precision for values that were never near any
// documented limit.
//
// Picking 64 bits would have meant the browser being wrong; picking "whatever
// the platform does" is what was already happening. Mar removed Float and
// added exact Decimal (ADR 0004) precisely so arithmetic could not drift, and
// an Int backed by a float64 in the browser is that drift coming back in
// through a different door.
//
// The range is symmetric, matching JavaScript's own MIN_SAFE_INTEGER /
// MAX_SAFE_INTEGER rather than two's-complement's off-by-one floor, because a
// range that is the same in both directions is one less thing to remember and
// one less place the three runtimes could disagree.
const (
	MaxSafeInt int64 = 1<<53 - 1   // 9007199254740991
	MinSafeInt int64 = -MaxSafeInt // -9007199254740991
)

// inIntRange is the single predicate. Everything that can produce or admit an
// Int goes through it, so there is one answer to "is this a Mar Int".
func inIntRange(v int64) bool {
	return v >= MinSafeInt && v <= MaxSafeInt
}

// intOverflow is what arithmetic raises. It names the operands because the
// interesting question is always which computation grew, not which line
// noticed.
func intOverflow(a int64, op string, b int64) error {
	return fmt.Errorf("Int overflow: %d %s %d is outside the range of Int (%d to %d)",
		a, op, b, MinSafeInt, MaxSafeInt)
}

// intOutOfRange is what a decode boundary raises. Different wording on
// purpose: nothing was computed, a value simply arrived that Mar has no way to
// represent, and the useful thing to say is where it came from.
func intOutOfRange(source string, v int64) error {
	return fmt.Errorf("Int out of range: %d from %s is outside the range of Int (%d to %d)",
		v, source, MinSafeInt, MaxSafeInt)
}

func addInts(a, b int64) (int64, error) {
	// Both operands are already in range, so the sum is at most 2^54 and
	// cannot wrap an int64. Only the range needs checking.
	c := a + b
	if !inIntRange(c) {
		return 0, intOverflow(a, "+", b)
	}
	return c, nil
}

func subInts(a, b int64) (int64, error) {
	c := a - b
	if !inIntRange(c) {
		return 0, intOverflow(a, "-", b)
	}
	return c, nil
}

func mulInts(a, b int64) (int64, error) {
	// A product of two in-range operands reaches 2^106, which DOES wrap an
	// int64, so the range check alone would read a wrapped value as fine.
	// The division identity catches the wrap first; `a == -1 && b ==
	// math.MinInt64`, the one case it misses, cannot occur, because the
	// operands are bounded well inside that.
	c := a * b
	if a != 0 && c/a != b {
		return 0, intOverflow(a, "*", b)
	}
	if !inIntRange(c) {
		return 0, intOverflow(a, "*", b)
	}
	return c, nil
}

// checkedVInt adapts the (int64, error) helpers to the Value world the binops
// live in, so the range check reads as one line at each operator.
func checkedVInt(v int64, err error) (Value, error) {
	if err != nil {
		return nil, err
	}
	return VInt{V: v}, nil
}
