package main

import (
	"strings"
	"testing"
)

// TestColors_disabled covers the case where colors are off — every
// helper should pass the input through verbatim with no ANSI bytes.
// This is the path most CI logs and piped output take.
func TestColors_disabled(t *testing.T) {
	SetColorEnabled(false)
	defer SetColorEnabled(false)

	cases := []struct {
		name string
		fn   func(string) string
	}{
		{"red", colorRed},
		{"green", colorGreen},
		{"yellow", colorYellow},
		{"cyan", colorCyan},
		{"magenta", colorMagenta},
		{"bold", colorBold},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fn("hello")
			if got != "hello" {
				t.Errorf("expected plain pass-through; got %q", got)
			}
			if strings.Contains(got, "\x1b") {
				t.Errorf("disabled mode leaked ANSI escape: %q", got)
			}
		})
	}
}

// TestColors_enabled confirms ANSI codes are wrapped around the
// input when colors are on. We don't pin specific codes (the helper
// is free to swap bright/normal variants); we just check that the
// returned string starts with ESC, contains the input, and ends with
// the reset code.
func TestColors_enabled(t *testing.T) {
	SetColorEnabled(true)
	defer SetColorEnabled(false)

	got := colorRed("err")
	if !strings.HasPrefix(got, "\x1b[") {
		t.Errorf("expected ANSI prefix; got %q", got)
	}
	if !strings.Contains(got, "err") {
		t.Errorf("payload missing; got %q", got)
	}
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("expected reset suffix; got %q", got)
	}
}

// TestErrorAndHintPrefixes pin the user-facing labels (so a future
// rename is intentional, not accidental).
func TestErrorAndHintPrefixes(t *testing.T) {
	SetColorEnabled(false)
	defer SetColorEnabled(false)

	if got := errorPrefix(); got != "Error:" {
		t.Errorf("errorPrefix uncolored: got %q, want %q", got, "Error:")
	}
	if got := hintPrefix(); got != "Hint:" {
		t.Errorf("hintPrefix uncolored: got %q, want %q", got, "Hint:")
	}
}
