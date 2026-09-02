package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parityfixture"
	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// `JSON.decode` checks a document against the type the call site asked for,
// and the shape it checks against is stamped on the node by the compiler. The
// checking function is shared with the request guard, which is well covered —
// but the WIRING is not the same on both sides, and the wiring is what broke
// the last time this went wrong: the guard existed, ran in production, and the
// dev loader handed it `nil` where the checked types should have been.
//
// No example decodes JSON, so nothing exercised the browser's half at all: the
// wire census counts this arm as zero on purpose, so the gap shows as a gap
// rather than passing for coverage. This runs the real path on both sides and
// compares the answers, which is the only thing that makes "the same decoder"
// mean anything.
func TestTheBrowserAndTheServerDecodeTheSameDocumentTheSameWay(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	const src = parityfixture.Source

	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check: %v", err)
	}

	names := parityfixture.Names

	// The server's answers, from the same tree the browser will get.
	loaded, err := runtime.LoadModule(mod)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	goSays := map[string]string{}
	for _, n := range names {
		v, ok := loaded.Env.Lookup(n)
		if !ok {
			t.Fatalf("%s is not bound", n)
		}
		s, ok := v.(runtime.VString)
		if !ok {
			t.Fatalf("%s is a %T, not a String", n, v)
		}
		goSays[n] = s.V
	}

	// The `good` case has to actually decode, or every other case would pass
	// against a decoder that refuses everything.
	if goSays["good"] != "ok 1 a" {
		t.Fatalf("the document that matches the type did not decode: %q", goSays["good"])
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
	write("driver.js", []byte(`
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const out = {};
for (const name of process.argv.slice(4)) {
  try { out[name] = globalThis.__marEvalValue(program, 'Main.' + name); }
  catch (e) { out[name] = 'THREW: ' + String((e && e.message) || e); }
}
process.stdout.write(JSON.stringify(out));
`))

	args := append([]string{filepath.Join(dir, "driver.js"),
		filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json")}, names...)
	cmd := exec.Command(node, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var jsSays map[string]string
	if err := json.Unmarshal(out, &jsSays); err != nil {
		t.Fatalf("driver output: %v\n%s", err, out)
	}

	for _, n := range names {
		// The browser renders a String with quotes; the server hands back the
		// bare characters. Compare what the program computed, not how each
		// side prints it.
		got := strings.Trim(jsSays[n], `"`)
		if got != goSays[n] {
			t.Errorf("%s:\n  server:  %q\n  browser: %q", n, goSays[n], got)
		}
	}
	// Only claim agreement if there was agreement: a summary line that prints
	// under a failure is a test lying in its own output.
	if !t.Failed() {
		t.Logf("server and browser agree on %d values; `good` decoded as %q", len(names), goSays["good"])
	}
}
