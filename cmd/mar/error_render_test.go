package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"mar/internal/runtime"
)

// captureStderr runs fn and returns whatever it wrote to os.Stderr.
// Colors are forced OFF so the output is plain text — the tests below
// pin message structure, not ANSI codes.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	SetColorEnabled(false)
	defer SetColorEnabled(false)

	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stderr = orig
	return <-done
}

// hintedError + printError split: the summary becomes an `Error:` line
// and the hint becomes a `Hint:` line, even when colors are off. The
// helper used to bake the literal "Hint:" prefix into the error string
// (the embedded-`\n\nHint:` pattern across cmd/mar/*.go) and printed
// the bundle raw, so the Hint label rendered as plain text with no
// color. That regression is what these tests guard against.
func TestPrintError_HintedError_SplitsSummaryAndHint(t *testing.T) {
	err := newHintedError(
		"file %q not found",
		"Create it, or set \"entry\" in mar.json to point elsewhere.",
		"Main.mar")
	out := captureStderr(t, func() { printError("mar dev", err) })

	wantErr := "Error: mar dev: file \"Main.mar\" not found"
	if !strings.Contains(out, wantErr) {
		t.Errorf("missing Error block %q in:\n%s", wantErr, out)
	}
	wantHint := "Hint: Create it, or set \"entry\" in mar.json to point elsewhere."
	if !strings.Contains(out, wantHint) {
		t.Errorf("missing Hint block %q in:\n%s", wantHint, out)
	}
	// Hint must not be merged into the Error line.
	if strings.Contains(out, "Error: mar dev: file \"Main.mar\" not found Create it") {
		t.Errorf("Hint leaked into Error line:\n%s", out)
	}
}

// Same split for runtime.BlockedMigrationError — the established
// structured-error type used by the migrator. printError unwraps both
// types via errorParts and renders identically.
func TestPrintError_BlockedMigrationError_SplitsSummaryAndHint(t *testing.T) {
	err := &runtime.BlockedMigrationError{
		Summary: "migration blocked for entity tasks: cannot add required column \"userId\".",
		Hint:    "Existing rows would violate the NOT NULL constraint.\n\n    ALTER TABLE tasks ADD COLUMN userId INTEGER;",
	}
	out := captureStderr(t, func() { printError("", err) })

	if !strings.Contains(out, "Error: migration blocked for entity tasks: cannot add required column \"userId\".") {
		t.Errorf("Error block missing:\n%s", out)
	}
	if !strings.Contains(out, "Hint: Existing rows would violate the NOT NULL constraint.") {
		t.Errorf("Hint block missing:\n%s", out)
	}
	if !strings.Contains(out, "    ALTER TABLE tasks ADD COLUMN userId INTEGER;") {
		t.Errorf("Hint body (SQL block) missing:\n%s", out)
	}
}

// Plain error path — printError falls back to diag.Format and writes
// to stderr. With a prefix, fprintError wraps it.
func TestPrintError_PlainError_UsesPrefix(t *testing.T) {
	err := errors.New("stat /tmp/x: no such file or directory")
	out := captureStderr(t, func() { printError("mar check", err) })
	want := "Error: mar check: stat /tmp/x: no such file or directory"
	if !strings.Contains(out, want) {
		t.Errorf("missing %q in:\n%s", want, out)
	}
}

// Without a prefix, plain errors print raw via diag.Format — the
// positioned-error renderer adds its own colored "Type error:" /
// "Parse error:" prefix for SourceError, so wrapping in fprintError
// would double-prefix.
func TestPrintError_PlainError_NoPrefix_PrintsRaw(t *testing.T) {
	err := errors.New("bare error text")
	out := captureStderr(t, func() { printError("", err) })
	if strings.Contains(out, "Error:") {
		t.Errorf("plain error without prefix should not get an Error: marker:\n%s", out)
	}
	if !strings.Contains(out, "bare error text") {
		t.Errorf("missing body in:\n%s", out)
	}
}

// printError must return a plain-text rendering matching what gets
// printed (without any ANSI), so callers can forward the message to
// the dev banner (lp.SetError) and the SSE channel (hub.Error).
func TestPrintError_ReturnsPlainText(t *testing.T) {
	err := newHintedError("summary line", "hint body")
	var ret string
	_ = captureStderr(t, func() { ret = printError("", err) })
	want := "summary line\n\nhint body"
	if ret != want {
		t.Errorf("returned %q, want %q", ret, want)
	}
}

// newHintedError formats the summary template with the supplied args.
// The hint is taken verbatim. Pins the helper's contract so the
// arg-order bug we hit during the rollout (summary, entryFile, hint
// vs. summary, hint, entryFile) doesn't sneak back.
func TestNewHintedError_ArgOrder(t *testing.T) {
	err := newHintedError(
		"file %q not found at line %d",
		"do this and that",
		"Main.mar", 42)
	var he *hintedError
	if !errors.As(err, &he) {
		t.Fatalf("expected *hintedError, got %T", err)
	}
	wantSummary := `file "Main.mar" not found at line 42`
	if he.Summary != wantSummary {
		t.Errorf("Summary = %q, want %q", he.Summary, wantSummary)
	}
	if he.Hint != "do this and that" {
		t.Errorf("Hint = %q, want %q", he.Hint, "do this and that")
	}
}

// hintedError.Error() bundles Summary + Hint so callers that just
// stringify (logs, tests, the SSE channel before we strip / split)
// see the full message — same contract as
// runtime.BlockedMigrationError.Error().
func TestHintedError_ErrorMethodBundles(t *testing.T) {
	he := &hintedError{Summary: "boom", Hint: "do X"}
	want := "boom\n\ndo X"
	if got := he.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
	// Empty hint → just the summary.
	he2 := &hintedError{Summary: "boom"}
	if got := he2.Error(); got != "boom" {
		t.Errorf("Error() with empty hint = %q, want %q", got, "boom")
	}
}
