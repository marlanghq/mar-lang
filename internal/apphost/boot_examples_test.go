package apphost

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/jsserve"
	"mar/internal/project"
	"mar/internal/runtime"
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
	dirs, err := filepath.Glob("../../examples/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no examples found: %v", err)
	}

	var booted, identified int
	var refusals, guessing []string
	for _, dir := range dirs {
		entry := filepath.Join(dir, "Main.mar")
		if _, err := os.Stat(entry); err != nil {
			continue // not a project (the-last-developer holds no sources)
		}
		name := filepath.Base(dir)
		func() {
			defer func() {
				// A panic here is a bug in the checks, not in the example.
				if r := recover(); r != nil {
					t.Errorf("%s panicked during boot: %v", name, r)
				}
			}()
			runtime.ResetForReload()
			runtime.SetDBPath(filepath.Join(t.TempDir(), "boot.db"))
			var notes bytes.Buffer
			routeNoteWriter = &notes
			defer func() { routeNoteWriter = os.Stderr }()

			lp := &jsserve.LiveProgram{}
			rEnv, mods, _, err := project.LoadIntoEnvWithModulesAndHook(entry,
				func(env *runtime.Env, ms []*ast.Module) { Install(env, ms, 0, lp) })
			if err != nil {
				// A boot check raised from inside the loader's hook arrives
				// HERE, not from eff.Run(). The first version of this test
				// treated a load failure as "skip", which let a check that
				// refused two working examples read as a pass — the exact
				// thing the test exists to prevent. A load failure that
				// mentions one of the checks is a refusal; anything else is
				// a broken example, which check-examples already covers.
				if isBootRefusal(err) {
					refusals = append(refusals, name+": "+strings.ReplaceAll(err.Error(), "\n", " "))
				}
				return
			}
			mainV, ok := project.LookupMain(rEnv, mods)
			if !ok {
				return
			}
			eff, isEff := mainV.(runtime.VEffect)
			if !isEff {
				return
			}
			booted++
			if _, err := eff.Run(); err == nil {
				// A boot check that never fires is not a check. Twelve
				// examples declare `identify`, and the framework used to
				// ignore it and guess; this asserts the declaration is what
				// answers, so a silent return to guessing fails here.
				if cfg := runtime.CurrentAuth(); cfg != nil && cfg.Identify != nil {
					col, fromIdentify := runtime.ResolveIdentityColumn(*cfg)
					if !fromIdentify {
						guessing = append(guessing, name+" (guessed `"+col+"`)")
					}
					identified++
				}
			} else {
				refusals = append(refusals, name+": "+strings.ReplaceAll(err.Error(), "\n", " "))
			}
		}()
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
