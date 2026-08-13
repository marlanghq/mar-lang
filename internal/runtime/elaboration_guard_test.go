package runtime

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// The runtime cannot re-derive what the typechecker decided: this package
// deliberately does not import the typechecker, so types are gone by the time
// Eval runs. An unelaborated tree therefore does not fail loudly; it produces
// a confident wrong answer: an Int where the checker proved a Decimal, or
// values evaluated in an order that reads placeholders.
//
// The guard turns that silent disagreement into a refusal. These tests pin it,
// because the whole value of the guard is that it fires.

func TestLoadModuleRefusesUnelaboratedTree(t *testing.T) {
	mod, err := parser.Parse("module M exposing (..)\nx = 1\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Parsed but never checked: the exact shape the guard exists for.
	_, err = LoadModule(mod)
	if err == nil {
		t.Fatal("loading an unchecked module should be refused")
	}
	if !strings.Contains(err.Error(), "elaborated") {
		t.Errorf("error should name the missing elaboration, got: %v", err)
	}
	if !strings.Contains(err.Error(), "CheckModule") {
		t.Errorf("error should say what to call, got: %v", err)
	}
}

func TestLoadModuleAcceptsCheckedTree(t *testing.T) {
	mod, err := parser.Parse("module M exposing (..)\nx = 1\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}
	if _, err := LoadModule(mod); err != nil {
		t.Fatalf("a checked module should load: %v", err)
	}
}

// A tree built in Go rather than parsed has nothing to elaborate, and the
// two places that build one say so. That claim has to be expressible, or the
// guard would block a legitimate path.
func TestSyntheticModuleCanClaimExemption(t *testing.T) {
	synthetic := &ast.Module{
		Decls: []ast.Decl{
			&ast.ValueDecl{Name: "x", Body: &ast.EUnit{}},
		},
	}
	if _, err := LoadModule(synthetic); err == nil {
		t.Fatal("an unmarked synthetic module should still be refused")
	}
	synthetic.MarkElaborated()
	if _, err := LoadModule(synthetic); err != nil {
		t.Fatalf("a marked synthetic module should load: %v", err)
	}
}

// The checker is what sets the mark, and it must do so on the way out.
func TestCheckModuleMarksTheTree(t *testing.T) {
	mod, err := parser.Parse("module M exposing (..)\nx = 1\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if mod.IsElaborated() {
		t.Fatal("a freshly parsed module must not claim to be elaborated")
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}
	if !mod.IsElaborated() {
		t.Fatal("CheckModule must mark the tree it elaborated")
	}
}
