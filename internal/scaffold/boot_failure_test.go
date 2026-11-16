package scaffold

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"mar/internal/parity"
)

// The static build's shell gets the same boot failure screen as the server's
// (jsserve.BootFailureHead, one source). A deployed app never shows the
// technical detail; the console has it.
func TestStaticShellBootFailureScreen(t *testing.T) {
	html := buildIndexHTML("pt-BR", "Diario", []byte(`{"modules":[],"entry":"__entry"}`))
	marRun := func(body string) string {
		return `() => { globalThis.extra = { runs: 0 }; globalThis.marRun = function (p) { globalThis.extra.runs++; ` + body + ` }; }`
	}
	cases := []struct {
		name, runtime string
		failed        bool
	}{
		{"runtime.js runs", `(src, n) => (` + marRun(`globalThis.extra.entry = p.entry;`) + `)`, false},
		{"runtime.js fails once, the fresh retry works", `(src, n) => n === 1 ? 'error' : (` + marRun(`globalThis.extra.entry = p.entry;`) + `)`, false},
		{"runtime.js does not download, twice", `(src, n) => 'error'`, true},
		{"the host answers runtime.js with HTML, twice", `(src, n) => 'garbage'`, true},
		{"marRun refuses the program", `(src, n) => (` + marRun(`throw new Error('program.json: unknown node kind');`) + `)`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := parity.RunShell(html, `const scenario = { fetch: () => Promise.reject(new Error('the static shell fetches nothing')), runtime: `+c.runtime+` };`)
			if errors.Is(err, parity.ErrNoNode) {
				t.Skip("node is not installed")
			}
			if err != nil {
				t.Fatalf("node: %v", err)
			}
			var r struct {
				Failed bool     `json:"failed"`
				Screen string   `json:"screen"`
				Detail *string  `json:"detail"`
				Errors []string `json:"errors"`
				Extra  map[string]any
			}
			if err := json.Unmarshal([]byte(out), &r); err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			if r.Failed != c.failed {
				t.Fatalf("failure screen = %v, want %v (screen %q, console %q)", r.Failed, c.failed, r.Screen, r.Errors)
			}
			if !c.failed {
				if r.Extra["runs"] != float64(1) || r.Extra["entry"] != "__entry" {
					t.Fatalf("marRun ran %v time(s) with entry %v, want once with the inline program", r.Extra["runs"], r.Extra["entry"])
				}
				return
			}
			if r.Screen != "This app couldn't load. | Check your connection and try again. | Reload" {
				t.Errorf("screen = %q", r.Screen)
			}
			if r.Detail != nil || strings.Contains(r.Screen, "runtime.js") {
				t.Errorf("a deployed app showed technical detail: %q", r.Screen)
			}
			if len(r.Errors) != 1 {
				t.Errorf("console.error calls = %q, want the cause once", r.Errors)
			}
		})
	}
}
