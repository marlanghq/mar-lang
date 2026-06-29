package jsserve

import (
	"fmt"
	"os"

	"mar/internal/clio"
)

// printBanner writes the startup banner: a bold "Dev mode <name>"
// section header, the URL on its own line, and a hot-reload note.
//
// `mailToStdout` is true when Auth.config is registered but SMTP
// isn't wired up (Host or Password empty) — in that case auth.Send
// falls back to printing the email to stdout, and the banner adds a
// line so the operator knows where to look for sign-in codes
// instead of being surprised by a wall of email-shaped log output
// on the first sign-in attempt.
//
// In CI / non-TTY contexts the output collapses to a single line so
// log scrapers don't get blank lines or ANSI escapes.
func printBanner(addr string, hub *ReloadHub, appName string, mailToStdout bool) {
	tty := isStdoutTTY()
	bold := wrapANSI(tty, "\x1b[1m", "\x1b[0m")
	cyan := wrapANSI(tty, "\x1b[36m", "\x1b[0m")
	// Dim uses a 256-color medium-dark gray (code 240) instead of the
	// stock `\x1b[2m` (faint) attribute — terminals render the faint
	// attribute very lightly, almost invisible on some color schemes.
	// 240 is a fixed shade ~40% from black, readable on both light
	// and dark backgrounds without being so vivid it competes with
	// the cyan/green elements next to it.
	// 256-color gray 245 (RGB ~138/138/138). Matches cmd/mar/color.go's
	// ansiDim — keep them in sync so the dev banner reads the same
	// shade as the rest of the CLI's secondary text.
	dim := wrapANSI(tty, "\x1b[38;5;245m", "\x1b[0m")

	if appName == "" {
		appName = "mar"
	}
	mode := "Production"
	if hub != nil {
		mode = "Dev mode"
	}
	url := "http://localhost" + addr
	adminURL := url + "/_mar/admin"

	// Leading blank line — separates the banner from any preceding
	// stderr output (boot logs, hints, etc.). Print to stderr so it
	// lands in the same stream as those preceding lines, avoiding
	// stdout/stderr interleaving where the blank disappears between
	// streams in some terminal buffers.
	//
	// Skip the blank when the previous stderr block already ended
	// with one (typically a `fprintHint(...)` from cmd/mar — e.g.
	// the "no admins configured" hint). Without this check the
	// pair `fprintHint → printBanner` shows TWO blanks between,
	// which reads as a gap, not a separator. The state lives in
	// internal/clio so both packages coordinate.
	if clio.WantLeadingBlank() {
		fmt.Fprintln(os.Stderr)
	}

	if !tty {
		hint := ""
		if hub != nil {
			hint = " (hot reload enabled)"
		}
		fmt.Printf("[mar] %s %s on %s%s\n", mode, appName, url, hint)
		fmt.Printf("[mar] admin: %s\n", adminURL)
		if mailToStdout {
			fmt.Printf("[mar] mail: no SMTP configured — sign-in codes print to this log\n")
		}
		// Non-tty path ends tight (no trailing blank). Clear any
		// prior MarkTrailingBlank claim so subsequent blocks emit
		// their own leading separator.
		clio.ClearTrailingBlank()
		return
	}

	fmt.Printf("%s  %s\n", bold(mode), appName)
	fmt.Printf("  %s %s\n", dim("Local:"), cyan(url))
	fmt.Printf("  %s %s\n", dim("Admin:"), cyan(adminURL))
	if hub != nil {
		// "Hot reload enabled." is descriptive status, not an
		// identifier or link — kept dim so cyan stays reserved for
		// addressable things (URLs, emails, IDs) per
		// docs/cli-style.md §3.
		fmt.Printf("  %s %s\n",
			dim("Hot reload enabled."),
			dim("Save any .mar file to rebuild."),
		)
	}
	if mailToStdout {
		// Same dim shade as the hot-reload line — descriptive
		// status, not an addressable link. Phrased so the operator
		// immediately knows where the codes go (here = this
		// terminal) without having to dig through docs after the
		// first sign-in attempt.
		fmt.Printf("  %s %s\n",
			dim("Mail: no SMTP configured."),
			dim("Sign-in codes print to this terminal."),
		)
	}
	fmt.Println()
	fmt.Printf("  %s\n", dim("Press Ctrl+C to stop."))
	fmt.Println()
	// Banner ends with a trailing blank — claim it so any
	// subsequent fprintError / fprintHint can skip its own
	// leading blank.
	clio.MarkTrailingBlank()
}

// isStdoutTTY returns true when stdout looks like an interactive
// terminal (so colors / unicode are safe to emit). Honors NO_COLOR.
func isStdoutTTY() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}

// wrapANSI returns a function that wraps a string in the given escape
// sequences when active is true; passes through unchanged otherwise.
func wrapANSI(active bool, prefix, suffix string) func(string) string {
	if !active {
		return func(s string) string { return s }
	}
	return func(s string) string { return prefix + s + suffix }
}
