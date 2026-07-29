package jsserve

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"mar/internal/typecheck"
)

// TestJSBuiltinsCoverClientStdlib is the web-side counterpart of the
// iOS drift test. Same idea: every qualified name in BaseEnv() that
// is reachable from frontend code must be `def(...)`-ed in
// runtime.js. Catches the class of bug where the typecheck and Go
// runtime know about a builtin but the browser bundle hasn't been
// updated — surfaces as "Error: unbound name: X" in the user's
// browser at runtime.
//
// When this test fails: implement each missing builtin in
// `internal/jsserve/runtime.js`, mirroring the Go runtime's
// semantics. If a name is genuinely server-only, add it to
// typecheck.IsBackendOnlyBuiltin (the iOS test will pick the same
// answer up automatically).
func TestJSBuiltinsCoverClientStdlib(t *testing.T) {
	required := typecheck.BaseQualifiedSymbols()

	defined, err := readJSBuiltinNames()
	if err != nil {
		t.Fatalf("reading JS builtins: %v", err)
	}

	var missing []string
	for name := range required {
		if typecheck.IsBackendOnlyBuiltin(name) {
			continue
		}
		if !defined[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("runtime.js is missing %d builtin(s) reachable from frontend code:\n  %s\n\nFix: add a `def('Foo.bar', ...)` for each in internal/jsserve/runtime.js, mirroring the Go runtime's semantics.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// readJSBuiltinNames extracts every `def('...')` / `def("...")`
// first-arg string literal from runtime.js. Same shape as the iOS
// test; the only difference is the file path and quote style.
func readJSBuiltinNames() (map[string]bool, error) {
	path := filepath.Join("runtime.js")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// `def('Foo.bar', ...` or `def("Foo.bar", ...`
	re := regexp.MustCompile(`def\(\s*['"]([^'"]+)['"]`)
	matches := re.FindAllSubmatch(data, -1)
	out := make(map[string]bool, len(matches))
	for _, m := range matches {
		out[string(m[1])] = true
	}
	return out, nil
}

// TestJSDeclarationStubsAreComplete closes the hole that let
// `Entity.enum` ship missing for as long as it did.
//
// IsBackendOnlyBuiltin skips the whole `Entity.` prefix above, and for
// CALLING that is right: no schema helper ever runs in a browser. But
// "never runs on the client" is not "never evaluated on the client".
// Top-level values evaluate eagerly, so a module that DECLARES an
// entity has to evaluate `Entity.define`, `Entity.text` and friends
// wherever it is loaded — including in the page bundle when the entity
// shares a module with frontend code. That is why runtime.js already
// carried stubs for eight of the nine column types: each was added by
// hand the day someone tripped over it, with nothing pinning the set.
// The ninth (enum) typechecked and then died in the browser with
// "unbound name: Entity.enum".
//
// That whole situation is gone, and this test now pins its absence. The
// bundler ships only the declarations a page reaches (ADR 0019), so a schema
// never arrives in the browser and never needs to evaluate there. The stubs
// were what kept a bundling mistake quiet; without them, one surfaces as an
// unbound name — and PickFrontMods refuses the build before that anyway.
//
// So the invariant flipped: Entity.* and Repo.* must NOT be defined in the
// client runtime. Re-adding one would be re-adding the silence.
func TestJSDefinesNoServerOnlyBuiltins(t *testing.T) {
	defined, err := readJSBuiltinNames()
	if err != nil {
		t.Fatalf("reading JS builtins: %v", err)
	}

	// The DATA layer only. `App.*`, `Auth.*` and `Service.implement` are also
	// server-only by IsBackendOnlyBuiltin, and the client still binds them:
	// the synthetic `__entry` module calls appFrontend, and a shared module
	// may declare a service beside its implementation. Whether those can be
	// pruned away too is a separate question nobody has measured, so this
	// test does not pretend to have an answer for them.
	serverOnlyData := []string{"Entity.", "Repo.", "Db.", "Server."}
	var present []string
	for name := range typecheck.BaseQualifiedSymbols() {
		inData := false
		for _, p := range serverOnlyData {
			if strings.HasPrefix(name, p) {
				inData = true
				break
			}
		}
		if inData && defined[name] {
			present = append(present, name)
		}
	}
	if len(present) > 0 {
		sort.Strings(present)
		t.Fatalf("runtime.js defines %d server-only builtin(s) the browser can no longer reach:\n  %s\n\n"+
			"These were stubs from before the bundler pruned to page-reachable declarations.\n"+
			"Keeping them turns a bundling regression into a silent no-op instead of an error.",
			len(present), strings.Join(present, "\n  "))
	}
}
