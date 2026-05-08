package iosbundle

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"mar/internal/typecheck"
)

// TestIOSBuiltinsCoverClientStdlib catches the class of bug where a
// builtin gets added to typecheck (so user code compiles) and to the
// Go runtime (so server-side eval works), but never lands in the iOS
// Swift runtime — leading to "view failed: unbound name: X" at runtime
// on device.
//
// Source of truth: every qualified name registered in
// typecheck.BaseEnv() that is reachable from frontend code (filtered
// via typecheck.IsBackendOnlyBuiltin). The iOS Swift bundle MUST
// define each via `env.define("Foo.bar", ...)`.
//
// When this test fails: implement each missing builtin in
// `internal/iosbundle/template/Sources/MarBuiltins.swift`, mirroring
// the Go runtime's semantics. If a name is genuinely server-only,
// add it to typecheck.IsBackendOnlyBuiltin (and the parallel JS test
// will pick the same answer up).
func TestIOSBuiltinsCoverClientStdlib(t *testing.T) {
	required := typecheck.BaseQualifiedSymbols()

	defined, err := readSwiftBuiltinNames()
	if err != nil {
		t.Fatalf("reading Swift builtins: %v", err)
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
		t.Fatalf("MarBuiltins.swift is missing %d builtin(s) reachable from frontend code:\n  %s\n\nFix: implement each in internal/iosbundle/template/Sources/MarBuiltins.swift, mirroring the Go runtime's semantics.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// readSwiftBuiltinNames extracts every `env.define("...")` first-arg
// string literal from MarBuiltins.swift. Only the FIRST argument
// matters — that's the registry key; the second is the value bound
// to it.
func readSwiftBuiltinNames() (map[string]bool, error) {
	path := filepath.Join("template", "Sources", "MarBuiltins.swift")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`env\.define\(\s*"([^"]+)"`)
	matches := re.FindAllSubmatch(data, -1)
	out := make(map[string]bool, len(matches))
	for _, m := range matches {
		out[string(m[1])] = true
	}
	return out, nil
}
