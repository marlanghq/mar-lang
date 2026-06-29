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
	"slices"
	"sort"
	"strings"
	"time"

	"mar/internal/admin"
	"mar/internal/project"
	"mar/internal/runtime"
)

// adminUsage returns the help text for `mar admin`. Same palette
// as the root `mar` usage in main.go: only the literal `mar` binary
// name is green; subcommand names and arg placeholders are bold;
// paths magenta; headers bold.
func adminUsage() string {
	bin := colorGreen("mar")
	name := func(s string) string { return colorBold(s) }
	path := func(s string) string { return colorMagenta(s) }
	hdr := func(s string) string { return colorBold(s) }
	run := func(rest string) string { return bin + " " + name(rest) }
	return bin + " " + name("admin") + ": manage the admin panel access list\n" +
		"\n" +
		hdr("Usage:") + "\n" +
		"  " + run("admin add") + " " + name("EMAIL") + "       Add " + name("EMAIL") + " to " + path(`mar.json["admins"]`) + "\n" +
		"  " + run("admin remove") + " " + name("EMAIL") + "    Remove " + name("EMAIL") + " from " + path(`mar.json["admins"]`) + "\n" +
		"  " + run("admin list") + "            Print the current admins\n" +
		"\n" +
		"The admins list in " + path("mar.json") + " is the source of truth. The runtime\n" +
		"re-syncs production from this list on every boot.\n" +
		"\n" +
		"For production runtime inspection (last login, post-sync state),\n" +
		"see " + run("fly admin list") + "."
}

// runAdmin dispatches `mar admin <sub> ...`. Returns the process
// exit code so main can propagate it.
func runAdmin(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, adminUsage())
		return 2
	}
	switch args[0] {
	case "add":
		if len(args) < 2 {
			fprintError("mar admin add: missing EMAIL")
			fprintHint("usage: %s", cmdSuggest("admin add <email>"))
			return 2
		}
		return runAdminAdd(args[1])
	case "remove", "rm":
		if len(args) < 2 {
			fprintError("mar admin remove: missing EMAIL")
			fprintHint("usage: %s", cmdSuggest("admin remove <email>"))
			return 2
		}
		return runAdminRemove(args[1])
	case "list", "ls":
		return runAdminList()
	case "-h", "--help", "help":
		fmt.Println(adminUsage())
		return 0
	default:
		fprintError("mar admin: unknown subcommand %q", args[0])
		fprintHint("see %s", cmdSuggest("admin --help"))
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
	// Validate the existing manifest before mutating it. If mar.json
	// is already broken, no point in editing it further — the user
	// needs to fix the underlying error first.
	manifest, err := project.LoadManifestStructure(".")
	if err != nil {
		fprintError("mar admin add: %v", err)
		return 1
	}
	path, raw, readErr := readMarJSON()
	if readErr != nil {
		fprintError("mar admin add: %v", readErr)
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
		// Padded like the happy path: leading blank separates from prior
		// output / the shell prompt, trailing blank because this returns
		// control to the shell (CLI style guide §1.1 + §1.2).
		fmt.Println()
		fmt.Printf("mar admin add: %s is already in admins\n", colorCyan(email))
		fmt.Println()
		return 0
	}

	if err := os.WriteFile(path, patched, 0o644); err != nil {
		fprintError("mar admin add: write %s: %v", colorMagenta(path), err)
		return 1
	}

	fmt.Println()
	fmt.Printf("mar admin add: %s added to admins\n", colorCyan(email))
	fmt.Println()
	fmt.Printf("  → %s updated\n", colorMagenta("mar.json"))
	fmt.Printf("  → next deploy will apply this to production's admin list\n")
	fmt.Println()
	fmt.Printf("In development, the sign-in code prints to the terminal (no SMTP needed).\n")
	fmt.Printf("In production, codes are sent via the SMTP configured in %s.\n",
		colorMagenta(`mar.json["mail"]`))
	fmt.Println()
	fmt.Printf("During development, the admin URL is %s.\n",
		colorCyan(devAdminURL(manifest)))
	fmt.Println()
	return 0
}

// devAdminURL builds the admin URL for `mar dev` from the manifest's
// configured server.port (default 3000). Exists so the post-add
// confirmation always points at the right port — projects that
// changed it via mar.json["server"]["port"] would otherwise see an
// incorrect 3000 and have to figure out the override themselves.
func devAdminURL(m *project.Manifest) string {
	port := 3000
	if m != nil && m.Server != nil && m.Server.Port != 0 {
		port = m.Server.Port
	}
	return fmt.Sprintf("http://localhost:%d/_mar/admin", port)
}

func runAdminRemove(email string) int {
	email = strings.TrimSpace(email)
	if _, err := project.LoadManifestStructure("."); err != nil {
		fprintError("mar admin remove: %v", err)
		return 1
	}
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
		// Padded like every other admin confirmation: leading blank to
		// separate from prior output, trailing blank since it returns to
		// the shell (CLI style guide §1.1 + §1.2).
		fmt.Println()
		fmt.Printf("mar admin remove: %s is not in admins (nothing to do)\n", colorCyan(email))
		fmt.Println()
		return 0
	}

	if err := os.WriteFile(path, patched, 0o644); err != nil {
		fprintError("mar admin remove: write %s: %v", colorMagenta(path), err)
		return 1
	}

	fmt.Println()
	fmt.Printf("mar admin remove: %s removed from admins\n", colorCyan(email))
	fmt.Println()
	fmt.Printf("  → %s updated\n", colorMagenta("mar.json"))
	fmt.Printf("  → next deploy will apply this to production's admin list\n")
	fmt.Printf("  → existing admin sessions for %s will be revoked at next boot\n", colorCyan(email))
	fmt.Println()
	return 0
}

func runAdminList() int {
	// Validate the manifest before printing — surfaces typos and
	// range violations at the CLI rather than letting them slide
	// until the next mar dev / mar build.
	if _, err := project.LoadManifestStructure("."); err != nil {
		fprintError("mar admin list: %v", err)
		return 1
	}
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
		fmt.Println()
		fmt.Printf("admins (from %s):\n", colorMagenta("mar.json"))
		fmt.Printf("  %s\n", colorYellow("(none)"))
		fmt.Println()
		fmt.Printf("Run %s to enable the admin panel.\n",
			cmdSuggest("admin add <email>"))
		fmt.Println()
		return 0
	}
	fmt.Println()
	fmt.Printf("admins (from %s):\n", colorMagenta("mar.json"))
	for _, e := range admins {
		fmt.Printf("  %s\n", colorCyan(e))
	}
	fmt.Println()
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
	if !slices.Contains(keys, "admins") {
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
		// Multi-line hint — continuation embedded in the format
		// string so fprintHint emits the whole block as one unit
		// (otherwise its trailing blank would split the Hint from
		// the "run ..." line).
		fprintHint(
			"no admins configured. The admin panel at %s is locked.\n"+
				"      run %s to enable it.",
			colorCyan("/_mar/admin"),
			cmdSuggest("admin add <email>"))
		adminHintShown = true
	}
	return nil
}

// adminHintShown — print the discovery hint only once per `mar dev`
// session, even though boot runs on every hot-reload.
var adminHintShown bool
