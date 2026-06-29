package runtime

import "testing"

// Integer divide-by-zero is total and yields 0 on every Mar runtime: the web
// (runtime.js) and iOS runtimes already return 0, so the Go runtime must agree
// — otherwise the same program would 500 on the server while returning 0 on the
// client.
func TestIntDivideByZeroIsTotalZero(t *testing.T) {
	if got := runValue(t, "10 / 0"); got != "0" {
		t.Fatalf("10 / 0: got %s, want 0", got)
	}
	if got := runValue(t, "0 / 0"); got != "0" {
		t.Fatalf("0 / 0: got %s, want 0", got)
	}
}

// The arithmetic/comparison/logic builtins assume type-checked input, but a
// typechecker gap (or an untyped value arriving via JSON.decode) must surface
// as an error, not a panic that takes down the serving goroutine.
func TestOperatorBuiltinsErrorInsteadOfPanicking(t *testing.T) {
	binops := []struct {
		name string
		fn   func([]Value) (Value, error)
	}{
		{"-", subOp},
		{"*", mulOp},
		{"/", divOp},
		{"&&", andOp},
		{"||", orOp},
	}
	for _, op := range binops {
		t.Run(op.name, func(t *testing.T) {
			if _, err := op.fn([]Value{VInt{V: 1}, VBool{V: true}}); err == nil {
				t.Fatalf("%s(Int, Bool): want error, got nil", op.name)
			}
		})
	}

	if _, err := compareValues(VInt{V: 1}, VString{V: "x"}); err == nil {
		t.Fatalf("compareValues(Int, String): want error, got nil")
	}
}
