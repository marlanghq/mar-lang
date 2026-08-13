package iosbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every module has to evaluate in its own scope.
//
// The Swift loader used to bind every module's BARE names into one shared Env.
// Every page module in every example declares `init`, `update`, `view` and
// `page`, so each module overwrote the one before it, and a closure, which
// resolves its free names when it RUNS, not when it is built: ended up
// reading whichever module happened to load last.
//
// What that looked like on a device: `examples/shared-cart` opened on the
// Settings screen. The route was right, the title was right (both come from
// literals in the page's own record), and the body was another module's,
// because `view = view global` went through the shared bindings. Nothing
// crashed and nothing was logged. The web runtime fixed this by giving each
// module its own frame (see loadModule in internal/jsserve/runtime.js, whose
// comment records that the same bug was intermittent there); iOS never got
// the same treatment, so the two platforms disagreed about what a program
// means: the worst kind of drift there is.
//
// These are structural checks. They cannot run Swift, so they cannot prove
// the scoping works; they can prove the per-module frame has not been
// flattened back into the shared env, which is the only way this regresses.
func TestEachModuleLoadsIntoItsOwnScope(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("template", "Sources", "MarLoader.swift"))
	if err != nil {
		t.Fatalf("reading the loader: %v", err)
	}
	src := string(b)

	checks := []struct {
		name    string
		needle  string
		symptom string
	}{
		{
			"the module gets a frame of its own",
			"Env(parent: env)",
			"two modules that both declare `view` would share one binding",
		},
		{
			"declarations evaluate in that frame",
			"Eval.eval(bodyExpr, modEnv)",
			"a closure would capture the shared env and resolve its free " +
				"names against whatever module loaded last",
		},
		{
			"bare names stay module-local",
			"modEnv.define(name, val)",
			"the bare binding would leak to every other module",
		},
		{
			"the qualified alias still reaches everyone",
			"env.define(modName + \".\" + name, val)",
			"`M.x` from another module would stop resolving",
		},
	}

	for _, c := range checks {
		if !containsStatement(src, c.needle) {
			t.Errorf("%s: MISSING.\n  symptom: %s\n  looked for: %s",
				c.name, c.symptom, c.needle)
		}
	}

	// The bare pre-bind of pass 2 must not go to the shared env either: a
	// placeholder there is a `.unit` that shadows another module's real value
	// until pass 3 overwrites it, which is the same collision one step earlier.
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if strings.Contains(trimmed, "env.define(name, .unit)") &&
			!strings.Contains(trimmed, "modEnv.define(name, .unit)") {
			t.Error("pass 2 pre-binds a bare name in the SHARED env.\n" +
				"  symptom: the placeholder shadows another module's value " +
				"until pass 3 runs — the same collision, one step earlier.\n" +
				"  Pre-bind into modEnv; only the module's own body needs it.")
		}
	}
}
