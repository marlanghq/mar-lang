package runtime

import (
	"fmt"
	"strings"
	"unicode"
)

// charBuiltins returns runtime functions for the Char module plus the
// String <-> [Char] bridges (toList / fromList / cons). Mirrors the
// JS runtime and Swift's MarChar.swift; the drift tests catch any
// divergence at the env.define level.
//
// Char in Mar is a Unicode code point (rune), the same model used by
// Elm / Go / Rust / JS / Swift's `Unicode.Scalar`. NOT a grapheme
// cluster — `String.toList "🇧🇷"` yields TWO Chars (the two regional
// indicator scalars), matching Elm semantics.
//
// charFromCode normalizes invalid inputs (negative, > 0x10FFFF, or
// surrogate range D800-DFFF) to U+FFFD ("replacement character"),
// keeping the three runtimes able to represent every result.
func charBuiltins() map[string]Value {
	return map[string]Value{
		"charToCode": nativeFn(1, func(args []Value) (Value, error) {
			c, ok := args[0].(VChar)
			if !ok {
				return nil, fmt.Errorf("Char.toCode: expected Char")
			}
			return VInt{V: int64(c.V)}, nil
		}),
		"charFromCode": nativeFn(1, func(args []Value) (Value, error) {
			n, ok := args[0].(VInt)
			if !ok {
				return nil, fmt.Errorf("Char.fromCode: expected Int")
			}
			return VChar{V: sanitizeCodePoint(n.V)}, nil
		}),
		// Predicates — operate on the Unicode property of the rune.
		// unicode.IsDigit etc. cover full Unicode, not just ASCII —
		// matches Elm's intent (Char.isDigit '٥' is True for Arabic-
		// Indic digit five).
		"charIsDigit": nativeFn(1, func(args []Value) (Value, error) {
			c, ok := args[0].(VChar)
			if !ok {
				return nil, fmt.Errorf("Char.isDigit: expected Char")
			}
			return VBool{V: unicode.IsDigit(c.V)}, nil
		}),
		"charIsAlpha": nativeFn(1, func(args []Value) (Value, error) {
			c, ok := args[0].(VChar)
			if !ok {
				return nil, fmt.Errorf("Char.isAlpha: expected Char")
			}
			return VBool{V: unicode.IsLetter(c.V)}, nil
		}),
		"charIsUpper": nativeFn(1, func(args []Value) (Value, error) {
			c, ok := args[0].(VChar)
			if !ok {
				return nil, fmt.Errorf("Char.isUpper: expected Char")
			}
			return VBool{V: unicode.IsUpper(c.V)}, nil
		}),
		"charIsLower": nativeFn(1, func(args []Value) (Value, error) {
			c, ok := args[0].(VChar)
			if !ok {
				return nil, fmt.Errorf("Char.isLower: expected Char")
			}
			return VBool{V: unicode.IsLower(c.V)}, nil
		}),
		"charToUpper": nativeFn(1, func(args []Value) (Value, error) {
			c, ok := args[0].(VChar)
			if !ok {
				return nil, fmt.Errorf("Char.toUpper: expected Char")
			}
			return VChar{V: unicode.ToUpper(c.V)}, nil
		}),
		"charToLower": nativeFn(1, func(args []Value) (Value, error) {
			c, ok := args[0].(VChar)
			if !ok {
				return nil, fmt.Errorf("Char.toLower: expected Char")
			}
			return VChar{V: unicode.ToLower(c.V)}, nil
		}),

		// String <-> [Char] bridges. Iterating a Go string with
		// `range` yields runes (UTF-8 decoded), which is what we want.
		"stringToList": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("String.toList: expected String")
			}
			out := make([]Value, 0, len(s.V))
			for _, r := range s.V {
				out = append(out, VChar{V: r})
			}
			return VList{Elements: out}, nil
		}),
		"stringFromList": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("String.fromList: expected List")
			}
			runes := make([]rune, 0, len(l.Elements))
			for _, e := range l.Elements {
				c, ok := e.(VChar)
				if !ok {
					return nil, fmt.Errorf("String.fromList: element not Char")
				}
				runes = append(runes, c.V)
			}
			return VString{V: string(runes)}, nil
		}),
		// String.cons : Char -> String -> String
		// Cheap Elm convenience; equivalent to `String.fromChar c ++ s`
		// (which we don't have today, so this is the lightest path).
		"stringCons": nativeFn(2, func(args []Value) (Value, error) {
			c, ok1 := args[0].(VChar)
			s, ok2 := args[1].(VString)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("String.cons: expected Char, String")
			}
			return VString{V: string(c.V) + s.V}, nil
		}),
		// String.map : (Char -> Char) -> String -> String
		// Apply fn to every Char, build a new String. Iterating with
		// `range` over a Go string yields runes (Unicode code points)
		// — same model as the JS for..of and Swift's
		// .unicodeScalars iteration. Multi-byte chars stay intact.
		"stringMap": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			s, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("String.map: second arg not a String")
			}
			var sb strings.Builder
			for _, r := range s.V {
				v, err := apply(fn, VChar{V: r})
				if err != nil {
					return nil, err
				}
				c, ok := v.(VChar)
				if !ok {
					return nil, fmt.Errorf("String.map: function didn't return Char")
				}
				sb.WriteRune(c.V)
			}
			return VString{V: sb.String()}, nil
		}),
		// String.filter : (Char -> Bool) -> String -> String
		"stringFilter": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			s, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("String.filter: second arg not a String")
			}
			var sb strings.Builder
			for _, r := range s.V {
				v, err := apply(fn, VChar{V: r})
				if err != nil {
					return nil, err
				}
				b, ok := v.(VBool)
				if !ok {
					return nil, fmt.Errorf("String.filter: predicate didn't return Bool")
				}
				if b.V {
					sb.WriteRune(r)
				}
			}
			return VString{V: sb.String()}, nil
		}),
		// String.foldl : (Char -> b -> b) -> b -> String -> b
		// Walks chars left-to-right. Combine fn takes Char first,
		// accumulator second — matches the Elm signature exactly so
		// `String.foldl (\c acc -> ...) seed s` reads naturally.
		"stringFoldl": nativeFn(3, func(args []Value) (Value, error) {
			fn := args[0]
			acc := args[1]
			s, ok := args[2].(VString)
			if !ok {
				return nil, fmt.Errorf("String.foldl: third arg not a String")
			}
			for _, r := range s.V {
				partial, err := apply(fn, VChar{V: r})
				if err != nil {
					return nil, err
				}
				next, err := apply(partial, acc)
				if err != nil {
					return nil, err
				}
				acc = next
			}
			return acc, nil
		}),
		// String.any : (Char -> Bool) -> String -> Bool — short-circuit.
		"stringAny": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			s, ok := args[1].(VString)
			if !ok {
				return nil, fmt.Errorf("String.any: second arg not a String")
			}
			for _, r := range s.V {
				v, err := apply(fn, VChar{V: r})
				if err != nil {
					return nil, err
				}
				b, ok := v.(VBool)
				if !ok {
					return nil, fmt.Errorf("String.any: predicate didn't return Bool")
				}
				if b.V {
					return VBool{V: true}, nil
				}
			}
			return VBool{V: false}, nil
		}),
	}
}

// sanitizeCodePoint clamps an arbitrary Int to a representable
// Unicode scalar value. Out-of-range or surrogate inputs collapse to
// U+FFFD (REPLACEMENT CHARACTER). This is the contract guaranteed
// across all three runtimes — Swift's Unicode.Scalar rejects
// surrogates outright, so we substitute up front rather than have
// JSON encode fail later.
func sanitizeCodePoint(n int64) rune {
	if n < 0 || n > 0x10FFFF {
		return '�'
	}
	if n >= 0xD800 && n <= 0xDFFF {
		return '�'
	}
	return rune(n)
}
