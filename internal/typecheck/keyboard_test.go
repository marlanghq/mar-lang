package typecheck

import "testing"

// Keyboard.Key is a builtin union whose ~100 constructors mirror the DOM
// event.code set. These lock in the two things that matter: valid key patterns
// compile, and a code that is NOT in the enum is a COMPILE error rather than a
// silent no-op: the whole reason Key is an enum instead of a raw String.

func TestKeyboardKeySubscriptionCompiles(t *testing.T) {
	src := `module M exposing (..)

type Msg = KeysChanged { down : List Keyboard.Key }

toDir : Keyboard.Key -> Int
toDir key =
    case key of
        Keyboard.ArrowUp -> 1
        Keyboard.KeyW -> 1
        Keyboard.Space -> 0
        _ -> 0

subs : Sub Msg
subs =
    Keyboard.watch KeysChanged
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("valid Keyboard usage should compile, got: %v", err)
	}
}

func TestKeyboardUnknownKeyRejected(t *testing.T) {
	// KeyQQ is not a real event.code. Matching it must be rejected: that
	// compile error is exactly what the enum buys over a stringly-typed key.
	src := `module M exposing (..)

toDir : Keyboard.Key -> Int
toDir key =
    case key of
        Keyboard.KeyQQ -> 1
        _ -> 0
`
	if _, err := checkSource(t, src); err == nil {
		t.Fatal("matching a non-existent key constructor (Keyboard.KeyQQ) should be rejected, but it compiled")
	}
}

func TestKeyboardKeyBindingsMatchCodes(t *testing.T) {
	b := keyboardKeyBindings()
	if len(b) != len(keyboardKeyCodes) {
		t.Fatalf("keyboardKeyBindings has %d entries, want %d (one per code)", len(b), len(keyboardKeyCodes))
	}
	for _, code := range []string{"KeyW", "ArrowUp", "Space", "F1", "Numpad0"} {
		if _, ok := b["Keyboard."+code]; !ok {
			t.Fatalf("expected a Keyboard.%s binding", code)
		}
	}
	ct := keyboardKeyCustomType()
	if ct.Name != "Keyboard.Key" {
		t.Fatalf("custom type name = %q, want Keyboard.Key", ct.Name)
	}
	if len(ct.Constructors) != len(keyboardKeyCodes) {
		t.Fatalf("custom type has %d constructors, want %d", len(ct.Constructors), len(keyboardKeyCodes))
	}
}
