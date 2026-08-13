package runtime

import (
	"crypto/rand"
	"encoding/binary"
	"math/bits"
)

// Random: Elm-style generators with a PURE, seedable core.
//
// A `Generator a` is `Seed -> (a, Seed)`: a nativeFn(1) that takes a Seed and
// returns VTuple{value, nextSeed}. Because it threads an explicit Seed instead
// of drawing from the ambient RNG, randomness now RUNS AND REPLAYS identically
// on every runtime and on both sides (the server included): the property the
// games hand-rolled an LCG for, and the reason `Random` is a both-side module.
//
// PRNG: PCG-XSH-RR 32 with a fixed odd increment (the "oneseq" stream). The
// 64-bit state is carried, opaquely, as two 32-bit halves inside a VTuple:
// `Random.Seed` at the type level, so well-typed code can't take it apart, and
// no new Value/codec/eq is needed (a Seed serializes and compares as any tuple
// does, and the two halves each fit a JS Number exactly). The constants below
// live identically in runtime.js (BigInt) and Swift (UInt64); a conformance
// vector test pins Go = JS = Swift for a fixed seed.
//
// `Random.generate` stays: it seeds from OS entropy and steps, so its Cmd is
// still one fresh unpredictable draw: same observable behaviour as before.
const (
	pcgMul = 6364136223846793005 // PCG / MMIX LCG multiplier
	pcgInc = 1442695040888963407 // fixed odd increment (oneseq stream)
)

// pcgStep advances the 64-bit state and returns (newState, 32-bit output). The
// XSH-RR output permutation is computed from the CURRENT state (PCG convention:
// output the old state, then advance).
func pcgStep(state uint64) (uint64, uint32) {
	newState := state*pcgMul + pcgInc
	xorshifted := uint32(((state >> 18) ^ state) >> 27)
	rot := uint32(state >> 59)
	out := bits.RotateLeft32(xorshifted, -int(rot)) // rotate right by rot
	return newState, out
}

// seedState reads the 64-bit state out of a Seed value (a VTuple of two 32-bit
// halves). The type system guarantees a Seed here; a bad shape means a bug.
func seedState(v Value) uint64 {
	t, ok := v.(VTuple)
	if !ok || len(t.Members) != 2 {
		return 0
	}
	hi, _ := t.Members[0].(VInt)
	lo, _ := t.Members[1].(VInt)
	return uint64(uint32(hi.V))<<32 | uint64(uint32(lo.V))
}

// makeSeed packs a 64-bit state into a Seed value: two 32-bit halves as Ints,
// each in [0, 2^32) so they survive a JS Number without precision loss.
func makeSeed(state uint64) Value {
	return VTuple{Members: []Value{
		VInt{V: int64(state >> 32)},
		VInt{V: int64(state & 0xFFFFFFFF)},
	}}
}

// scramble turns an arbitrary Int into a well-mixed 64-bit state, so nearby
// seeds (1, 2, 3…) don't yield correlated first outputs.
func scramble(n int64) uint64 {
	s := uint64(n)*pcgMul + pcgInc
	s, _ = pcgStep(s)
	return s
}

// entropySeed draws 8 bytes of OS entropy and packs them into a Seed.
func entropySeed(where string) (Value, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, errEffect(where + ": OS entropy unavailable")
	}
	return makeSeed(binary.BigEndian.Uint64(b[:])), nil
}

func randomBuiltins() map[string]Value {
	// runGen applies a Generator to a Seed, unpacking (value, nextSeed).
	runGen := func(g Value, seed Value) (Value, Value, error) {
		r, err := apply(g, seed)
		if err != nil {
			return nil, nil, err
		}
		t, ok := r.(VTuple)
		if !ok || len(t.Members) != 2 {
			return nil, nil, errEffect("Random: generator did not return (value, seed)")
		}
		return t.Members[0], t.Members[1], nil
	}
	// asGen wraps a stepping function as a Generator (a nativeFn(1) over Seed).
	asGen := func(step func(seed Value) (Value, Value, error)) Value {
		return nativeFn(1, func(args []Value) (Value, error) {
			val, next, err := step(args[0])
			if err != nil {
				return nil, err
			}
			return VTuple{Members: []Value{val, next}}, nil
		})
	}

	return map[string]Value{
		// Random.initialSeed : Int -> Seed
		"randomInitialSeed": nativeFn(1, func(args []Value) (Value, error) {
			n, ok := args[0].(VInt)
			if !ok {
				return nil, errEffect("Random.initialSeed: expected Int")
			}
			return makeSeed(scramble(n.V)), nil
		}),

		// Random.step : Generator a -> Seed -> (a, Seed), pure, runs anywhere.
		"randomStep": nativeFn(2, func(args []Value) (Value, error) {
			val, next, err := runGen(args[0], args[1])
			if err != nil {
				return nil, err
			}
			return VTuple{Members: []Value{val, next}}, nil
		}),

		// Random.seed : Task Seed, real OS entropy as a Seed (both sides).
		"randomSeed": VEffect{
			Tag: "randomSeed",
			Run: func() (Value, error) { return entropySeed("Random.seed") },
		},

		// Random.generate : (a -> msg) -> Generator a -> Cmd msg, seeds from
		// entropy, steps once, delivers the value as a Msg.
		"randomGenerate": nativeFn(2, func(args []Value) (Value, error) {
			toMsg, g := args[0], args[1]
			return VEffect{
				Tag: "randomGenerate",
				Run: func() (Value, error) {
					seed, err := entropySeed("Random.generate")
					if err != nil {
						return nil, err
					}
					val, _, err := runGen(g, seed)
					if err != nil {
						return nil, err
					}
					return apply(toMsg, val)
				},
			}, nil
		}),

		// Random.int : Int -> Int -> Generator Int (inclusive range).
		// One 32-bit draw, so ranges up to ~2^32 are uniform: ample for games.
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
			return asGen(func(seed Value) (Value, Value, error) {
				newSt, out := pcgStep(seedState(seed))
				span := uint64(b-a) + 1 // unsigned distance; 0 == full 64-bit range
				var v int64
				if span == 0 {
					v = a + int64(out)
				} else {
					v = a + int64(uint64(out)%span)
				}
				return VInt{V: v}, makeSeed(newSt), nil
			}), nil
		}),

		// Random.constant : a -> Generator a (consumes no randomness).
		"randomConstant": nativeFn(1, func(args []Value) (Value, error) {
			v := args[0]
			return asGen(func(seed Value) (Value, Value, error) { return v, seed, nil }), nil
		}),

		// Random.uniform : a -> List a -> Generator a (first required → total).
		"randomUniform": nativeFn(2, func(args []Value) (Value, error) {
			rest, ok := args[1].(VList)
			if !ok {
				return nil, errEffect("Random.uniform: expected a list")
			}
			items := append([]Value{args[0]}, rest.Elements...)
			return asGen(func(seed Value) (Value, Value, error) {
				newSt, out := pcgStep(seedState(seed))
				idx := int(uint64(out) % uint64(len(items)))
				return items[idx], makeSeed(newSt), nil
			}), nil
		}),

		// Random.list : Int -> Generator a -> Generator (List a).
		"randomList": nativeFn(2, func(args []Value) (Value, error) {
			n, ok := args[0].(VInt)
			if !ok {
				return nil, errEffect("Random.list: expected Int")
			}
			g := args[1]
			return asGen(func(seed Value) (Value, Value, error) {
				count := int(n.V)
				if count < 0 {
					count = 0
				}
				out := make([]Value, count)
				cur := seed
				for i := 0; i < count; i++ {
					v, next, err := runGen(g, cur)
					if err != nil {
						return nil, nil, err
					}
					out[i], cur = v, next
				}
				return VList{Elements: out}, cur, nil
			}), nil
		}),

		// Random.pair : Generator a -> Generator b -> Generator (a, b).
		"randomPair": nativeFn(2, func(args []Value) (Value, error) {
			g1, g2 := args[0], args[1]
			return asGen(func(seed Value) (Value, Value, error) {
				v1, s1, err := runGen(g1, seed)
				if err != nil {
					return nil, nil, err
				}
				v2, s2, err := runGen(g2, s1)
				if err != nil {
					return nil, nil, err
				}
				return VTuple{Members: []Value{v1, v2}}, s2, nil
			}), nil
		}),

		// Random.map : (a -> b) -> Generator a -> Generator b.
		"randomMap": nativeFn(2, func(args []Value) (Value, error) {
			f, g := args[0], args[1]
			return asGen(func(seed Value) (Value, Value, error) {
				v, next, err := runGen(g, seed)
				if err != nil {
					return nil, nil, err
				}
				fv, err := apply(f, v)
				if err != nil {
					return nil, nil, err
				}
				return fv, next, nil
			}), nil
		}),

		// Random.map2 : (a -> b -> c) -> Generator a -> Generator b -> Generator c.
		"randomMap2": nativeFn(3, func(args []Value) (Value, error) {
			f, g1, g2 := args[0], args[1], args[2]
			return asGen(func(seed Value) (Value, Value, error) {
				v1, s1, err := runGen(g1, seed)
				if err != nil {
					return nil, nil, err
				}
				v2, s2, err := runGen(g2, s1)
				if err != nil {
					return nil, nil, err
				}
				fv, err := apply(f, v1)
				if err != nil {
					return nil, nil, err
				}
				fv, err = apply(fv, v2)
				if err != nil {
					return nil, nil, err
				}
				return fv, s2, nil
			}), nil
		}),

		// Random.map3 : (a -> b -> c -> d) -> ... -> Generator d.
		"randomMap3": nativeFn(4, func(args []Value) (Value, error) {
			f, g1, g2, g3 := args[0], args[1], args[2], args[3]
			return asGen(func(seed Value) (Value, Value, error) {
				v1, s1, err := runGen(g1, seed)
				if err != nil {
					return nil, nil, err
				}
				v2, s2, err := runGen(g2, s1)
				if err != nil {
					return nil, nil, err
				}
				v3, s3, err := runGen(g3, s2)
				if err != nil {
					return nil, nil, err
				}
				fv, err := apply(f, v1)
				if err != nil {
					return nil, nil, err
				}
				fv, err = apply(fv, v2)
				if err != nil {
					return nil, nil, err
				}
				fv, err = apply(fv, v3)
				if err != nil {
					return nil, nil, err
				}
				return fv, s3, nil
			}), nil
		}),

		// Random.andThen : (a -> Generator b) -> Generator a -> Generator b.
		"randomAndThen": nativeFn(2, func(args []Value) (Value, error) {
			f, g := args[0], args[1]
			return asGen(func(seed Value) (Value, Value, error) {
				v, s1, err := runGen(g, seed)
				if err != nil {
					return nil, nil, err
				}
				g2, err := apply(f, v)
				if err != nil {
					return nil, nil, err
				}
				return runGen(g2, s1)
			}), nil
		}),
	}
}
