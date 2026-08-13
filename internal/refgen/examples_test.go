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

// parseExpr compiles a Mar expression the way the REPL does: a throwaway
// binding parsed and inferred against the stdlib env, and hands back both the
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
// has no meaningful value to compare against: a Canvas.rect call is a Shape, a
// UI.text call is a View, Time.now is a Task, so the bar is that it compiles.
// That still catches the failure that matters here: if the signature changes
// under it, the snippet stops typechecking and this test goes red, the same way
// a drifted signature fails the staleness test. What it deliberately does not
// claim is that the snippet draws the right rectangle; the examples that can be
// checked by machine are, and the rest are honest usage.
func checkExample(src string) error {
	t, body, err := parseExpr(src)
	if err != nil {
		// Not an expression. Some examples have to be declarations: a Path or
		// a table only comes into being through a binding, and naming the thing
		// is half of what the example is teaching, so try again as a module
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
// equality is run and must hold. A wrong example, or a stdlib change that
// quietly breaks one: fails here.
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
//
// Only a Bool EXPRESSION earns the rewrite, and the check has to be written
// that way round. It used to skip anything parseExpr could not read, which
// meant a declaration snippet was waved through without ever being looked at:
// `greeting = if App.locale == "pt-BR" then ...` is not an expression, so it
// failed to parse, so it was skipped, so it shipped to the page as
// `greeting = if App.locale    -- "pt-BR" then "Ola" else "Hello"`. The
// examples this guard cannot classify are exactly the ones it must not trust.
func TestNonEqualityExamplesAreUnambiguous(t *testing.T) {
	for _, e := range Entries() {
		for _, ex := range e.Examples {
			if ty, _, err := parseExpr(ex); err == nil && typecheck.Pretty(ty) == "Bool" {
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

// TestExamplesDoNotFakeAMessage closes the hole that let `UI.button [] 0 "Save"`
// sit in the reference for months inside a green suite.
//
// The free `a` in `View a` / `Sub a` / `Cmd a` is the APP'S MESSAGE. Checked on
// its own, an example can instantiate it with anything: `UI.button [] 0 "Save"`
// infers `View Int`, `UI.textField [] "Email" "" (\s -> s)` infers `View String`,
// and both compile. Neither is code a page could ever contain, because
// `Page.create` unifies the view's message with update's, so the reader is being
// shown something that does not typecheck where they will put it. It also reads
// wrong: a bare `0` in the message slot looks like an id.
//
// THE RULE IS THE INVERSE OF THE OBVIOUS ONE, and measuring is what found that.
// A free message variable is almost always CORRECT: `UI.text [] "Hello"` is a
// view that emits nothing, so `View a` is exactly right, and so are `UI.empty`,
// `Nav.push` and `Sound.play`. What cannot be right is a CONCRETE payload in an
// example written as a bare expression, because a real message type has to be
// declared, and declaring one forces the example into module form. So a
// concrete payload here is always a placeholder wearing a message's clothes.
//
// `()` is the exception and a real one: `main : Cmd ()` is the type of an app's
// entry point, which is what `App.frontend` and friends return.
//
// This rule found nine examples that a careful manual sweep had just missed.
func TestExamplesDoNotFakeAMessage(t *testing.T) {
	carriers := []string{"View ", "Sub ", "Cmd "}
	for _, e := range Entries() {
		for _, ex := range e.Examples {
			ty, _, err := parseExpr(ex)
			if err != nil {
				// A declaration block. Those can name a real Msg, and the ones
				// that need to already do.
				continue
			}
			p := typecheck.Pretty(ty)
			for _, c := range carriers {
				if !strings.HasPrefix(p, c) {
					continue
				}
				payload := p[len(c):]
				if payload == "" || payload == "()" {
					continue
				}
				if len(payload) == 1 && payload[0] >= 'a' && payload[0] <= 'z' {
					continue // still polymorphic, which is the healthy case
				}
				t.Errorf("%s: the example fakes a message.\n    %s\n    infers %s, but the message slot of a %sis the app's Msg.\n    Write the example as a declaration block that names one:\n        type Msg = Something\n\n        name = ...",
					e.Qualified(), ex, p, c)
			}
		}
	}
}
