package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LoadIntoEnvForRuntime is the path a DEPLOYED app takes: the mar-runtime stub
// unpacks the bundled sources and loads them. It used to skip type-checking on
// the grounds that the build already validated types: true, and beside the
// point. Checking is what ELABORATES the tree, and an unelaborated tree does
// not fail loudly; it produces a confident wrong answer or dies inside a
// builtin. `1 + 1.50` type-checks, runs under `mar dev`, and used to die here
// with `+: expected Int`, which is a defect that only ever appeared after
// deploying. See ADR 0017.
func TestRuntimeLoadKeepsElaboration(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	src := `module Main exposing (total)

-- The literal 1 is polymorphic; only elaboration records that it became a
-- Decimal here (ADR 0013). Parse alone leaves it an Int and + refuses.
total : Decimal
total = 1 + 1.50
`
	if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	rEnv, _, err := LoadIntoEnvForRuntime(entry, nil)
	if err != nil {
		t.Fatalf("the deployed path should load a program that mar check accepts: %v", err)
	}
	v, ok := rEnv.Lookup("Main.total")
	if !ok {
		t.Fatal("total was not bound")
	}
	if got := v.Display(); !strings.HasPrefix(got, "2.5") {
		t.Errorf("total = %s, want 2.50", got)
	}
}

// The corollary: a program the checker rejects must not load either. Before,
// the deployed path would happily evaluate whatever parsed.
func TestRuntimeLoadRejectsATypeError(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "Main.mar")
	src := `module Main exposing (bad)

bad : Int
bad = "not an int"
`
	if err := os.WriteFile(entry, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadIntoEnvForRuntime(entry, nil); err == nil {
		t.Fatal("a type-incorrect program should not load")
	}
}
