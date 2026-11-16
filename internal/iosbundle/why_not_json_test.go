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

// The phone told a person their document was not valid JSON when it was.
//
//	JSON.decode "{\"v\":1e400}"  ->  err the document is not valid JSON
//
// That document is valid. Foundation will not hold the number, and the phone
// reported the refusal as a syntax error, which sends someone looking for a
// missing brace that is not there.
//
// Foundation reports every failure as NSCocoaErrorDomain 3840, so the code says
// nothing; NSDebugDescription is what separates them. Reading a debug string is
// fragile, so it is used only to say something MORE precise, and no match falls
// back to the sentence this always gave -- an OS that rewords it degrades to
// yesterday's behaviour rather than to something worse. That fallback is the
// second case here, and it was verified by breaking the discriminator.
//
// The three runtimes still answer this document differently; that divergence is
// recorded beside the code in internal/runtime/json.go, because agreeing means
// deciding what each one ACCEPTS as a number.
func TestThePhoneSaysWhyADocumentWasRefused(t *testing.T) {
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

	const src = `module Main exposing (..)


say : String -> String
say s =
    case JSON.decode s of
        Ok r ->
            "ok " ++ r.v

        Err e ->
            "err " ++ e


huge : String
huge =
    say "{\"v\":1e400}"


bad : String
bad =
    say "<html>"


keyless : String
keyless =
    say "{oops}"
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

	for _, c := range []struct{ name, value, want string }{
		// The phone used to say "the document holds a number too large", because
		// Foundation refused the whole document and it never saw the number.
		// It reads the literals out of the text now, so it names the number and
		// says the same sentence the other two runtimes say. The case stays
		// here because what it still proves is that a VALID document is not
		// reported as invalid JSON.
		{"a number no runtime can hold, in a document that IS valid JSON",
			"huge", "JSON: 1e400 is not a number Mar can hold exactly: an Int is at most 9007199254740991 and a Decimal at most 34 significant digits and 34 decimal places"},
		{"a page where a document was expected", "bad", "the document is not valid JSON"},
		{"an object with no key", "keyless", "the document is not valid JSON"},
	} {
		t.Run(c.name, func(t *testing.T) {
			out, err := exec.Command(bin, progPath, "Main."+c.value).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			got := strings.TrimSpace(string(out))
			if got != "err "+c.want {
				t.Errorf("want %q\ngot  %q", "err "+c.want, got)
			}
		})
	}
}
