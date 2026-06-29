// Progress feedback for silent, short-lived operations.
//
// Mar's CLI shells out to `fly` for most remote work, and many of
// those calls (auth check, status probe, SSH-tunneled backup) run
// for several seconds without printing anything. Without a
// spinner the CLI looks frozen.
//
// Two flavors share the same animation:
//
//   progressStep    — fn returns nothing. Used at the top of
//                     deploys for auth/status probes where the
//                     fn's own failure path calls os.Exit.
//   progressStepErr — fn returns an error. Renders a red ✗ on
//                     failure (operator sees which step failed),
//                     green ✓ on success. Caller handles the
//                     returned error.
//
// Both helpers degrade gracefully on non-TTY stdout (CI logs,
// piped output): no animation, just a "label…" line up-front and a
// "✓ label" / "✗ label" line on completion. Verbose but readable
// in scrollback.

package main

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/term"

	"mar/internal/clio"
)

// spinnerFrames is the Braille rotation used by both helpers.
// 10 frames at spinnerInterval == full cycle in 0.8s, which reads
// as "smoothly spinning" without looking jittery on slower
// terminals (mosh, SSH-over-flaky-link, low-refresh tmux).
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// spinnerInterval controls the per-frame delay. 80ms is the
// sweet spot: fast enough to read as continuous motion, slow
// enough that a brief 200ms operation doesn't get one frame
// before the ✓ replaces it (one frame looks like a glitch).
const spinnerInterval = 80 * time.Millisecond

// clearLine wipes the current TTY line in place. Used between
// the transient spinner line and the final ✓ / ✗ line, and by
// the deploy health-check between its "waiting…" countdown and
// the failure message.
func clearLine() {
	fmt.Print("\r\033[K")
}

// progressStep runs fn while showing a Braille spinner next to
// label. Doesn't report errors: the wrapped fn handles its own
// error path (calls fprintError + os.Exit, or returns up the
// stack). The wrapper just guarantees the progress line gets
// closed off cleanly so any subsequent error output isn't smashed
// against the unfinished spinner.
//
// For operations that DO return errors and want a red ✗ on
// failure, use progressStepErr instead.
//
// TTY: animates frames in-place, then wipes and redraws as
// "  ✓ <label>" once fn returns. One line total per step. We
// deliberately omit the "[mar fly deploy]" prefix used elsewhere
// in the banner — these steps fire in quick succession near the
// banner and the prefix adds noise without information.
//
// Non-TTY: prints "  <label>…" line up-front, then "  ✓ <label>"
// on completion. Two lines, no animation.
func progressStep(label string, fn func()) {
	_ = progressStepErr(label, func() error {
		fn()
		return nil
	})
}

// progressStepErr is the error-aware variant of progressStep.
// Returns whatever fn returned. On failure, renders "  ✗ <label>"
// in red so the operator sees which step failed; the caller is
// then responsible for printing the error detail beneath.
//
// fn must NOT write to stdout while running — concurrent writes
// would interleave with the spinner frames. Callers that wrap
// `exec.Command` should use `.Output()` or `.CombinedOutput()` to
// capture the child's output rather than piping it through.
func progressStepErr(label string, fn func() error) error {
	// Each step writes a single tight line (no trailing blank) on
	// stdout. The clio coordinator tracks trailing-blank state on
	// stderr so the next stderr block (banner, error, hint) can
	// decide whether to add its own leading blank. Since our line
	// is NOT a blank, clear the flag — without this, a prior
	// `fprintInfo`-style banner that DID end with a blank leaves
	// the flag set, and a subsequent `fprintError` after our step
	// fails skips its leading blank and stacks directly under our
	// ✗ line.
	defer clio.ClearTrailingBlank()

	isTTY := term.IsTerminal(int(os.Stdout.Fd()))
	if !isTTY {
		fmt.Printf("  %s…\n", label)
		err := fn()
		if err != nil {
			fmt.Printf("  %s %s\n", colorRed("✗"), label)
			return err
		}
		fmt.Printf("  %s %s\n", colorGreen("✓"), label)
		return nil
	}

	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		i := 0
		for {
			// Redraw the frame on each tick. \r\033[K wipes the
			// previous frame; the spinner glyph is dim so the
			// label stays the eye-catching part of the line.
			fmt.Printf("\r\033[K  %s %s",
				colorDim(spinnerFrames[i%len(spinnerFrames)]), label)
			i++
			select {
			case <-stop:
				return
			case <-time.After(spinnerInterval):
			}
		}
	}()

	err := fn()

	close(stop)
	<-done
	clearLine()
	if err != nil {
		fmt.Printf("  %s %s\n", colorRed("✗"), label)
		return err
	}
	fmt.Printf("  %s %s\n", colorGreen("✓"), label)
	return nil
}

// withStdoutSilenced runs fn with os.Stdout redirected to the
// platform's null device (/dev/null on Unix, NUL on Windows).
// Anything fn or its callees write to stdout is discarded.
// stderr is untouched — real errors still surface.
//
// Used to wrap calls inside progressStepErr where the wrapped
// function emits informational stdout output that would collide
// with the spinner's in-place redraw. progressStepErr's doc
// already says fn must not write to stdout; this helper enforces
// the contract for callers we don't own (scaffold.Build et al.).
//
// Side effect note: os.Stdout is a process-wide global. While
// silenced, any concurrent goroutine that writes to stdout also
// hits the null device. In practice the only concurrent writer
// is the spinner goroutine; "spinner pauses for the duration of
// fn" is the correct behavior here.
//
// Falls back to running fn with the real stdout if the null
// device can't be opened (very rare — would mean the OS is
// misconfigured). The output collides but the deploy still
// completes; better than aborting over a cosmetic concern.
func withStdoutSilenced(fn func() error) error {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return fn()
	}
	defer devNull.Close()
	orig := os.Stdout
	os.Stdout = devNull
	defer func() { os.Stdout = orig }()
	return fn()
}
