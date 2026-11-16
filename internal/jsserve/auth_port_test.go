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
  ok: ` + okOf(c.Status) + `, status: ` + strconv.Itoa(c.Status) + `, headers: { get: () => null },
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
			if c.Outcome != "" {
				if got != c.Outcome {
					t.Errorf("%d %q\n  want: %q\n  got:  %q", c.Status, c.Body, c.Outcome, got)
				}
				return
			}
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

// The browser signs in a User with one column of every kind an Entity can
// declare, from the body the server is held to sending for it
// (parityfixture.RichUserBody; TestTheSignInAnswerIsTheUserInTheWireFormat is
// the server's half). The server used to send that user with its Decimal as
// null (RichUserOldBody), and this program answers that with the shape
// mismatch instead of a user, which it still has to.
func TestTheBrowserSignsInAUserWithEveryColumnKind(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	mod, err := parser.Parse(parityfixture.RichUserProgram)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check: %v", err)
	}
	pj, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(pj), `"name":"verifyCode","wire"`) {
		t.Fatal("the Auth.verifyCode occurrence carries no shape; the user would be taken unchecked")
	}
	rt, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, c := range []struct{ body, want string }{
		{parityfixture.RichUserBody, parityfixture.RichUserShown},
		{parityfixture.RichUserOldBody, parityfixture.RichUserOldSays},
	} {
		if got := signInInNode(t, node, dir, rt, pj, c.body); got != c.want {
			t.Errorf("signing in from %s\n  want: %s\n  got:  %s", c.body, c.want, got)
		}
	}
}

// signInInNode runs the program's Main.verify in node against a fetch that
// answers 200 with body, and returns what the program dispatched.
func signInInNode(t *testing.T, node, dir string, rt, pj []byte, body string) string {
	t.Helper()
	quoted, _ := json.Marshal(body)
	drv := `
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
globalThis.fetch = () => Promise.resolve({
  ok: true, status: 200, headers: { get: () => null },
  text: () => Promise.resolve(` + string(quoted) + `),
});
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
globalThis.__marTestRunCmd(program, 'Main.verify').then(v => process.stdout.write(String(v)));
`
	for name, b := range map[string][]byte{"runtime.js": rt, "program.json": pj, "driver.js": []byte(drv)} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(node, filepath.Join(dir, "driver.js"), filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	got := strings.TrimSpace(string(out))
	if rest, ok := strings.CutPrefix(got, "Got "); ok {
		got = strings.Trim(strings.Trim(rest, "()"), `"`)
	}
	return got
}
