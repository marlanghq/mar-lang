package typecheck

import (
	"sort"
	"strings"
	"testing"
)

// The side table has to cover every qualified builtin a user can write. Its
// hand-maintained predecessor drifted precisely because nothing forced it to
// stay complete: a new builtin inherited "not backend-only" by silence, and
// `App.frontend` sat in the wrong column for a release.
//
// Same spirit as internal/ctorgen's staleness test: add a builtin, and the
// build tells you the classification is missing rather than guessing for you.
func TestEveryBuiltinHasASide(t *testing.T) {
	flat := baseBindings()
	qualified := map[string]bool{}
	for n := range flat {
		if strings.Contains(n, ".") {
			qualified[n] = true
		}
	}
	for n := range qualifiedAliases(flat) {
		qualified[n] = true
	}
	if len(qualified) == 0 {
		t.Fatal("no qualified builtins found — this test is not testing anything")
	}

	missing := map[string][]string{}
	for name := range qualified {
		if _, ok := SideOf(name); !ok {
			mod := name[:strings.LastIndex(name, ".")]
			missing[mod] = append(missing[mod], name)
		}
	}
	if len(missing) == 0 {
		return
	}
	var mods []string
	for m := range missing {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	for _, m := range mods {
		sort.Strings(missing[m])
		t.Errorf("module %q has no side: add it to sideByModule in sides.go "+
			"(%d names, e.g. %s)", m, len(missing[m]), missing[m][0])
	}
}

// Names whose side differs from their module's. If one of these regresses to
// its module default the check still passes overall, so assert them directly.
func TestSplitModulesKeepPerNameSides(t *testing.T) {
	cases := map[string]Side{
		"Service.declare":   SideBoth,
		"Service.call":      SideFrontend,
		"Service.implement": SideBackend,
		"Auth.me":           SideFrontend,
		"Auth.protect":      SideBackend,
		"Repo.all":          SideBackend,
		"UI.text":           SideFrontend,
		"Time.now":          SideBoth,
		"App.fullstack":     SideEntry,
		"not":               SideBoth, // bare global
	}
	for name, want := range cases {
		got, ok := SideOf(name)
		if !ok {
			t.Errorf("SideOf(%q): unknown", name)
			continue
		}
		if got != want {
			t.Errorf("SideOf(%q) = %v, want %v", name, got, want)
		}
	}
}

// The coverage exemption is derived from the side, and the derivation is the
// point: these two facts drifted apart when they were two lists.
func TestBackendOnlyIsDerivedFromSide(t *testing.T) {
	for _, name := range []string{"Repo.all", "Entity.define", "Auth.protect", "Service.implement", "App.frontend"} {
		if !IsBackendOnlyBuiltin(name) {
			t.Errorf("%s should be exempt from client-runtime coverage", name)
		}
	}
	for _, name := range []string{"Service.declare", "Service.call", "UI.text", "List.map", "Auth.me"} {
		if IsBackendOnlyBuiltin(name) {
			t.Errorf("%s must NOT be exempt — client runtimes implement it", name)
		}
	}
}
