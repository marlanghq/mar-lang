package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/typecheck"
)

// After signing in, the app returns you to where you were: `?next=` carries
// that path, and `?next=` comes from the URL bar. `isSafeReturnPath` is what
// keeps it from carrying you somewhere else — an open redirect off the back of
// a real sign-in is a convincing phishing step, because the domain in the bar
// was the right one right up to the redirect.
//
// It had no test. Found by giving runtime.js the coverage it never had: the
// function was entered by nothing. It is not reachable from a driver directly
// (it lives inside the runtime's closure), so it is exercised the way a user
// reaches it — through `Auth.completeSignIn`, with the query string and the
// navigator stubbed.
func TestSignInOnlyReturnsYouToThisSite(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	mod, err := parser.Parse(`module Main exposing (..)


done : Cmd ()
done =
    Auth.completeSignIn
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check: %v", err)
	}

	dir := t.TempDir()
	rt, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	programJSON, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("runtime.js", rt)
	write("program.json", programJSON)
	// One node run per case: the runtime keeps module-level state, and a
	// fresh process is the honest way to ask "where would this send me".
	write("driver.js", []byte(`
const fs = require('fs');
const next = process.argv[4];
globalThis.window = globalThis;
globalThis.location = { search: next === '' ? '' : '?next=' + encodeURIComponent(next) };
globalThis.document = undefined;
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
let went = '(never navigated)';
globalThis.__marNav = { replaceFresh: (t) => { went = t; }, replace: (t) => { went = t; } };
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const cmd = globalThis.__marEvalRaw(program, 'Main.done');
try { cmd.run(); } catch (e) { went = 'THREW: ' + String((e && e.message) || e); }
process.stdout.write(went);
`))

	cases := []struct{ name, next, want string }{
		{"a path on this site", "/notes/7", "/notes/7"},
		{"no next at all", "", "/"},

		// Everything below must land on "/" instead of where it asked.
		{"an absolute URL", "https://evil.example.com/x", "/"},
		{"a javascript: URL", "javascript:alert(1)", "/"},
		{"a protocol-relative URL", "//evil.example.com", "/"},
		{"a backslash the browser may read as a slash", "/\\evil.example.com", "/"},
		{"path traversal", "/../etc", "/"},
		{"a scheme smuggled into the first segment", "/javascript:alert(1)", "/"},
		{"a bare host with no leading slash", "evil.example.com/x", "/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(node,
				filepath.Join(dir, "driver.js"),
				filepath.Join(dir, "runtime.js"),
				filepath.Join(dir, "program.json"),
				tc.next)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("node: %v\n%s", err, stderr.String())
			}
			if got := string(out); got != tc.want {
				t.Errorf("?next=%q sent the user to %q, want %q", tc.next, got, tc.want)
			}
		})
	}
}
