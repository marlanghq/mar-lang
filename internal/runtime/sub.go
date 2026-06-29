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
	}
}
