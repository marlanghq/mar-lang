// Post-deploy health check.
//
// `mar fly deploy` exits 0 the moment Fly accepts the image and assigns
// it to a machine — which is BEFORE the app has actually booted. Many
// real-world failures land in that gap: a broken migration, a missing
// secret, an SMTP check that explodes on first boot. Without a probe,
// the operator sees "deployed ✓" in the terminal and only discovers
// the breakage later when a user reports the app is down.
//
// What we do: after `fly deploy` returns, poll `https://<app>.fly.dev/`
// for up to 60 seconds. Mar's runtime is strict on boot — migrations,
// validateProductionConfig, SMTP check, DB lockfile all run before the
// HTTP server starts listening — so a successful response (any status
// < 500) means the app fully booted. On failure, dump the last 50
// lines from `fly logs` inline so the operator sees what crashed
// without leaving the terminal.
//
// No flags. The probe is mandatory on every deploy. The only escape
// is Ctrl+C, and even then the deploy itself completed before this
// step started — interrupting just skips the verification.

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	// healthCheckTimeout caps the total polling duration. 90s covers
	// the first-deploy case: fly's edge proxy needs time to learn the
	// route to a brand-new app (30-60s in distant regions like gru),
	// on top of the actual boot. Subsequent deploys are typically up
	// within 10-15s because the edge already has the route cached.
	// Apps that legitimately take longer than 90s (huge migrations,
	// cold image pull on a fresh runner) will hit this and dump logs —
	// operator can then run `mar fly logs` to watch the rest.
	healthCheckTimeout = 90 * time.Second

	// healthCheckPollInterval is the delay between HTTP probes. 2s is
	// fast enough that a sub-10s boot still feels responsive (5
	// probes), slow enough that a long boot doesn't hammer the
	// machine with handshakes.
	healthCheckPollInterval = 2 * time.Second

	// healthCheckRequestTimeout caps a single HTTP request. Kept
	// generous because a freshly-routed Fly app sometimes needs a few
	// seconds for the proxy to find the new machine — a tight 1s
	// timeout would falsely fail those first attempts.
	healthCheckRequestTimeout = 5 * time.Second

	// healthCheckLogTailLines is how many recent log lines we dump on
	// failure. 50 covers a full Go panic stack trace plus a few
	// surrounding boot lines; the user can run `mar fly logs --follow`
	// for the full stream if 50 isn't enough.
	healthCheckLogTailLines = 50
)

// runHealthCheck polls appURL until it responds healthy or the timeout
// elapses. On success, prints a success line. On failure, prints the
// failure header and dumps the recent log buffer for appName.
//
// Returns true on success, false on failure. Caller decides the exit
// code — this function never calls os.Exit itself.
func runHealthCheck(appName, appURL string) bool {
	if err := waitForAppHealthy(appURL, healthCheckTimeout); err != nil {
		fmt.Println()
		fprintError("mar fly deploy: %v", err)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintf(os.Stderr, "Last %d lines from %s:\n",
			healthCheckLogTailLines, colorCyan(appName))
		dumpRecentLogs(appName, healthCheckLogTailLines)
		fmt.Fprintln(os.Stderr)
		// `mar fly logs` already streams by default (no `--follow`
		// flag — fly logs tails out of the box). Suggesting
		// `--follow` would have the dispatcher treat the flag as a
		// project path and stat it, which fails with a confusing
		// "no such file" error.
		fmt.Fprintf(os.Stderr, "For full logs, run: %s\n",
			flySuggestion("logs", "."))
		fmt.Fprintln(os.Stderr)
		return false
	}
	return true
}

// waitForAppHealthy polls appURL on a fixed interval until it responds
// successfully or the timeout elapses.
//
// "Healthy" = any HTTP response with status < 500. We accept 2xx
// (rendered page), 3xx (redirect to /sign-in), and 4xx (auth-gated
// page returning 401) all as proof the framework booted and the
// server is listening. Only connection failures, timeouts, and 5xx
// are treated as "not up yet".
//
// Progress feedback: when stdout is a TTY, redraws a single in-place
// line with the elapsed time. When piped (CI, log capture), prints a
// one-shot "waiting" line up front and the success/failure line at
// the end — no in-place updates.
//
// Output merging: on success, the success message replaces fly's
// trailing "Visit your newly deployed app at <url>" line via ANSI
// cursor moves, so the URL appears once instead of twice. See
// mergeWithFlyVisitLine for the cursor dance.
func waitForAppHealthy(appURL string, timeout time.Duration) error {
	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	deadline := time.Now().Add(timeout)
	start := time.Now()

	if !isTTY {
		fmt.Printf("[mar fly deploy] waiting for app to come up (timeout %s)…\n", timeout)
	}

	for {
		if probeHealthy(appURL) {
			elapsed := time.Since(start)
			elapsedStr := "<1s"
			if elapsed >= time.Second {
				elapsedStr = elapsed.Round(time.Second).String()
			}
			if isTTY {
				mergeWithFlyVisitLine()
			}
			// Trailing blank per docs/cli-style.md §1 — the success
			// line is the last thing we print before
			// printDeploySuccessSummary may decide to print nothing,
			// so the blank here is what stands the success line off
			// from the shell prompt.
			fmt.Printf("[mar fly deploy] %s App is up at %s (healthy in %s)\n\n",
				colorGreen("✓"), colorCyan(appURL), elapsedStr)
			return nil
		}
		if time.Now().After(deadline) {
			if isTTY {
				clearLine()
			}
			return fmt.Errorf("app did not respond in %s", timeout)
		}
		if isTTY {
			elapsed := time.Since(start).Round(time.Second)
			fmt.Printf("\r\033[K[mar fly deploy] waiting for app to come up… (%s)",
				colorizeElapsed(elapsed, timeout))
		}
		time.Sleep(healthCheckPollInterval)
	}
}

// colorizeElapsed paints the spinner's elapsed-time counter in a
// color that escalates as the wait approaches the timeout:
//
//	< 20% of timeout  → dim (normal: boot is progressing)
//	20%–60% of timeout → yellow (taking a bit longer than typical)
//	>= 60% of timeout → red (concerning: approaching timeout)
//
// For the default 60s timeout that's roughly: dim until 12s,
// yellow from 12s to 36s, red beyond 36s. The thresholds scale
// with the timeout so they'd still make sense if we ever raise
// or lower it.
//
// Why a gradient and not just "red at 50%": typical mar app boots
// finish in 3–10s, so anything past ~12s already deserves a
// "huh, slower than usual" signal — yellow says that without
// implying failure. Red is reserved for the late stretch where
// the operator should start mentally preparing to investigate.
func colorizeElapsed(elapsed, timeout time.Duration) string {
	s := elapsed.String()
	ratio := float64(elapsed) / float64(timeout)
	switch {
	case ratio >= 0.6:
		return colorRed(s)
	case ratio >= 0.2:
		return colorYellow(s)
	default:
		return colorDim(s)
	}
}

// mergeWithFlyVisitLine clears the current line (any spinner residue)
// AND fly's trailing "Visit your newly deployed app at <url>" + the
// blank line below it, so our success message can replace them as a
// single combined line.
//
// Layout fly leaves at the cursor when it exits:
//
//	line N:   Visit your newly deployed app at <url>
//	line N+1: (blank — fly's trailing \n)
//	line N+2: (cursor here, possibly with spinner residue)
//
// The ANSI sequence:
//   - \r\033[K   — column 0, clear current line (wipes spinner)
//   - \033[2A    — up 2 lines (cursor lands at start of fly's URL line)
//   - \033[J     — clear from cursor to end of screen
//
// After this, cursor is at the start of where fly's URL line was,
// ready for our combined success message to be printed in its place.
//
// Only safe in TTY mode; ANSI escapes render as garbage in plain logs.
func mergeWithFlyVisitLine() {
	fmt.Print("\r\033[K\033[2A\033[J")
}

// probeHealthy returns true when the app responds with a non-5xx status.
// Anything else — connection refused, TLS error, request timeout, 5xx
// — counts as "not up yet". Errors are swallowed: probe failures are
// expected during the warmup window.
func probeHealthy(url string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), healthCheckRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode < 500
}

// dumpRecentLogs runs `fly logs --no-tail` and prints the last maxLines
// to stderr, framed with horizontal rules so the boundary between
// mar's output and Fly's output is unambiguous.
//
// Best-effort: if `fly logs` fails or returns nothing useful, we print
// a short note instead of crashing. The deploy already failed; we don't
// want a second failure from log retrieval to muddy the diagnosis.
func dumpRecentLogs(appName string, maxLines int) {
	out, err := exec.Command("fly", "logs", "--app", appName, "--no-tail").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "(could not fetch logs: %v)\n", err)
		return
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	sep := strings.Repeat("─", 60)
	fmt.Fprintln(os.Stderr, colorDim(sep))
	for _, l := range lines {
		fmt.Fprintln(os.Stderr, l)
	}
	fmt.Fprintln(os.Stderr, colorDim(sep))
}
