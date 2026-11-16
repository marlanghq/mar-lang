package iosbundle

import (
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/jsserve"
	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// The phone's half of internal/jsserve/service_result_request_test.go: a
// Result in a request carries its constructor tag, which the server's guard
// checks, as it does for every other constructor.
//
// The request is the one the phone's own buildServiceRequest builds, and it is
// handed to the server's route with the request shape stamped the way
// internal/project does it.
func TestThePhoneSendsAResultTheServerAccepts(t *testing.T) {
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc is not installed")
	}
	if testing.Short() {
		t.Skip("compiles the Swift core; not for -short")
	}
	const src = `module Main exposing (..)


okReq : { r : Result String Int }
okReq =
    { r = Ok 1 }


errReq : { r : Result String Int }
errReq =
    { r = Err "bad" }


postIt : Service { r : Result String Int } Int
postIt =
    Service.declare POST "/api/post"
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
	if err := assembleSwiftCore(t, dir); err != nil {
		t.Fatalf("assembling the core: %v", err)
	}
	bin := filepath.Join(dir, "driver")
	build := exec.Command(swiftc, "-o", bin)
	sources, _ := filepath.Glob(filepath.Join(dir, "*.swift"))
	build.Args = append(build.Args, sources...)
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("swiftc: %v\n%s", err, out)
	}
	pj, err := json.Marshal(map[string]any{"modules": []any{jsserve.SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}
	progPath := filepath.Join(dir, "program.json")
	if err := os.WriteFile(progPath, pj, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct{ value, verb, path, want string }{
		{"okReq", "POST", "/api/post", `{"r":{"__args":[1],"__ctor":"Ok"}}`},
		{"errReq", "POST", "/api/post", `{"r":{"__args":["bad"],"__ctor":"Err"}}`},
		{"okReq", "GET", "/api/get", `{"r":{"__args":[1],"__ctor":"Ok"}}`},
		{"errReq", "GET", "/api/get", `{"r":{"__args":["bad"],"__ctor":"Err"}}`},
	} {
		t.Run(c.verb+" "+c.value, func(t *testing.T) {
			out, err := exec.Command(bin, "--service-request", progPath, c.verb, c.path, "Main."+c.value).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			lines := strings.SplitN(strings.TrimSuffix(string(out), "\n"), "\n", 2)
			if len(lines) != 2 {
				t.Fatalf("driver printed %q", out)
			}
			u, err := url.Parse(lines[0])
			if err != nil {
				t.Fatal(err)
			}
			status, body := serveOnce(t, c.verb, c.path, shape, u.Path, u.RawQuery, lines[1])
			if status != 200 || body != c.want {
				t.Errorf("the phone sent %s %s %s\n  server answered: %d %s\n  want:            200 %s",
					c.verb, lines[0], lines[1], status, body, c.want)
			}
		})
	}
}

// serveOnce runs one request through the server's own service route, whose
// handler echoes the request it was handed through the server's encoder.
func serveOnce(t *testing.T, verb, path string, shape runtime.WireShape, reqPath, query, body string) (int64, string) {
	t.Helper()
	h := runtime.VFn{Arity: 1, Native: func(args []runtime.Value) (runtime.Value, error) {
		in := args[0]
		return runtime.VEffect{Run: func() (runtime.Value, error) { return in, nil }}, nil
	}}
	s := shape
	route := runtime.ExposedServiceToRoute(runtime.VExposedService{Service: runtime.VService{
		Verb: verb, Path: path, Handler: h, InputShape: &s}}).(runtime.VRecord)
	req := runtime.VRecord{
		Fields: map[string]runtime.Value{
			"path":  runtime.VString{V: reqPath},
			"query": runtime.VString{V: query},
			"body":  runtime.VString{V: body},
		},
		Order: []string{"path", "query", "body"},
	}
	v, err := route.Fields["handler"].(runtime.VFn).Native([]runtime.Value{req})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := v.(runtime.VEffect).Run()
	if err != nil {
		t.Fatal(err)
	}
	rec := resp.(runtime.VRecord)
	return rec.Fields["status"].(runtime.VInt).V, strings.TrimSpace(rec.Fields["body"].(runtime.VString).V)
}
