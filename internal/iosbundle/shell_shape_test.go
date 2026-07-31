package iosbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shell decides what a multi-page app IS, not just how it looks.
//
// iOS used to route all-public, all-static apps into a TabView with an icon
// guessed from the path. That is not a skin over the same app: tabs keep every
// page alive side by side, so ADR-0009 — a push re-inits the page, Back
// restores the model it had — simply did not apply on iOS for those apps,
// while the identical program on the web was an ordinary navigation stack.
// `App.frontend [a, b]` has to mean the same thing on both platforms.
//
// A regression here compiles, renders, and looks reasonable in a screenshot;
// only the navigation semantics diverge. So the check is structural: the tab
// container must not come back, and the multi-page branch must reach the
// stack.
func TestMultiPageAppsAreANavigationStack(t *testing.T) {
	dir := filepath.Join("template", "Sources")
	names, err := filepath.Glob(filepath.Join(dir, "*.swift"))
	if err != nil {
		t.Fatalf("listing the template sources: %v", err)
	}

	for _, name := range names {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for _, banned := range []string{"TabView", ".tabItem"} {
			if containsStatement(string(b), banned) {
				t.Errorf("%s uses %s.\n"+
					"  symptom: pages live side by side instead of on a stack, "+
					"so ADR-0009 stops holding on iOS — a push no longer "+
					"re-inits and Back no longer restores, and the same "+
					"program behaves differently on the web.",
					filepath.Base(name), banned)
			}
		}
	}

	// Banning the old container is not enough on its own — the next container
	// would have a different name. So pin the multi-page branch itself: the
	// `else` that follows the single-page case is where the shape is chosen,
	// and the one thing allowed to live there is the stack. Anchoring on the
	// whole file passed the sabotage above, because the single-page branch
	// mentions StackShell too.
	shell, err := os.ReadFile(filepath.Join(dir, "ContentView.swift"))
	if err != nil {
		t.Fatalf("reading ContentView.swift: %v", err)
	}
	const singlePage = "} else if pages.count == 1 {"
	i := strings.Index(string(shell), singlePage)
	if i < 0 {
		t.Fatalf("could not find %q — this check has gone stale", singlePage)
	}
	multiPage := string(shell)[i+len(singlePage):]
	if j := strings.Index(multiPage, "} else {"); j >= 0 {
		multiPage = multiPage[j:]
	} else {
		t.Fatal("the single-page case has no multi-page `else` — this check has gone stale")
	}
	if !containsStatement(multiPage, "StackShell(pages: pages)") {
		t.Error("the multi-page branch no longer mounts StackShell.\n" +
			"  symptom: whatever replaced it decides the app's navigation " +
			"model, and nothing else in the suite would notice.")
	}
}
