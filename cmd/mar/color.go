// CLI color helpers — small, opinionated, self-disabling.
//
// Two design rules:
//
//   1. Auto-disable when output isn't a TTY (piped to file, captured
//      in CI, etc.). ANSI escape codes in a log file or in a grep
//      pipeline are noise.
//   2. Respect the NO_COLOR convention (https://no-color.org). If the
//      env var is set to anything non-empty, all helpers return plain
//      text even in a real terminal.
//
// Three color groups exposed:
//
//   - Status: red / green / yellow — error / success / warning.
//   - Identifier: cyan / magenta    — values, paths, names.
//   - Emphasis: bold                — headers and key labels.
//
// Use cases match the lispy mar's palette so users transitioning
// between versions see roughly the same visual cues:
//
//   colorRed     — error headlines, dangerous actions
//   colorGreen   — success messages, commands the user should run
//   colorYellow  — "Hint:" labels, recoverable warnings
//   colorCyan    — app names, resource codes, fly region codes
//   colorMagenta — file paths, env variable names
//   colorBold    — section headers (e.g. "Fly app name", "Next steps")
//
// All helpers take a writer-aware decision via the package-level
// `colorEnabled` flag. The flag is computed once at first use,
// reading os.Stdout's TTY status; tests can override via
// SetColorEnabled.

package main

import (
	"fmt"
	"os"
	"sync"

	"golang.org/x/term"
)

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiCyan    = "\x1b[36m"
	ansiMagenta = "\x1b[35m"

	// Bright variants — used when we want emphasis stronger than
	// the dim default red on a dark terminal. The lispy version
	// favored these; we follow.
	ansiBoldRed     = "\x1b[1;31m"
	ansiBoldGreen   = "\x1b[1;32m"
	ansiBoldYellow  = "\x1b[1;33m"
	ansiBoldCyan    = "\x1b[1;36m"
	ansiBoldMagenta = "\x1b[1;35m"
)

var (
	colorOnce    sync.Once
	colorEnabled bool
)

// initColorState computes whether output is a TTY and the user
// hasn't opted out via NO_COLOR. Cached after first call so repeated
// emit sites don't re-stat stdout.
func initColorState() {
	colorOnce.Do(func() {
		// Standard NO_COLOR convention: any non-empty value
		// disables colors. https://no-color.org.
		if v := os.Getenv("NO_COLOR"); v != "" {
			colorEnabled = false
			return
		}
		colorEnabled = term.IsTerminal(int(os.Stdout.Fd()))
	})
}

// SetColorEnabled overrides the auto-detected state. Test-only;
// production code should let initColorState do its job.
func SetColorEnabled(enabled bool) {
	// Run init first to mark the once.Do as fired, then override.
	// Without the init call, the next caller would re-detect from
	// stdout and clobber our override.
	initColorState()
	colorEnabled = enabled
}

// wrap is the universal "apply ANSI prefix + reset, or pass through"
// helper. All semantic helpers below funnel through this so the
// disabled-by-default behavior is consistent.
func wrap(prefix, s string) string {
	initColorState()
	if !colorEnabled {
		return s
	}
	return prefix + s + ansiReset
}

// Status colors.
func colorRed(s string) string    { return wrap(ansiBoldRed, s) }
func colorGreen(s string) string  { return wrap(ansiBoldGreen, s) }
func colorYellow(s string) string { return wrap(ansiBoldYellow, s) }

// Identifier colors.
func colorCyan(s string) string    { return wrap(ansiBoldCyan, s) }
func colorMagenta(s string) string { return wrap(ansiBoldMagenta, s) }

// Emphasis.
func colorBold(s string) string { return wrap(ansiBold, s) }

// errorPrefix is the standard "command-failed" prefix used at the
// start of stderr error messages. Bold red when colors are on,
// plain "Error:" otherwise.
func errorPrefix() string {
	return colorRed("Error:")
}

// hintPrefix is the standard "here's what to try next" prefix for
// hints printed alongside errors. Bold yellow.
func hintPrefix() string {
	return colorYellow("Hint:")
}

// fprintError writes a formatted error to stderr with the standard
// red `Error:` prefix. Format args are printed plain — color the
// caller-provided strings explicitly via colorMagenta / colorCyan
// when desired.
func fprintError(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", errorPrefix(), fmt.Sprintf(format, args...))
}

// fprintHint writes a hint line to stderr. Caller is responsible for
// any inline coloring of the hint body (path, command, etc.).
func fprintHint(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", hintPrefix(), fmt.Sprintf(format, args...))
}
