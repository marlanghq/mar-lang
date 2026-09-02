package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// A path parameter crosses the boundary twice: `linkTo` writes it into a URL,
// and the router reads it back. `internal/runtime/path_roundtrip_test.go` pins
// that round trip on the SERVER — and the browser is where the user actually
// clicks the link.
//
// Nothing exercised the browser's half. Found by giving runtime.js coverage it
// never had (`NODE_V8_COVERAGE` + tools/js_coverage.py): `pathParamString`,
// `decodePathSegment` and `matchPathPattern` were entered by no test at all,
// while the server's halves have a census. This is the port where three
// different URL encodings once lived, one per runtime.
//
// The comparison is against the SERVER's answer for the same input, because
// "the browser encodes something" is not the property that matters — "the two
// write the same URL" is.
func TestTheBrowserBuildsTheSameLinksTheServerDoes(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	// One module, one `linkTo` per case, so each top-level value is the URL
	// the browser built.
	cases := []struct{ name, param, mar string }{
		{"a plain slug", "ok", `"ok"`},
		{"a space", "a b", `"a b"`},
		{"a slash, which used to split the segment in two", "a/b", `"a/b"`},
		{"a question mark", "a?b", `"a?b"`},
		{"a hash", "a#b", `"a#b"`},
		{"an ampersand", "a&b", `"a&b"`},
		{"a non-ASCII letter", "ç", `"ç"`},
		{"the unreserved set, which must NOT be escaped", "a-b._~c", `"a-b._~c"`},
	}

	var src strings.Builder
	src.WriteString("module Main exposing (..)\n\n\nnote : Path { slug : String }\nnote =\n    \"/notes/{slug:String}\"\n")
	names := make([]string, len(cases))
	for i, c := range cases {
		names[i] = "link" + string(rune('A'+i))
		src.WriteString("\n\n" + names[i] + " : String\n" + names[i] + " =\n    linkTo note { slug = " + c.mar + " }\n")
	}

	mod, err := parser.Parse(src.String())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check: %v", err)
	}

	// The server's answers, from the same tree.
	loaded, err := runtime.LoadModule(mod)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	server := map[string]string{}
	for _, n := range names {
		v, ok := loaded.Env.Lookup(n)
		if !ok {
			t.Fatalf("%s is not bound", n)
		}
		s, ok := v.(runtime.VString)
		if !ok {
			t.Fatalf("%s is a %T", n, v)
		}
		server[n] = s.V
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
	var browser map[string]string
	if err := json.Unmarshal(out, &browser); err != nil {
		t.Fatalf("driver output: %v\n%s", err, out)
	}

	for i, c := range cases {
		name := names[i]
		got := strings.Trim(browser[name], `"`)
		want := server[name]
		if got != want {
			t.Errorf("%s (%q):\n  server:  %s\n  browser: %s", c.name, c.param, want, got)
		}
		// And the URL has to still be one segment under /notes/, or the
		// router cannot match the pattern the link was built from.
		if rest, ok := strings.CutPrefix(want, "/notes/"); !ok || strings.Contains(rest, "/") {
			t.Errorf("%s (%q): %s is not a single segment under /notes/", c.name, c.param, want)
		}
	}
}
