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

// The client half of a service call had no test at all, and that is a large
// part of why it went unchecked.
//
// A request body is validated against its declared type before a handler sees
// it. The response was not validated by either client, because the declared
// type never reached them: a built program carries no type information of any
// kind, and the service value the client holds is a verb and a path. So both
// clients decoded whatever arrived and handed it to code checked against
// something else. In the browser a constructor the type does not declare threw
// inside the live page's update and was then swallowed by the stale-message
// guard — a frozen screen, no banner, no console line in a production bundle.
//
// The call site's type now rides on the elaborated node, the same channel the
// request side uses, and a mismatch goes where a body that would not decode
// already went: Err ServerError.
func TestServiceResponseIsCheckedInTheBrowser(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	src := `module Main exposing (..)

type Status
    = Open
    | Closed String

statusOf : Service () Status
statusOf = Service.declare GET "/api/status"

type Msg
    = GotStatus (Result Service.Error Status)

fetchStatus : Cmd Msg
fetchStatus = Service.call statusOf () GotStatus
`
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	programJSON, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("runtime.js", []byte(runtimeJS))
	write("program.json", programJSON)

	cases := []struct {
		body string
		want string
	}{
		{`{"__ctor":"Open"}`, `GotStatus (Ok Open)`},
		{`{"__ctor":"Closed","__args":["maintenance"]}`, `GotStatus (Ok (Closed "maintenance"))`},
		// A tag the type does not declare. This is the one that froze the
		// screen: it reached the page's update and threw there.
		{`{"__ctor":"Purple"}`, "should be one of `Open`, `Closed`, got `Purple`"},
		// The right tag carrying the wrong thing. This one did not even
		// throw — it rendered "closed because undefined" and looked fine.
		{`{"__ctor":"Closed","__args":[42]}`, "should be a string, got a whole number"},
		{`{"__ctor":"Closed","__args":[]}`, "`Closed` takes 1 value, got 0"},
	}
	for _, c := range cases {
		driver := `
const fs = require('fs');
globalThis.fetch = () => Promise.resolve({
  ok: true, status: 200, headers: { get: () => null },
  text: () => Promise.resolve(` + "`" + c.body + "`" + `),
});
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
globalThis.__marTestRunCmd(program, 'Main.fetchStatus')
  .then(v => process.stdout.write(String(v)));
`
		write("driver.js", []byte(driver))
		cmd := exec.Command(node, filepath.Join(dir, "driver.js"),
			filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"))
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("node run for %s: %v\n%s", c.body, err, stderr.String())
		}
		if !strings.Contains(string(out), c.want) {
			t.Errorf("body %s\n  got:  %s\n  want it to contain: %s", c.body, out, c.want)
		}
	}
}
