package runtime

import (
	"strings"
	"testing"
)

// `JSON.encode : a -> String` takes anything, and the encoder has a case for
// the values that have a JSON form. What reaches the bottom of that switch is
// a value a RUNTIME built for itself — a Page, a View, an Entity — and the
// message used to be `JSON.encode: unsupported runtime.VPage`: a Go type name
// handed to someone who wrote Mar.
//
// The tier cannot move without the API giving up `a`, which is a language
// decision and not a fix. What can move is the vocabulary.
func TestEncodeNamesTheMarKindItCannotEncode(t *testing.T) {
	cases := []struct {
		name string
		v    Value
		want string
	}{
		{"a page", VPage{Path: "/"}, "a Page"},
		{"an entity", VEntity{Table: "notes"}, "an Entity"},
		{"a function", VFn{Arity: 1}, "a function"},
		{"a command", VEffect{Tag: "x"}, "a Cmd or Task"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := encodeValue(tc.v)
			if err == nil {
				t.Fatal("this has no JSON form and encoding it should say so")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the message should name the Mar kind %q, got: %v", tc.want, err)
			}
			if strings.Contains(err.Error(), "runtime.V") {
				t.Errorf("the message names a Go type: %v", err)
			}
			if !strings.Contains(err.Error(), "built FROM") {
				t.Errorf("the message should say what to do instead, got: %v", err)
			}
		})
	}

	// And what does have a form still encodes, or the switch above it broke.
	t.Run("an ordinary record still encodes", func(t *testing.T) {
		got, err := encodeValue(VRecord{
			Fields: map[string]Value{"a": VInt{V: 1}},
			Order:  []string{"a"},
		})
		if err != nil {
			t.Fatalf("a record is exactly what JSON.encode is for: %v", err)
		}
		if got != `{"a":1}` {
			t.Errorf("got %s", got)
		}
	})
}
