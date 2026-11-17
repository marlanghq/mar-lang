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

// A Duration is a whole number of milliseconds inside the range of Int, so a
// constructor whose interval does not fit refuses, in the sentence the server
// and the browser use (internal/runtime durationOutOfRange). Run, not scanned:
// a source scan cannot tell a refusal from one that was switched off.
func TestThePhoneRefusesADurationPastInt(t *testing.T) {
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

	const tail = " is outside the range of a Duration (-9007199254740991 to 9007199254740991 milliseconds)"
	for _, c := range []struct {
		expr, want string
		refused    bool
	}{
		{"String.fromInt (Time.toSeconds (Time.weeks 9007199254740991))",
			"Duration out of range: Time.weeks 9007199254740991" + tail, true},
		{"String.fromInt (Time.toSeconds (Time.seconds 9007199254741))",
			"Duration out of range: Time.seconds 9007199254741" + tail, true},
		{"String.fromInt (Time.toSeconds (Time.seconds 9007199254740))", "9007199254740", false},
	} {
		src := "module Main exposing (..)\n\n\nwrapped : String\nwrapped =\n    " + c.expr + "\n"
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
		if c.refused && err == nil {
			t.Errorf("%s: the phone built the Duration instead of refusing it: %s", c.expr, got)
			continue
		}
		if !c.refused && err != nil {
			t.Errorf("%s: %v\n%s", c.expr, err, got)
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("%s\n  want %q\n  got  %q", c.expr, c.want, got)
		}
	}
}
