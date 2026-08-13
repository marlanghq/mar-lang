package iosbundle

import (
	"os"
	"strings"
	"testing"
)

// ADR 0020 gives a runtime failure three screens, and the words on them are
// part of the decision rather than decoration. Two runtimes write those words
// independently, internal/jsserve/runtime.js and the Swift template, so
// nothing but a test stops one from drifting into a friendlier, less honest
// phrasing than the other.
//
// The lines are spelled out here rather than read from either file: a test
// that derived them from one side would pass while both sides said something
// nobody agreed to.
var failureCopy = []string{
	"This app has a critical bug.",
	// update, a tagger, or an effect: the app is still standing.
	"Something unexpected happened and your request could not be completed.",
	"Nothing was changed. The app is back at its last consistent state.",
	// view or init: the message takes the page.
	"Something unexpected happened and this screen could not be shown.",
	"Go back to return the app to its last consistent state.",
	// view or init with nothing underneath. Promises nothing, because
	// there is nothing to promise.
	"The app cannot continue until a developer fixes it.",
}

func readOrFail(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestBothRuntimesShipTheSameFailureCopy(t *testing.T) {
	sources := map[string]string{
		"the JS runtime":    readOrFail(t, "../jsserve/runtime.js"),
		"the Swift runtime": readOrFail(t, "template/Sources/MarFailure.swift"),
	}
	for name, src := range sources {
		for _, line := range failureCopy {
			if !strings.Contains(src, line) {
				t.Errorf("%s is missing an approved line: %q", name, line)
			}
		}
	}
}

// The copy exists to avoid a specific dishonesty: every runtime error a checked
// program can still raise is deterministic, so telling someone to try again is
// telling them to do something that cannot work. `Service.errorToString` says
// it and is right to, a network comes back, which is exactly why this must
// not borrow the phrasing.
func TestFailureCopyNeverOffersARetry(t *testing.T) {
	for _, line := range failureCopy {
		lower := strings.ToLower(line)
		for _, banned := range []string{"try again", "retry", "reload"} {
			if strings.Contains(lower, banned) {
				t.Errorf("%q suggests %q, but the failure is a bug and will repeat", line, banned)
			}
		}
	}
}

// Only the middle case has somewhere to go, so only it gets a button. The
// other two must not, for opposite reasons: the app is still usable, or there
// is nothing to return to.
func TestOnlyTheCaseWithSomewhereToGoOffersABackButton(t *testing.T) {
	js := readOrFail(t, "../jsserve/runtime.js")
	if !strings.Contains(js, "kind === 'page'") {
		t.Error("the JS runtime should gate the Back button on the 'page' case")
	}
	swift := readOrFail(t, "template/Sources/MarFailure.swift")
	if !strings.Contains(swift, "kind == .page") {
		t.Error("the Swift runtime should gate the Back button on the .page case")
	}
}
