package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mar/internal/parityfixture"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// The browser's half of the same table the phone is held to.
//
// This one drives the whole path — a real Service.call against a fake fetch —
// rather than the decoder alone, so it also proves the decoded message is what
// actually reaches a Msg. The phone's test calls decodeServerError directly,
// because on that side the surrounding path is URLSession. Different levels,
// one table: the value of the table is that neither side gets to keep its own
// idea of what the answers are.
func TestTheBrowserWordsAFailedCallLikeTheTable(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	const src = `module Main exposing (..)


type Msg
    = Got String


ping : Service () Int
ping =
    Service.declare GET "/api/ping"


said : Result Service.Error Int -> Msg
said r =
    case r of
        Ok n ->
            Got (String.fromInt n)

        Err e ->
            Got (Service.errorToString e)


call : Cmd Msg
call =
    Service.call ping () said
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
	if err := os.WriteFile(filepath.Join(dir, "runtime.js"), rt, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "program.json"), programJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, c := range parityfixture.HTTPErrorCases {
		t.Run(c.Why, func(t *testing.T) {
			body, _ := json.Marshal(c.Body)
			drv := `
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
globalThis.fetch = () => Promise.resolve({
  ok: false, status: ` + strconv.Itoa(c.Status) + `, headers: { get: () => null },
  text: () => Promise.resolve(` + string(body) + `),
});
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
globalThis.__marTestRunCmd(program, 'Main.call').then(v => process.stdout.write(String(v)));
`
			if err := os.WriteFile(filepath.Join(dir, "driver.js"), []byte(drv), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(node, filepath.Join(dir, "driver.js"),
				filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"))
			var stderr strings.Builder
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("node: %v\n%s", err, stderr.String())
			}
			// `Got "…"`: the Msg the program dispatched, displayed. Unquoting
			// rather than Contains, because two of these answers differ from
			// the raw body only by what is absent from them.
			shown := strings.TrimSpace(string(out))
			rest, ok := strings.CutPrefix(shown, "Got ")
			if !ok {
				t.Fatalf("the program dispatched something else: %s", shown)
			}
			// The display form parenthesises a ctor argument that contains a
			// space, so `Got ("HTTP 500")` and `Got "boom"` are both shapes
			// this has to read.
			if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
				rest = rest[1 : len(rest)-1]
			}
			got, err := strconv.Unquote(rest)
			if err != nil {
				t.Fatalf("cannot read the message out of %s: %v", shown, err)
			}
			if got != c.Says {
				t.Errorf("%d with body %q\n  want: %q\n  got:  %q", c.Status, c.Body, c.Says, got)
			}
		})
	}
}
