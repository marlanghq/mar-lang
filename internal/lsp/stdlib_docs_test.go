package lsp_test

import (
	"sort"
	"strings"
	"testing"

	"mar/internal/lsp"
	"mar/internal/refgen"
	"mar/internal/typecheck"
)

// The reference page and the editor describe the same language, and only one of
// them had anything to say. Measured before this: 733 stdlib symbols in the
// editor and ZERO with a description, while internal/refgen carried a sentence
// for 526 of the 544 qualified names and a worked example for every one of its
// 535 entries.
//
// So hover on `Decimal.toCents` was a type and the word "built-in", with the
// sentence explaining it sitting in the same tree. Not a matter of taste: the
// prose was already written, already reviewed, and already published.
//
// What makes the examples worth showing rather than merely nice is that they
// are the reference's, and a test evaluates every one and requires it to be
// True. Hover cannot drift from what the language does, because the thing it
// prints is the thing that is checked.
func TestTheEditorSaysWhatTheReferenceSays(t *testing.T) {
	docs := map[string]refgen.FnEntry{}
	for _, e := range refgen.Entries() {
		if e.Module == refgen.BasicsModule {
			docs[e.Name] = e
			continue
		}
		docs[e.Qualified()] = e
	}

	var missing, wrong []string
	described := 0
	for name := range typecheck.AllQualifiedSymbols() {
		want, documented := docs[name]
		sym, found := lsp.LookupStdlib(name)
		if !found {
			missing = append(missing, name+" (not in the editor at all)")
			continue
		}
		if !documented {
			// Nothing authored for it. The nine Mar.Admin.* are the internal
			// admin service, which the reference deliberately omits, and they
			// are the only ones -- so this arm is not a hole to fill, it is
			// the shape of the reference being about the language a person
			// writes rather than about every name that exists.
			if sym.Doc != "" {
				wrong = append(wrong, name+" has a description the reference does not")
			}
			continue
		}
		if sym.Doc != want.Desc {
			missing = append(missing, name+" (the editor's prose is not the reference's)")
			continue
		}
		if len(sym.Examples) != len(want.Examples) {
			wrong = append(wrong, name+" carries a different number of examples")
			continue
		}
		described++
	}
	sort.Strings(missing)
	sort.Strings(wrong)
	for _, m := range append(missing, wrong...) {
		t.Error(m)
	}

	// A floor, so this cannot pass by describing nothing. 535 is every
	// qualified name except the nine Mar.Admin.* the reference omits on
	// purpose -- I first wrote 526 here, which is the count BEFORE the bare
	// globals are keyed by their Basics entries, and a floor nine below the
	// real number is nine regressions this would not notice.
	const floor = 535
	if described < floor {
		t.Errorf("the editor describes %d qualified names and used to describe %d.\n"+
			"    Either a builtin lost its reference entry, or the lookup stopped matching.",
			described, floor)
	}

	// And the two spellings that are easy to get wrong, since Basics is not a
	// module: `not` is written bare and documented as Basics.not.
	for _, n := range []string{"not", "always", "linkTo", "modBy"} {
		s, ok := lsp.LookupStdlib(n)
		if !ok {
			t.Errorf("the editor does not know the bare global %q", n)
			continue
		}
		if s.Doc == "" {
			t.Errorf("the bare global %q has no description; the reference documents it as %s.%s",
				n, refgen.BasicsModule, n)
		}
	}

	if !t.Failed() {
		t.Logf("the editor describes %d of %d qualified names, with the reference's own prose and examples",
			described, len(typecheck.AllQualifiedSymbols()))
	}
}

// Hover has to actually print it. The wiring can be right and the renderer
// still drop it, which is the gap that existed for the whole of the reference's
// life so far.
func TestHoverPrintsTheDescriptionAndTheExample(t *testing.T) {
	s, ok := lsp.LookupStdlib("Decimal.toCents")
	if !ok {
		t.Fatal("Decimal.toCents is not in the editor's stdlib")
	}
	out := lsp.HoverMarkdown(s)
	for _, want := range []string{
		"Decimal.toCents : Decimal -> Int", // the type
		"count of hundredths",              // the prose
		"Decimal.fromCents 250",            // the example
		"-- 250",                           // its verified result, as a comment
		"_built-in_",                       // and the footer that was all there was
	} {
		if !strings.Contains(out, want) {
			t.Errorf("hover does not contain %q:\n%s", want, out)
		}
	}
}
