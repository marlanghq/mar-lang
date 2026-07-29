package runtime

// Device (docs/proposals/device.md) reports live, truthful device capabilities
// to a frontend — the precision of the primary pointer, whether it can hover,
// the viewport size, and the dark / reduced-motion preferences — all read from
// CSS media queries in the JS runtime (never a user-agent string, which iPadOS
// lies about). These Go builtins are inert like the Sound ones: Device.watch is
// a CLIENT subscription the server never fires, and the pure helpers are defined
// for completeness so the builtin vocabulary stays uniform across runtimes (the
// drift parity tests). The touchOnly / canHover logic still mirrors the JS
// semantics exactly, so a server-side eval that ever reached them is correct
// rather than a silent stub.
func deviceBuiltins() map[string]Value {
	// deviceBool reads a boolean field off a Device record, defaulting to false
	// for anything that isn't the expected record (the server never builds one).
	deviceBool := func(v Value, field string) bool {
		if r, ok := v.(VRecord); ok {
			if bv, ok := r.Fields[field].(VBool); ok {
				return bv.V
			}
		}
		return false
	}
	return map[string]Value{
		// Pointer constructors — global (like Order's LT / Method's GET), nullary.
		// The runtime tag is the bare ctor name, matching how the JS runtime
		// builds the record from `(pointer: coarse)` / `(pointer: fine)`.
		"Coarse": VCtor{Tag: "Coarse"},
		"Fine":   VCtor{Tag: "Fine"},

		// Device.watch : (Device -> msg) -> Sub msg. The real matchMedia +
		// resize wiring lives in the JS runtime; inert here (a Sub the server
		// never fires), like Sound.loop / Sound.once.
		"deviceWatch": nativeFn(1, func(args []Value) (Value, error) {
			return VEffect{Tag: "deviceWatch", Run: func() (Value, error) { return VUnit{}, nil }}, nil
		}),

		// Device.touchOnly : coarse primary pointer, nothing fine attached, no
		// hover — finger-only hardware (iPhone/iPad yes; iPad+trackpad no).
		"deviceTouchOnly": nativeFn(1, func(args []Value) (Value, error) {
			coarse := false
			if r, ok := args[0].(VRecord); ok {
				if c, ok := r.Fields["pointer"].(VCtor); ok {
					coarse = c.Tag == "Coarse"
				}
			}
			return VBool{V: coarse && !deviceBool(args[0], "anyFine") && !deviceBool(args[0], "supportsHover")}, nil
		}),
		// Device.canHover : the primary input has a real hover story.
		"deviceCanHover": nativeFn(1, func(args []Value) (Value, error) {
			return VBool{V: deviceBool(args[0], "supportsHover")}, nil
		}),
	}
}
