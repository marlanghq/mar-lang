package runtime

import (
	"math/big"
	"strings"
	"testing"
)

// A Duration is a whole number of milliseconds inside the range of Int, so a
// constructor whose interval does not fit refuses, naming itself and its
// argument. `Time.weeks n` with an `n` known only at runtime cannot be ruled
// out by the compiler, so this is a runtime error, in one wording on all three
// runtimes: the browser's and the phone's are run against the same sentence
// in internal/jsserve and internal/iosbundle duration_range_test.go.
func TestADurationTooLongForIntIsRefused(t *testing.T) {
	if _, err := durationOf("Time.weeks", MaxSafeInt, 7*24*60*60*1000); err == nil {
		t.Fatal("Time.weeks of the largest Int was accepted")
	} else if !strings.Contains(err.Error(), "Duration out of range: Time.weeks 9007199254740991 is outside the range of a Duration") {
		t.Errorf("the refusal does not say what it is or where it came from: %v", err)
	}
	// The edge, inclusive on both sides.
	for _, c := range []struct {
		n, unit, want int64
	}{
		{MaxSafeInt, 1, MaxSafeInt},
		{-MaxSafeInt, 1, -MaxSafeInt},
		{MaxSafeInt / 1000, 1000, MaxSafeInt / 1000 * 1000},
		{1, 7 * 24 * 60 * 60 * 1000, 604800000},
	} {
		d, err := durationOf("Time.x", c.n, c.unit)
		if err != nil || d.Millis != c.want {
			t.Errorf("%d x %d: got %v, %v; want %d", c.n, c.unit, d, err, c.want)
		}
	}
	if _, err := durationOf("Time.seconds", MaxSafeInt/1000+1, 1000); err == nil {
		t.Error("one second past the edge was accepted")
	}
}

// The same hole, in the API Mar's whole Decimal doctrine exists to protect.
//
// `Decimal.toCents` and the Decimal-to-Int conversions bounded their result at
// int64 rather than at Mar's Int, and the browser bounded its at 53 bits. So
// for any value in the window between the two — 2^53 up to 2^63 — the same
// money computation returned a number on the server and the phone and was
// refused in the page. 2^53 itself, one past MaxSafeInt, was enough:
//
//	Decimal.truncate 9007199254740992.0   server: 9007199254740992
//	                                      browser: refused
//
// Under the goal's terms this cannot move earlier than runtime either: the
// Decimal is only known when it is computed. What it can be is one answer.
func TestDecimalToIntUsesMarsBoundAndNotInt64s(t *testing.T) {
	dec := func(digits string, scale int) VDecimal {
		n, ok := new(big.Int).SetString(digits, 10)
		if !ok {
			t.Fatalf("bad literal %q", digits)
		}
		return VDecimal{Coef: n, Scale: scale}
	}

	// One past the edge: an int64, and not a Mar Int.
	if _, err := decToInt(dec("9007199254740992", 0), "Down"); err == nil {
		t.Error("2^53 was accepted as an Int; that is the browser's first refusal")
	}
	// Deep inside int64's range and far outside Mar's.
	if _, err := decToInt(dec("9223372036854775807", 0), "Down"); err == nil {
		t.Error("int64's ceiling was accepted as an Int")
	}
	// The edge itself still works, from both directions.
	for _, c := range []struct {
		digits string
		want   int64
	}{
		{"9007199254740991", MaxSafeInt},
		{"-9007199254740991", MinSafeInt},
	} {
		got, err := decToInt(dec(c.digits, 0), "Down")
		if err != nil {
			t.Errorf("%s: %v", c.digits, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s became %d, want %d", c.digits, got, c.want)
		}
	}
}
