package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"mar/internal/clio"
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

// TestCmdSuggest_placeholderColoring verifies that <placeholder>
// segments inside a cmdSuggest argument get the cyan ("identifier")
// treatment while the literal command parts stay bold. Colors-off
// mode should pass everything through verbatim — no special
// handling of <...> segments.
func TestCmdSuggest_placeholderColoring(t *testing.T) {
	t.Run("colors off — verbatim pass-through", func(t *testing.T) {
		SetColorEnabled(false)
		defer SetColorEnabled(false)
		got := cmdSuggest("admin add <email>")
		want := "mar admin add <email>"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("colors on — bold literals + cyan placeholders", func(t *testing.T) {
		SetColorEnabled(true)
		defer SetColorEnabled(false)
		got := cmdSuggest("admin add <email>")
		// We don't pin specific ANSI codes (helpers are free to swap
		// bright/normal variants), but we DO pin the relative order:
		// "admin add " comes before "<email>", each segment is
		// independently wrapped, and no ANSI bleeds across them.
		if !strings.Contains(got, "mar") {
			t.Errorf("missing 'mar': %q", got)
		}
		if !strings.Contains(got, "admin add") {
			t.Errorf("missing literal command: %q", got)
		}
		if !strings.Contains(got, "<email>") {
			t.Errorf("missing placeholder: %q", got)
		}
		// Each segment is wrapped in its own reset, so an embedded
		// reset must appear before the placeholder starts.
		idxLit := strings.Index(got, "admin add")
		idxPh := strings.Index(got, "<email>")
		if idxLit == -1 || idxPh == -1 || idxLit >= idxPh {
			t.Fatalf("unexpected segment order: %q", got)
		}
		between := got[idxLit:idxPh]
		if !strings.Contains(between, "\x1b[0m") {
			t.Errorf("expected ANSI reset between bold literal and cyan placeholder; between=%q", between)
		}
	})

	t.Run("no placeholders — falls back to bold-only", func(t *testing.T) {
		SetColorEnabled(true)
		defer SetColorEnabled(false)
		got := cmdSuggest("dev")
		// Just one literal segment. Should be wrapped exactly once.
		if strings.Count(got, "\x1b[0m") != 2 {
			// Two resets: one after "mar" (green), one after "dev" (bold).
			t.Errorf("expected 2 ANSI resets (one per segment), got %q", got)
		}
	})

	t.Run("multiple placeholders", func(t *testing.T) {
		SetColorEnabled(false)
		defer SetColorEnabled(false)
		got := cmdSuggest("format [--check] <file.mar> [<file.mar>...]")
		want := "mar format [--check] <file.mar> [<file.mar>...]"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
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

// withCapturedStderr swaps os.Stderr for a pipe, runs fn, and
// returns whatever fn wrote. Also resets the fprint state flag so
// the test sees a fresh helper state.
func withCapturedStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	// Reset shared state — earlier tests in the same process may
	// have left hasTrail=true, which would suppress the leading
	// blank we're trying to verify. The state lives in
	// internal/clio so a single ClearTrailingBlank covers both
	// the in-package fprint helpers AND the banner's coordination.
	clio.ClearTrailingBlank()
	t.Cleanup(func() {
		os.Stderr = orig
	})

	done := make(chan string)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- string(buf)
	}()
	fn()
	_ = w.Close()
	out := <-done
	return out
}

// TestFprintError_StandaloneEmitsLeadingAndTrailingBlank — single
// Error: line gets `\nError: ...\n\n`. The trailing blank pushes
// the shell prompt off the message (docs/cli-style.md §1).
func TestFprintError_StandaloneEmitsLeadingAndTrailingBlank(t *testing.T) {
	SetColorEnabled(false)
	out := withCapturedStderr(t, func() {
		fprintError("oops")
	})
	want := "\nError: oops\n\n"
	if out != want {
		t.Errorf("output mismatch:\n got %q\nwant %q", out, want)
	}
}

// TestFprintErrorThenHint_SingleBlankBetween — chained Error → Hint
// should have ONE blank line between (not two from each helper's
// own leading+trailing). The state flag suppresses Hint's leading
// blank when Error just emitted a trailing one.
func TestFprintErrorThenHint_SingleBlankBetween(t *testing.T) {
	SetColorEnabled(false)
	out := withCapturedStderr(t, func() {
		fprintError("oops")
		fprintHint("try X")
	})
	want := "\nError: oops\n\nHint: try X\n\n"
	if out != want {
		t.Errorf("output mismatch:\n got %q\nwant %q", out, want)
	}
}

// TestFprintHint_Standalone — a Hint with no preceding helper call
// still gets its leading + trailing blanks.
func TestFprintHint_Standalone(t *testing.T) {
	SetColorEnabled(false)
	out := withCapturedStderr(t, func() {
		fprintHint("try X")
	})
	want := "\nHint: try X\n\n"
	if out != want {
		t.Errorf("output mismatch:\n got %q\nwant %q", out, want)
	}
}

// TestFprintHint_MultiLineContinuation — hints with embedded `\n`
// in the format string are emitted as a single block with the
// trailing blank AFTER the last continuation, not between the
// header line and the continuation.
func TestFprintHint_MultiLineContinuation(t *testing.T) {
	SetColorEnabled(false)
	out := withCapturedStderr(t, func() {
		fprintHint("first\n      second\n      third")
	})
	want := "\nHint: first\n      second\n      third\n\n"
	if out != want {
		t.Errorf("output mismatch:\n got %q\nwant %q", out, want)
	}
}

// TestColorizeHint_PreservesEmbeddedANSI pins the rule that lines
// already carrying ANSI escapes are treated as prose, not as code
// blocks — so any color the CLI embedded (e.g. colorGreen on a
// runnable command suggestion) survives intact regardless of
// indent.
//
// Without this guard, a 6-space-indented continuation line like
// `      After it succeeds, re-run <green>mar fly deploy<reset>.`
// got tokenized as code: each whitespace-delimited token wrapped
// with colorDim, which split mid-escape and left only the first
// token (`mar`) visibly green while the rest rendered dim.
func TestColorizeHint_PreservesEmbeddedANSI(t *testing.T) {
	SetColorEnabled(true)
	defer SetColorEnabled(false)

	// Build a two-line hint mimicking the deploy-time "app not found"
	// case: line 1 is prose (no leading spaces), line 2 is a
	// 6-space-indented continuation. Both reference a colorGreen
	// runnable command.
	hint := "run " + colorGreen("mar fly provision") + " first.\n" +
		"      After it succeeds, re-run " + colorGreen("mar fly deploy") + "."

	got := colorizeHint(hint)

	// Both runnable commands must appear verbatim with their full
	// green span — no fragment dim-wrapping. The exact ANSI prefix
	// for colorGreen is `ansiBoldGreen`; the trailing reset is
	// `ansiReset`.
	wantA := ansiBoldGreen + "mar fly provision" + ansiReset
	wantB := ansiBoldGreen + "mar fly deploy" + ansiReset
	if !strings.Contains(got, wantA) {
		t.Errorf("first green span missing or broken; got: %q", got)
	}
	if !strings.Contains(got, wantB) {
		t.Errorf("second green span missing or broken; got: %q", got)
	}
}
