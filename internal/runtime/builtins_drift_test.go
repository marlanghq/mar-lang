// Black-box test in `runtime_test`, not `runtime`, so it can import
// typecheck without dragging that dep into the runtime package itself.

package runtime_test

import (
	"sort"
	"strings"
	"testing"

	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// TestRuntimeQualifiedAliasesMatchTypecheck catches the drift class
// where a builtin gets added to typecheck.qualifiedAliases (so
// `List.foo` typechecks) but never gets the matching entry in
// runtime.qualifiedAliasMapping, so the program load fails at
// runtime with "unbound qualified name: List.foo".
//
// The parallel iOS / JS drift tests catch the corresponding miss on
// the client side; this one catches the server.
//
// IsBackendOnlyBuiltin is intentionally NOT applied: backend-only
// names still have to be runtime-resolvable on the Go side (which
// IS the backend). The filter only matters for client-side runtimes.
//
// What "defined" means widened with the question. The subject used to be the
// alias TABLE, so the question could only be asked of the 373 names that table
// is built from; asking it of all 544 named 154 constructors as missing, which
// they are not -- Keyboard.KeyA and Service.Offline are VCtor values from the
// generated registry, resolvable and simply not aliases. The failure this test
// exists to catch is "unbound qualified name" at load, so the honest question
// is whether the name RESOLVES, by whichever route, and now it covers every
// name a program can write rather than the two thirds that happen to be
// aliases.
func TestRuntimeQualifiedAliasesMatchTypecheck(t *testing.T) {
	required := typecheck.AllQualifiedSymbols()
	env := runtime.BaseEnv()
	aliases := runtime.QualifiedAliasNames()
	defined := map[string]bool{}
	for name := range aliases {
		defined[name] = true
	}
	for name := range required {
		if _, ok := env.Lookup(name); ok {
			defined[name] = true
		}
	}

	var missing []string
	for name := range required {
		if !defined[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	t.Fatalf("the runtime cannot resolve %d qualified name(s) that typecheck advertises:\n  %s\n\n"+
		"A program using one loads and then dies with `unbound qualified name`.\n"+
		"Fix: add the alias to internal/stdnames/aliases.go and its function to\n"+
		"builtins() in internal/runtime/builtins.go,\n"+
		"or, for a constructor, to the table internal/ctorgen generates from.",
		len(missing), strings.Join(missing, "\n  "))
}
