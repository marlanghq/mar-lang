package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/conformance"
	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// Two of the three runtimes run from here: the Go evaluator directly, and the
// JS one through node. The Swift runtime runs the same corpus from
// internal/iosbundle, where its sources live.
//
// The corpus, the expectations and the coverage gate are in
// internal/conformance: see that package for why a shared answer sheet is the
// point rather than the runtimes merely agreeing with each other.

func TestStdlibGoJSConformance(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; skipping JS conformance run")
	}
	mod, err := parser.Parse(conformance.Source)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}
	loaded, err := runtime.LoadModule(mod)
	if err != nil {
		t.Fatalf("go runtime load: %v", err)
	}
	goVal, err := loaded.Get("results")
	if err != nil {
		t.Fatalf("go runtime get: %v", err)
	}

	goCases, err := conformance.SplitCases(strings.Trim(goVal.Display(), `"`))
	if err != nil {
		t.Fatalf("go output: %v", err)
	}
	jsCases, err := conformance.SplitCases(runInNode(t, nodePath, mod, conformance.Entry))
	if err != nil {
		t.Fatalf("js output: %v", err)
	}

	// Whether they agree with each other, and whether each is right, are
	// separate questions. Both get asked.
	for _, line := range conformance.Difference("Go", goCases, "JS", jsCases) {
		t.Error(line)
	}
	for who, cases := range map[string]map[string]string{"Go": goCases, "JS": jsCases} {
		problems, err := conformance.Check(who, cases)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range problems {
			t.Error(p)
		}
	}
	if !t.Failed() {
		t.Logf("%d stdlib answers agree between Go, JS and the hand-written expectations", len(goCases))
	}
}

// runInNode evaluates a top-level value in the JS runtime and returns its
// rendering, so the two answers can be compared as text.
func runInNode(t *testing.T, nodePath string, mod *ast.Module, qualified string) string {
	t.Helper()
	dir := t.TempDir()
	programJSON, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(mod)}})
	if err != nil {
		t.Fatalf("marshal program: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.js"), []byte(runtimeJS), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "program.json"), programJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
process.stdout.write(String(globalThis.__marEvalValue(program, '` + qualified + `')));
`
	if err := os.WriteFile(filepath.Join(dir, "driver.js"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(nodePath, filepath.Join(dir, "driver.js"),
		filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node run: %v\n%s", err, stderr.String())
	}
	return strings.Trim(strings.TrimSpace(string(out)), `"`)
}
