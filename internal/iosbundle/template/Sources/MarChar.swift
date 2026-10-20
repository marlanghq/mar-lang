// Char module + String <-> [Char] bridges: Swift port of the Go
// internal/runtime/char.go and the JS builtins in runtime.js.
//
// Char in Mar is a Unicode scalar value (here: Unicode.Scalar). NOT a
// grapheme cluster. `String.toList "🇧🇷"` yields TWO chars, matching
// Elm semantics: the regional indicators are two separate scalars.
//
// padLeft / padRight live here too because they now take a Char
// (Elm-style) rather than a String: co-locating with the rest of
// the Char-flavored stdlib makes the dependency explicit.

import Foundation

enum MarChar {
    static let simpleUpper: [UInt32: UInt32] = [
        0x1F80: 0x1F88, 0x1F81: 0x1F89, 0x1F82: 0x1F8A, 0x1F83: 0x1F8B, 0x1F84: 0x1F8C,
        0x1F85: 0x1F8D, 0x1F86: 0x1F8E, 0x1F87: 0x1F8F, 0x1F90: 0x1F98, 0x1F91: 0x1F99,
        0x1F92: 0x1F9A, 0x1F93: 0x1F9B, 0x1F94: 0x1F9C, 0x1F95: 0x1F9D, 0x1F96: 0x1F9E,
        0x1F97: 0x1F9F, 0x1FA0: 0x1FA8, 0x1FA1: 0x1FA9, 0x1FA2: 0x1FAA, 0x1FA3: 0x1FAB,
        0x1FA4: 0x1FAC, 0x1FA5: 0x1FAD, 0x1FA6: 0x1FAE, 0x1FA7: 0x1FAF, 0x1FB3: 0x1FBC,
        0x1FC3: 0x1FCC, 0x1FF3: 0x1FFC
    ]
    static let simpleLower: [UInt32: UInt32] = [0x0130: 0x0069]

    static func simpleCase(_ c: Unicode.Scalar, _ mapped: String, _ table: [UInt32: UInt32]) -> Unicode.Scalar {
        let pts = Array(mapped.unicodeScalars)
        if pts.count == 1 { return pts[0] }
        if let t = table[c.value], let s = Unicode.Scalar(t) { return s }
        return c
    }

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

        // Predicates: Unicode-aware via the scalar's properties. The
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

        // Char.toUpper / Char.toLower are Unicode's SIMPLE case mappings,
        // not the full ones String.toUpper uses. A Char is one scalar (ADR
        // 0038 clause 1) and a full mapping can produce several: the full
        // uppercase of the sharp s is two letters, which a Char has nowhere
        // to put.
        //
        // Wherever the full mapping is exactly one scalar it IS the simple
        // mapping, across all 1.1M scalars. So only the longer ones need a
        // table, and only those whose simple mapping is not the identity: 27
        // Greek ypogegrammeni, plus one lowercase. simple_case_test.go
        // regenerates both from Go's unicode data.
        let toUpper = MarFn.native(1) { args in
            guard case .char(let c) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Char", got: Eval.typeOf(args[0]))
            }
            return .char(simpleCase(c, String(c).uppercased(), simpleUpper))
        }
        env.define("charToUpper",  .fn(toUpper))
        env.define("Char.toUpper", .fn(toUpper))

        let toLower = MarFn.native(1) { args in
            guard case .char(let c) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Char", got: Eval.typeOf(args[0]))
            }
            return .char(simpleCase(c, String(c).lowercased(), simpleLower))
        }
        env.define("charToLower",  .fn(toLower))
        env.define("Char.toLower", .fn(toLower))

        // String <-> [Char] bridges. Iterating `.unicodeScalars`
        // walks scalar values (not grapheme clusters), matching the Go
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
        // `.unicodeScalars`, matching the Go and JS
        // sides: `String.toList "🇧🇷"` yields two scalars, so
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

        // stringAny : (Char -> Bool) -> String -> Bool, short-circuit.
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
