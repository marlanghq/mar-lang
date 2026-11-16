package iosbundle

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"mar/internal/jsserve"
	"mar/internal/parityfixture"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// The phone's half of parityfixture.DecimalBoundCases and
// DecimalLongTextCases: each case is its own one-value program, because a
// value that raises stops the module holding it, and the answer is either what
// the value returns or the sentence it raises. A long-text case also has to
// finish within LongTextBudget, process start included. The server's half is
// TestTheServerAnswersTheDecimalBoundCases.
func TestThePhoneAnswersTheDecimalBoundCases(t *testing.T) {
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
	long := len(parityfixture.DecimalBoundCases)
	for i, c := range slices.Concat(parityfixture.DecimalBoundCases, parityfixture.DecimalLongTextCases) {
		src := "module Main exposing (..)\n\n\nx : String\nx =\n    " + c.Expr + "\n"
		mod, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("%s: parse: %v", c.Name, err)
		}
		if _, err := typecheck.CheckModule(mod); err != nil {
			t.Fatalf("%s: check: %v", c.Name, err)
		}
		pj, err := json.Marshal(map[string]any{"modules": []any{jsserve.SerializeModule(mod)}})
		if err != nil {
			t.Fatal(err)
		}
		prog := filepath.Join(dir, c.Name+".json")
		if err := os.WriteFile(prog, pj, 0o644); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		out, runErr := exec.Command(bin, prog, "Main.x").CombinedOutput()
		if took := time.Since(start); i >= long && took > parityfixture.LongTextBudget {
			t.Errorf("%s took %v on the phone; text has to be read in time linear in its length", c.Name, took)
		}
		got := strings.TrimSuffix(string(out), "\n")
		if runErr != nil {
			msg, ok := strings.CutPrefix(got, "error: ")
			if !ok {
				t.Errorf("%s: the driver failed without an error line: %v\n%s", c.Name, runErr, out)
				continue
			}
			got = msg
		}
		if got != c.Says {
			t.Errorf("%s:\n  phone: %s\n  want:  %s", c.Name, got, c.Says)
		}
	}
}
