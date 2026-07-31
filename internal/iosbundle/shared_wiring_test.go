package iosbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Shared state (ADR-0026) landed on the web first, and the drift test that
// guards the three builtins only checks that the NAMES exist in Swift. That is
// exactly the gap the deferral comment warned about: stubbing the names would
// let an app compile for iOS and then quietly lose its cart.
//
// So this file checks the WIRING instead — the five places where a missing
// line turns the feature into a stub that compiles. Each check names the
// symptom, because a failure here is silent at runtime: nothing crashes, the
// screen simply stops agreeing with the model.
//
// These are structural checks, not behavioural ones. They cannot prove the
// cart survives navigation on a device; they can prove that the wire which
// carries it has not been cut. Nothing else in the suite can do even that,
// which is why they are worth having.
func TestSharedStoreIsWiredNotStubbed(t *testing.T) {
	dir := filepath.Join("template", "Sources")
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		return string(b)
	}

	store := read("MarSharedStore.swift")
	builtins := read("MarBuiltins.swift")
	context := read("MarAppContext.swift")
	runtime := read("MarPageRuntime.swift")
	viewModel := read("AppViewModel.swift")

	checks := []struct {
		name    string
		source  string
		needle  string
		symptom string
	}{
		{
			"the store dispatches through the app's own update",
			store, "Eval.apply(Eval.apply(updateFn, msg), model)",
			"a shared message would change nothing",
		},
		{
			"App.shared registers a real store, not a placeholder",
			builtins, "MarSharedRegistry.store(",
			"every page would see its own copy of the model",
		},
		{
			"Cmd.toShared reaches the store",
			builtins, "MarSharedRegistry.lookup(key)?.dispatch(msg)",
			"the effect would run and drop the message on the floor",
		},
		{
			"Page.withShared survives decoding",
			context, "__SharedPage",
			"a page built with Page.withShared would vanish from the app",
		},
		{
			"the decoder keeps the builder unapplied",
			context, "SharedBinding(key: key, builder:",
			"the page would freeze against the model it had at launch",
		},
		{
			"the page's four functions are resolved live",
			runtime, "private var liveArgs: [MarValue]?",
			"the page would keep the functions it captured at mount",
		},
		{
			"a shared change re-renders every mounted page",
			runtime, "MarSharedRegistry.addObserver(ObjectIdentifier(self))",
			"the model would move and the screen would not follow",
		},
		{
			"the page stops listening when it goes away",
			runtime, "MarSharedRegistry.removeObserver(ObjectIdentifier(self))",
			"a torn-down page would keep reconciling subs forever",
		},
		{
			"the view actually reads the change signal",
			runtime, "_ = touch",
			"@Observable tracks reads, so an unread counter redraws nothing",
		},
		{
			"a shared subscription routes to the store",
			runtime, "sharedTaggerOwners[Self.taggerIdentity(tagger)]",
			"a shared sub's message would land in the page's update instead",
		},
		{
			"loading a program restarts the key sequence",
			viewModel, "MarSharedRegistry.beginProgramLoad()",
			"the background refresh after cold start would mint a second key " +
				"for the same def, so the pages would bind to a fresh empty " +
				"store while the screen on the stack still read the old one",
		},
		{
			"a reload keeps the model it had",
			store, "preservedModels[key]",
			"a mar dev save would empty the cart, which a page model survives",
		},
	}

	// The store must not be touched from `init`. SwiftUI evaluates
	// `PageRuntime(page:)` on every pass of the parent's body and keeps only
	// the first instance, so a side effect here runs on objects that are
	// discarded a moment later — the callback they installed dies with them.
	// Checked as an absence, because the symptom (a screen that stops
	// following the model after any unrelated re-render) points nowhere near
	// the line that caused it.
	// The designated initializer, not the convenience one that forwards to
	// it — anchoring on the shorter signature matched the wrong two lines and
	// the sabotage sailed through.
	const designatedInit = "init(page: DecodedPage, user: MarValue?, params: MarValue?) {"
	i := strings.Index(runtime, designatedInit)
	if i < 0 {
		t.Fatalf("could not find %q — this check has gone stale", designatedInit)
	}
	j := strings.Index(runtime[i:], "\n    }")
	if j < 0 {
		t.Fatal("could not find the end of PageRuntime's initializer")
	}
	initBody := runtime[i : i+j]
	for _, forbidden := range []string{
		"MarSharedRegistry.addObserver",
		"MarSharedRegistry.drainInitEffects",
	} {
		if containsStatement(initBody, forbidden) {
			t.Errorf("PageRuntime.init calls %s.\n"+
				"  symptom: SwiftUI builds throwaway PageRuntimes on every "+
				"body pass; whatever this installs belongs to an instance "+
				"that is about to be discarded.\n"+
				"  Put it in mount() and undo it in unmount().", forbidden)
		}
	}

	for _, c := range checks {
		if !containsStatement(c.source, c.needle) {
			t.Errorf("%s: MISSING.\n  symptom: %s\n  looked for: %s",
				c.name, c.symptom, c.needle)
		}
	}
}

// containsStatement reports whether the needle appears as live code rather
// than inside a comment. A plain strings.Contains is not enough: commenting a
// line out leaves the needle in the file, so the check meant to notice the
// line's absence would still pass. Found by sabotage, not by review.
func containsStatement(source, needle string) bool {
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(trimmed, needle) {
			return true
		}
	}
	return false
}

// The deferral list must stay empty. It existed to hold Shared while the store
// was missing; re-adding a name to it is how a future gap would go quiet, so
// the list being empty is itself the assertion.
func TestNothingIsDeferredOnIOS(t *testing.T) {
	b, err := os.ReadFile("builtins_drift_test.go")
	if err != nil {
		t.Fatalf("reading the drift test: %v", err)
	}
	src := string(b)
	if !strings.Contains(src, "iosDeferred := map[string]bool{}") {
		t.Error("the iOS deferral list is no longer empty.\n" +
			"  Deferring a builtin means an app compiles for iOS and behaves\n" +
			"  differently there. If that is deliberate, say so here and in\n" +
			"  the deferral comment — do not let it pass silently.")
	}
}
