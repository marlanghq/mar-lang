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
