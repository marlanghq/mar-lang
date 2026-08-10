package conformance

import (
	"sort"
	"strings"
	"testing"

	"mar/internal/typecheck"
)

// The gate. A stdlib function nobody exercises is a function whose runtimes are
// free to disagree, so not testing one has to be a build failure rather than an
// omission somebody might notice.
func TestCorpusCoversEveryFunctionInScope(t *testing.T) {
	var missing []string
	for _, name := range typecheck.BaseEnv().Names() {
		i := strings.Index(name, ".")
		if i <= 0 {
			continue // bare operators and globals; not module surface
		}
		if !Scope[name[:i]] {
			continue
		}
		if !strings.Contains(Source, name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d stdlib functions are not exercised by the corpus, so the runtimes\n"+
			"are free to disagree about them. Add a case to Source for each, or move\n"+
			"its module to OutOfScope with a reason:\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// The same gate for the names spelled WITHOUT a qualifier. The module walk
// above skips them by construction ("bare operators and globals; not module
// surface"), and that blind spot is where `modBy` lived: defined in all three
// runtimes, exercised in none, and wrong in one for as long as it took a game
// to leave a window minimised overnight.
func TestCorpusCoversEveryBareGlobal(t *testing.T) {
	var missing []string
	for name := range typecheck.BareGlobals() {
		if _, explained := OutOfScopeBare[name]; explained {
			continue
		}
		if !strings.Contains(Source, name) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d bare globals are not exercised by the corpus, so the runtimes are\n"+
			"free to disagree about them. Add a case to Source for each, or explain it\n"+
			"in OutOfScopeBare:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
}

// Every stdlib module is either compared here or explained. A module in
// neither list fails the build, which is what keeps a newly added one from
// going untested by simply not being noticed — the first run of this check
// found JSON, whose encoder every client and server share.
func TestEveryStdlibModuleIsClassified(t *testing.T) {
	seen := map[string]bool{}
	for _, name := range typecheck.BaseEnv().Names() {
		if i := strings.Index(name, "."); i > 0 {
			seen[name[:i]] = true
		}
	}
	var unclassified []string
	for module := range seen {
		if Scope[module] {
			continue
		}
		if _, explained := OutOfScope[module]; !explained {
			unclassified = append(unclassified, module)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf("%d stdlib modules are neither in Scope nor explained in OutOfScope:\n  %s\n"+
			"Decide whether their answers must be identical across runtimes. If yes, add\n"+
			"cases to Source and put them in Scope; if no, put them in OutOfScope with the\n"+
			"reason.", len(unclassified), strings.Join(unclassified, "\n  "))
	}

	// And the reverse: a module that stops existing should not leave a stale
	// excuse behind, which would read as coverage of something that is gone.
	var stale []string
	for module := range OutOfScope {
		if !seen[module] {
			stale = append(stale, module)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("OutOfScope explains %d modules the stdlib no longer has:\n  %s",
			len(stale), strings.Join(stale, "\n  "))
	}
}

// The expectations have to parse, and every one of them has to name a case the
// corpus can actually produce. Without this, a typo in a label would look like
// a passing test that quietly checks one thing less.
func TestExpectationsAreWellFormed(t *testing.T) {
	want, err := Expectations()
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		t.Fatal("no expectations parsed")
	}
	for name := range want {
		i := strings.Index(name, ".")
		if i <= 0 {
			t.Errorf("expectation %q is not qualified by a block name", name)
			continue
		}
		block, label := name[:i], name[i+1:]
		found := false
		for _, b := range Blocks {
			if b == block {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expectation %q names block %q, which `results` does not join", name, block)
		}
		if !strings.Contains(Source, `"`+label+`="`) {
			t.Errorf("expectation %q has no matching case label in the corpus", name)
		}
	}
}
