package project

import (
	"os"
	"path/filepath"
	"testing"

	"mar/internal/ast"
	"mar/internal/runtime"
)

// Every load path elaborates, or a program is quietly wrong on one of them.
//
// This is the hazard ADR 0017 names, and it has bitten twice: once when the
// dev loader passed a literal nil where the checked types belonged, so the
// request guard was live in production and absent on every developer's
// machine for a month; and once in a comment at the production runtime's call
// site claiming its loader was "parse-only", which is the opposite of what it
// does.
//
// `1 + 1.50` is the cheapest witness. It passes `mar check` — the literal `1`
// elaborates to a Decimal — and a tree that skipped elaboration hands the
// evaluator a bare Int and dies with `+: expected Int`.

const elaborationWitness = `module Main exposing (total)

total : Decimal
total =
    1 + 1.50
`

func writeProbe(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Main.mar"), []byte(elaborationWitness), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mar.json"),
		[]byte(`{"name":"probe","locale":"en"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "Main.mar")
}

func totalOf(t *testing.T, env *runtime.Env, mods []*ast.Module) runtime.Value {
	t.Helper()
	v, ok := env.Lookup("Main.total")
	if !ok {
		if v, ok = env.Lookup("total"); !ok {
			t.Fatal("the probe declares `total`")
		}
	}
	_ = mods
	return v
}

func TestEveryLoadPathElaborates(t *testing.T) {
	entry := writeProbe(t)

	t.Run("the serve path", func(t *testing.T) {
		runtime.ResetForReload()
		env, mods, _, err := LoadIntoEnvWithModulesAndHook(entry, nil)
		if err != nil {
			t.Fatalf("`1 + 1.50` is a checked program: %v", err)
		}
		if _, ok := totalOf(t, env, mods).(runtime.VDecimal); !ok {
			t.Fatalf("the literal `1` must have elaborated to a Decimal, got %T", totalOf(t, env, mods))
		}
	})

	t.Run("the deployed path", func(t *testing.T) {
		// The one the production binary uses. A comment at its call site
		// used to claim it skipped checking.
		runtime.ResetForReload()
		env, mods, err := LoadIntoEnvForRuntime(entry, nil)
		if err != nil {
			t.Fatalf("the deployed binary must load what `mar build` produced: %v", err)
		}
		if _, ok := totalOf(t, env, mods).(runtime.VDecimal); !ok {
			t.Fatalf("the deployed path skipped elaboration, got %T", totalOf(t, env, mods))
		}
	})
}
