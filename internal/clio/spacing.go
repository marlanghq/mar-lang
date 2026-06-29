// Package clio coordinates terminal-output spacing across mar's
// stderr-writing surfaces (cmd/mar's `fprintError` / `fprintHint` /
// `fprintWarn`, and the dev banner in internal/jsserve).
//
// Why a shared package: docs/cli-style.md §1 says each multi-line
// block emits a leading blank line to separate from prior output,
// and a trailing blank only when it's the last thing printed.
// Without coordination, consecutive blocks each emit their OWN
// leading + trailing, producing TWO blank lines between them —
// reads as a gap, not a separator.
//
// The fix used in cmd/mar/color.go is to track whether the previous
// stderr write ended with a blank. If yes, the next block skips its
// leading blank. That state lived as a package-level var in
// cmd/mar/color.go, so it only coordinated calls within that
// package. When `mar dev` chained an `fprintHint` (admin panel
// locked) with the dev banner from internal/jsserve, the two
// packages didn't share state — two blanks slipped between.
//
// This package lifts the state to a place both packages can import.
// The contract is simple: callers that write a block ending in a
// blank line call MarkTrailingBlank(); callers about to write a new
// block call WantLeadingBlank() to decide whether to emit one.
package clio

import "sync"

var (
	mu       sync.Mutex
	hasTrail bool
)

// WantLeadingBlank reports whether the next block should emit its
// leading blank line. Returns false when the previous stderr write
// already ended with a blank (skip the redundant separator); true
// when nothing has been written, or the last write was tight.
func WantLeadingBlank() bool {
	mu.Lock()
	defer mu.Unlock()
	return !hasTrail
}

// MarkTrailingBlank records that the most recent stderr write ended
// with a blank line. Subsequent calls to WantLeadingBlank will
// return false until a tight write clears the state.
func MarkTrailingBlank() {
	mu.Lock()
	defer mu.Unlock()
	hasTrail = true
}

// ClearTrailingBlank resets the state so the next block emits its
// own leading blank. Used by tests for isolation, and by callers
// that wrote a tight (no-trailing-blank) line and want to clear
// any prior MarkTrailingBlank claim.
func ClearTrailingBlank() {
	mu.Lock()
	defer mu.Unlock()
	hasTrail = false
}
