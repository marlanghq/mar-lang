package iosbundle

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"mar/internal/jsserve"
	"mar/internal/parityfixture"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// The phone's half of the auth port, against the same table.
//
// Both clients are driven at the layer where the answer is decided:
// makeAuthOutcomeResult here, a whole Auth.verifyCode against a fake fetch in
// the browser's test. The phone's failure was the worse of the two — a body
// that would not parse became `()`, so `Auth.SignedIn ()` was handed to code
// typed as the app's user, and nothing anywhere said so.
func TestThePhoneChecksWhoTheSignInEndpointSaysYouAre(t *testing.T) {
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc is not installed")
	}
	if testing.Short() {
		t.Skip("compiles the Swift core; not for -short")
	}
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

	for _, c := range parityfixture.AuthPortCases {
		t.Run(c.Why, func(t *testing.T) {
			out, err := exec.Command(bin, "--auth-verify", itoa(c.Status), c.Body).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			got := strings.TrimSuffix(string(out), "\n")
			if c.Outcome != "" {
				if got != "Ok "+c.Outcome {
					t.Errorf("%d %q\n  want: %q\n  got:  %q", c.Status, c.Body, "Ok "+c.Outcome, got)
				}
				return
			}
			if c.Says == "" {
				// The accepted case prints the built value; what matters is
				// that it IS the signed-in ctor and carries the record.
				if !strings.Contains(got, `tag: "SignedIn"`) || !strings.Contains(got, "record") {
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

// The phone signs in a User with one column of every kind an Entity can
// declare, from the body the server is held to sending for it
// (parityfixture.RichUserBody), with the shape the checker stamps on
// RichUserProgram's Auth.verifyCode. The user the app is handed, written back
// in the wire format, has to be that body again: each field came through as
// its own type, the Decimal at its scale and the Time to the millisecond. And
// the body the server used to send (RichUserOldBody) has to be refused with
// the same sentence as in the browser: the write-back alone would pass with
// any shape, since RichUserBody is already in the wire format.
func TestThePhoneSignsInAUserWithEveryColumnKind(t *testing.T) {
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc is not installed")
	}
	if testing.Short() {
		t.Skip("compiles the Swift core; not for -short")
	}
	mod, err := parser.Parse(parityfixture.RichUserProgram)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check: %v", err)
	}
	pj, err := json.Marshal(map[string]any{"modules": []any{jsserve.SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}
	shape := verifyCodeShape(t, pj)

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
	run := func(body string) string {
		out, err := exec.Command(bin, "--auth-verify", "200", body, string(shape)).CombinedOutput()
		if err != nil {
			t.Fatalf("driver: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	got, ok := strings.CutPrefix(run(parityfixture.RichUserBody), "SignedIn ")
	if !ok {
		t.Fatalf("the phone did not sign the user in:\n  %s", got)
	}
	if back, want := strictJSON(t, got), strictJSON(t, parityfixture.RichUserBody); !reflect.DeepEqual(back, want) {
		t.Errorf("the phone signed in\n  %s\nfrom\n  %s", got, parityfixture.RichUserBody)
	}
	if got := run(parityfixture.RichUserOldBody); got != parityfixture.RichUserOldSays {
		t.Errorf("the body the server used to send\n  %s\nwant: %s\ngot:  %s", parityfixture.RichUserOldBody, parityfixture.RichUserOldSays, got)
	}
}

// strictJSON reads one JSON document with its numbers as written, so 1 and
// 1.0 are different answers, and refuses anything after it.
func strictJSON(t *testing.T, s string) any {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(s))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, s)
	}
	if d.More() {
		t.Fatalf("data after the document: %s", s)
	}
	return v
}

// verifyCodeShape is the user shape the checker stamped on the program's
// Auth.verifyCode occurrence, as the phone reads it from program.json.
func verifyCodeShape(t *testing.T, program []byte) json.RawMessage {
	t.Helper()
	var found json.RawMessage
	var walk func(json.RawMessage)
	walk = func(raw json.RawMessage) {
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) == nil {
			if string(obj["name"]) == `"verifyCode"` && obj["wire"] != nil {
				found = obj["wire"]
			}
			for _, v := range obj {
				walk(v)
			}
			return
		}
		var arr []json.RawMessage
		if json.Unmarshal(raw, &arr) == nil {
			for _, v := range arr {
				walk(v)
			}
		}
	}
	walk(program)
	if found == nil {
		t.Fatal("the Auth.verifyCode occurrence carries no shape; the user would be taken unchecked")
	}
	return found
}
