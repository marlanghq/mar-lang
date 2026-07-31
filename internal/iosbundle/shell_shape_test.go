package iosbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What has to match across platforms is the page LIFECYCLE, not the chrome.
//
// A program says `App.frontend [a, b]` — these are the pages. How the user
// gets between them is the platform's call, and it should differ: a stack and
// a back button on iPhone, plausibly a sidebar on iPad, a drawer on Android, a
// URL bar on the web. None of that is Mar's business.
//
// What IS Mar's business is ADR-0009: a page's model lives on the navigation
// stack, so going somewhere new runs init and coming back restores what the
// page had. An app can observe that directly — whether init re-runs, whether
// its effects fire again, whether the model it left is still there. If it
// answers differently per platform, the same program is two programs.
//
// The old iOS TabView broke exactly that, and not because it was a tab bar:
// it mounted every page at once and switched by visibility, so navPath never
// moved and there was no push to re-init from. A tab bar that selects by
// writing navPath would keep the lifecycle intact and is fine.
//
// So the invariant checked here is chrome-agnostic: the shell RESOLVES a
// route, it does not mount pages. Exactly one page host is alive, and which
// one is a function of AppContext.navPath.
func TestTheShellResolvesRoutesAndDoesNotMountPages(t *testing.T) {
	dir := filepath.Join("template", "Sources")
	b, err := os.ReadFile(filepath.Join(dir, "ContentView.swift"))
	if err != nil {
		t.Fatalf("reading ContentView.swift: %v", err)
	}
	source := string(b)

	// The shells: the one that picks the app's shape, and the one that owns
	// navigation for a multi-page app. Whatever chrome they grow, neither may
	// hold a page host of its own.
	for _, shell := range []string{"LoadedShell", "StackShell"} {
		body, ok := structBody(source, shell)
		if !ok {
			t.Errorf("could not find `struct %s` — this check has gone stale, and "+
				"it is the only thing standing between the chrome and ADR-0009.", shell)
			continue
		}
		if containsStatement(body, "MarPageHost(") {
			t.Errorf("%s builds a MarPageHost itself.\n"+
				"  symptom: the shell decides what is mounted instead of resolving "+
				"a route, which is how several pages end up alive at once. Then a "+
				"move between them is a visibility change, not a push: init never "+
				"re-runs, Back restores nothing, and ADR-0009 quietly stops "+
				"holding on iOS while the web still honors it.\n"+
				"  Mount through RouteView(path:) so the live page stays a "+
				"function of navPath.", shell)
		}
	}

	// And the multi-page shell has to actually read the nav path — a shell
	// that mounts through RouteView but picks the path from somewhere else
	// (a @State selection, an index into `pages`) satisfies the check above
	// and still detaches navigation from Nav.push/Nav.replace.
	body, ok := structBody(source, "StackShell")
	if ok && !containsStatement(body, "ctx.navPath") {
		t.Error("StackShell no longer reads ctx.navPath.\n" +
			"  symptom: Nav.push and Nav.replace stop driving what is on screen, " +
			"so the app's own navigation effects and the chrome disagree.")
	}

	// This is a proxy and it is worth naming as one: it can show that the
	// wiring still runs through navPath, never that the lifecycle is right.
	// The web has a real behavioural guard for that (nav_lifecycle_test.go
	// drives the actual runtime); iOS does not, and the sequence was checked
	// by hand on the simulator. Until an iOS equivalent exists, a change to
	// the shell deserves that check re-run by hand.
}

// structBody returns the source between `struct <name>` and the closing brace
// in column 0 that ends it. Swift top-level declarations always close there,
// so no brace counting is needed — and matching too much is the failure mode
// that lets a sabotage through, not too little.
func structBody(source, name string) (string, bool) {
	i := strings.Index(source, "struct "+name)
	if i < 0 {
		return "", false
	}
	rest := source[i:]
	if j := strings.Index(rest, "\n}\n"); j >= 0 {
		return rest[:j], true
	}
	return rest, true
}
