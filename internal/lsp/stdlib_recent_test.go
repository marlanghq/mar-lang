package lsp

import (
	"strings"
	"testing"
)

// TestStdlibCoversRecentAdditions sanity-checks that every builtin we
// added in the recent stdlib expansion rounds (Dict, Set, Char, the
// String/Char bridges, the Order ADT) shows up in the LSP symbol set
// the editor sees. Since LSP reads from typecheck.BaseQualifiedSymbols
// directly, the failure mode is "stdlib regression deleted something";
// adding to env.go without thinking about LSP is automatically safe.
func TestStdlibCoversRecentAdditions(t *testing.T) {
	syms := StdlibSymbols()
	have := make(map[string]Symbol, len(syms))
	for _, s := range syms {
		have[s.Name] = s
	}

	mustHave := []string{
		// Dict (comparable-key constraint)
		"Dict.empty", "Dict.singleton", "Dict.insert", "Dict.get",
		"Dict.fromList", "Dict.toList", "Dict.foldl", "Dict.union",
		// Set
		"Set.empty", "Set.fromList", "Set.member", "Set.union",
		// Char module
		"Char.toCode", "Char.fromCode",
		"Char.isDigit", "Char.isAlpha", "Char.isUpper", "Char.isLower",
		"Char.toUpper", "Char.toLower",
		// String bridges
		"String.toList", "String.fromList", "String.cons",
		"String.map", "String.filter", "String.foldl", "String.any",
		// Order ADT
		"Order", "LT", "EQ", "GT",
		// sortWith should be present (and reference Order)
		"List.sortWith",
	}

	for _, name := range mustHave {
		if _, ok := have[name]; !ok {
			t.Errorf("missing stdlib symbol from LSP: %s", name)
		}
	}

	// sortWith should advertise Order in its rendered type, not Int.
	// Catches the regression of "someone re-introduced -1/0/1 Int
	// comparator semantics".
	if s, ok := have["List.sortWith"]; ok {
		if !strings.Contains(s.Type, "Order") {
			t.Errorf("List.sortWith should mention Order in its type, got: %s", s.Type)
		}
	}

	// Dict.empty's type should mention Comparable through naming, or
	// at least reach Dict.
	if s, ok := have["Dict.empty"]; ok {
		if !strings.Contains(s.Type, "Dict") {
			t.Errorf("Dict.empty type should mention Dict, got: %s", s.Type)
		}
	}
}
