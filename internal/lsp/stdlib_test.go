package lsp

import (
	"sort"
	"strings"
	"testing"
)

func TestStdlibSymbolsContainsCoreFramework(t *testing.T) {
	syms := StdlibSymbols()
	if len(syms) == 0 {
		t.Fatal("StdlibSymbols returned no symbols")
	}
	// Spot-check a few names that should always be there. If the
	// underlying typecheck.BaseEnv reshuffles, we want a loud failure.
	want := []string{
		"UI.title",
		"Service.declare",
		"Auth.config",
		"Maybe",
		"Just",
		"Nothing",
		"Result",
		"Ok",
		"Err",
	}
	got := map[string]bool{}
	for _, s := range syms {
		got[s.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("StdlibSymbols missing %q", name)
		}
	}
}

func TestStdlibSymbolsSorted(t *testing.T) {
	syms := StdlibSymbols()
	sorted := sort.SliceIsSorted(syms, func(i, j int) bool {
		return syms[i].Name < syms[j].Name
	})
	if !sorted {
		t.Fatal("StdlibSymbols are not sorted alphabetically")
	}
}

func TestLookupStdlibQualifiedAndConstructor(t *testing.T) {
	cases := []struct {
		name string
		kind SymbolKind
	}{
		{"UI.title", SymValue},
		{"Service.declare", SymValue},
		{"Maybe", SymCustomType},
		{"Just", SymConstructor},
		{"Nothing", SymConstructor},
	}
	for _, c := range cases {
		s, ok := LookupStdlib(c.name)
		if !ok {
			t.Errorf("LookupStdlib(%q): not found", c.name)
			continue
		}
		if s.Kind != c.kind {
			t.Errorf("LookupStdlib(%q): kind=%v, want %v", c.name, s.Kind, c.kind)
		}
	}
}

func TestLookupStdlibUnknown(t *testing.T) {
	if _, ok := LookupStdlib("NotARealStdlibSymbol"); ok {
		t.Fatal("LookupStdlib accepted unknown symbol")
	}
}

func TestStdlibValuesHavePrettyTypes(t *testing.T) {
	// Built-in values without a type signature would render as a bare
	// name in hover — almost certainly a regression in BaseEnv plumbing.
	for _, s := range StdlibSymbols() {
		if s.Kind != SymValue {
			continue
		}
		if s.Type == "" {
			t.Errorf("stdlib value %q has empty Type", s.Name)
		}
	}
}

func TestStdlibHoverHasBuiltinFooter(t *testing.T) {
	s, ok := LookupStdlib("UI.title")
	if !ok {
		t.Fatal("UI.title not in stdlib")
	}
	md := HoverMarkdown(s)
	if !strings.Contains(md, "_built-in_") {
		t.Errorf("HoverMarkdown for stdlib value missing built-in footer: %q", md)
	}
	if !strings.Contains(md, "UI.title") {
		t.Errorf("HoverMarkdown missing symbol name: %q", md)
	}
}

func TestStdlibHoverConstructorFooter(t *testing.T) {
	s, ok := LookupStdlib("Just")
	if !ok {
		t.Fatal("Just not in stdlib")
	}
	md := HoverMarkdown(s)
	if !strings.Contains(md, "_built-in constructor_") {
		t.Errorf("HoverMarkdown for stdlib constructor missing built-in footer: %q", md)
	}
}

func TestStdlibCustomTypeSummary(t *testing.T) {
	s, ok := LookupStdlib("Maybe")
	if !ok {
		t.Fatal("Maybe not in stdlib")
	}
	if s.Kind != SymCustomType {
		t.Fatalf("Maybe kind=%v, want SymCustomType", s.Kind)
	}
	// Summary should look like `type Maybe a = Just _ | Nothing` —
	// we only assert it starts with "type Maybe" so additions to the
	// constructor list don't break the test, but the prefix proves the
	// reconstruction happened.
	if !strings.HasPrefix(s.Summary, "type Maybe") {
		t.Errorf("Maybe summary prefix: got %q", s.Summary)
	}
	if !strings.Contains(s.Summary, "Just") || !strings.Contains(s.Summary, "Nothing") {
		t.Errorf("Maybe summary missing constructors: %q", s.Summary)
	}
}
