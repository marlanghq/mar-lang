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

// What the user is told when a service call fails.
//
// The runtime turns an HTTP status and a body into a `Service.Error`: 401 is
// `Unauthorized`, 429 is `RateLimited`, and anything else is `ServerError`
// carrying the server's own message — the operator's snake_case code, intact,
// so a program can match on it.
//
// `decodeServerError` — the part that digs that message out of the body — was
// entered by no test. The existing service test covers the happy path and the
// shape guard; the failure path had the status codes and the body shapes
// between them untested, and the body shapes are the ones a server actually
// varies: a JSON envelope, a bare string, an empty body.
func TestAFailedServiceCallSaysWhatTheServerSaid(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}

	const src = `module Main exposing (..)


type Msg
    = Got (Result Service.Error Int)


ping : Service () Int
ping =
    Service.declare GET "/api/ping"


call : Cmd Msg
call =
    Service.call ping () Got
`
	mod, err := parser.Parse(src)
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

	// `absent` is what makes the envelope cases mean anything: `want` alone
	// passes even with the unwrapping removed, because the raw body CONTAINS
	// the message it wraps. Measured — the first version of this test went
	// green with `decodeServerError` gutted.
	cases := []struct{ name, status, body, want, absent string }{
		{
			"a JSON envelope, which is what the framework's own handlers send",
			"500", `{"error":"note_not_found"}`, "note_not_found", `error\`,
		},
		{
			"a bare body from something that is not the framework",
			"500", `upstream exploded`, "upstream exploded", "",
		},
		{
			// The whole body comes through. Matched on its content rather
			// than its exact spelling: the display form escapes the quotes,
			// and this test is about what survives, not about quoting.
			"JSON without an error field falls back to the raw body",
			"500", `{"detail":"nope"}`, "detail", "",
		},
		{
			"an empty body still names the status, rather than nothing",
			"503", ``, "HTTP 503", "",
		},
		{
			"401 is its own case, whatever the body says",
			"401", `{"error":"expired"}`, "Unauthorized", "expired",
		},
		{
			"429 is its own case too",
			"429", `{"error":"slow down"}`, "RateLimited", "slow down",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			write("driver.js", []byte(`
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
globalThis.fetch = () => Promise.resolve({
  ok: false, status: `+tc.status+`, headers: { get: () => null },
  text: () => Promise.resolve(`+"`"+tc.body+"`"+`),
});
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
globalThis.__marTestRunCmd(program, 'Main.call').then(v => process.stdout.write(String(v)));
`))
			cmd := exec.Command(node, filepath.Join(dir, "driver.js"),
				filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"))
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("node: %v\n%s", err, stderr.String())
			}
			if !strings.Contains(string(out), tc.want) {
				t.Errorf("status %s with body %q\n  got:  %s\n  want: it to contain %q",
					tc.status, tc.body, out, tc.want)
			}
			if tc.absent != "" && strings.Contains(string(out), tc.absent) {
				t.Errorf("status %s with body %q\n  got:  %s\n  the message still carries %q, so the body was passed through rather than read",
					tc.status, tc.body, out, tc.absent)
			}
		})
	}
}
