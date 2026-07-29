package runtime

import (
	"strings"
	"testing"
)

// Int used to mean three different things. Go wrapped around, Swift trapped,
// and JavaScript — where Int is a double — quietly stopped being able to tell
// 9007199254740993 from 9007199254740992. None of the three announced it, and
// the conformance corpus missed it because the gate covers which FUNCTIONS are
// exercised, not which VALUES.
//
// These pin the range itself, and the two doors a value can come in through
// without any arithmetic having happened.

func TestArithmeticRefusesToLeaveTheRange(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func() (Value, error)
	}{
		{"addition", func() (Value, error) { return addOp([]Value{VInt{V: MaxSafeInt}, VInt{V: 1}}) }},
		{"subtraction", func() (Value, error) { return subOp([]Value{VInt{V: MinSafeInt}, VInt{V: 1}}) }},
		// The product of two in-range operands reaches 2^106, which wraps an
		// int64 — so a range check on the result alone would read the wrapped
		// value as perfectly fine.
		{"multiplication", func() (Value, error) {
			return mulOp([]Value{VInt{V: 3037000500}, VInt{V: 3037000500}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, err := tc.run()
			if err == nil {
				t.Fatalf("leaving the range produced %s instead of an error", v.Display())
			}
			if !strings.Contains(err.Error(), "Int overflow") {
				t.Errorf("expected an overflow error, got: %v", err)
			}
		})
	}
}

func TestArithmeticAtTheEdgeStillAnswers(t *testing.T) {
	v, err := addOp([]Value{VInt{V: MaxSafeInt - 1}, VInt{V: 1}})
	if err != nil {
		t.Fatalf("the last representable Int was refused: %v", err)
	}
	if n, ok := v.(VInt); !ok || n.V != MaxSafeInt {
		t.Fatalf("got %s, want %d", v.Display(), MaxSafeInt)
	}
}

// A number too big to BE an Int is a parse failure, not an error: the type
// already says the text might not be a number.
func TestStringToIntIsNothingOutsideTheRange(t *testing.T) {
	fn, ok := BaseEnv().Lookup("String.toInt")
	if !ok {
		t.Fatal("String.toInt is not in the base environment")
	}
	for _, tc := range []struct{ in, want string }{
		{"9007199254740991", "Just 9007199254740991"},
		{"9007199254740992", "Nothing"},
		{"-9007199254740992", "Nothing"},
		{"99999999999999999999", "Nothing"},
	} {
		got, err := Apply(fn, VString{V: tc.in})
		if err != nil {
			t.Fatalf("String.toInt %q: %v", tc.in, err)
		}
		if got.Display() != tc.want {
			t.Errorf("String.toInt %q = %s, want %s", tc.in, got.Display(), tc.want)
		}
	}
}

// The database is 64 bits wide and Mar is 53, so a row can hold a number the
// language cannot represent — from data written before the bound existed, a
// restore from another tool, or an external 64-bit id. That is our own storage
// disagreeing with the language, so it raises.
func TestDatabaseIntegerOutOfRangeIsRefused(t *testing.T) {
	f := EntityField{Name: "count", SQLType: "INTEGER"}
	if _, err := decodeColumn(f, int64(MaxSafeInt)); err != nil {
		t.Fatalf("the largest representable Int was refused: %v", err)
	}
	_, err := decodeColumn(f, int64(MaxSafeInt)+1)
	if err == nil {
		t.Fatal("a 64-bit row value was accepted as an Int")
	}
	if !strings.Contains(err.Error(), "the database") {
		t.Errorf("the message should say where the value came from, got: %v", err)
	}
}

// A body is someone else's input, so an out-of-range integer is a malformed
// request rather than a broken program. The check lives in the decoder and the
// service dispatcher already answers 422 for any decode failure, so this
// verifies the decoder refuses at all — the status is covered by the service
// tests.
func TestWireIntegerOutOfRangeIsRefused(t *testing.T) {
	if _, err := decodeJSON(`{"n":9007199254740991}`); err != nil {
		t.Fatalf("the largest representable Int was refused: %v", err)
	}
	_, err := decodeJSON(`{"n":9007199254740993}`)
	if err == nil {
		t.Fatal("a wire integer beyond Int's range was accepted")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("the message should say where the value came from, got: %v", err)
	}
}
