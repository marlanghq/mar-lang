package runtime

import (
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/typecheck"
)

// The logical operators short-circuit, and the reason it is a correctness
// property rather than an optimisation is that Mar is pure but NOT total. Pure
// checked code has exactly two ways to fail -- the 53-bit Int bound and the
// recursion budget -- and both are reachable from an operand. A strict `&&`
// therefore turns a guard into the thing that breaks:
//
//	n < 1 && 9007199254740991 + n > 0     raised an Int overflow with n = 5
//	depth < 80 && walk tree > 0           spent the stack the guard protected
//
// The second one is worst on iOS, where the stack holds about 85 Mar frames.
//
// These tests use the two failure modes as detectors: if the right operand is
// evaluated, the expression does not return a different answer, it errors. So a
// regression cannot pass quietly.
func evalBool(t *testing.T, expr string) (string, error) {
	t.Helper()
	src := "module SC exposing (answer)\n\nanswer : Bool\nanswer =\n    " + expr + "\n"
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse %q: %v", expr, err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check %q: %v", expr, err)
	}
	loaded, err := LoadModule(mod)
	if err != nil {
		return "", err
	}
	v, err := loaded.Get("answer")
	if err != nil {
		return "", err
	}
	b, ok := v.(VBool)
	if !ok {
		t.Fatalf("%q: not a Bool: %#v", expr, v)
	}
	if b.V {
		return "True", nil
	}
	return "False", nil
}

func TestLogicalOperatorsShortCircuit(t *testing.T) {
	// The right operand of each of these overflows Int if it is evaluated. The
	// left operand decides, so it must not be.
	skips := []struct{ expr, want string }{
		{"False && (9007199254740991 + 1) > 0", "False"},
		{"True || (9007199254740991 + 1) > 0", "True"},
		{"1 > 2 && (9007199254740991 + 1) > 0", "False"},
		{"2 > 1 || (9007199254740991 + 1) > 0", "True"},
	}
	for _, c := range skips {
		got, err := evalBool(t, c.expr)
		if err != nil {
			t.Errorf("%s: evaluated the right operand it should have skipped: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %s, want %s", c.expr, got, c.want)
		}
	}
}

func TestLogicalOperatorsStillEvaluateWhenTheyMust(t *testing.T) {
	// The control. Without it, an evaluator that never touched the right operand
	// at all would pass the test above. `&&` with a True left and `||` with a
	// False left have to look.
	musts := []struct{ expr, want string }{
		{"True && 2 > 1", "True"},
		{"True && 1 > 2", "False"},
		{"False || 2 > 1", "True"},
		{"False || 1 > 2", "False"},
	}
	for _, c := range musts {
		got, err := evalBool(t, c.expr)
		if err != nil {
			t.Errorf("%s: %v", c.expr, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s = %s, want %s", c.expr, got, c.want)
		}
	}

	// And the sharp version: a True left operand MUST reach a right operand that
	// overflows, because short-circuiting is about what the left decides, not
	// about skipping work.
	if _, err := evalBool(t, "True && (9007199254740991 + 1) > 0"); err == nil {
		t.Error("True && <overflow> returned a value; the right operand has to be evaluated")
	} else if !strings.Contains(err.Error(), "outside the range of Int") {
		t.Errorf("True && <overflow>: unexpected error %v", err)
	}
}

func TestShortCircuitSpendsNoStackOnTheSkippedBranch(t *testing.T) {
	// The failure mode that matters on iOS, where the host stack holds about 85
	// Mar frames: a recursion the guard says not to enter. With a strict `&&`
	// this blew the budget; short-circuiting it never starts.
	src := `module SC exposing (answer)


deep : Int -> Int
deep n =
    if n == 0 then
        0

    else
        1 + deep (n - 1)


answer : Bool
answer =
    5 < 1 && deep 400000 > 0
`
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check: %v", err)
	}
	loaded, err := LoadModule(mod)
	if err != nil {
		t.Fatalf("a guarded recursion was entered anyway: %v", err)
	}
	v, err := loaded.Get("answer")
	if err != nil {
		t.Fatalf("get answer: %v", err)
	}
	if b, ok := v.(VBool); !ok || b.V {
		t.Errorf("answer = %#v, want False", v)
	}
	// 400000 is deliberately past the depth budget AND past what the host stack
	// could hold: if this test ever starts failing by taking the process down
	// rather than by reporting, that is the regression, loudly.
}
