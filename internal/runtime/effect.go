package runtime

// VEffect is a description of an effectful computation.
//
// An Effect is a value that, when interpreted by the runtime, produces
// either an error or a result. User code only constructs and combines
// Effects; the runtime is what executes them.
//
// This is an MVP shape. Real interpretation (HTTP, DB, etc.) will be added
// when those subsystems exist.
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
// Names mirror the planned mar-side API:
//
//	Effect.succeed : a -> Effect e a
//	Effect.fail    : e -> Effect e a
//	Effect.map     : (a -> b) -> Effect e a -> Effect e b
//	Effect.andThen : (a -> Effect e b) -> Effect e a -> Effect e b
//
// MVP: flat names (effectSucceed etc.) until module-qualified names are
// implemented.
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

// RunEffect is the entry point for executing an Effect from outside the
// language (e.g. from cmd/mar's `run` command). It interprets the effect
// description and returns a Result-like Value:
//
//	Ok value  | success
//	Err error | failure (the error encoded as a Value)
func RunEffect(v Value) (Value, error) {
	eff, ok := v.(VEffect)
	if !ok {
		// Not an effect: treat as already-resolved value.
		return v, nil
	}
	result, err := eff.Run()
	if err != nil {
		if ee, ok := err.(effectError); ok {
			return VCtor{Tag: "Err", Args: []Value{ee.value}}, nil
		}
		return nil, err
	}
	return VCtor{Tag: "Ok", Args: []Value{result}}, nil
}
