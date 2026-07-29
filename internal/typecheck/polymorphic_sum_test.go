package typecheck

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
)

// List.sum is one name over two element types, and the checker is the only
// party that can tell them apart in the case that matters — an empty list. It
// records the answer on the reference node (ast.EQualified.Impl), so these
// tests are about what ends up written there, not just what typechecks.

func checkSrc(t *testing.T, src string) *CheckResult {
	t.Helper()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	res, err := CheckModule(mod)
	if err != nil {
		t.Fatalf("typecheck: %v", err)
	}
	return res
}

func TestPolymorphicSumSignature(t *testing.T) {
	env := BaseEnv()
	for _, name := range []string{"List.sum", "List.product"} {
		scheme, ok := env.Lookup(name)
		if !ok {
			t.Fatalf("%s missing from BaseEnv", name)
		}
		// The published signature feeds the LSP and the /reference site, so
		// an erased constraint here would advertise a type the compiler
		// rejects — you cannot sum strings.
		if got, want := Pretty(Instantiate(scheme)), "List number -> number"; got != want {
			t.Errorf("%s : %s, want %s", name, got, want)
		}
	}
}

func TestPolymorphicSumAcceptsBothElementTypes(t *testing.T) {
	checkSrc(t, `module M exposing (..)

ints : Int
ints =
    List.sum [ 1, 2, 3 ]


money : Decimal
money =
    List.sum [ 1.50, 2.25 ]


times : Decimal
times =
    List.product [ 1.5, 2.0 ]
`)
}

func TestPolymorphicSumRejectsNonNumbers(t *testing.T) {
	mod, err := parser.Parse(`module M exposing (..)

bad : String
bad =
    List.sum [ "a", "b" ]
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	_, err = CheckModule(mod)
	if err == nil {
		t.Fatal("summing strings should not typecheck")
	}
	if !strings.Contains(err.Error(), "number") {
		t.Errorf("error should name the number constraint, got: %v", err)
	}
}

// The load-bearing one. An empty list has no element to inspect, so the zero
// the runtime hands back can only come from the checker having written down
// which implementation this occurrence resolved to.
func TestEmptyListElaboratesToDecimalImplementation(t *testing.T) {
	mod, err := parser.Parse(`module M exposing (..)

nothingYet : List Decimal
nothingYet =
    []


total : Decimal
total =
    List.sum nothingYet


count : Int
count =
    List.sum []
`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}

	var impls []string
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		switch n := e.(type) {
		case *ast.EQualified:
			if n.Name == "sum" {
				impls = append(impls, n.Impl)
			}
		case *ast.EApp:
			walk(n.Fn)
			walk(n.Arg)
		}
	}
	for _, d := range mod.Decls {
		if vd, ok := d.(*ast.ValueDecl); ok {
			walk(vd.Body)
		}
	}

	if len(impls) != 2 {
		t.Fatalf("expected 2 List.sum references, found %d", len(impls))
	}
	// Source order: the Decimal one, then the Int one.
	if impls[0] != "listSumDecimal" {
		t.Errorf("Decimal call site: Impl = %q, want listSumDecimal", impls[0])
	}
	if impls[1] != "" {
		t.Errorf("Int call site: Impl = %q, want empty (the default is Int)", impls[1])
	}
}

// The Decimal implementations are registered in the runtimes but deliberately
// absent from the typecheck env, which is what keeps the language at one name.
func TestDecimalImplementationsAreNotWritable(t *testing.T) {
	env := BaseEnv()
	for _, name := range []string{"listSumDecimal", "listProductDecimal", "Decimal.sum"} {
		if _, ok := env.Lookup(name); ok {
			t.Errorf("%s is reachable from Mar source; it must stay internal", name)
		}
	}
}
