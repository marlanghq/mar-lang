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
	"mar/internal/typecheck"
)

// The browser's half of the auth port.
//
// Everything a program receives from the network is checked against the type
// the call site asked for — except, until now, the user the sign-in endpoint
// answers with, which is the one value in an app that decides who someone IS.
//
// The program below is the whole point of the test: `u.id` is an Int because
// the checker says so, and the table's third case sends a String. Before the
// shape, that String arrived.
func TestTheBrowserChecksWhoTheSignInEndpointSaysYouAre(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	const src = `module Main exposing (..)


type alias User =
    { id : Int, email : String }


type Msg
    = Got String


seen : Auth.VerifyOutcome User -> Msg
seen o =
    case o of
        Auth.SignedIn u ->
            Got ("SignedIn " ++ String.fromInt u.id ++ " " ++ u.email)

        Auth.WrongCode ->
            Got "WrongCode"

        Auth.TooManyAttempts ->
            Got "TooManyAttempts"


fromResult : Result Service.Error (Auth.VerifyOutcome User) -> Msg
fromResult r =
    case r of
        Ok o ->
            seen o

        Err e ->
            Got (Service.errorToString e)


verify : Cmd Msg
verify =
    Auth.verifyCode { email = "a@b.c", code = "1234" } fromResult
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
	pj, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}
	// The shape has to have SURVIVED to the program the browser runs: without
	// it every case below passes for the wrong reason, because an unstamped
	// node still answers Ok for anything that parses.
	if !strings.Contains(string(pj), `"name":"verifyCode","wire"`) {
		t.Fatal("the Auth.verifyCode occurrence carries no shape; nothing below would mean anything")
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.js"), rt, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "program.json"), pj, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, c := range parityfixture.AuthPortCases {
		t.Run(c.Why, func(t *testing.T) {
			q, _ := json.Marshal(c.Body)
			drv := `
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
globalThis.fetch = () => Promise.resolve({
  ok: true, status: 200, headers: { get: () => null },
  text: () => Promise.resolve(` + string(q) + `),
});
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
globalThis.__marTestRunCmd(program, 'Main.verify').then(v => process.stdout.write(String(v)));
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
			shown := strings.TrimSpace(string(out))
			rest, ok := strings.CutPrefix(shown, "Got ")
			if !ok {
				t.Fatalf("the program dispatched something else: %s", shown)
			}
			if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
				rest = rest[1 : len(rest)-1]
			}
			got := strings.Trim(rest, `"`)
			got = strings.ReplaceAll(got, `\"`, `"`)
			if c.Says == "" {
				if !strings.HasPrefix(got, "SignedIn ") {
					t.Errorf("%q should have signed the user in, and answered %q", c.Body, got)
				}
				return
			}
			if got != c.Says {
				t.Errorf("%q\n  want: %q\n  got:  %q", c.Body, c.Says, got)
			}
		})
	}
}
