package typecheck_test

import (
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"mar/internal/typecheck"
)

// The editor plugin's grammar carries three lists of the language's own names,
// and nothing checked them. Measured when this was written: it knew 9 of the
// 18 bare constructors. GET, POST, PUT, PATCH, DELETE, Coarse, Fine, Crisp and
// Pixelated are all names a program writes with no module, and none of them
// coloured as a constructor.
//
// It is the same shape as everything else in this work -- a list of names kept
// by hand in a second place -- and the same answer: a list with a census does
// not go stale. The three lists are held to different standards on purpose,
// which is the part worth reading:
//
//	constructors   EXACTLY the language's bare upper-case names
//	bare globals   EXACTLY BareGlobals()
//	types          a SUPERSET of ReservedTypeNames()
//
// The types list is a superset because the plugin also colours the view host's
// phantom types -- Color, Size, Shape -- which are not reserved: an app may
// define its own Color, and whether the editor should still paint the word is
// the plugin's taste, not the language's rule. Demanding equality there would
// be this test having an opinion it has no standing for.
func TestTheEditorGrammarKnowsTheLanguagesNames(t *testing.T) {
	const grammar = "../../vscode-mar/syntaxes/mar.tmLanguage.json"
	b, err := os.ReadFile(grammar)
	if err != nil {
		t.Fatalf("the plugin's grammar: %v", err)
	}
	var doc any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("the plugin's grammar is not valid JSON: %v", err)
	}

	var matches []string
	var walk func(any)
	walk = func(o any) {
		switch v := o.(type) {
		case map[string]any:
			for k, x := range v {
				if k == "match" {
					if s, ok := x.(string); ok {
						matches = append(matches, s)
					}
					continue
				}
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(doc)

	// An alternation of bare words, which is how each of the three lists is
	// written. Anything with a character class or a capture of its own is a
	// structural rule rather than a vocabulary list.
	alternation := regexp.MustCompile(`^\\b\(([A-Za-z|_&*+/<>=:!.-]+)\)\\b$`)
	lists := map[string][]string{}
	for _, m := range matches {
		g := alternation.FindStringSubmatch(m)
		if g == nil {
			continue
		}
		names := strings.Split(g[1], "|")
		sort.Strings(names)
		// Keyed by a name that only that list contains.
		switch {
		case contains(names, "Just"):
			lists["ctors"] = names
		case contains(names, "always"):
			lists["globals"] = names
		case contains(names, "Duration"):
			lists["types"] = names
		}
	}

	// A list this test cannot find is a list it is not checking, and a silent
	// pass is the failure mode this whole series keeps naming.
	for _, want := range []string{"ctors", "globals", "types"} {
		if lists[want] == nil {
			t.Fatalf("no %s list found in the grammar: the shape changed, and this test "+
				"stopped looking rather than started failing", want)
		}
	}

	var langCtors []string
	for n := range typecheck.AllBareSymbols() {
		if n != "" && n[0] >= 'A' && n[0] <= 'Z' {
			langCtors = append(langCtors, n)
		}
	}
	sort.Strings(langCtors)
	if d := diff(langCtors, lists["ctors"]); d != "" {
		t.Errorf("the grammar's constructors do not match the language's:\n%s\n"+
			"    Every bare upper-case name a program can write should colour as one.", d)
	}

	var langGlobals []string
	for n := range typecheck.BareGlobals() {
		langGlobals = append(langGlobals, n)
	}
	sort.Strings(langGlobals)
	if d := diff(langGlobals, lists["globals"]); d != "" {
		t.Errorf("the grammar's bare globals do not match the language's:\n%s", d)
	}

	for _, n := range typecheck.ReservedTypeNames() {
		if !contains(lists["types"], n) {
			t.Errorf("the grammar does not know the reserved type %q; "+
				"a name the language owns should colour as one", n)
		}
	}

	if !t.Failed() {
		t.Logf("the grammar carries %d constructors, %d bare globals and %d type names, "+
			"covering all %d reserved ones",
			len(lists["ctors"]), len(lists["globals"]), len(lists["types"]),
			len(typecheck.ReservedTypeNames()))
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func diff(want, got []string) string {
	var b strings.Builder
	for _, w := range want {
		if !contains(got, w) {
			b.WriteString("      missing from the grammar: " + w + "\n")
		}
	}
	for _, g := range got {
		if !contains(want, g) {
			b.WriteString("      in the grammar, not in the language: " + g + "\n")
		}
	}
	return b.String()
}
