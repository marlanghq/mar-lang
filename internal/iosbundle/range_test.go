package iosbundle

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/jsserve"
	"mar/internal/parityfixture"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// The phone's half of parityfixture.RangeCases, run, not scanned.
func TestThePhoneKeepsATimeAndADateInsideTheirRanges(t *testing.T) {
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
	for _, c := range parityfixture.RangeCases {
		src := "module Main exposing (..)\n\n\nwrapped : String\nwrapped =\n    " + c.Expr + "\n"
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
		want := c.Says
		if c.Refused {
			want += c.Tail
			if err == nil {
				t.Errorf("%s: the phone built the value instead of refusing it: %s", c.Expr, got)
				continue
			}
		} else if err != nil {
			t.Errorf("%s: %v\n%s", c.Expr, err, got)
			continue
		}
		if !strings.Contains(got, want) {
			t.Errorf("%s\n  want %q\n  got  %q", c.Expr, want, got)
		}
	}
}
