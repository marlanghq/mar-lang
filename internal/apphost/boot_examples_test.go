package apphost

import (
	"strings"
	"testing"
)

// The floor under every boot check in this package.
//
// Moving a failure from run time to boot time is only an improvement while it
// refuses programs that were already broken. A boot check that refuses a
// program which works today is not a stricter language, it is a regression —
// so every example in the repository is booted here, far enough to run `main`,
// which is what registers the routes the checks read.
//
// It boots them rather than type-checking them on purpose: `mar check` cannot
// see any of this. A route table exists only once `main` has run.
// isBootRefusal separates "this example does not load" from "a check in this
// package refused it". Only the second is this test's subject.
func isBootRefusal(err error) bool {
	msg := err.Error()
	for _, marker := range []string{
		"App.backend:", "App.fullstack:", "App.frontend:",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func TestNoExampleIsRefusedAtBoot(t *testing.T) {
	if testing.Short() {
		t.Skip("boots every example; skipped under -short")
	}

	var booted, identified int
	var refusals, guessing []string
	for _, ex := range loadEveryExample(t) {
		// A panic here is a bug in the checks, not in the example.
		if ex.panicked != nil {
			t.Errorf("%s panicked during boot: %v", ex.name, ex.panicked)
			continue
		}
		if ex.loadErr != nil {
			// A boot check raised from inside the loader's hook arrives
			// HERE, not from eff.Run(). The first version of this test
			// treated a load failure as "skip", which let a check that
			// refused two working examples read as a pass — the exact
			// thing the test exists to prevent. A load failure that
			// mentions one of the checks is a refusal; anything else is
			// a broken example, which check-examples already covers.
			if isBootRefusal(ex.loadErr) {
				refusals = append(refusals, ex.name+": "+strings.ReplaceAll(ex.loadErr.Error(), "\n", " "))
			}
			continue
		}
		if !ex.ran {
			continue
		}
		booted++
		if ex.runErr != nil {
			refusals = append(refusals, ex.name+": "+strings.ReplaceAll(ex.runErr.Error(), "\n", " "))
			continue
		}
		// A boot check that never fires is not a check. Examples
		// declare `identify` (the log line below counts them), and
		// the framework used to ignore it and guess; this asserts the
		// declaration is what answers, so a silent return to guessing
		// fails here.
		if ex.hasIdentify {
			if !ex.fromIdentify {
				guessing = append(guessing, ex.name+" (guessed `"+ex.identityCol+"`)")
			}
			identified++
		}
	}

	if booted < 30 {
		t.Fatalf("only %d examples booted; the harness is not exercising them", booted)
	}
	if len(guessing) > 0 {
		t.Errorf("%d example(s) declare `identify` and the framework still guessed the column:\n  %s",
			len(guessing), strings.Join(guessing, "\n  "))
	}
	if identified == 0 {
		t.Error("no example exercised `identify`; the check is not being reached")
	}
	if len(refusals) > 0 {
		t.Fatalf("%d example(s) that work today would no longer start:\n  %s",
			len(refusals), strings.Join(refusals, "\n  "))
	}
	t.Logf("%d examples boot with every check on, and none is refused; %d resolve their identity column from `identify`", booted, identified)
}
