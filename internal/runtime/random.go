package runtime

import "math/rand"

// Random — Elm-style generators. A `Generator a` is represented at runtime as a
// unit-thunk VFn (a nativeFn(1) that ignores its argument): applying it to unit
// produces one fresh random value from the platform RNG. The type system keeps
// `Generator a` distinct from `() -> a`, and only Random.generate (a Cmd) ever
// runs it — so the impurity stays behind the opaque type. This mirrors how
// Task/Cmd/Sub reuse one runtime representation behind distinct types.
//
// No Seed/step API: generators draw from the ambient RNG, not a threaded seed.
// Randomness is per-runtime; cross-runtime value parity is neither expected nor
// testable. (math/rand's global source is auto-seeded since Go 1.20.)
func randomBuiltins() map[string]Value {
	// runGen produces one value from a generator thunk.
	runGen := func(g Value) (Value, error) { return apply(g, VUnit{}) }
	// asGen wraps a producer as a Generator (a unit-thunk ignoring its arg).
	asGen := func(produce func() (Value, error)) Value {
		return nativeFn(1, func(_ []Value) (Value, error) { return produce() })
	}

	return map[string]Value{
		// Random.generate : (a -> msg) -> Generator a -> Cmd msg
		// Runs the generator and delivers the value as a Msg. On the frontend
		// the MVU runner dispatches it; on the backend (no loop) it just maps —
		// same shape as cmdPerform.
		"randomGenerate": nativeFn(2, func(args []Value) (Value, error) {
			toMsg, g := args[0], args[1]
			return VEffect{
				Tag: "randomGenerate",
				Run: func() (Value, error) {
					v, err := runGen(g)
					if err != nil {
						return nil, err
					}
					return apply(toMsg, v)
				},
			}, nil
		}),

		// Random.int : Int -> Int -> Generator Int (inclusive range)
		"randomInt": nativeFn(2, func(args []Value) (Value, error) {
			lo, ok1 := args[0].(VInt)
			hi, ok2 := args[1].(VInt)
			if !ok1 || !ok2 {
				return nil, errEffect("Random.int: expected (Int, Int)")
			}
			a, b := lo.V, hi.V
			if a > b {
				a, b = b, a
			}
			return asGen(func() (Value, error) {
				return VInt{V: a + rand.Int63n(b-a+1)}, nil
			}), nil
		}),

		// Random.constant : a -> Generator a
		"randomConstant": nativeFn(1, func(args []Value) (Value, error) {
			v := args[0]
			return asGen(func() (Value, error) { return v, nil }), nil
		}),

		// Random.uniform : a -> List a -> Generator a (first is required so the
		// generator is total — there is no empty case to crash on).
		"randomUniform": nativeFn(2, func(args []Value) (Value, error) {
			rest, ok := args[1].(VList)
			if !ok {
				return nil, errEffect("Random.uniform: expected a list")
			}
			items := append([]Value{args[0]}, rest.Elements...)
			return asGen(func() (Value, error) {
				return items[rand.Intn(len(items))], nil
			}), nil
		}),

		// Random.list : Int -> Generator a -> Generator (List a)
		"randomList": nativeFn(2, func(args []Value) (Value, error) {
			n, ok := args[0].(VInt)
			if !ok {
				return nil, errEffect("Random.list: expected Int")
			}
			g := args[1]
			return asGen(func() (Value, error) {
				count := int(n.V)
				if count < 0 {
					count = 0
				}
				out := make([]Value, count)
				for i := 0; i < count; i++ {
					v, err := runGen(g)
					if err != nil {
						return nil, err
					}
					out[i] = v
				}
				return VList{Elements: out}, nil
			}), nil
		}),

		// Random.pair : Generator a -> Generator b -> Generator (a, b)
		"randomPair": nativeFn(2, func(args []Value) (Value, error) {
			g1, g2 := args[0], args[1]
			return asGen(func() (Value, error) {
				v1, err := runGen(g1)
				if err != nil {
					return nil, err
				}
				v2, err := runGen(g2)
				if err != nil {
					return nil, err
				}
				return VTuple{Members: []Value{v1, v2}}, nil
			}), nil
		}),

		// Random.map : (a -> b) -> Generator a -> Generator b
		"randomMap": nativeFn(2, func(args []Value) (Value, error) {
			f, g := args[0], args[1]
			return asGen(func() (Value, error) {
				v, err := runGen(g)
				if err != nil {
					return nil, err
				}
				return apply(f, v)
			}), nil
		}),

		// Random.map2 : (a -> b -> c) -> Generator a -> Generator b -> Generator c
		"randomMap2": nativeFn(3, func(args []Value) (Value, error) {
			f, g1, g2 := args[0], args[1], args[2]
			return asGen(func() (Value, error) {
				v1, err := runGen(g1)
				if err != nil {
					return nil, err
				}
				v2, err := runGen(g2)
				if err != nil {
					return nil, err
				}
				fv, err := apply(f, v1)
				if err != nil {
					return nil, err
				}
				return apply(fv, v2)
			}), nil
		}),

		// Random.map3 : (a -> b -> c -> d) -> Generator a -> Generator b -> Generator c -> Generator d
		"randomMap3": nativeFn(4, func(args []Value) (Value, error) {
			f, g1, g2, g3 := args[0], args[1], args[2], args[3]
			return asGen(func() (Value, error) {
				v1, err := runGen(g1)
				if err != nil {
					return nil, err
				}
				v2, err := runGen(g2)
				if err != nil {
					return nil, err
				}
				v3, err := runGen(g3)
				if err != nil {
					return nil, err
				}
				fv, err := apply(f, v1)
				if err != nil {
					return nil, err
				}
				fv, err = apply(fv, v2)
				if err != nil {
					return nil, err
				}
				return apply(fv, v3)
			}), nil
		}),

		// Random.andThen : (a -> Generator b) -> Generator a -> Generator b
		"randomAndThen": nativeFn(2, func(args []Value) (Value, error) {
			f, g := args[0], args[1]
			return asGen(func() (Value, error) {
				v, err := runGen(g)
				if err != nil {
					return nil, err
				}
				g2, err := apply(f, v)
				if err != nil {
					return nil, err
				}
				return runGen(g2)
			}), nil
		}),
	}
}
