package typecheck

// keyboardKeyCodes is the list of DOM `event.code` values for a typical US
// keyboard. The Keyboard.Key union's constructors mirror these one-to-one
// (Keyboard.<code>), so `case key of Keyboard.KeyW -> ...` is exhaustiveness-
// checked and a typo like `Keyboard.Keyy` is a COMPILE error rather than a
// silent no-op. Keys are PHYSICAL (layout-independent): the physical W key is
// `KeyW` on any layout, so WASD works everywhere; the character actually typed
// is a separate, open-ended concern we don't model here.
//
// This list is the source of truth for the union in typecheck. The JS runtime
// builds a Key value straight from `event.code` (it needs no copy of the list);
// a code outside this set simply won't match any constructor, so it falls to
// the user's `_` branch. Add codes here when a game needs them.
var keyboardKeyCodes = []string{
	// letters
	"KeyA", "KeyB", "KeyC", "KeyD", "KeyE", "KeyF", "KeyG", "KeyH", "KeyI",
	"KeyJ", "KeyK", "KeyL", "KeyM", "KeyN", "KeyO", "KeyP", "KeyQ", "KeyR",
	"KeyS", "KeyT", "KeyU", "KeyV", "KeyW", "KeyX", "KeyY", "KeyZ",
	// top-row digits
	"Digit0", "Digit1", "Digit2", "Digit3", "Digit4",
	"Digit5", "Digit6", "Digit7", "Digit8", "Digit9",
	// function row
	"F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12",
	// arrows
	"ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight",
	// whitespace / editing / navigation
	"Space", "Enter", "Tab", "Backspace", "Escape", "Delete", "Insert",
	"Home", "End", "PageUp", "PageDown",
	// modifiers (left/right are distinct physical keys)
	"ShiftLeft", "ShiftRight", "ControlLeft", "ControlRight",
	"AltLeft", "AltRight", "MetaLeft", "MetaRight",
	// locks / system
	"CapsLock", "NumLock", "ScrollLock", "PrintScreen", "Pause", "ContextMenu",
	// US punctuation
	"Backquote", "Minus", "Equal", "BracketLeft", "BracketRight", "Backslash",
	"Semicolon", "Quote", "Comma", "Period", "Slash",
	// numeric keypad
	"Numpad0", "Numpad1", "Numpad2", "Numpad3", "Numpad4",
	"Numpad5", "Numpad6", "Numpad7", "Numpad8", "Numpad9",
	"NumpadAdd", "NumpadSubtract", "NumpadMultiply", "NumpadDivide",
	"NumpadDecimal", "NumpadEnter",
}

// keyboardKeyCustomType is the Keyboard.Key union registration used for
// exhaustiveness checking: each code becomes a nullary constructor whose
// result is Keyboard.Key.
func keyboardKeyCustomType() CustomType {
	ctors := make(map[string]CustomCtor, len(keyboardKeyCodes))
	for _, code := range keyboardKeyCodes {
		ctors[code] = CustomCtor{Result: TKeyboardKey}
	}
	order := make([]string, len(keyboardKeyCodes))
	copy(order, keyboardKeyCodes)
	return CustomType{
		Name:         "Keyboard.Key",
		Params:       nil,
		Constructors: ctors,
		CtorOrder:    order,
		Module:       "Keyboard",
	}
}

// keyboardKeyBindings is the value-env half: each constructor is a nullary
// value of type Keyboard.Key, bound under its qualified name (Keyboard.KeyW),
// mirroring how Service.Offline etc. are bound. Merged into baseBindings.
func keyboardKeyBindings() map[string]Type {
	out := make(map[string]Type, len(keyboardKeyCodes))
	for _, code := range keyboardKeyCodes {
		out["Keyboard."+code] = TKeyboardKey
	}
	return out
}
