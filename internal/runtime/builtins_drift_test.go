// Black-box test in `runtime_test`, not `runtime` — so it can import
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
// runtime.qualifiedAliasMapping — so the program load fails at
// runtime with "unbound qualified name: List.foo".
//
// The parallel iOS / JS drift tests catch the corresponding miss on
// the client side; this one catches the server.
//
// IsBackendOnlyBuiltin is intentionally NOT applied: backend-only
// names still have to be runtime-resolvable on the Go side (which
// IS the backend). The filter only matters for client-side runtimes.
func TestRuntimeQualifiedAliasesMatchTypecheck(t *testing.T) {
	required := typecheck.BaseQualifiedSymbols()
	defined := runtime.QualifiedAliasNames()

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
	t.Fatalf("runtime is missing %d qualified alias(es) that typecheck advertises:\n  %s\n\nFix: add each entry to qualifiedAliasMapping() in internal/runtime/builtins.go.",
		len(missing), strings.Join(missing, "\n  "))
}
