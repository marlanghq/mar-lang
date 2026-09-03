package iosbundle

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/jsserve"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// A Decimal.Division is a question, not a number: the inert exact quotient,
// waiting for Decimal.rounded or withRemainder to say how much precision the
// answer gets. It has no JSON form, and the server and the browser refuse it.
//
// The phone encoded it as NULL, silently, because its codec cannot throw:
//
//	JSON.encode { q = 1.0 / 3.0 }   ->   {"q":null}
//
// off the device, with the receiver getting a null where a number belonged and
// nothing anywhere saying so. That is the worst shape a difference can take --
// not an error on one platform and not another, but a plausible WRONG ANSWER
// on one and a refusal on the others.
//
// The sentence is checked in internal/parityfixture; this checks the BEHAVIOUR,
// because a source scan cannot tell a refusal from a refusal that was disabled.
func TestThePhoneRefusesToEncodeAnUnresolvedDivision(t *testing.T) {
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

	// Wrapped in a record on purpose: the shape lint refuses a division passed
	// DIRECTLY to JSON.encode, so the direct case never reaches a runtime. This
	// is the case the lint cannot see, which is exactly the one that shipped
	// null.
	const src = `module Main exposing (..)


wrapped : String
wrapped =
    JSON.encode { q = 1.0 / 3.0 }
`
	mod, err := parser.Parse(src)
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
	progPath := filepath.Join(dir, "program.json")
	if err := os.WriteFile(progPath, pj, 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := exec.Command(bin, progPath, "Main.wrapped").CombinedOutput()
	got := strings.TrimSpace(string(out))
	if err == nil {
		t.Fatalf("the phone encoded an unresolved division instead of refusing it: %s\n\n"+
			"A null on the wire where a number belongs is a wrong answer, not an error, and\n"+
			"the receiver has no way to tell.", got)
	}
	if !strings.Contains(got, "cannot encode a Decimal.Division") {
		t.Errorf("the phone refused, but not in the words the other two use:\n  %s", got)
	}
}
