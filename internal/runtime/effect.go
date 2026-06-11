package runtime

// VEffect is a description of an effectful computation.
//
// An Effect is a value that, when interpreted by the runtime, produces
// either an error or a result. User code only constructs and combines
// Effects; the runtime is what executes them. Concrete effects (HTTP,
// DB, time) are built on top of this base shape.
type VEffect struct {
	// Run is invoked by the runtime when the effect is executed. It returns
	// either a Value (the success case) or an error (the failure case).
	//
	// Conceptually the effect's success/failure are encoded as Result Ok/Err
	// values when surfaced back to user code via Effect.toMsg.
	Run func() (Value, error)

	// Tag is a debug string indicating what kind of effect this is.
	Tag string
}

func (VEffect) isValue() {}
func (e VEffect) Display() string {
	if e.Tag == "" {
		return "<effect>"
	}
	return "<effect:" + e.Tag + ">"
}

// Pure returns an effect that always succeeds with v, without performing any
// real I/O.
func Pure(v Value) VEffect {
	return VEffect{
		Tag: "pure",
		Run: func() (Value, error) { return v, nil },
	}
}

// Fail returns an effect that always fails with err.
func Fail(err error) VEffect {
	return VEffect{
		Tag: "fail",
		Run: func() (Value, error) { return nil, err },
	}
}

// AndThen sequences two effects: run first, pass its result to f, run f's effect.
func AndThen(first VEffect, f func(Value) VEffect) VEffect {
	return VEffect{
		Tag: "andThen",
		Run: func() (Value, error) {
			v, err := first.Run()
			if err != nil {
				return nil, err
			}
			return f(v).Run()
		},
	}
}

// effectBuiltins returns the runtime functions for building/combining Effects.
//
// User-facing API (resolved through the qualified-alias mapping):
//
//	Effect.succeed : a -> Effect e a
//	Effect.fail    : e -> Effect e a
//	Effect.map     : (a -> b) -> Effect e a -> Effect e b
//	Effect.andThen : (a -> Effect e b) -> Effect e a -> Effect e b
//
// The flat keys (effectSucceed, etc.) are the internal binding names;
// qualified module syntax (`Effect.succeed`) resolves to them via the
// alias map in builtins.go.
func effectBuiltins() map[string]Value {
	return map[string]Value{
		"effectSucceed": nativeFn(1, func(args []Value) (Value, error) {
			return Pure(args[0]), nil
		}),
		"effectFail": nativeFn(1, func(args []Value) (Value, error) {
			v := args[0]
			return Fail(effectError{value: v}), nil
		}),
		"effectMap": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			eff, ok := args[1].(VEffect)
			if !ok {
				return nil, errEffect("effectMap: not an Effect")
			}
			return AndThen(eff, func(v Value) VEffect {
				out, err := apply(fn, v)
				if err != nil {
					return Fail(err)
				}
				return Pure(out)
			}), nil
		}),
		"effectAndThen": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			eff, ok := args[1].(VEffect)
			if !ok {
				return nil, errEffect("effectAndThen: not an Effect")
			}
			return AndThen(eff, func(v Value) VEffect {
				out, err := apply(fn, v)
				if err != nil {
					return Fail(err)
				}
				next, ok := out.(VEffect)
				if !ok {
					return Fail(errEffect("effectAndThen: function did not return an Effect"))
				}
				return next
			}), nil
		}),

		// Effect.forEach : (a -> Effect e ()) -> List a -> Effect e ()
		"effectForEach": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			list, ok := args[1].(VList)
			if !ok {
				return nil, errEffect("effectForEach: not a list")
			}
			return VEffect{
				Tag: "forEach",
				Run: func() (Value, error) {
					for _, e := range list.Elements {
						effVal, err := apply(fn, e)
						if err != nil {
							return nil, err
						}
						eff, ok := effVal.(VEffect)
						if !ok {
							return nil, errEffect("effectForEach: function did not return an Effect")
						}
						if _, err := eff.Run(); err != nil {
							return nil, err
						}
					}
					return VUnit{}, nil
				},
			}, nil
		}),

		// Effect.none : Effect e a — succeeds with unit (or rather, unit value).
		"effectNone": VEffect{
			Tag: "none",
			Run: func() (Value, error) { return VUnit{}, nil },
		},

		// Effect.batch : List (Effect e msg) -> Effect e msg
		// Fire-and-forget fan-out: runs every child effect; each child
		// delivers through its own toMsg (frontend) or performs its
		// side effects (backend). Produces unit as its own value, the
		// same dynamic shape Effect.none uses for an `Effect e a`.
		"effectBatch": nativeFn(1, func(args []Value) (Value, error) {
			list, ok := args[0].(VList)
			if !ok {
				return nil, errEffect("effectBatch: not a list")
			}
			return VEffect{
				Tag: "batch",
				Run: func() (Value, error) {
					for _, e := range list.Elements {
						eff, ok := e.(VEffect)
						if !ok {
							return nil, errEffect("effectBatch: list element is not an Effect")
						}
						if _, err := eff.Run(); err != nil {
							return nil, err
						}
					}
					return VUnit{}, nil
				},
			}, nil
		}),

		// Effect.sequence : List (Effect e a) -> Effect e (List a)
		"effectSequence": nativeFn(1, func(args []Value) (Value, error) {
			list, ok := args[0].(VList)
			if !ok {
				return nil, errEffect("effectSequence: not a list")
			}
			return VEffect{
				Tag: "sequence",
				Run: func() (Value, error) {
					out := make([]Value, len(list.Elements))
					for i, e := range list.Elements {
						eff, ok := e.(VEffect)
						if !ok {
							return nil, errEffect("effectSequence: list element is not an Effect")
						}
						v, err := eff.Run()
						if err != nil {
							return nil, err
						}
						out[i] = v
					}
					return VList{Elements: out}, nil
				},
			}, nil
		}),
	}
}

// effectError carries a Value (the user-level error) through Go's error type.
type effectError struct{ value Value }

func (e effectError) Error() string {
	return "effect error: " + e.value.Display()
}

func errEffect(msg string) error {
	return effectError{value: VString{V: msg}}
}

// serviceErrorString folds a Service.Error value to its default display
// string: plain English for the framework-built cases, the server's own
// message for ServerError. Apps that want different copy, or per-case
// behavior (retry on Offline, redirect on Unauthorized), match the union.
func serviceErrorString(v Value) string {
	c, ok := v.(VCtor)
	if !ok {
		return v.Display()
	}
	switch c.Tag {
	case "Offline":
		return "Can't reach the server. Check your connection and try again."
	case "Unauthorized":
		return "Your session has expired. Please sign in again."
	case "ServerError":
		if len(c.Args) == 1 {
			if s, ok := c.Args[0].(VString); ok {
				return s.V
			}
			return c.Args[0].Display()
		}
	}
	return c.Tag
}
