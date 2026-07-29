package refgen

import (
	"os"
	"testing"
)

// dataMarPath is the committed generated module, relative to this package dir.
const dataMarPath = "../../" + DataMarRelPath

// TestDataMarIsCurrent is the lock on the scheme: it regenerates the module in
// memory and fails if the committed file disagrees. This makes "add a stdlib
// function (or edit content.go) and forget to regenerate" impossible to merge —
// the signatures on the website stay identical to the compiler's.
func TestDataMarIsCurrent(t *testing.T) {
	have, err := os.ReadFile(dataMarPath)
	if err != nil {
		t.Fatalf("reading %s (run `go generate ./internal/refgen` if it does not exist): %v", dataMarPath, err)
	}
	if string(have) != MarModule() {
		t.Fatalf("%s is stale.\nFix: go generate ./internal/refgen", DataMarRelPath)
	}
}

// TestCategoriesCoverAllExports keeps the grouping exhaustive and honest: every
// function the compiler exports for a covered module appears in exactly one
// category, and every categorized name is really exported (so a typo in
// content.go is a failure, not a silently missing entry).
func TestCategoriesCoverAllExports(t *testing.T) {
	for _, mod := range Modules {
		exported := map[string]bool{}
		for n := range exportsOf(mod) {
			exported[n] = true
		}
		seen := map[string]bool{}
		for _, cg := range categories[mod] {
			for _, name := range cg.Funcs {
				if !exported[name] {
					t.Errorf("%s.%s is categorized in content.go but not exported by the compiler (typo?)", mod, name)
				}
				if seen[name] {
					t.Errorf("%s.%s is listed in more than one category", mod, name)
				}
				seen[name] = true
			}
		}
		for name := range exported {
			if !seen[name] {
				t.Errorf("%s.%s is exported but missing from the categories in content.go", mod, name)
			}
		}
	}
}

// TestDescriptionsCoverAllExports keeps the prose exhaustive: every documented
// function has a description. (Entries only includes categorized functions, so
// this rides on the category coverage above.)
func TestDescriptionsCoverAllExports(t *testing.T) {
	var missing []string
	for _, e := range Entries() {
		if e.Desc == "" {
			missing = append(missing, e.Qualified())
		}
	}
	if len(missing) > 0 {
		t.Fatalf("missing descriptions in internal/refgen/content.go for: %v", missing)
	}
}

// TestEntriesShape pins basic invariants so a broken extraction is loud.
func TestEntriesShape(t *testing.T) {
	entries := Entries()
	if len(entries) < 50 {
		t.Fatalf("suspiciously few reference entries: %d (List+String+Maybe should be ~60)", len(entries))
	}
	want := map[string]string{
		"List.map":          "(a -> b) -> List a -> List b",
		"Maybe.withDefault": "a -> Maybe a -> a",
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Qualified()] = e.Signature
	}
	for q, sig := range want {
		if got[q] != sig {
			t.Fatalf("%s : %q, want %q", q, got[q], sig)
		}
	}
}

// TestBlurbsCoverAllModules keeps the index honest: a module added to Modules
// without a one-liner would render as a bare name with an empty line under it,
// which is how the reference looked for a while before the blurbs moved out of
// the website and into the generator.
func TestBlurbsCoverAllModules(t *testing.T) {
	for _, mod := range Modules {
		if blurbs[mod] == "" {
			t.Errorf("%s has no blurb (add one to the blurbs map in the content files)", mod)
		}
	}
}

// TestModuleGroupsCoverAllModules keeps the index exhaustive: every module sits
// in exactly one group, and no group names a module that is not in the
// reference. Without this, adding a module to Modules would put it on nobody's
// section and it would simply not appear.
func TestModuleGroupsCoverAllModules(t *testing.T) {
	inReference := map[string]bool{}
	for _, m := range Modules {
		inReference[m] = true
	}
	grouped := map[string]bool{}
	for _, g := range moduleGroups {
		for _, m := range g.Modules {
			if !inReference[m] {
				t.Errorf("group %q names %s, which is not in Modules", g.Title, m)
			}
			if grouped[m] {
				t.Errorf("%s appears in more than one group", m)
			}
			grouped[m] = true
		}
	}
	for _, m := range Modules {
		if !grouped[m] {
			t.Errorf("%s is in the reference but in no group (add it to moduleGroups in content.go)", m)
		}
	}
}
