// Char module + String <-> [Char] bridges — Swift port of the Go
// internal/runtime/char.go and the JS builtins in runtime.js.
//
// Char in Mar is a Unicode code point (here: Unicode.Scalar). NOT a
// grapheme cluster. `String.toList "🇧🇷"` yields TWO chars, matching
// Elm semantics — the regional indicators are two separate scalars.
//
// padLeft / padRight live here too because they now take a Char
// (Elm-style) rather than a String — co-locating with the rest of
// the Char-flavored stdlib makes the dependency explicit.

import Foundation

enum MarChar {
    static func register(_ env: Env) {
        // Char.toCode / fromCode
        let toCode = MarFn.native(1) { args in
            guard case .char(let c) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Char", got: Eval.typeOf(args[0]))
            }
            return .int(Int(c.value))
        }
        env.define("charToCode",  .fn(toCode))
        env.define("Char.toCode", .fn(toCode))

        let fromCode = MarFn.native(1) { args in
            guard case .int(let n) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Int", got: Eval.typeOf(args[0]))
            }
            return .char(MarJSONCodec.scalarFromCode(n))
        }
        env.define("charFromCode",  .fn(fromCode))
        env.define("Char.fromCode", .fn(fromCode))

        // Predicates — Unicode-aware via the scalar's properties. The
        // CharacterSet checks read the .value directly so they line up
        // with the Go side's `unicode.IsDigit` / `unicode.IsLetter`.
        let isDigit = MarFn.native(1) { args in
            guard case .char(let c) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Char", got: Eval.typeOf(args[0]))
            }
            return .bool(CharacterSet.decimalDigits.contains(c))
        }
        env.define("charIsDigit",  .fn(isDigit))
        env.define("Char.isDigit", .fn(isDigit))

        let isAlpha = MarFn.native(1) { args in
            guard case .char(let c) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Char", got: Eval.typeOf(args[0]))
            }
            return .bool(CharacterSet.letters.contains(c))
        }
        env.define("charIsAlpha",  .fn(isAlpha))
        env.define("Char.isAlpha", .fn(isAlpha))

        let isUpper = MarFn.native(1) { args in
            guard case .char(let c) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Char", got: Eval.typeOf(args[0]))
            }
            return .bool(CharacterSet.uppercaseLetters.contains(c))
        }
        env.define("charIsUpper",  .fn(isUpper))
        env.define("Char.isUpper", .fn(isUpper))

        let isLower = MarFn.native(1) { args in
            guard case .char(let c) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Char", got: Eval.typeOf(args[0]))
            }
            return .bool(CharacterSet.lowercaseLetters.contains(c))
        }
        env.define("charIsLower",  .fn(isLower))
        env.define("Char.isLower", .fn(isLower))

        // toUpper / toLower — uppercase/lowercase via String, then take
        // the first scalar of the result. Some locales (Turkish, e.g.)
        // can multi-scalar a single char (`İ` lowercases to `i` +
        // combining mark); taking the first scalar keeps the type
        // honest at the cost of being slightly approximate in those
        // rare cases. Matches the JS runtime's behavior.
        let toUpper = MarFn.native(1) { args in
            guard case .char(let c) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Char", got: Eval.typeOf(args[0]))
            }
            let up = String(c).uppercased()
            return .char(up.unicodeScalars.first ?? c)
        }
        env.define("charToUpper",  .fn(toUpper))
        env.define("Char.toUpper", .fn(toUpper))

        let toLower = MarFn.native(1) { args in
            guard case .char(let c) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Char", got: Eval.typeOf(args[0]))
            }
            let lo = String(c).lowercased()
            return .char(lo.unicodeScalars.first ?? c)
        }
        env.define("charToLower",  .fn(toLower))
        env.define("Char.toLower", .fn(toLower))

        // String <-> [Char] bridges. Iterating `.unicodeScalars`
        // walks code points (not grapheme clusters), matching the Go
        // and JS sides exactly.
        let stringToList = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            return .list(s.unicodeScalars.map { MarValue.char($0) })
        }
        env.define("stringToList",  .fn(stringToList))
        env.define("String.toList", .fn(stringToList))

        let stringFromList = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            var out = ""
            for v in xs {
                guard case .char(let c) = v else {
                    throw MarRuntimeError.message("String.fromList: element not Char")
                }
                out.append(Character(c))
            }
            return .string(out)
        }
        env.define("stringFromList",  .fn(stringFromList))
        env.define("String.fromList", .fn(stringFromList))

        let stringCons = MarFn.native(2) { args in
            guard case .char(let c) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Char", got: Eval.typeOf(args[0]))
            }
            guard case .string(let s) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[1]))
            }
            return .string(String(c) + s)
        }
        env.define("stringCons",  .fn(stringCons))
        env.define("String.cons", .fn(stringCons))

        // String higher-order ops over Char. We iterate
        // `.unicodeScalars` (code points), matching the Go and JS
        // sides — `String.toList "🇧🇷"` yields two scalars, so
        // `String.map` walks both of them.
        let stringMap = MarFn.native(2) { args in
            guard case .string(let s) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[1]))
            }
            var out = ""
            for scalar in s.unicodeScalars {
                let r = try Eval.apply(args[0], .char(scalar))
                guard case .char(let mapped) = r else {
                    throw MarRuntimeError.message("String.map: function didn't return Char")
                }
                out.append(Character(mapped))
            }
            return .string(out)
        }
        env.define("stringMap",  .fn(stringMap))
        env.define("String.map", .fn(stringMap))

        let stringFilter = MarFn.native(2) { args in
            guard case .string(let s) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[1]))
            }
            var out = ""
            for scalar in s.unicodeScalars {
                let r = try Eval.apply(args[0], .char(scalar))
                guard case .bool(let keep) = r else {
                    throw MarRuntimeError.message("String.filter: predicate didn't return Bool")
                }
                if keep { out.unicodeScalars.append(scalar) }
            }
            return .string(out)
        }
        env.define("stringFilter",  .fn(stringFilter))
        env.define("String.filter", .fn(stringFilter))

        // stringFoldl : (Char -> b -> b) -> b -> String -> b
        let stringFoldl = MarFn.native(3) { args in
            guard case .string(let s) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[2]))
            }
            var acc = args[1]
            for scalar in s.unicodeScalars {
                let partial = try Eval.apply(args[0], .char(scalar))
                acc = try Eval.apply(partial, acc)
            }
            return acc
        }
        env.define("stringFoldl",  .fn(stringFoldl))
        env.define("String.foldl", .fn(stringFoldl))

        // stringAny : (Char -> Bool) -> String -> Bool — short-circuit.
        let stringAny = MarFn.native(2) { args in
            guard case .string(let s) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[1]))
            }
            for scalar in s.unicodeScalars {
                let r = try Eval.apply(args[0], .char(scalar))
                guard case .bool(let b) = r else {
                    throw MarRuntimeError.message("String.any: predicate didn't return Bool")
                }
                if b { return .bool(true) }
            }
            return .bool(false)
        }
        env.define("stringAny",  .fn(stringAny))
        env.define("String.any", .fn(stringAny))
    }
}
