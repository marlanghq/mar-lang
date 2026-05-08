package jsserve

import (
	"fmt"
	"os"
)

// printBanner writes the startup banner. Keeps the lispy era's
// "Dev mode <name>" + indented detail-line layout: bold section
// header, the URL on its own line, hot-reload note as the next.
//
// In CI / non-TTY contexts the output collapses to a single line so
// log scrapers don't get blank lines or ANSI escapes.
func printBanner(addr string, hub *ReloadHub, appName string) {
	tty := isStdoutTTY()
	bold := wrapANSI(tty, "\x1b[1m", "\x1b[0m")
	cyan := wrapANSI(tty, "\x1b[36m", "\x1b[0m")
	dim := wrapANSI(tty, "\x1b[2m", "\x1b[0m")

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
	fmt.Fprintln(os.Stderr)

	if !tty {
		hint := ""
		if hub != nil {
			hint = " (hot reload enabled)"
		}
		fmt.Printf("[mar] %s %s on %s%s\n", mode, appName, url, hint)
		fmt.Printf("[mar] admin: %s\n", adminURL)
		return
	}

	fmt.Printf("%s  %s\n", bold(mode), appName)
	fmt.Printf("  %s %s\n", dim("Local:"), cyan(url))
	fmt.Printf("  %s %s\n", dim("Admin:"), cyan(adminURL))
	if hub != nil {
		fmt.Printf("  %s %s\n",
			cyan("Hot reload enabled."),
			dim("Save any .mar file to rebuild."),
		)
	}
	fmt.Println()
	fmt.Printf("  %s\n", dim("Press Ctrl+C to stop."))
	fmt.Println()
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
