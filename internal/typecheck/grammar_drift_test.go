package typecheck

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The editor grammars are hand-written and live outside the compiler, so
// nothing forces them to follow the language. They rotted twice before this
// test existed: `Float` stayed in the builtin-type list for a release after
// the type was deleted, and decimal literals were painted
// `invalid.illegal.float` long after `19.99` became a valid Decimal — the
// editor was calling correct code an error.
//
// This covers the half that can be checked mechanically: the bare globals,
// which the checker exports. The builtin-type list is still maintained by
// hand; there is no exported "every type name" set to diff it against.
//
// Only the VSCode grammar is checked. The Sublime one lives in its own repo
// (marlanghq/mar-sublime) and a test reaching outside this checkout would
// fail for anyone who cloned only mar-lang. Keep the two in sync by hand.
const vscodeGrammarPath = "../../vscode-mar/syntaxes/mar.tmLanguage.json"

func TestVSCodeGrammarKnowsEveryBareGlobal(t *testing.T) {
	raw, err := os.ReadFile(vscodeGrammarPath)
	if err != nil {
		t.Fatalf("read %s: %v", vscodeGrammarPath, err)
	}

	var grammar struct {
		Repository map[string]struct {
			Patterns []struct {
				Name  string `json:"name"`
				Match string `json:"match"`
			} `json:"patterns"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(raw, &grammar); err != nil {
		t.Fatalf("parse %s: %v", vscodeGrammarPath, err)
	}

	var globals string
	for _, p := range grammar.Repository["identifiers"].Patterns {
		if p.Name == "support.function.mar" {
			globals = p.Match
		}
	}
	if globals == "" {
		t.Fatalf("no support.function.mar rule in the identifiers section of %s: "+
			"if it was renamed, this test has to follow it", vscodeGrammarPath)
	}

	for name := range BareGlobals() {
		// The rule is one alternation of whole words, so membership is a
		// substring check against `|name|`-style boundaries.
		if !strings.Contains("|"+globals+"|", "|"+name+"|") &&
			!strings.Contains(globals, "("+name+"|") &&
			!strings.Contains(globals, "|"+name+")") {
			t.Errorf("bare global %q is missing from the editor grammar (%s). "+
				"It is in scope with no import and no module prefix, so nothing "+
				"else in the file hints that it is a language builtin.",
				name, vscodeGrammarPath)
		}
	}
}

// A decimal literal is valid Mar. Painting it `invalid` tells the programmer
// their correct code is broken, which is worse than no highlighting at all.
func TestVSCodeGrammarDoesNotCallValidCodeIllegal(t *testing.T) {
	raw, err := os.ReadFile(vscodeGrammarPath)
	if err != nil {
		t.Fatalf("read %s: %v", vscodeGrammarPath, err)
	}
	if strings.Contains(string(raw), "invalid.illegal") {
		t.Errorf("%s marks something `invalid.illegal`. The language has no "+
			"syntax the editor should flag on its own — `mar check` reports "+
			"errors, with a position and a message the grammar cannot produce.",
			vscodeGrammarPath)
	}
}
