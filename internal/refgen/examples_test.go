package refgen

import (
	"fmt"
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// parseExpr compiles a Mar expression the way the REPL does — a throwaway
// binding parsed and inferred against the stdlib env — and hands back both the
// inferred type and the tree, so the caller can decide whether to run it.
// checkModuleExample compiles an example that is a block of declarations rather
// than a single expression. Some things cannot be written as one: a Path only
// comes into being through an annotated binding, so the only honest example for
// Nav.pushTo or Page.dynamic declares a route and then uses it. Those go through
// the full module checker instead, and are compile-only by nature.
func checkModuleExample(src string) error {
	mod, err := parser.Parse("module Ex exposing (..)\n" + src + "\n")
	if err != nil {
		return err
	}
	_, err = typecheck.CheckModule(mod)
	return err
}

func parseExpr(src string) (typecheck.Type, ast.Expr, error) {
	mod, err := parser.Parse("module Ex exposing (..)\n__ex = " + src + "\n")
	if err != nil {
		return nil, nil, err
	}
	if len(mod.Decls) == 0 {
		return nil, nil, fmt.Errorf("no declaration parsed")
	}
	vd, ok := mod.Decls[0].(*ast.ValueDecl)
	if !ok {
		return nil, nil, fmt.Errorf("not a value declaration")
	}
	t, err := typecheck.InferExpr(vd.Body, typecheck.BaseEnv())
	if err != nil {
		return nil, nil, err
	}
	return t, vd.Body, nil
}

// checkExample verifies one authored example, at a tier read off the example
// itself rather than declared alongside it.
//
// An equality like `List.map negate [1, 2, 3] == [-1, -2, -3]` infers as Bool:
// it makes a claim, so it is evaluated and must come out True. Anything else
// has no meaningful value to compare against — a Canvas.rect call is a Shape, a
// UI.text call is a View, Time.now is a Task — so the bar is that it compiles.
// That still catches the failure that matters here: if the signature changes
// under it, the snippet stops typechecking and this test goes red, the same way
// a drifted signature fails the staleness test. What it deliberately does not
// claim is that the snippet draws the right rectangle; the examples that can be
// checked by machine are, and the rest are honest usage.
func checkExample(src string) error {
	t, body, err := parseExpr(src)
	if err != nil {
		// Not an expression. Some examples have to be declarations — a Path or
		// a table only comes into being through a binding, and naming the thing
		// is half of what the example is teaching — so try again as a module
		// body. Whether it is one line or several does not matter; what matters
		// is whether it parses as an expression, which is a question the parser
		// can answer better than a heuristic on the text.
		return checkModuleExample(src)
	}
	if typecheck.Pretty(t) != "Bool" {
		return nil
	}
	v, err := runtime.Eval(body, runtime.BaseEnv())
	if err != nil {
		return err
	}
	b, ok := v.(runtime.VBool)
	if !ok {
		return fmt.Errorf("expected a Bool, got %T", v)
	}
	if !b.V {
		return fmt.Errorf("evaluates to False")
	}
	return nil
}

// TestExamplesHold is the trust anchor for the worked examples: every one is
// real Mar that compiles against the live stdlib, and every one that states an
// equality is run and must hold. A wrong example — or a stdlib change that
// quietly breaks one — fails here.
func TestExamplesHold(t *testing.T) {
	checked, evaluated := 0, 0
	for _, e := range Entries() {
		for _, ex := range e.Examples {
			checked++
			if ty, _, err := parseExpr(ex); err == nil && typecheck.Pretty(ty) == "Bool" {
				evaluated++
			}
			if err := checkExample(ex); err != nil {
				t.Errorf("%s: bad example:\n    %s\n    %v", e.Qualified(), ex, err)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no examples were checked — the examples map looks empty")
	}
	t.Logf("verified %d examples: %d run as equalities, %d compiled", checked, evaluated, checked-evaluated)
}

// TestNonEqualityExamplesAreUnambiguous guards the display path. The generator
// rewrites the first ` == ` in an example into `    -- `, so the page shows the
// result as a Mar comment. That rewrite is only correct for examples that ARE
// an equality; a compile-only snippet carrying ` == ` somewhere inside (in a
// lambda, say) would be silently mangled on the page. Keep the two apart.
func TestNonEqualityExamplesAreUnambiguous(t *testing.T) {
	for _, e := range Entries() {
		for _, ex := range e.Examples {
			ty, _, err := parseExpr(ex)
			if err != nil || typecheck.Pretty(ty) == "Bool" {
				continue
			}
			if strings.Contains(ex, " == ") {
				t.Errorf("%s: compile-only example contains ` == `, which the display rewrite would mangle:\n    %s", e.Qualified(), ex)
			}
		}
	}
}

// TestEveryEntryHasExample keeps example coverage total: every documented
// function carries at least one example. New stdlib functions do not ship to
// the reference until they have one.
func TestEveryEntryHasExample(t *testing.T) {
	var missing []string
	for _, e := range Entries() {
		if len(e.Examples) == 0 {
			missing = append(missing, e.Qualified())
		}
	}
	if len(missing) > 0 {
		t.Fatalf("functions without an example (add one to the examples map in content.go): %v", missing)
	}
}
