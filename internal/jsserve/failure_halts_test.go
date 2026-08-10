package jsserve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ADR 0028: a failure whose site is `view` or `init` halts the program — every
// subscription torn down, every further dispatch refused. A failure in
// `update` does not, because the model it would have produced was never
// installed and the next message may take a different branch.
//
// The regression this pins is not subtle and cost eleven hours of battery: a
// game left in a hidden window divided by a zero width, `view` threw, and the
// runtime kept ticking sixty times a second behind the failure message for the
// rest of the night — 2.4 million dispatches nobody would ever see.
//
// Source-level, like TestFailureScreensInstallTheStylesheetThemselves above:
// there is no DOM harness here that could run a program to completion. What it
// proves is that the halt is still wired into the one funnel every `page`
// failure goes through, on BOTH runtimes, which is where a refactor would drop
// it.
func TestAViewFailureHaltsTheProgram(t *testing.T) {
	src, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatalf("read runtime.js: %v", err)
	}
	js := string(src)

	body, ok := functionBody(js, "function presentPageFailure(")
	if !ok {
		t.Fatal("presentPageFailure is gone — if it was renamed, this test has to follow it")
	}
	for _, want := range []string{"halted = true", "teardownAllSubs()"} {
		if !strings.Contains(body, want) {
			t.Errorf("presentPageFailure must contain %q (ADR 0028): a view that threw\n"+
				"will throw again for the same model, so the program has to stop rather\n"+
				"than run forever behind a dead screen", want)
		}
	}

	// The teardown alone is not enough: an effect that resolves after the
	// failure, or a handler still attached to the DOM being replaced, would
	// otherwise advance the model past the state being reported about.
	dispatch, ok := functionBody(js, "currentDispatch = (msg) => {")
	if !ok {
		t.Fatal("currentDispatch is gone — if it was renamed, this test has to follow it")
	}
	if !strings.Contains(dispatch, "if (halted) return;") {
		t.Error("currentDispatch must refuse messages once halted (ADR 0028): tearing " +
			"down the subscriptions does not catch what was already in flight")
	}

	// Parity. The standing rule is that the two runtimes behave the same; a
	// halt that only exists in the browser is exactly the kind of drift the
	// examples would not reveal, because a failed screen looks identical on
	// both until you measure the battery.
	swiftPath := filepath.Join("..", "iosbundle", "template", "Sources", "MarPageRuntime.swift")
	swiftSrc, err := os.ReadFile(swiftPath)
	if err != nil {
		t.Fatalf("read %s: %v", swiftPath, err)
	}
	swift := string(swiftSrc)
	for _, want := range []string{"halted = true", "teardownSubs()", "if halted { return }"} {
		if !strings.Contains(swift, want) {
			t.Errorf("MarPageRuntime.swift must contain %q: ADR 0028 is a runtime "+
				"policy, and a policy one platform keeps is a drift", want)
		}
	}
	// ...and only for the case that cannot recover. If the Swift side ever
	// halts on a `dispatch` failure too, an app whose screen is still correct
	// would die on a transient error in one branch of update.
	if !strings.Contains(swift, "if where_ == .page {") {
		t.Error("the Swift halt must be gated on FailureSite.page: a dispatch failure " +
			"leaves a consistent screen and the next message may succeed")
	}
}
