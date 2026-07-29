package runtime

// Sub is the frontend subscription type (Sub msg). Like Task and Cmd, a Sub
// shares the runtime VEffect representation — the Task/Cmd/Sub split is purely
// at the type level (see types.go). The Go runtime never reconciles
// subscriptions: there is no MVU loop on the backend, and `subscriptions` is a
// frontend page field, evaluated client-side by the JS/Swift runtimes. These
// builtins exist so the builtin vocabulary stays uniform across runtimes (the
// drift parity tests) and so any incidental backend evaluation is inert rather
// than a crash. The real reconcile (start/stop timers, diff by structural key)
// lives in the JS and Swift runtimes.
//
// User-facing API (via the qualified-alias map in builtins.go):
//
//	Sub.none  : Sub msg                    -- subscribe to nothing
//	Sub.batch : List (Sub msg) -> Sub msg  -- combine subscriptions
//
// Time.every (a Time.* builtin that yields a Sub) lives in time.go.
func subBuiltins() map[string]Value {
	return map[string]Value{
		// Sub.none : Sub msg — the empty subscription (the do-nothing sub).
		"subNone": VEffect{
			Tag: "subNone",
			Run: func() (Value, error) { return VUnit{}, nil },
		},

		// Sub.batch : List (Sub msg) -> Sub msg — combine subscriptions.
		// Inert on the backend; the frontend flattens the list and reconciles
		// each item against the model.
		"subBatch": nativeFn(1, func(args []Value) (Value, error) {
			if _, ok := args[0].(VList); !ok {
				return nil, errEffect("subBatch: not a list")
			}
			return VEffect{
				Tag: "subBatch",
				Run: func() (Value, error) { return VUnit{}, nil },
			}, nil
		}),

		// Keyboard.watch : ({ down : List Keyboard.Key } -> msg) -> Sub msg — the
		// held-key mirror. Inert on the backend (no MVU loop); the frontend keeps
		// the current down-set from window keydown/keyup + blur and delivers the
		// whole set as a record on every change.
		"keyboardWatch": nativeFn(1, func(args []Value) (Value, error) {
			return VEffect{Tag: "keyboardWatch", Run: func() (Value, error) { return VUnit{}, nil }}, nil
		}),
		// Gamepad.watch : (pad -> msg) -> Sub msg — the full-pad mirror (sticks +
		// held buttons). Inert on the backend; the frontend polls the Gamepad API
		// per frame and delivers the snapshot on change. Web-first, like Keyboard.
		"gamepadWatch": nativeFn(1, func(args []Value) (Value, error) {
			return VEffect{Tag: "gamepadWatch", Run: func() (Value, error) { return VUnit{}, nil }}, nil
		}),
	}
}
