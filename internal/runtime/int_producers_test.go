package runtime

import (
	"math"
	"math/big"
	"os"
	"strings"
	"testing"
)

// `Time.toSeconds` was the one Int-producing site that did not go through
// intrange.go, and the file's own claim is that everything does: "Everything
// that can produce or admit an Int goes through it, so there is one answer to
// 'is this a Mar Int'."
//
// A Duration is a float64 holding seconds, and `Time.weeks` of a legal Int is
// about 5e21 of them. Asking for that as an Int used to produce three
// different wrong answers, none of them an error:
//
//	server   9223372036854775807   int64's ceiling, silently
//	browser  5.4432e21             a float, which is not an Int at all
//	phone    (crash)               Int(_:) traps on a Double that large
//
// The expression is legal Mar and type-checks. It cannot be caught earlier
// than this: `Time.weeks n` with an `n` that is only known at runtime is not
// something the compiler or the boot pass can rule out. So it stays a runtime
// error — but one error, in one wording, on all three.
func TestADurationTooBigForIntIsRefusedRatherThanGuessed(t *testing.T) {
	tooBig := float64(MaxSafeInt) * 604800 // Time.weeks MaxSafeInt

	if _, err := intFromSeconds("Time.toSeconds", tooBig); err == nil {
		t.Fatal("a duration of 5e21 seconds was accepted as an Int")
	} else if !strings.Contains(err.Error(), "Int out of range") ||
		!strings.Contains(err.Error(), "Time.toSeconds") {
		t.Errorf("the refusal does not say what it is or where it came from: %v", err)
	}

	// The boundary is inclusive on both sides, and nothing inside it moves.
	for _, c := range []struct {
		seconds float64
		want    int64
	}{
		{float64(MaxSafeInt), MaxSafeInt},
		{float64(MinSafeInt), MinSafeInt},
		{1.5, 1},
		{-1.5, -1}, // toward zero, matching Math.trunc and .towardZero
		{0, 0},
	} {
		got, err := intFromSeconds("Time.toSeconds", c.seconds)
		if err != nil {
			t.Errorf("%v seconds: %v", c.seconds, err)
			continue
		}
		if got != c.want {
			t.Errorf("%v seconds became %d, want %d", c.seconds, got, c.want)
		}
	}

	// One past the edge, on both sides, and the shapes a float can take that
	// an Int cannot.
	for _, bad := range []float64{
		float64(MaxSafeInt) + 1,
		float64(MinSafeInt) - 1,
		math.Inf(1),
		math.Inf(-1),
		math.NaN(),
	} {
		if _, err := intFromSeconds("Time.toSeconds", bad); err == nil {
			t.Errorf("%v seconds was accepted as an Int", bad)
		}
	}
}

// The refusal has to read the same on the phone and in the browser, and there
// is no harness that can run a program to failure on all three: a Mar error is
// not catchable by design, so it cannot come back as a String the way every
// value in internal/parityfixture does. What can be checked is that the three
// sources carry the same sentence — the same technique the repository already
// uses for `no case branch matched`, and honest about proving the wording
// rather than the behaviour.
func TestTheRefusalIsWordedTheSameInAllThreeRuntimes(t *testing.T) {
	for _, f := range []struct{ label, path string }{
		{"browser", "../jsserve/runtime.js"},
		{"phone", "../iosbundle/template/Sources/MarIntRange.swift"},
	} {
		b, err := readFileForTest(f.path)
		if err != nil {
			t.Fatalf("%s: %v", f.label, err)
		}
		for _, fragment := range []string{
			"Int out of range: ",
			" seconds from ",
			" is outside the range of Int",
		} {
			if !strings.Contains(b, fragment) {
				t.Errorf("the %s runtime's refusal is missing %q; the three wordings have drifted",
					f.label, fragment)
			}
		}
	}
}

func readFileForTest(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
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
