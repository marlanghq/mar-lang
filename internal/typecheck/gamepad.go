package typecheck

// gamepadButtons is the W3C "standard gamepad" button set. The Gamepad.Button
// union's constructors mirror these one-to-one (Gamepad.<name>), so
// `case btn of Gamepad.A -> ...` is exhaustiveness-checked and a typo is a
// COMPILE error rather than a silent no-op. The JS runtime polls
// navigator.getGamepads() and builds a Button from the standard button index
// (see runtime.js): a controller outside the standard mapping simply won't
// match, falling to the user's `_` branch.
//
// Names are the common console labels; the index → name mapping lives in the
// JS runtime (the source of truth for the layout). This list is the source of
// truth for the union in typecheck. Add names here when a game needs them.
var gamepadButtons = []string{
	// face buttons
	"A", "B", "X", "Y",
	// shoulders / triggers
	"L1", "R1", "L2", "R2",
	// centre + stick clicks
	"Select", "Start", "L3", "R3",
	// d-pad
	"Up", "Down", "Left", "Right",
}

// gamepadButtonCustomType is the Gamepad.Button union registration used for
// exhaustiveness checking: each name becomes a nullary constructor whose result
// is Gamepad.Button. Mirrors keyboardKeyCustomType.
func gamepadButtonCustomType() CustomType {
	ctors := make(map[string]CustomCtor, len(gamepadButtons))
	for _, b := range gamepadButtons {
		ctors[b] = CustomCtor{Result: TGamepadButton}
	}
	order := make([]string, len(gamepadButtons))
	copy(order, gamepadButtons)
	return CustomType{
		Name:         "Gamepad.Button",
		Params:       nil,
		Constructors: ctors,
		CtorOrder:    order,
		Module:       "Gamepad",
	}
}

// gamepadButtonBindings is the value-env half: each constructor is a nullary
// value of type Gamepad.Button bound under its qualified name (Gamepad.A),
// mirroring keyboardKeyBindings / Service.Offline. Merged into baseBindings.
func gamepadButtonBindings() map[string]Type {
	out := make(map[string]Type, len(gamepadButtons))
	for _, b := range gamepadButtons {
		out["Gamepad."+b] = TGamepadButton
	}
	return out
}
