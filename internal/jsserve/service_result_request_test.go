package jsserve

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// A Result in a request from the browser. The server writes every constructor
// as {"__args":[...],"__ctor":"Tag"}, Ok and Err included, and its request
// guard checks for that tag; anything else in the place of a Result is
//
//	invalid request: `r` should be one of `Ok`, `Err`, got a whole number
//
// The request is read off a stubbed fetch and handed to the server's own
// dispatcher with the request shape stamped the way internal/project does it,
// so the assertion is what the SERVER accepts, not what this file expects.
func TestTheBrowserSendsAResultTheServerAccepts(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	const src = `module Main exposing (..)


type Msg
    = Got (Result Service.Error Int)


postIt : Service { r : Result String Int } Int
postIt =
    Service.declare POST "/api/post"


getIt : Service { r : Result String Int } Int
getIt =
    Service.declare GET "/api/get"


postOk : Cmd Msg
postOk =
    Service.call postIt { r = Ok 1 } Got


postErr : Cmd Msg
postErr =
    Service.call postIt { r = Err "bad" } Got


getOk : Cmd Msg
getOk =
    Service.call getIt { r = Ok 1 } Got


getErr : Cmd Msg
getErr =
    Service.call getIt { r = Err "bad" } Got
`
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := typecheck.CheckModule(mod)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	con := typecheck.StripForall(res.ValueTypes["postIt"]).(typecheck.TCon)
	shape := typecheck.WireShapeOf(con.Args[0], typecheck.CtorsFromEnv(typecheck.BaseEnv()))

	dir := t.TempDir()
	rt, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	pj, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}
	for name, b := range map[string][]byte{
		"runtime.js":   rt,
		"program.json": pj,
		"driver.js": []byte(`
const fs = require('fs');
let asked = null;
globalThis.window = globalThis; globalThis.document = undefined;
globalThis.fetch = (url, init) => {
  asked = { url, body: (init && init.body) || '' };
  return Promise.resolve({ ok: true, status: 200, headers: { get: () => null },
                           text: () => Promise.resolve('1') });
};
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
globalThis.__marEvalRaw(program, process.argv[4]).run();
process.stdout.write(JSON.stringify(asked));
`),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	echo := func(verb, path string) runtime.Value {
		h := runtime.VFn{Arity: 1, Native: func(args []runtime.Value) (runtime.Value, error) {
			in := args[0]
			return runtime.VEffect{Run: func() (runtime.Value, error) { return in, nil }}, nil
		}}
		s := shape
		return runtime.ExposedServiceToRoute(runtime.VExposedService{Service: runtime.VService{
			Verb: verb, Path: path, Handler: h, InputShape: &s}})
	}
	withMaxBodyBytes(t, 1<<20)
	routes := []runtime.Value{echo("POST", "/api/post"), echo("GET", "/api/get")}

	for _, c := range []struct{ value, verb, want string }{
		{"postOk", "POST", `{"r":{"__args":[1],"__ctor":"Ok"}}`},
		{"postErr", "POST", `{"r":{"__args":["bad"],"__ctor":"Err"}}`},
		{"getOk", "GET", `{"r":{"__args":[1],"__ctor":"Ok"}}`},
		{"getErr", "GET", `{"r":{"__args":["bad"],"__ctor":"Err"}}`},
	} {
		t.Run(c.value, func(t *testing.T) {
			out, err := exec.Command(node, filepath.Join(dir, "driver.js"),
				filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"), "Main."+c.value).CombinedOutput()
			if err != nil {
				t.Fatalf("node: %v\n%s", err, out)
			}
			var asked struct{ URL, Body string }
			if err := json.Unmarshal(out, &asked); err != nil {
				t.Fatalf("driver output %q: %v", out, err)
			}
			r := httptest.NewRequest(c.verb, asked.URL, strings.NewReader(asked.Body))
			w := httptest.NewRecorder()
			if !dispatchBackend(routes, r.URL.Path, w, r) {
				t.Fatalf("%s %s matched no route", c.verb, asked.URL)
			}
			// The echo handler answers with the request it was handed, through
			// the server's own encoder.
			if got := strings.TrimSpace(w.Body.String()); w.Code != 200 || got != c.want {
				t.Errorf("the browser sent %s %s %s\n  server answered: %d %s\n  want:            200 %s",
					c.verb, asked.URL, asked.Body, w.Code, got, c.want)
			}
		})
	}
}
