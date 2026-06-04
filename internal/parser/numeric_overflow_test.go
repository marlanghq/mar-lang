package parser

import (
	"strings"
	"testing"
)

// An integer literal wider than int64 must be a clear, positioned error rather
// than a silently-truncated wrong value.
func TestIntLiteralOutOfRangeErrors(t *testing.T) {
	// 20 nines > max int64 (9223372036854775807).
	err := mustParseErr(t, "module M exposing (..)\nx = 99999999999999999999\n")
	if !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("int literal: got %v, want 'out of range'", err)
	}
}

// Same rule on the pattern side (the other affected parse path).
func TestIntPatternOutOfRangeErrors(t *testing.T) {
	src := `module M exposing (..)
f x =
    case x of
        99999999999999999999 -> 1
        _ -> 0
`
	err := mustParseErr(t, src)
	if !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("int pattern: got %v, want 'out of range'", err)
	}
}

// A magnitude that overflows float64 (~1.8e308) to +Inf must error rather than
// silently becoming +Inf (which is also invalid JSON on the wire). The lexer
// has no scientific notation yet, so use a long integer part with a fraction.
func TestFloatLiteralOutOfRangeErrors(t *testing.T) {
	err := mustParseErr(t, "module M exposing (..)\nx = "+strings.Repeat("9", 400)+".0\n")
	if !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("float literal: got %v, want 'out of range'", err)
	}
}
