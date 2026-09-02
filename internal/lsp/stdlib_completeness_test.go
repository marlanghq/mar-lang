package lsp

import (
	"sort"
	"strings"
	"testing"

	"mar/internal/typecheck"
)

// Hover, go-to-definition and completion all read one table, and that table
// was missing a fifth of the language: BaseQualifiedSymbols is built from a
// mapping of Qualified -> bare aliases, so a name already written qualified at
// the source never entered it. Measured before this test existed:
//
//	Decimal.toCents   not found
//	Keyboard.KeyA     not found
//	Service.Offline   not found
//	List.map          found
//
// All four compile. An editor that can describe the fourth and not the first
// three is wrong about the language rather than opinionated about what to
// offer, which is why this is a test and not a preference.
func TestTheEditorKnowsEveryNameAProgramCanWrite(t *testing.T) {
	var missing []string
	for name := range typecheck.AllQualifiedSymbols() {
		if _, ok := LookupStdlib(name); !ok {
			missing = append(missing, name)
		}
	}
	for name := range typecheck.BareGlobals() {
		if _, ok := LookupStdlib(name); !ok {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		show := missing
		if len(show) > 12 {
			show = append(show[:12:12], "…")
		}
		t.Errorf("the editor cannot describe %d name(s) a program can write:\n  %s\n\n"+
			"Hover and go-to-definition go through LookupStdlib, so each of these is a\n"+
			"name that compiles and about which the editor says nothing.",
			len(missing), strings.Join(show, "\n  "))
	}
	if !t.Failed() {
		t.Logf("the editor describes all %d qualified names and all %d bare globals",
			len(typecheck.AllQualifiedSymbols()), len(typecheck.BareGlobals()))
	}
}
