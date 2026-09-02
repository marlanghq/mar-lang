package parityfixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A bug in Mar and a mistake in the user's code should not read the same.
//
// The Go evaluator drew that line and kept it: of its twelve errors, ten became
// "internal error: … — a checked program cannot do this, so this is a bug in
// Mar rather than in your code. Please report it.", and two stayed as real
// program errors. The other two evaluators had no line at all -- measured,
// neither runtime.js nor MarEval.swift contained the phrase "internal error"
// anywhere -- so on the phone and in the browser a user read a sentence that
// sounded like their fault, and the report never came.
//
// The sentence has to be the same on all three, and it cannot ride in the
// fixture: it is raised, not returned, and a Mar error is not catchable by
// design. So this is the source-level check, the same technique the Int refusal
// uses, and honest about proving the wording rather than the behaviour.
func TestTheThreeRuntimesSayTheSameThingAboutABugInMar(t *testing.T) {
	const sentence = "a checked program cannot do this, so this is a bug in Mar rather than in your code. Please report it."
	const prefix = "internal error: "

	for _, f := range []struct{ label, path string }{
		{"server", "../runtime/eval.go"},
		{"browser", "../jsserve/runtime.js"},
		{"phone", "../iosbundle/template/Sources/MarValue.swift"},
	} {
		b, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("%s: %v", f.label, err)
		}
		body := string(b)
		for _, need := range []string{prefix, sentence} {
			if !strings.Contains(body, need) {
				t.Errorf("the %s runtime does not carry %q; the three wordings have drifted",
					f.label, need)
			}
		}
	}

	// And the line itself: `no case branch matched` is the user's program on
	// every runtime, both clients match on its text, and it must never join the
	// internal ones.
	for _, f := range []struct{ label, path string }{
		{"browser", "../jsserve/runtime.js"},
		{"phone", "../iosbundle/template/Sources/MarValue.swift"},
		{"phone-eval", "../iosbundle/template/Sources/MarEval.swift"},
	} {
		b, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatalf("%s: %v", f.label, err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			if !strings.Contains(line, "no case branch matched") {
				continue
			}
			if strings.Contains(line, "marInternal") || strings.Contains(line, "internalError") ||
				strings.Contains(line, "internalText") {
				t.Errorf("%s:%s makes `no case branch matched` an internal error.\n"+
					"    It is the user's program: a case expression that matched nothing. Both\n"+
					"    clients match on its text, and a test says so.",
					filepath.Base(f.path), strings.TrimSpace(line))
			}
		}
	}
}
