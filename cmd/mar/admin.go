// `mar admin` — manage the project's admin panel access list.
//
// Three subcommands, all operating on mar.json from the project root:
//
//   mar admin add EMAIL     - append email to mar.json["admins"]
//   mar admin remove EMAIL  - remove email from mar.json["admins"]
//   mar admin list          - print the current admins
//
// The CLI never opens the production DB — it only edits mar.json.
// Runtime sync of _mar_admins happens at boot (or via the mar dev
// file watcher in development). For production runtime inspection,
// see `mar fly admin list` (Phase 5).

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"mar/internal/admin"
	"mar/internal/project"
	"mar/internal/runtime"
)

const adminUsage = `mar admin — manage the admin panel access list

Usage:
  mar admin add EMAIL       Add EMAIL to mar.json["admins"]
  mar admin remove EMAIL    Remove EMAIL from mar.json["admins"]
  mar admin list            Print the current admins

The admins list is the source of truth. The runtime syncs the
_mar_admins DB table from this list on every boot.

For production runtime inspection (last login, post-sync state),
see "mar fly admin list".`

// runAdmin dispatches `mar admin <sub> ...`. Returns the process
// exit code so main can propagate it.
func runAdmin(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, adminUsage)
		return 2
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			fprintError("mar admin add: missing EMAIL")
			fprintHint("usage: %s", colorGreen("mar admin add YOUR_EMAIL"))
			return 2
		}
		return runAdminAdd(args[1])
	case "remove", "rm":
		if len(args) < 2 {
			fprintError("mar admin remove: missing EMAIL")
			fprintHint("usage: %s", colorGreen("mar admin remove YOUR_EMAIL"))
			return 2
		}
		return runAdminRemove(args[1])
	case "list", "ls":
		return runAdminList()
	case "-h", "--help", "help":
		fmt.Println(adminUsage)
		return 0
	default:
		fprintError("mar admin: unknown subcommand %q", args[0])
		fprintHint("see %s", colorGreen("mar admin --help"))
		return 2
	}
}

// adminEmailShape mirrors the regex in internal/project/validate.go
// so the CLI catches obvious garbage before writing mar.json (and
// also before LoadManifest's validation rejects it on the next read).
var adminEmailShape = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)

func runAdminAdd(email string) int {
	email = strings.TrimSpace(email)
	if !adminEmailShape.MatchString(email) {
		fprintError("mar admin add: %q is not a valid email", email)
		return 1
	}
	path, raw, err := readMarJSON()
	if err != nil {
		fprintError("mar admin add: %v", err)
		return 1
	}

	patched, changed, err := patchAdmins(raw, func(admins []string) []string {
		// Append + dedupe (case-insensitive). Preserves existing order
		// — we don't sort because the user may have intentional ordering.
		for _, e := range admins {
			if strings.EqualFold(e, email) {
				return admins // already present
			}
		}
		return append(admins, email)
	})
	if err != nil {
		fprintError("mar admin add: %v", err)
		return 1
	}

	if !changed {
		fmt.Printf("mar admin add: %s is already in admins\n", colorCyan(email))
		return 0
	}

	if err := os.WriteFile(path, patched, 0o644); err != nil {
		fprintError("mar admin add: write %s: %v", path, err)
		return 1
	}

	fmt.Println()
	fmt.Printf("mar admin add: %s added to admins\n", colorCyan(email))
	fmt.Println()
	fmt.Println("  → mar.json updated")
	fmt.Println("  → next deploy will sync this to _mar_admins on production")
	fmt.Println()
	fmt.Println("In development, the admin panel auth code prints to the terminal (no SMTP needed).")
	fmt.Println("In production, codes are sent via the SMTP configured in mar.json[\"mail\"].")
	fmt.Println()
	fmt.Printf("The dev panel URL is %s (or whatever port mar dev printed).\n",
		colorGreen("http://localhost:3000/_mar/admin"))
	return 0
}

func runAdminRemove(email string) int {
	email = strings.TrimSpace(email)
	path, raw, err := readMarJSON()
	if err != nil {
		fprintError("mar admin remove: %v", err)
		return 1
	}

	patched, changed, err := patchAdmins(raw, func(admins []string) []string {
		out := admins[:0]
		for _, e := range admins {
			if !strings.EqualFold(e, email) {
				out = append(out, e)
			}
		}
		return out
	})
	if err != nil {
		fprintError("mar admin remove: %v", err)
		return 1
	}

	if !changed {
		fmt.Printf("mar admin remove: %s is not in admins (nothing to do)\n", colorCyan(email))
		return 0
	}

	if err := os.WriteFile(path, patched, 0o644); err != nil {
		fprintError("mar admin remove: write %s: %v", path, err)
		return 1
	}

	fmt.Println()
	fmt.Printf("mar admin remove: %s removed from admins\n", colorCyan(email))
	fmt.Println()
	fmt.Println("  → mar.json updated")
	fmt.Println("  → next deploy will sync this to _mar_admins on production")
	fmt.Printf("  → existing admin sessions for %s will be revoked at next boot\n", email)
	return 0
}

func runAdminList() int {
	_, raw, err := readMarJSON()
	if err != nil {
		fprintError("mar admin list: %v", err)
		return 1
	}
	admins, err := readAdminsFromRaw(raw)
	if err != nil {
		fprintError("mar admin list: %v", err)
		return 1
	}
	if len(admins) == 0 {
		fmt.Println("admins (from mar.json):")
		fmt.Println("  (none)")
		fmt.Println()
		fmt.Println("Run `mar admin add YOUR_EMAIL` to enable the admin panel.")
		return 0
	}
	fmt.Println("admins (from mar.json):")
	for _, e := range admins {
		fmt.Printf("  %s\n", e)
	}
	return 0
}

// readMarJSON locates mar.json in the current directory (the
// convention — the user runs `mar admin add` from the project
// root) and returns its absolute path + raw bytes.
func readMarJSON() (path string, raw []byte, err error) {
	path = "mar.json"
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return abs, nil, fmt.Errorf("mar.json not found in current directory")
		}
		return abs, nil, err
	}
	return abs, data, nil
}

// patchAdmins decodes raw mar.json, runs `transform` on the
// admins slice, re-encodes with stable indentation. Returns the
// patched bytes + whether anything actually changed.
//
// Trade-off: re-encoding preserves all other top-level fields but
// not necessarily their original ordering / whitespace. For the v1
// CLI this is acceptable — `mar admin add` is rare; users can
// re-format with their editor if they care about layout.
func patchAdmins(raw []byte, transform func([]string) []string) ([]byte, bool, error) {
	// Decode to ordered keys so we can update only `admins` and
	// preserve insertion order in the output. Go's encoding/json
	// doesn't expose key order on map[string]interface{}; do it by
	// hand with a simple ordered-map representation.

	keys, values, err := decodeOrdered(raw)
	if err != nil {
		return nil, false, err
	}

	var existing []string
	if v, ok := values["admins"]; ok {
		// Decode the raw admins array.
		if err := json.Unmarshal(v, &existing); err != nil {
			return nil, false, fmt.Errorf("mar.json admins: %v", err)
		}
	}

	updated := transform(append([]string(nil), existing...))
	if equalStringSlice(existing, updated) {
		return raw, false, nil
	}

	// Re-encode admins as a JSON array with one entry per line —
	// human-friendly diffs.
	var sb strings.Builder
	if len(updated) == 0 {
		sb.WriteString("[]")
	} else {
		sb.WriteString("[\n")
		for i, e := range updated {
			sb.WriteString("    ")
			eb, _ := json.Marshal(e)
			sb.Write(eb)
			if i < len(updated)-1 {
				sb.WriteString(",")
			}
			sb.WriteString("\n")
		}
		sb.WriteString("  ]")
	}
	values["admins"] = json.RawMessage(sb.String())

	// If admins didn't exist before, add it to keys (after "name" if
	// present, else at the end, for predictable layout).
	if !sliceContains(keys, "admins") {
		insertAt := len(keys)
		for i, k := range keys {
			if k == "name" {
				insertAt = i + 1
				break
			}
		}
		keys = append(keys[:insertAt], append([]string{"admins"}, keys[insertAt:]...)...)
	}

	out, err := encodeOrdered(keys, values)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

// readAdminsFromRaw extracts the admins list from raw mar.json bytes
// without going through full Manifest validation — `mar admin list`
// should work even if some unrelated field is malformed.
func readAdminsFromRaw(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	_, values, err := decodeOrdered(raw)
	if err != nil {
		return nil, err
	}
	v, ok := values["admins"]
	if !ok {
		return nil, nil
	}
	var admins []string
	if err := json.Unmarshal(v, &admins); err != nil {
		return nil, fmt.Errorf("mar.json admins: %v", err)
	}
	return admins, nil
}

// decodeOrdered parses a top-level JSON object preserving key
// insertion order. Returns the keys in order + a map from key to
// raw value. Used so patchAdmins can update one field without
// reordering the rest.
func decodeOrdered(raw []byte) (keys []string, values map[string]json.RawMessage, err error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	t, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if delim, ok := t.(json.Delim); !ok || delim != '{' {
		return nil, nil, fmt.Errorf("mar.json: expected object at top level")
	}
	values = make(map[string]json.RawMessage)
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := t.(string)
		if !ok {
			return nil, nil, fmt.Errorf("mar.json: expected string key")
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, nil, err
		}
		keys = append(keys, key)
		values[key] = val
	}
	return keys, values, nil
}

// encodeOrdered marshals an ordered map back to indented JSON. Two
// space indent matches the convention in scaffolded mar.json files
// across the repo.
func encodeOrdered(keys []string, values map[string]json.RawMessage) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("{\n")
	for i, k := range keys {
		sb.WriteString("  ")
		kb, _ := json.Marshal(k)
		sb.Write(kb)
		sb.WriteString(": ")
		// Re-indent the raw value so it aligns with our two-space
		// outer indent. JSON parser preserves whitespace inside
		// values; we round-trip through Marshal/Unmarshal for
		// objects/arrays to get consistent indentation.
		formatted, err := reindent(values[k], "  ")
		if err != nil {
			return nil, err
		}
		sb.Write(formatted)
		if i < len(keys)-1 {
			sb.WriteString(",")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("}\n")
	return []byte(sb.String()), nil
}

// reindent normalizes a raw JSON value's indentation. Scalars and
// our hand-formatted admins array pass through; nested objects /
// arrays get re-marshalled with consistent two-space outer prefix.
func reindent(raw json.RawMessage, prefix string) ([]byte, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return []byte("null"), nil
	}
	first := trimmed[0]
	// Scalars (string/number/bool/null) — emit verbatim.
	if first != '{' && first != '[' {
		return []byte(trimmed), nil
	}
	// For arrays/objects we already provided custom-formatted, just
	// keep them. Detect by looking for newlines (our admins encoder
	// adds them); otherwise pretty-print with json.MarshalIndent.
	if strings.Contains(trimmed, "\n") {
		return []byte(trimmed), nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.MarshalIndent(v, prefix, "  ")
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sliceContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// LoadAdminsFromManifest is a small convenience for callers (the
// boot-time sync, mostly) that don't need to mutate mar.json — they
// just want the validated admins slice with consistent canonicalization.
func LoadAdminsFromManifest(m *project.Manifest) []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.Admins))
	seen := make(map[string]bool, len(m.Admins))
	for _, e := range m.Admins {
		canon := strings.ToLower(strings.TrimSpace(e))
		if canon == "" || seen[canon] {
			continue
		}
		seen[canon] = true
		out = append(out, canon)
	}
	sort.Strings(out)
	return out
}

// bootAdminPanel runs at `mar dev` boot (and on every hot-reload):
// ensures the framework admin tables exist, then syncs _mar_admins
// against mar.json["admins"]. No-op when the project has no DB
// configured (Repo isn't in use → admin panel can't have a session
// store yet anyway). Logs a discovery hint when the admins list is
// empty so devs know the panel exists.
func bootAdminPanel(manifest *project.Manifest) error {
	if runtime.CurrentDBPath() == "" {
		return nil
	}
	db, err := runtime.OpenDB()
	if err != nil {
		return fmt.Errorf("admin panel: %w", err)
	}
	desired := LoadAdminsFromManifest(manifest)
	added, removed, err := admin.Boot(db, desired, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("admin panel: %w", err)
	}
	// Hot-reload makes this run on every save; only log when something
	// actually changed.
	if added != 0 || removed != 0 {
		fmt.Printf("[mar] admin panel: synced %d admins (+%d -%d)\n", len(desired), added, removed)
	}
	if len(desired) == 0 && !adminHintShown {
		fmt.Fprintln(os.Stderr,
			colorYellow("hint:")+" no admins configured — the admin panel at /_mar/admin is locked.")
		fmt.Fprintln(os.Stderr,
			"      run "+colorGreen("mar admin add YOUR_EMAIL")+" to enable it.")
		adminHintShown = true
	}
	return nil
}

// adminHintShown — print the discovery hint only once per `mar dev`
// session, even though boot runs on every hot-reload.
var adminHintShown bool
