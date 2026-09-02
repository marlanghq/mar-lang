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

// A dynamic service path — `PATCH /tasks/{id:Int}`, `GET /notes/{slug:String}` —
// has the same encoder problem `linkTo` had, on the wire instead of in the
// address bar. The comment beside the code records what it cost:
//
//	Service.call getNote { slug = "a'b" }   asked for  /item/a'b
//	linkTo       notePath { slug = "a'b" }  pointed at /item/a%27b
//
// One language, one job, two answers. `make guards-js` showed the client half
// still had no test: `pathParamString` was entered by nothing, because the
// examples that use dynamic service paths are not run by `go test`.
//
// The assertion is the URL the browser actually requests, read off a stubbed
// fetch, compared against what the SERVER builds for the same call.
func TestTheBrowserAsksForThePathTheServerWouldBuild(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	const src = `module Main exposing (..)


type Msg
    = Got (Result Service.Error Int)


getNote : Service { slug : String } Int
getNote =
    Service.declare GET "/api/notes/{slug:String}"


fetchIt : String -> Cmd Msg
fetchIt s =
    Service.call getNote { slug = s } Got
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
	// One program per case: the runtime has no "apply this function" hook for a
	// driver, and adding one to shipped code for a test is the wrong trade. A
	// nullary value is the same call with the argument already in place.
	for _, slug := range []string{"ok", "a b", "a/b", "a'b", "ç", "a-b._~c"} {
		t.Run(slug, func(t *testing.T) {
			one := strings.Replace(src,
				"fetchIt : String -> Cmd Msg\nfetchIt s =\n    Service.call getNote { slug = s } Got",
				"fetchIt : Cmd Msg\nfetchIt =\n    Service.call getNote { slug = "+quoteMar(slug)+" } Got", 1)
			m, err := parser.Parse(one)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if _, err := typecheck.CheckModule(m); err != nil {
				t.Fatalf("check: %v", err)
			}
			pj, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(m)}})
			if err != nil {
				t.Fatal(err)
			}
			write("program.json", pj)
			write("driver.js", []byte(`
const fs = require('fs');
let asked = '(never fetched)';
globalThis.window = globalThis; globalThis.document = undefined;
globalThis.fetch = (url) => {
  asked = url;
  return Promise.resolve({ ok: true, status: 200, headers: { get: () => null },
                           text: () => Promise.resolve('1') });
};
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
const cmd = globalThis.__marEvalRaw(program, 'Main.fetchIt');
try { cmd.run(); } catch (e) { asked = 'THREW: ' + String((e && e.message) || e); }
process.stdout.write(String(asked));
`))
			cmd := exec.Command(node, filepath.Join(dir, "driver.js"),
				filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"))
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("node: %v\n%s", err, stderr.String())
			}
			got := string(out)

			// What the server builds for the same value, from the same
			// encoder the router matches against.
			want := "/api/notes/" + serverEncodedSegment(t, slug)
			if got != want {
				t.Errorf("%q:\n  browser asked for: %s\n  server would build: %s", slug, got, want)
			}
		})
	}
}

// serverEncodedSegment asks the SERVER's encoder what this value looks like in
// a path segment, so the comparison is against the other runtime rather than
// against a string this test made up.
func serverEncodedSegment(t *testing.T, raw string) string {
	t.Helper()
	src := "module M exposing (..)\n\n\nnote : Path { slug : String }\nnote =\n    \"/api/notes/{slug:String}\"\n\n\nout : String\nout =\n    linkTo note { slug = " + quoteMar(raw) + " }\n"
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check: %v", err)
	}
	loaded, err := runtime.LoadModule(mod)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	v, ok := loaded.Env.Lookup("out")
	if !ok {
		t.Fatal("out is not bound")
	}
	s, ok := v.(runtime.VString)
	if !ok {
		t.Fatalf("out is a %T", v)
	}
	return strings.TrimPrefix(s.V, "/api/notes/")
}
