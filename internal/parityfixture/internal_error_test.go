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

// The two sentences that are NOT internal errors and are still written three
// times: the ones a user reads when their own program went wrong.
//
// Found on the third structural axis -- string literals appearing in two or
// more runtimes. That axis is noisy in general (a naive scan over runtime.js
// returns hundreds of code fragments), but filtered to non-comment lines of
// real prose it answers cleanly: eleven sentences are shared, and all but
// these two were already compared by the fixture through Service.errorToString
// and the not-JSON message.
//
// Neither can ride in the fixture. Both are raised: one when the stack budget
// runs out, one when a record arrives without a field the program's types
// promised. So this is the source-level check again, and honest about proving
// the wording rather than the behaviour.
func TestTheSentencesAUserReadsAreWrittenTheSameThreeTimes(t *testing.T) {
	// The SHARED part, not the whole message. The browser appends advice only
	// a browser can act on -- "Under `mar dev` that usually means the page is
	// still holding the model it loaded with... Reload the page." -- and a
	// phone has no page to reload. Measured, not assumed: the first version of
	// this test asked for the whole sentence and reported the browser as
	// having lost it, when what it had was a tail the other two should not
	// have. Chrome belongs to the platform; the sentence does not.
	for _, c := range []struct{ what, sentence string }{
		{"runaway recursion", "A function is calling itself without reaching a base case"},
		{"a field the types promised", "did not come from this program's types"},
		// The phone used to encode an unresolved division as null, silently,
		// because its codec cannot throw. `JSON.encode { q = 1.0 / 3.0 }`
		// shipped {"q":null} off the device and the receiver got a null where a
		// number belonged. The other two refused with this sentence.
		{"an unresolved division", "cannot encode a Decimal.Division"},
	} {
		for _, f := range []struct{ label, path string }{
			{"server", "../runtime"},
			{"browser", "../jsserve"},
			{"phone", "../iosbundle/template/Sources"},
		} {
			if !sentenceIn(t, f.path, c.sentence) {
				t.Errorf("the %s runtime no longer says %q.\n"+
					"    It is the sentence a user reads about %s, and the other two still say it.",
					f.label, c.sentence, c.what)
			}
		}
	}
}

func sentenceIn(t *testing.T, dir, sentence string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext != ".go" && ext != ".js" && ext != ".swift" {
			continue
		}
		seen++
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		// Source text, not runtime values, so the escaping is each language's.
		// The browser writes this sentence inside single quotes and escapes the
		// apostrophe: `program\'s`. Comparing raw bytes reported the browser as
		// having lost a sentence it says. Fourth measurement artifact in this
		// work, and the same shape as the other three -- the instrument
		// appearing to accuse the code.
		body := strings.NewReplacer(`\'`, "'", `\"`, `"`).Replace(string(b))
		if strings.Contains(body, sentence) {
			return true
		}
	}
	if seen == 0 {
		t.Fatalf("no sources scanned in %s; the walk is wrong, not the code", dir)
	}
	return false
}
