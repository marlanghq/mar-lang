// Helpers for discovering env:VAR references inside mar.json.
//
// Three callers today, all using the same definition:
//
//   - mar fly provision: knows which Fly secrets to prompt for
//   - mar fly backup metadata: tells the operator which secrets to
//     re-set when restoring
//   - mar fly restore (future): same
//
// Centralized here so the regex stays consistent. Env var names
// follow the POSIX convention: uppercase letters + digits +
// underscore, must start with letter or underscore. Anything else
// (lowercase, hyphens, etc.) is rejected — matches the shape of
// mar.json[*].env:NAME references that would actually resolve at
// runtime, so backup metadata accurately reflects what restore
// will need.

package project

import (
	"os"
	"regexp"
	"sort"
)

// envRefRegex matches `"env:VAR_NAME"` inside JSON text. The string
// scan is intentional rather than parsing the JSON — future fields
// may gain env:VAR support without us needing to enumerate them
// here, and the regex's POSIX-name constraint avoids spurious
// matches inside string content (which would have to look like
// `"env:THIS_IS_UPPER_CASE"` to be flagged at all).
var envRefRegex = regexp.MustCompile(`"env:([A-Z_][A-Z0-9_]*)"`)

// EnvRefsFromBytes scans raw mar.json content and returns the
// distinct env:VAR names it references, sorted alphabetically.
// Returns nil (not an empty slice) when no refs are found —
// matches the Go convention of "no items" being a nil slice and
// the prior behavior of the regex caller in fly.go.
func EnvRefsFromBytes(raw []byte) []string {
	matches := envRefRegex.FindAllSubmatch(raw, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		seen[string(m[1])] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// EnvRefsFromFile is a small convenience over EnvRefsFromBytes that
// reads the file at `path`. Surfaces the read error verbatim so the
// caller can wrap with command-specific context.
func EnvRefsFromFile(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return EnvRefsFromBytes(raw), nil
}
