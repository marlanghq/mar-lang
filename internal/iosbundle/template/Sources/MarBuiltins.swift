// All the natively-implemented mar functions the user code can call.
// One-to-one port of `makeBuiltinEnv` in internal/jsserve/runtime.js.
//
// Two naming conventions register every builtin: the snake/camel name
// the parser desugars qualified calls into (`stringFromInt`,
// `navigationStack`, ...) and the dotted name a user can also write
// directly (`String.fromInt`, `UI.navigationStack`). Same value, two keys.

import Foundation

enum MarBuiltins {

    // makeEnv defines every runtime builtin in one place, organized
    // by section markers. The length is intentional — this is the
    // dispatch table for the language; splitting it would scatter
    // related entries.
    // swiftlint:disable:next function_body_length
    static func makeEnv() -> Env {
        let env = Env(parent: nil)

        // MARK: Booleans / Maybe / Result / Order
        //
        // Order (LT/EQ/GT) is the three-way comparison result returned
        // by List.sortWith comparators. Same convention as Elm — using
        // a named sum type beats raw -1/0/1 Ints because the call site
        // reads as "compareName a b -> LT" instead of "-> -1".
        env.define("True",  .bool(true))
        env.define("False", .bool(false))
        env.define("Nothing", .ctor(tag: "Nothing", args: [], origin: nil))
        env.define("Just",  .fn(MarFn.native(1) { .ctor(tag: "Just", args: $0, origin: nil) }))
        env.define("Ok",    .fn(MarFn.native(1) { .ctor(tag: "Ok",   args: $0, origin: nil) }))
        env.define("Err",   .fn(MarFn.native(1) { .ctor(tag: "Err",  args: $0, origin: nil) }))
        env.define("LT",    .ctor(tag: "LT", args: [], origin: nil))
        env.define("EQ",    .ctor(tag: "EQ", args: [], origin: nil))
        env.define("GT",    .ctor(tag: "GT", args: [], origin: nil))

        // MARK: Arithmetic (integer for now; matches runtime.js)
        env.define("+", .fn(MarFn.native(2) { args in .int(asInt(args[0]) + asInt(args[1])) }))
        env.define("-", .fn(MarFn.native(2) { args in .int(asInt(args[0]) - asInt(args[1])) }))
        env.define("*", .fn(MarFn.native(2) { args in .int(asInt(args[0]) * asInt(args[1])) }))
        env.define("/", .fn(MarFn.native(2) { args in
            let b = asInt(args[1])
            return .int(b == 0 ? 0 : asInt(args[0]) / b)
        }))

        // MARK: Comparison
        env.define("==", .fn(MarFn.native(2) { args in .bool(args[0].equalsMar(args[1])) }))
        env.define("/=", .fn(MarFn.native(2) { args in .bool(!args[0].equalsMar(args[1])) }))
        env.define("<",  .fn(MarFn.native(2) { args in .bool(args[0].compareMar(args[1]) <  0) }))
        env.define(">",  .fn(MarFn.native(2) { args in .bool(args[0].compareMar(args[1]) >  0) }))
        env.define("<=", .fn(MarFn.native(2) { args in .bool(args[0].compareMar(args[1]) <= 0) }))
        env.define(">=", .fn(MarFn.native(2) { args in .bool(args[0].compareMar(args[1]) >= 0) }))

        // MARK: Logic
        env.define("&&", .fn(MarFn.native(2) { args in .bool(asBool(args[0]) && asBool(args[1])) }))
        env.define("||", .fn(MarFn.native(2) { args in .bool(asBool(args[0]) || asBool(args[1])) }))

        // MARK: Append (string + list, polymorphic)
        env.define("++", .fn(MarFn.native(2) { args in
            switch (args[0], args[1]) {
            case (.string(let a), .string(let b)): return .string(a + b)
            case (.list(let a), .list(let b)):     return .list(a + b)
            default: throw MarRuntimeError.message("++: unsupported operand types")
            }
        }))

        // MARK: Cons
        env.define("::", .fn(MarFn.native(2) { args in
            guard case .list(let tail) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            return .list([args[0]] + tail)
        }))

        // MARK: Pipes
        env.define("|>", .fn(MarFn.native(2) { args in try Eval.apply(args[1], args[0]) }))
        env.define("<|", .fn(MarFn.native(2) { args in try Eval.apply(args[0], args[1]) }))

        // MARK: String stdlib
        let stringFromInt = MarFn.native(1) { args in .string(String(asInt(args[0]))) }
        env.define("stringFromInt",   .fn(stringFromInt))
        env.define("String.fromInt",  .fn(stringFromInt))

        let stringLength = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            return .int(s.count)
        }
        env.define("stringLength",  .fn(stringLength))
        env.define("String.length", .fn(stringLength))

        // String.contains : String -> String -> Bool
        // First arg is the needle, second is the haystack —
        // pipe-friendly: `haystack |> String.contains "foo"`.
        let stringContains = MarFn.native(2) { args in
            guard case .string(let needle) = args[0],
                  case .string(let hay)    = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String, String", got: Eval.typeOf(args[0]))
            }
            return .bool(hay.contains(needle))
        }
        env.define("stringContains",  .fn(stringContains))
        env.define("String.contains", .fn(stringContains))

        // String.startsWith : String -> String -> Bool
        // Same arg order: `s |> String.startsWith "prefix"`.
        let stringStartsWith = MarFn.native(2) { args in
            guard case .string(let prefix) = args[0],
                  case .string(let s)      = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String, String", got: Eval.typeOf(args[0]))
            }
            return .bool(s.hasPrefix(prefix))
        }
        env.define("stringStartsWith",  .fn(stringStartsWith))
        env.define("String.startsWith", .fn(stringStartsWith))

        let stringToUpper = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            return .string(s.uppercased())
        }
        env.define("stringToUpper",  .fn(stringToUpper))
        env.define("String.toUpper", .fn(stringToUpper))

        let stringToLower = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            return .string(s.lowercased())
        }
        env.define("stringToLower",  .fn(stringToLower))
        env.define("String.toLower", .fn(stringToLower))

        // String.split : String -> String -> List String
        // First arg is the separator. Empty separator = list of single-
        // codepoint strings (matches the Go runtime's strings.Split).
        let stringSplit = MarFn.native(2) { args in
            guard case .string(let sep) = args[0],
                  case .string(let s)   = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String, String", got: Eval.typeOf(args[0]))
            }
            let parts: [String]
            if sep.isEmpty {
                // Mirror strings.Split(s, "") — emit one element per
                // Unicode scalar so behaviour matches across runtimes.
                parts = s.map { String($0) }
            } else {
                parts = s.components(separatedBy: sep)
            }
            return .list(parts.map { .string($0) })
        }
        env.define("stringSplit",  .fn(stringSplit))
        env.define("String.split", .fn(stringSplit))

        // String.join : String -> List String -> String
        let stringJoin = MarFn.native(2) { args in
            guard case .string(let sep) = args[0],
                  case .list(let xs)    = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String, List String", got: Eval.typeOf(args[0]))
            }
            var parts: [String] = []
            parts.reserveCapacity(xs.count)
            for x in xs {
                guard case .string(let s) = x else {
                    throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(x))
                }
                parts.append(s)
            }
            return .string(parts.joined(separator: sep))
        }
        env.define("stringJoin",  .fn(stringJoin))
        env.define("String.join", .fn(stringJoin))

        // String.trim : String -> String
        // Strip leading + trailing whitespace, including newlines, to
        // match the Go runtime's strings.TrimSpace semantics.
        let stringTrim = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            return .string(s.trimmingCharacters(in: .whitespacesAndNewlines))
        }
        env.define("stringTrim",  .fn(stringTrim))
        env.define("String.trim", .fn(stringTrim))

        // String.endsWith : String suffix -> String s -> Bool
        let stringEndsWith = MarFn.native(2) { args in
            guard case .string(let suf) = args[0], case .string(let s) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String, String", got: Eval.typeOf(args[0]))
            }
            return .bool(s.hasSuffix(suf))
        }
        env.define("stringEndsWith",  .fn(stringEndsWith))
        env.define("String.endsWith", .fn(stringEndsWith))

        // String.toInt / toFloat : Maybe-returning parsers — Nothing
        // on any parse failure (empty / non-digit / overflow).
        let stringToInt = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            if let n = Int(s.trimmingCharacters(in: .whitespacesAndNewlines)) {
                return .ctor(tag: "Just", args: [.int(n)], origin: nil)
            }
            return .ctor(tag: "Nothing", args: [], origin: nil)
        }
        env.define("stringToInt",  .fn(stringToInt))
        env.define("String.toInt", .fn(stringToInt))

        let stringToFloat = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            if let f = Double(s.trimmingCharacters(in: .whitespacesAndNewlines)) {
                return .ctor(tag: "Just", args: [.float(f)], origin: nil)
            }
            return .ctor(tag: "Nothing", args: [], origin: nil)
        }
        env.define("stringToFloat",  .fn(stringToFloat))
        env.define("String.toFloat", .fn(stringToFloat))

        // String.fromFloat — Swift's default String(Double) gives
        // shortest round-trip representation.
        let stringFromFloat = MarFn.native(1) { args in
            guard case .float(let f) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Float", got: Eval.typeOf(args[0]))
            }
            return .string(String(f))
        }
        env.define("stringFromFloat",  .fn(stringFromFloat))
        env.define("String.fromFloat", .fn(stringFromFloat))

        // String.replace : needle -> replacement -> s -> String
        let stringReplace = MarFn.native(3) { args in
            guard case .string(let needle) = args[0],
                  case .string(let rep) = args[1],
                  case .string(let s) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "String, String, String", got: Eval.typeOf(args[0]))
            }
            return .string(s.replacingOccurrences(of: needle, with: rep))
        }
        env.define("stringReplace",  .fn(stringReplace))
        env.define("String.replace", .fn(stringReplace))

        // String.repeat : Int -> String -> String
        let stringRepeat = MarFn.native(2) { args in
            guard case .int(let n) = args[0], case .string(let s) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Int, String", got: Eval.typeOf(args[0]))
            }
            if n <= 0 { return .string("") }
            return .string(String(repeating: s, count: n))
        }
        env.define("stringRepeat",  .fn(stringRepeat))
        env.define("String.repeat", .fn(stringRepeat))

        // String.padLeft / padRight — pad with a Char (Elm-style).
        // Stays in sync with the Go/JS sides where `pad` is a single
        // code point repeated to fill.
        func padString(_ s: String, _ width: Int, _ pad: Unicode.Scalar, _ left: Bool) -> String {
            if s.count >= width { return s }
            let need = width - s.count
            let filler = String(repeating: String(pad), count: need)
            return left ? filler + s : s + filler
        }

        let stringPadLeft = MarFn.native(3) { args in
            guard case .int(let w) = args[0],
                  case .char(let pad) = args[1],
                  case .string(let s) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "Int, Char, String", got: Eval.typeOf(args[0]))
            }
            return .string(padString(s, w, pad, true))
        }
        env.define("stringPadLeft",  .fn(stringPadLeft))
        env.define("String.padLeft", .fn(stringPadLeft))

        let stringPadRight = MarFn.native(3) { args in
            guard case .int(let w) = args[0],
                  case .char(let pad) = args[1],
                  case .string(let s) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "Int, Char, String", got: Eval.typeOf(args[0]))
            }
            return .string(padString(s, w, pad, false))
        }
        env.define("stringPadRight",  .fn(stringPadRight))
        env.define("String.padRight", .fn(stringPadRight))

        // String.indexes : needle -> s -> List Int — non-overlapping
        // byte offsets of every occurrence. Matches Elm + the Go/JS
        // runtimes.
        let stringIndexes = MarFn.native(2) { args in
            guard case .string(let needle) = args[0], case .string(let s) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "String, String", got: Eval.typeOf(args[0]))
            }
            if needle.isEmpty { return .list([]) }
            var out: [MarValue] = []
            // Walk a position cursor through s, jumping past each
            // match. Counts UTF-16-style code units (NSString
            // semantics) to stay byte-compatible with the JS runtime.
            let ns = s as NSString
            let nl = needle as NSString
            var search = NSRange(location: 0, length: ns.length)
            while search.length > 0 {
                let r = ns.range(of: needle, options: [], range: search)
                if r.location == NSNotFound { break }
                out.append(.int(r.location))
                let next = r.location + nl.length
                search = NSRange(location: next, length: ns.length - next)
            }
            return .list(out)
        }
        env.define("stringIndexes",  .fn(stringIndexes))
        env.define("String.indexes", .fn(stringIndexes))

        // MARK: List stdlib
        let listLength = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            return .int(xs.count)
        }
        env.define("listLength",  .fn(listLength))
        env.define("List.length", .fn(listLength))

        let listMap = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            var out: [MarValue] = []
            out.reserveCapacity(xs.count)
            for x in xs { out.append(try Eval.apply(args[0], x)) }
            return .list(out)
        }
        env.define("listMap",  .fn(listMap))
        env.define("List.map", .fn(listMap))

        let listSum = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            var sum = 0
            for x in xs { sum += asInt(x) }
            return .int(sum)
        }
        env.define("listSum",  .fn(listSum))
        env.define("List.sum", .fn(listSum))

        let listFilter = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            var out: [MarValue] = []
            for x in xs {
                if case .bool(true) = try Eval.apply(args[0], x) { out.append(x) }
            }
            return .list(out)
        }
        env.define("listFilter",  .fn(listFilter))
        env.define("List.filter", .fn(listFilter))

        // MARK: List.reverse
        // Returns a new reversed list — does not mutate the source.
        let listReverse = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            return .list(Array(xs.reversed()))
        }
        env.define("listReverse",  .fn(listReverse))
        env.define("List.reverse", .fn(listReverse))

        // List.foldl : (b -> a -> b) -> b -> List a -> b
        // Tight-loop fold; errors from the function abort the fold.
        let listFoldl = MarFn.native(3) { args in
            let fn = args[0]
            var acc = args[1]
            guard case .list(let xs) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[2]))
            }
            for x in xs {
                let partial = try Eval.apply(fn, acc)
                acc = try Eval.apply(partial, x)
            }
            return acc
        }
        env.define("listFoldl",  .fn(listFoldl))
        env.define("List.foldl", .fn(listFoldl))

        // List.range : Int -> Int -> List Int — inclusive of both
        // endpoints; empty list when from > to (matches the Go runtime).
        let listRange = MarFn.native(2) { args in
            guard case .int(let from) = args[0],
                  case .int(let to)   = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Int, Int", got: Eval.typeOf(args[0]))
            }
            if from > to {
                return .list([])
            }
            var out: [MarValue] = []
            out.reserveCapacity(to - from + 1)
            for i in from...to {
                out.append(.int(i))
            }
            return .list(out)
        }
        env.define("listRange",  .fn(listRange))
        env.define("List.range", .fn(listRange))

        // List.head : List a -> Maybe a
        let listHead = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            if let first = xs.first {
                return .ctor(tag: "Just", args: [first], origin: nil)
            }
            return .ctor(tag: "Nothing", args: [], origin: nil)
        }
        env.define("listHead",  .fn(listHead))
        env.define("List.head", .fn(listHead))

        // List.tail : List a -> Maybe (List a)
        // Nothing when the list is empty; Just (rest) otherwise.
        let listTail = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            if xs.isEmpty {
                return .ctor(tag: "Nothing", args: [], origin: nil)
            }
            return .ctor(tag: "Just", args: [.list(Array(xs.dropFirst()))], origin: nil)
        }
        env.define("listTail",  .fn(listTail))
        env.define("List.tail", .fn(listTail))

        let listIsEmpty = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            return .bool(xs.isEmpty)
        }
        env.define("listIsEmpty",  .fn(listIsEmpty))
        env.define("List.isEmpty", .fn(listIsEmpty))

        // List.concat : List (List a) -> List a
        let listConcat = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            var out: [MarValue] = []
            for x in xs {
                guard case .list(let inner) = x else {
                    throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(x))
                }
                out.append(contentsOf: inner)
            }
            return .list(out)
        }
        env.define("listConcat",  .fn(listConcat))
        env.define("List.concat", .fn(listConcat))

        // MARK: List.take / List.drop
        let listTake = MarFn.native(2) { args in
            guard case .int(let n) = args[0], case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Int, List", got: Eval.typeOf(args[0]))
            }
            if n <= 0 { return .list([]) }
            if n >= xs.count { return .list(xs) }
            return .list(Array(xs.prefix(n)))
        }
        env.define("listTake",  .fn(listTake))
        env.define("List.take", .fn(listTake))

        let listDrop = MarFn.native(2) { args in
            guard case .int(let n) = args[0], case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Int, List", got: Eval.typeOf(args[0]))
            }
            if n <= 0 { return .list(xs) }
            if n >= xs.count { return .list([]) }
            return .list(Array(xs.dropFirst(n)))
        }
        env.define("listDrop",  .fn(listDrop))
        env.define("List.drop", .fn(listDrop))

        // MARK: List.move — pure splice (from → to). Mirrors the Go
        // and JS impls: no-op on from == to or out-of-bounds indices
        // so stale Msgs (race between client and server where the
        // list shrunk) don't corrupt the data.
        let listMove = MarFn.native(3) { args in
            guard case .int(let from) = args[0], case .int(let to) = args[1], case .list(let xs) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "Int, Int, List", got: Eval.typeOf(args[0]))
            }
            let n = xs.count
            if from == to || from < 0 || from >= n || to < 0 || to >= n { return .list(xs) }
            var out = xs
            let elt = out.remove(at: from)
            out.insert(elt, at: to)
            return .list(out)
        }
        env.define("listMove",  .fn(listMove))
        env.define("List.move", .fn(listMove))

        // MARK: List.member — structural equality.
        let listMember = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            for x in xs where args[0].equalsMar(x) { return .bool(true) }
            return .bool(false)
        }
        env.define("listMember",  .fn(listMember))
        env.define("List.member", .fn(listMember))

        // MARK: List.any / List.all — short-circuit.
        let listAny = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            for x in xs {
                if case .bool(true) = try Eval.apply(args[0], x) { return .bool(true) }
            }
            return .bool(false)
        }
        env.define("listAny",  .fn(listAny))
        env.define("List.any", .fn(listAny))

        let listAll = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            for x in xs {
                if case .bool(false) = try Eval.apply(args[0], x) { return .bool(false) }
            }
            return .bool(true)
        }
        env.define("listAll",  .fn(listAll))
        env.define("List.all", .fn(listAll))

        // MARK: List.foldr : (a -> b -> b) -> b -> List a -> b
        let listFoldr = MarFn.native(3) { args in
            var acc = args[1]
            guard case .list(let xs) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[2]))
            }
            for x in xs.reversed() {
                let partial = try Eval.apply(args[0], x)
                acc = try Eval.apply(partial, acc)
            }
            return acc
        }
        env.define("listFoldr",  .fn(listFoldr))
        env.define("List.foldr", .fn(listFoldr))

        // MARK: List.indexedMap : (Int -> a -> b) -> List a -> List b
        let listIndexedMap = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            var out: [MarValue] = []
            out.reserveCapacity(xs.count)
            for (i, x) in xs.enumerated() {
                let partial = try Eval.apply(args[0], .int(i))
                out.append(try Eval.apply(partial, x))
            }
            return .list(out)
        }
        env.define("listIndexedMap",  .fn(listIndexedMap))
        env.define("List.indexedMap", .fn(listIndexedMap))

        // MARK: List.repeat : Int -> a -> List a
        let listRepeat = MarFn.native(2) { args in
            guard case .int(let n) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Int", got: Eval.typeOf(args[0]))
            }
            if n <= 0 { return .list([]) }
            return .list(Array(repeating: args[1], count: n))
        }
        env.define("listRepeat",  .fn(listRepeat))
        env.define("List.repeat", .fn(listRepeat))

        // MARK: List.intersperse : a -> List a -> List a
        let listIntersperse = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            if xs.count <= 1 { return .list(xs) }
            var out: [MarValue] = []
            out.reserveCapacity(2 * xs.count - 1)
            for (i, x) in xs.enumerated() {
                if i > 0 { out.append(args[0]) }
                out.append(x)
            }
            return .list(out)
        }
        env.define("listIntersperse",  .fn(listIntersperse))
        env.define("List.intersperse", .fn(listIntersperse))

        // MARK: List.partition : (a -> Bool) -> List a -> (List a, List a)
        let listPartition = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            var yes: [MarValue] = []
            var no: [MarValue] = []
            for x in xs {
                if case .bool(true) = try Eval.apply(args[0], x) {
                    yes.append(x)
                } else {
                    no.append(x)
                }
            }
            return .tuple([.list(yes), .list(no)])
        }
        env.define("listPartition",  .fn(listPartition))
        env.define("List.partition", .fn(listPartition))

        // MARK: List.concatMap : (a -> List b) -> List a -> List b
        let listConcatMap = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            var out: [MarValue] = []
            for x in xs {
                let r = try Eval.apply(args[0], x)
                guard case .list(let inner) = r else {
                    throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(r))
                }
                out.append(contentsOf: inner)
            }
            return .list(out)
        }
        env.define("listConcatMap",  .fn(listConcatMap))
        env.define("List.concatMap", .fn(listConcatMap))

        // MARK: List.filterMap : (a -> Maybe b) -> List a -> List b
        let listFilterMap = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            var out: [MarValue] = []
            for x in xs {
                let r = try Eval.apply(args[0], x)
                if case .ctor(let tag, let cargs, _) = r, tag == "Just", cargs.count == 1 {
                    out.append(cargs[0])
                }
            }
            return .list(out)
        }
        env.define("listFilterMap",  .fn(listFilterMap))
        env.define("List.filterMap", .fn(listFilterMap))

        // MARK: List.maximum / List.minimum : List a -> Maybe a
        // compareMar only orders Int/Float/String; non-comparable
        // element types silently return Nothing rather than throwing.
        let listMaximum = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            guard var best = xs.first else { return .ctor(tag: "Nothing", args: [], origin: nil) }
            for x in xs.dropFirst() where best.compareMar(x) < 0 { best = x }
            return .ctor(tag: "Just", args: [best], origin: nil)
        }
        env.define("listMaximum",  .fn(listMaximum))
        env.define("List.maximum", .fn(listMaximum))

        let listMinimum = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            guard var best = xs.first else { return .ctor(tag: "Nothing", args: [], origin: nil) }
            for x in xs.dropFirst() where best.compareMar(x) > 0 { best = x }
            return .ctor(tag: "Just", args: [best], origin: nil)
        }
        env.define("listMinimum",  .fn(listMinimum))
        env.define("List.minimum", .fn(listMinimum))

        // MARK: List.product : List Int -> Int
        let listProduct = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            var p = 1
            for x in xs { p *= asInt(x) }
            return .int(p)
        }
        env.define("listProduct",  .fn(listProduct))
        env.define("List.product", .fn(listProduct))

        // MARK: List.sort / sortBy / sortWith
        // Swift's Array.sort(by:) is stable since Swift 5.0.
        let listSort = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            return .list(xs.sorted { $0.compareMar($1) < 0 })
        }
        env.define("listSort",  .fn(listSort))
        env.define("List.sort", .fn(listSort))

        let listSortBy = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            // Cache keys: fn runs once per element, not O(n log n).
            var keys: [MarValue] = []
            keys.reserveCapacity(xs.count)
            for x in xs { keys.append(try Eval.apply(args[0], x)) }
            let idx = Array(0..<xs.count).sorted { keys[$0].compareMar(keys[$1]) < 0 }
            return .list(idx.map { xs[$0] })
        }
        env.define("listSortBy",  .fn(listSortBy))
        env.define("List.sortBy", .fn(listSortBy))

        // listSortWith : (a -> a -> Order) -> List a -> List a
        // Comparator returns LT/EQ/GT (a 3-way sum), not -1/0/1. The
        // sort callback maps LT → "is less than", EQ/GT → "is not".
        let listSortWith = MarFn.native(2) { args in
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            // Swift's sort doesn't support throwing comparators, so
            // we materialize a side error via reference type.
            final class SortError { var err: Error? }
            let box = SortError()
            let sorted = xs.sorted { a, b in
                guard box.err == nil else { return false }
                do {
                    let partial = try Eval.apply(args[0], a)
                    let v = try Eval.apply(partial, b)
                    if case .ctor(let tag, _, _) = v {
                        switch tag {
                        case "LT": return true
                        case "EQ", "GT": return false
                        default:
                            box.err = MarRuntimeError.message(
                                "List.sortWith: comparator returned \(tag), expected LT/EQ/GT")
                            return false
                        }
                    }
                    box.err = MarRuntimeError.typeMismatch(expected: "Order", got: Eval.typeOf(v))
                    return false
                } catch {
                    box.err = error
                    return false
                }
            }
            if let err = box.err { throw err }
            return .list(sorted)
        }
        env.define("listSortWith",  .fn(listSortWith))
        env.define("List.sortWith", .fn(listSortWith))

        // MARK: Maybe.withDefault
        let maybeWithDefault = MarFn.native(2) { args in
            if case .ctor(let tag, let cargs, _) = args[1], tag == "Just" {
                return cargs[0]
            }
            return args[0]
        }
        env.define("maybeWithDefault",  .fn(maybeWithDefault))
        env.define("Maybe.withDefault", .fn(maybeWithDefault))

        // Maybe.map : (a -> b) -> Maybe a -> Maybe b
        let maybeMap = MarFn.native(2) { args in
            let fn = args[0]
            guard case .ctor(let tag, let cargs, _) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Maybe", got: Eval.typeOf(args[1]))
            }
            if tag == "Just", let inner = cargs.first {
                let v = try Eval.apply(fn, inner)
                return .ctor(tag: "Just", args: [v], origin: nil)
            }
            return args[1]
        }
        env.define("maybeMap",  .fn(maybeMap))
        env.define("Maybe.map", .fn(maybeMap))

        // Maybe.andThen : (a -> Maybe b) -> Maybe a -> Maybe b
        let maybeAndThen = MarFn.native(2) { args in
            let fn = args[0]
            guard case .ctor(let tag, let cargs, _) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Maybe", got: Eval.typeOf(args[1]))
            }
            if tag == "Just", let inner = cargs.first {
                return try Eval.apply(fn, inner)
            }
            return args[1]
        }
        env.define("maybeAndThen",  .fn(maybeAndThen))
        env.define("Maybe.andThen", .fn(maybeAndThen))

        // Result.map : (a -> b) -> Result e a -> Result e b
        let resultMap = MarFn.native(2) { args in
            let fn = args[0]
            guard case .ctor(let tag, let cargs, _) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Result", got: Eval.typeOf(args[1]))
            }
            if tag == "Ok", let inner = cargs.first {
                let v = try Eval.apply(fn, inner)
                return .ctor(tag: "Ok", args: [v], origin: nil)
            }
            return args[1]
        }
        env.define("resultMap",  .fn(resultMap))
        env.define("Result.map", .fn(resultMap))

        // Result.andThen : (a -> Result e b) -> Result e a -> Result e b
        let resultAndThen = MarFn.native(2) { args in
            let fn = args[0]
            guard case .ctor(let tag, let cargs, _) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Result", got: Eval.typeOf(args[1]))
            }
            if tag == "Ok", let inner = cargs.first {
                return try Eval.apply(fn, inner)
            }
            return args[1]
        }
        env.define("resultAndThen",  .fn(resultAndThen))
        env.define("Result.andThen", .fn(resultAndThen))

        // Result.mapError : (e1 -> e2) -> Result e1 a -> Result e2 a
        let resultMapError = MarFn.native(2) { args in
            let fn = args[0]
            guard case .ctor(let tag, let cargs, _) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Result", got: Eval.typeOf(args[1]))
            }
            if tag == "Err", let inner = cargs.first {
                let v = try Eval.apply(fn, inner)
                return .ctor(tag: "Err", args: [v], origin: nil)
            }
            return args[1]
        }
        env.define("resultMapError",  .fn(resultMapError))
        env.define("Result.mapError", .fn(resultMapError))

        // MARK: Result.withDefault / fromMaybe / toMaybe
        let resultWithDefault = MarFn.native(2) { args in
            if case .ctor(let tag, let cargs, _) = args[1], tag == "Ok", cargs.count == 1 {
                return cargs[0]
            }
            return args[0]
        }
        env.define("resultWithDefault",  .fn(resultWithDefault))
        env.define("Result.withDefault", .fn(resultWithDefault))

        let resultFromMaybe = MarFn.native(2) { args in
            if case .ctor(let tag, let cargs, _) = args[1], tag == "Just", cargs.count == 1 {
                return .ctor(tag: "Ok", args: [cargs[0]], origin: nil)
            }
            return .ctor(tag: "Err", args: [args[0]], origin: nil)
        }
        env.define("resultFromMaybe",  .fn(resultFromMaybe))
        env.define("Result.fromMaybe", .fn(resultFromMaybe))

        let resultToMaybe = MarFn.native(1) { args in
            if case .ctor(let tag, let cargs, _) = args[0], tag == "Ok", cargs.count == 1 {
                return .ctor(tag: "Just", args: [cargs[0]], origin: nil)
            }
            return .ctor(tag: "Nothing", args: [], origin: nil)
        }
        env.define("resultToMaybe",  .fn(resultToMaybe))
        env.define("Result.toMaybe", .fn(resultToMaybe))

        // MARK: Maybe.map2 / map3 / andMap / filter
        let maybeMap2 = MarFn.native(3) { args in
            guard case .ctor(let ta, let aa, _) = args[1],
                  case .ctor(let tb, let ab, _) = args[2],
                  ta == "Just", tb == "Just", aa.count == 1, ab.count == 1 else {
                return .ctor(tag: "Nothing", args: [], origin: nil)
            }
            let partial = try Eval.apply(args[0], aa[0])
            let v = try Eval.apply(partial, ab[0])
            return .ctor(tag: "Just", args: [v], origin: nil)
        }
        env.define("maybeMap2",  .fn(maybeMap2))
        env.define("Maybe.map2", .fn(maybeMap2))

        let maybeMap3 = MarFn.native(4) { args in
            guard case .ctor(let ta, let aa, _) = args[1],
                  case .ctor(let tb, let ab, _) = args[2],
                  case .ctor(let tc, let ac, _) = args[3],
                  ta == "Just", tb == "Just", tc == "Just",
                  aa.count == 1, ab.count == 1, ac.count == 1 else {
                return .ctor(tag: "Nothing", args: [], origin: nil)
            }
            let p1 = try Eval.apply(args[0], aa[0])
            let p2 = try Eval.apply(p1, ab[0])
            let v  = try Eval.apply(p2, ac[0])
            return .ctor(tag: "Just", args: [v], origin: nil)
        }
        env.define("maybeMap3",  .fn(maybeMap3))
        env.define("Maybe.map3", .fn(maybeMap3))

        let maybeAndMap = MarFn.native(2) { args in
            guard case .ctor(let tv, let va, _) = args[0],
                  case .ctor(let tf, let fa, _) = args[1],
                  tv == "Just", tf == "Just", va.count == 1, fa.count == 1 else {
                return .ctor(tag: "Nothing", args: [], origin: nil)
            }
            let v = try Eval.apply(fa[0], va[0])
            return .ctor(tag: "Just", args: [v], origin: nil)
        }
        env.define("maybeAndMap",  .fn(maybeAndMap))
        env.define("Maybe.andMap", .fn(maybeAndMap))

        let maybeFilter = MarFn.native(2) { args in
            guard case .ctor(let tag, let cargs, _) = args[1],
                  tag == "Just", cargs.count == 1 else {
                return .ctor(tag: "Nothing", args: [], origin: nil)
            }
            if case .bool(true) = try Eval.apply(args[0], cargs[0]) {
                return args[1]
            }
            return .ctor(tag: "Nothing", args: [], origin: nil)
        }
        env.define("maybeFilter",  .fn(maybeFilter))
        env.define("Maybe.filter", .fn(maybeFilter))

        // MARK: Tuple — 2-tuple helpers.
        let tupleFirst = MarFn.native(1) { args in
            guard case .tuple(let xs) = args[0], xs.count >= 2 else {
                throw MarRuntimeError.typeMismatch(expected: "2-tuple", got: Eval.typeOf(args[0]))
            }
            return xs[0]
        }
        env.define("tupleFirst",  .fn(tupleFirst))
        env.define("Tuple.first", .fn(tupleFirst))

        let tupleSecond = MarFn.native(1) { args in
            guard case .tuple(let xs) = args[0], xs.count >= 2 else {
                throw MarRuntimeError.typeMismatch(expected: "2-tuple", got: Eval.typeOf(args[0]))
            }
            return xs[1]
        }
        env.define("tupleSecond",  .fn(tupleSecond))
        env.define("Tuple.second", .fn(tupleSecond))

        let tuplePair = MarFn.native(2) { args in
            return .tuple([args[0], args[1]])
        }
        env.define("tuplePair",  .fn(tuplePair))
        env.define("Tuple.pair", .fn(tuplePair))

        let tupleMapFirst = MarFn.native(2) { args in
            guard case .tuple(let xs) = args[1], xs.count >= 2 else {
                throw MarRuntimeError.typeMismatch(expected: "2-tuple", got: Eval.typeOf(args[1]))
            }
            let v = try Eval.apply(args[0], xs[0])
            return .tuple([v, xs[1]])
        }
        env.define("tupleMapFirst",  .fn(tupleMapFirst))
        env.define("Tuple.mapFirst", .fn(tupleMapFirst))

        let tupleMapSecond = MarFn.native(2) { args in
            guard case .tuple(let xs) = args[1], xs.count >= 2 else {
                throw MarRuntimeError.typeMismatch(expected: "2-tuple", got: Eval.typeOf(args[1]))
            }
            let v = try Eval.apply(args[0], xs[1])
            return .tuple([xs[0], v])
        }
        env.define("tupleMapSecond",  .fn(tupleMapSecond))
        env.define("Tuple.mapSecond", .fn(tupleMapSecond))

        let tupleMapBoth = MarFn.native(3) { args in
            guard case .tuple(let xs) = args[2], xs.count >= 2 else {
                throw MarRuntimeError.typeMismatch(expected: "2-tuple", got: Eval.typeOf(args[2]))
            }
            let a = try Eval.apply(args[0], xs[0])
            let b = try Eval.apply(args[1], xs[1])
            return .tuple([a, b])
        }
        env.define("tupleMapBoth",  .fn(tupleMapBoth))
        env.define("Tuple.mapBoth", .fn(tupleMapBoth))

        // MARK: Dict / Set
        //
        // Lives in MarDict.swift (parallel to the Go side's
        // internal/runtime/dict.go) to keep this file from growing
        // unbounded. Same sorted-pairs invariant, same wire markers
        // (`__dict` / `__set`).
        MarDict.register(env)

        // MARK: Char
        //
        // Char.* + String <-> [Char] bridges. Lives in MarChar.swift
        // (parallel to internal/runtime/char.go on the server). The
        // padLeft/padRight pair updated above to take a Char depends
        // on this module being registered alongside.
        MarChar.register(env)

        // MARK: View constructors

        registerViewBuiltins(env)

        // MARK: Page.create
        let pageCreate = MarFn.native(1) { args in
            guard case .record(let fs, _) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "record", got: Eval.typeOf(args[0]))
            }
            // [path, init, update, view, title]
            let pathV = fs["path"] ?? .string("")
            let initV = fs["init"] ?? .unit
            let updateV = fs["update"] ?? .unit
            let viewV = fs["view"] ?? .unit
            let titleV = fs["title"] ?? .string("")
            return .ctor(tag: "__Page",
                         args: [pathV, initV, updateV, viewV, titleV],
                         origin: nil)
        }
        env.define("pageCreate",  .fn(pageCreate))
        env.define("Page.create", .fn(pageCreate))

        // MARK: Page.protected
        //
        // Same shape as Page.create plus User-aware handler
        // signatures. We emit a `__ProtectedPage` ctor (distinct
        // from `__Page`) so AppContext can detect protected pages,
        // gate them on Auth.me, and thread the User into
        // init/update/view as the first argument. The redirect
        // destination is centralized in Auth.config.signInPage —
        // the renderer reads it from AppContext at render time.
        let pageProtected = MarFn.native(1) { args in
            guard case .record(let fs, _) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "record", got: Eval.typeOf(args[0]))
            }
            let pathV = fs["path"] ?? .string("")
            let initV = fs["init"] ?? .unit
            let updateV = fs["update"] ?? .unit
            let viewV = fs["view"] ?? .unit
            let titleV = fs["title"] ?? .string("")
            return .ctor(tag: "__ProtectedPage",
                         args: [pathV, initV, updateV, viewV, titleV],
                         origin: nil)
        }
        env.define("pageProtected",  .fn(pageProtected))
        env.define("Page.protected", .fn(pageProtected))

        // MARK: Page.adminProtected (web-only)
        //
        // The built-in admin panel is a web-target tool — there is no iOS
        // admin app. We register the name so the builtin-coverage drift test
        // passes, but it errors if ever invoked on iOS.
        let pageAdminProtected = MarFn.native(1) { _ in
            throw MarRuntimeError.message("Page.adminProtected is web-only; the admin panel has no iOS app")
        }
        env.define("pageAdminProtected",  .fn(pageAdminProtected))
        env.define("Page.adminProtected", .fn(pageAdminProtected))

        let pageDynamicAdminProtected = MarFn.native(1) { _ in
            throw MarRuntimeError.message("Page.dynamicAdminProtected is web-only; the admin panel has no iOS app")
        }
        env.define("pageDynamicAdminProtected",  .fn(pageDynamicAdminProtected))
        env.define("Page.dynamicAdminProtected", .fn(pageDynamicAdminProtected))

        // Mar.Admin.* — privileged server-introspection for the web admin
        // panel. No iOS admin app, so these are web-only: registered (each
        // name spelled literally for the builtin-coverage drift test) but they
        // error if ever invoked on iOS.
        let marAdminWebOnly = MarFn.native(1) { _ in
            throw MarRuntimeError.message("Mar.Admin.* is web-only; the admin panel has no iOS app")
        }
        env.define("marAdminServerInfo", .fn(marAdminWebOnly))
        env.define("Mar.Admin.serverInfo", .fn(marAdminWebOnly))
        env.define("marAdminDbStats", .fn(marAdminWebOnly))
        env.define("Mar.Admin.dbStats", .fn(marAdminWebOnly))
        env.define("marAdminRecentRequests", .fn(marAdminWebOnly))
        env.define("Mar.Admin.recentRequests", .fn(marAdminWebOnly))
        env.define("marAdminListEntities", .fn(marAdminWebOnly))
        env.define("Mar.Admin.listEntities", .fn(marAdminWebOnly))
        env.define("marAdminListEntityRows", .fn(marAdminWebOnly))
        env.define("Mar.Admin.listEntityRows", .fn(marAdminWebOnly))
        env.define("marAdminListBackups", .fn(marAdminWebOnly))
        env.define("Mar.Admin.listBackups", .fn(marAdminWebOnly))
        env.define("marAdminRequestCode", .fn(marAdminWebOnly))
        env.define("Mar.Admin.requestCode", .fn(marAdminWebOnly))
        env.define("marAdminVerifyCode", .fn(marAdminWebOnly))
        env.define("Mar.Admin.verifyCode", .fn(marAdminWebOnly))
        env.define("marAdminSignOut", .fn(marAdminWebOnly))
        env.define("Mar.Admin.signOut", .fn(marAdminWebOnly))

        // MARK: Page.dynamic
        //
        // Pattern path with `:param` segments. Emits `__DynamicPage`
        // so AppContext can match URLs against the pattern at
        // navigation time and thread a Params record through
        // init/update/view as the leading argument. Same record shape
        // as Page.create — only the ctor tag differs.
        let pageDynamic = MarFn.native(1) { args in
            guard case .record(let fs, _) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "record", got: Eval.typeOf(args[0]))
            }
            let pathV = fs["path"] ?? .string("")
            let initV = fs["init"] ?? .unit
            let updateV = fs["update"] ?? .unit
            let viewV = fs["view"] ?? .unit
            let titleV = fs["title"] ?? .string("")
            return .ctor(tag: "__DynamicPage",
                         args: [pathV, initV, updateV, viewV, titleV],
                         origin: nil)
        }
        env.define("pageDynamic",  .fn(pageDynamic))
        env.define("Page.dynamic", .fn(pageDynamic))

        // MARK: Page.dynamicProtected
        //
        // Pattern path + auth gate. Combines __DynamicPage's URL
        // matching with __ProtectedPage's Auth.me bootstrap. Handler
        // signature is `User -> Params -> ...` (User first).
        let pageDynamicProtected = MarFn.native(1) { args in
            guard case .record(let fs, _) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "record", got: Eval.typeOf(args[0]))
            }
            let pathV = fs["path"] ?? .string("")
            let initV = fs["init"] ?? .unit
            let updateV = fs["update"] ?? .unit
            let viewV = fs["view"] ?? .unit
            let titleV = fs["title"] ?? .string("")
            return .ctor(tag: "__DynamicProtectedPage",
                         args: [pathV, initV, updateV, viewV, titleV],
                         origin: nil)
        }
        env.define("pageDynamicProtected",  .fn(pageDynamicProtected))
        env.define("Page.dynamicProtected", .fn(pageDynamicProtected))

        // MARK: Nav.push / Nav.replace
        //
        // Programmatic navigation. Reads the URL string from the
        // effect args and asks AppContext to swap the active page.
        // Replace also invalidates the cached User so a logout flow
        // re-fetches /_auth/me on the next protected entry.
        let navPush = MarFn.native(1) { args in
            let path: String
            if case .string(let s) = args[0] { path = s } else { path = "" }
            return .effect(MarEffect(tag: "navPush") {
                Task { @MainActor in AppContext.shared.navigate(path: path, replace: false) }
                return .unit
            })
        }
        env.define("navPush",  .fn(navPush))
        env.define("Nav.push", .fn(navPush))

        let navReplace = MarFn.native(1) { args in
            let path: String
            if case .string(let s) = args[0] { path = s } else { path = "" }
            return .effect(MarEffect(tag: "navReplace") {
                Task { @MainActor in AppContext.shared.navigate(path: path, replace: true) }
                return .unit
            })
        }
        env.define("navReplace",  .fn(navReplace))
        env.define("Nav.replace", .fn(navReplace))

        // Auth.completeSignIn : Effect e msg
        // Drains the framework-managed `pendingReturnPath` set when a
        // 401 redirected the user to sign-in; navigates there. Falls
        // back to "/" when no return target was captured (user landed
        // on sign-in directly, or this is the first cold start).
        // Resets the redirect-coalescer so the next genuine session
        // expiry triggers a fresh redirect.
        //
        // Uses `invalidateUser: false` because Auth.verifyCode just
        // populated AppContext.currentUser with the freshly-signed-in
        // user, and the destination is almost always a Page.protected
        // that would otherwise re-fetch Auth.me unnecessarily (and
        // race against the just-set session cookie).
        //
        // Lives under Auth.* (not Nav.*) because it bundles auth-
        // specific cleanup with the navigation step; Nav.* is kept
        // focused on pure navigation primitives.
        let authCompleteSignIn = MarEffect(tag: "authCompleteSignIn") {
            Task { @MainActor in
                let target = AppContext.shared.pendingReturnPath ?? "/"
                AppContext.shared.pendingReturnPath = nil
                AppContext.shared.redirectingToSignIn = false
                AppContext.shared.navigate(path: target, replace: true, invalidateUser: false)
            }
            return .unit
        }
        env.define("authCompleteSignIn",  .effect(authCompleteSignIn))
        env.define("Auth.completeSignIn", .effect(authCompleteSignIn))

        // MARK: linkTo / Nav.pushTo / Nav.replaceTo
        //
        // Type-safe navigation built on top of `Path r`. The user
        // passes a Path (a String at runtime — the typechecker
        // enforces the surface contract) plus the params record;
        // MarPath parses the pattern, validates the record's shape,
        // and renders the URL.
        //
        // linkTo is pure (returns the URL string); Nav.pushTo and
        // Nav.replaceTo wrap the result in an Effect that drives
        // AppContext.navigate(path:replace:) — same hook used by
        // the older Nav.push / Nav.replace.
        let linkTo = MarFn.native(2) { args in
            guard case .string(let src) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Path", got: Eval.typeOf(args[0]))
            }
            let pattern = MarPath.parse(src)
            let url = try MarPath.build(pattern, params: args[1])
            return .string(url)
        }
        env.define("linkTo", .fn(linkTo))

        let navPushTo = MarFn.native(2) { args in
            guard case .string(let src) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Path", got: Eval.typeOf(args[0]))
            }
            let pattern = MarPath.parse(src)
            // Build eagerly so missing-param / wrong-type errors fire
            // at the call site, not later inside the Effect closure.
            let url = try MarPath.build(pattern, params: args[1])
            return .effect(MarEffect(tag: "navPushTo") {
                Task { @MainActor in AppContext.shared.navigate(path: url, replace: false) }
                return .unit
            })
        }
        env.define("navPushTo",  .fn(navPushTo))
        env.define("Nav.pushTo", .fn(navPushTo))

        let navReplaceTo = MarFn.native(2) { args in
            guard case .string(let src) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Path", got: Eval.typeOf(args[0]))
            }
            let pattern = MarPath.parse(src)
            let url = try MarPath.build(pattern, params: args[1])
            return .effect(MarEffect(tag: "navReplaceTo") {
                Task { @MainActor in AppContext.shared.navigate(path: url, replace: true) }
                return .unit
            })
        }
        env.define("navReplaceTo",  .fn(navReplaceTo))
        env.define("Nav.replaceTo", .fn(navReplaceTo))

        // MARK: App.frontend / backend / fullstack
        //
        // Each returns an Effect that, when run, captures the page
        // list (or no-op for backend) into the global AppContext so
        // the iOS shell can read it back. Mirrors the JS overrides
        // for `mar dev` / `mar build`.
        // App.frontend / fullstack effects run synchronously from
        // AppViewModel.runProgram, which is @MainActor. We assert
        // that here so the effect closure can call into the
        // MainActor-isolated AppContext.
        let appFrontend = MarFn.native(1) { args in
            return .effect(MarEffect(tag: "mountPages") {
                MainActor.assumeIsolated {
                    AppContext.shared.capturePages(args[0])
                }
                return .unit
            })
        }
        env.define("appFrontend",   .fn(appFrontend))
        env.define("App.frontend",  .fn(appFrontend))

        let appBackend = MarFn.native(1) { _ in
            .effect(MarEffect(tag: "noop") { .unit })
        }
        env.define("appBackend",  .fn(appBackend))
        env.define("App.backend", .fn(appBackend))

        let appFullstack = MarFn.native(1) { args in
            return .effect(MarEffect(tag: "mountPages") {
                if case .record(let fs, _) = args[0], let pages = fs["pages"] {
                    MainActor.assumeIsolated {
                        AppContext.shared.capturePages(pages)
                    }
                }
                return .unit
            })
        }
        env.define("appFullstack",  .fn(appFullstack))
        env.define("App.fullstack", .fn(appFullstack))

        // MARK: Effect.* — sync helpers
        let effectSucceed = MarFn.native(1) { args in
            .effect(MarEffect(tag: "pure") { args[0] })
        }
        env.define("effectSucceed",  .fn(effectSucceed))
        env.define("Effect.succeed", .fn(effectSucceed))

        // Effect.fail : e -> Effect e a — throws when run, carrying
        // the user-supplied error value. Mirror of the Go runtime's
        // effectError; if the failure isn't caught upstream, it
        // surfaces as a dispatcher-level error.
        let effectFail = MarFn.native(1) { args in
            let err = args[0]
            return .effect(MarEffect(tag: "fail") {
                // Most user code passes a String; if it's anything else,
                // fall through to the type name so we still surface a
                // useful message rather than swallowing it.
                let msg: String
                if case .string(let s) = err {
                    msg = s
                } else {
                    msg = "(non-string error: " + Eval.typeOf(err) + ")"
                }
                throw MarRuntimeError.message("Effect.fail: " + msg)
            })
        }
        env.define("effectFail",  .fn(effectFail))
        env.define("Effect.fail", .fn(effectFail))

        // Effect.forEach : (a -> Effect e ()) -> List a -> Effect e ()
        // Sequential — each effect runs in order, halting on the first
        // error. Returns unit on success.
        let effectForEach = MarFn.native(2) { args in
            let fn = args[0]
            guard case .list(let xs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[1]))
            }
            return .effect(MarEffect(tag: "forEach") {
                for x in xs {
                    let v = try Eval.apply(fn, x)
                    guard case .effect(let eff) = v else {
                        throw MarRuntimeError.typeMismatch(expected: "Effect", got: Eval.typeOf(v))
                    }
                    _ = try eff.run()
                }
                return .unit
            })
        }
        env.define("effectForEach",  .fn(effectForEach))
        env.define("Effect.forEach", .fn(effectForEach))

        // Effect.sequence : List (Effect e a) -> Effect e (List a)
        // Runs each effect, collecting the results into a list.
        let effectSequence = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            return .effect(MarEffect(tag: "sequence") {
                var out: [MarValue] = []
                out.reserveCapacity(xs.count)
                for x in xs {
                    guard case .effect(let eff) = x else {
                        throw MarRuntimeError.typeMismatch(expected: "Effect", got: Eval.typeOf(x))
                    }
                    out.append(try eff.run())
                }
                return .list(out)
            })
        }
        env.define("effectSequence",  .fn(effectSequence))
        env.define("Effect.sequence", .fn(effectSequence))

        let effectMap = MarFn.native(2) { args in
            guard case .effect(let inner) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Effect", got: Eval.typeOf(args[1]))
            }
            let fn = args[0]
            return .effect(MarEffect(tag: "map") {
                let v = try inner.run()
                return try Eval.apply(fn, v)
            })
        }
        env.define("effectMap",  .fn(effectMap))
        env.define("Effect.map", .fn(effectMap))

        let effectAndThen = MarFn.native(2) { args in
            guard case .effect(let inner) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Effect", got: Eval.typeOf(args[1]))
            }
            let fn = args[0]
            return .effect(MarEffect(tag: "andThen") {
                let v = try inner.run()
                let next = try Eval.apply(fn, v)
                guard case .effect(let outer) = next else {
                    throw MarRuntimeError.typeMismatch(expected: "Effect", got: Eval.typeOf(next))
                }
                return try outer.run()
            })
        }
        env.define("effectAndThen",  .fn(effectAndThen))
        env.define("Effect.andThen", .fn(effectAndThen))

        env.define("effectNone",  .effect(MarEffect(tag: "none") { .unit }))
        env.define("Effect.none", .effect(MarEffect(tag: "none") { .unit }))

        // MARK: Time — Duration type + unit smart constructors
        let mkDuration: (Int) -> MarFn = { mult in
            MarFn.native(1) { args in
                guard case .int(let n) = args[0] else {
                    throw MarRuntimeError.typeMismatch(expected: "Int", got: Eval.typeOf(args[0]))
                }
                return .duration(n * mult)
            }
        }
        env.define("timeSeconds", .fn(mkDuration(1)));         env.define("Time.seconds", .fn(mkDuration(1)))
        env.define("timeMinutes", .fn(mkDuration(60)));        env.define("Time.minutes", .fn(mkDuration(60)))
        env.define("timeHours",   .fn(mkDuration(60*60)));     env.define("Time.hours",   .fn(mkDuration(60*60)))
        env.define("timeDays",    .fn(mkDuration(24*60*60)));  env.define("Time.days",    .fn(mkDuration(24*60*60)))
        env.define("timeWeeks",   .fn(mkDuration(7*24*60*60)));env.define("Time.weeks",   .fn(mkDuration(7*24*60*60)))
        let timeToSeconds = MarFn.native(1) { args in
            guard case .duration(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Duration", got: Eval.typeOf(args[0]))
            }
            return .int(s)
        }
        env.define("timeToSeconds",  .fn(timeToSeconds))
        env.define("Time.toSeconds", .fn(timeToSeconds))

        // Absolute moments. Time.now reads the wall clock as an
        // Effect; arithmetic shifts moments by Durations; .diff
        // produces a Duration between two moments.
        let timeNow = MarEffect(tag: "timeNow") {
            .time(Int(Date().timeIntervalSince1970 * 1000))
        }
        env.define("timeNow",  .effect(timeNow))
        env.define("Time.now", .effect(timeNow))

        let timeAdd = MarFn.native(2) { args in
            guard case .time(let t) = args[0],
                  case .duration(let d) = args[1] else {
                throw MarRuntimeError.typeMismatch(
                    expected: "(Time, Duration)",
                    got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])))")
            }
            return .time(t + d * 1000)
        }
        env.define("timeAdd",  .fn(timeAdd))
        env.define("Time.add", .fn(timeAdd))

        let timeSub = MarFn.native(2) { args in
            guard case .time(let t) = args[0],
                  case .duration(let d) = args[1] else {
                throw MarRuntimeError.typeMismatch(
                    expected: "(Time, Duration)",
                    got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])))")
            }
            return .time(t - d * 1000)
        }
        env.define("timeSub",  .fn(timeSub))
        env.define("Time.sub", .fn(timeSub))

        let timeDiff = MarFn.native(2) { args in
            guard case .time(let a) = args[0],
                  case .time(let b) = args[1] else {
                throw MarRuntimeError.typeMismatch(
                    expected: "(Time, Time)",
                    got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])))")
            }
            return .duration((b - a) / 1000)
        }
        env.define("timeDiff",  .fn(timeDiff))
        env.define("Time.diff", .fn(timeDiff))

        let mkCompare: (@escaping (Int, Int) -> Bool) -> MarFn = { op in
            MarFn.native(2) { args in
                guard case .time(let a) = args[0], case .time(let b) = args[1] else {
                    throw MarRuntimeError.typeMismatch(
                        expected: "(Time, Time)",
                        got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])))")
                }
                return .bool(op(a, b))
            }
        }
        env.define("timeBefore",  .fn(mkCompare(<)))
        env.define("Time.before", .fn(mkCompare(<)))
        env.define("timeAfter",   .fn(mkCompare(>)))
        env.define("Time.after",  .fn(mkCompare(>)))

        let isoFormatter: ISO8601DateFormatter = {
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime]
            return f
        }()
        let timeToIso = MarFn.native(1) { args in
            guard case .time(let ms) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Time", got: Eval.typeOf(args[0]))
            }
            let d = Date(timeIntervalSince1970: TimeInterval(ms) / 1000)
            return .string(isoFormatter.string(from: d))
        }
        env.define("timeToIso",  .fn(timeToIso))
        env.define("Time.toIso", .fn(timeToIso))

        let timeFromIso = MarFn.native(1) { args in
            guard case .string(let s) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            if let d = isoFormatter.date(from: s) {
                return .ctor(tag: "Just",
                             args: [.time(Int(d.timeIntervalSince1970 * 1000))],
                             origin: nil)
            }
            return .ctor(tag: "Nothing", args: [], origin: nil)
        }
        env.define("timeFromIso",  .fn(timeFromIso))
        env.define("Time.fromIso", .fn(timeFromIso))

        let timeToMillis = MarFn.native(1) { args in
            guard case .time(let ms) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Time", got: Eval.typeOf(args[0]))
            }
            return .int(ms)
        }
        env.define("timeToMillis",  .fn(timeToMillis))
        env.define("Time.toMillis", .fn(timeToMillis))

        // Calendar-aware constructors and arithmetic. Uses
        // Calendar(.gregorian) pinned to UTC so behavior matches
        // the Go and JS runtimes — months/years normalize the same
        // way (Jan 31 + 1 month = Mar 3) and there's no DST
        // weirdness since everything's UTC.
        var utcCalendar = Calendar(identifier: .gregorian)
        utcCalendar.timeZone = TimeZone(identifier: "UTC") ?? .current

        let timeFromYMD = MarFn.native(3) { args in
            guard case .int(let y) = args[0],
                  case .int(let m) = args[1],
                  case .int(let d) = args[2] else {
                throw MarRuntimeError.typeMismatch(
                    expected: "(Int, Int, Int)",
                    got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])), \(Eval.typeOf(args[2])))")
            }
            var components = DateComponents()
            components.year = y; components.month = m; components.day = d
            components.hour = 0; components.minute = 0; components.second = 0
            components.timeZone = TimeZone(identifier: "UTC")
            let date = utcCalendar.date(from: components) ?? Date(timeIntervalSince1970: 0)
            return .time(Int(date.timeIntervalSince1970 * 1000))
        }
        env.define("timeFromYMD",  .fn(timeFromYMD))
        env.define("Time.fromYMD", .fn(timeFromYMD))

        let mkCalendarShift: (Calendar.Component) -> MarFn = { component in
            MarFn.native(2) { args in
                guard case .time(let ms) = args[0], case .int(let n) = args[1] else {
                    throw MarRuntimeError.typeMismatch(
                        expected: "(Time, Int)",
                        got: "(\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1])))")
                }
                let base = Date(timeIntervalSince1970: TimeInterval(ms) / 1000)
                let shifted = utcCalendar.date(byAdding: component, value: n, to: base) ?? base
                return .time(Int(shifted.timeIntervalSince1970 * 1000))
            }
        }
        env.define("timeAddDays",    .fn(mkCalendarShift(.day)))
        env.define("Time.addDays",   .fn(mkCalendarShift(.day)))
        env.define("timeAddMonths",  .fn(mkCalendarShift(.month)))
        env.define("Time.addMonths", .fn(mkCalendarShift(.month)))
        env.define("timeAddYears",   .fn(mkCalendarShift(.year)))
        env.define("Time.addYears",  .fn(mkCalendarShift(.year)))

        // Component getters (UTC). Calendar.component returns the
        // calendar field for a Date — month is 1-indexed natively,
        // matching what the Go and JS runtimes expose.
        let mkComponent: (Calendar.Component) -> MarFn = { component in
            MarFn.native(1) { args in
                guard case .time(let ms) = args[0] else {
                    throw MarRuntimeError.typeMismatch(expected: "Time", got: Eval.typeOf(args[0]))
                }
                let date = Date(timeIntervalSince1970: TimeInterval(ms) / 1000)
                return .int(utcCalendar.component(component, from: date))
            }
        }
        env.define("timeYear",    .fn(mkComponent(.year)));    env.define("Time.year",   .fn(mkComponent(.year)))
        env.define("timeMonth",   .fn(mkComponent(.month)));   env.define("Time.month",  .fn(mkComponent(.month)))
        env.define("timeDay",     .fn(mkComponent(.day)));     env.define("Time.day",    .fn(mkComponent(.day)))
        env.define("timeHour",    .fn(mkComponent(.hour)));    env.define("Time.hour",   .fn(mkComponent(.hour)))
        env.define("timeMinute",  .fn(mkComponent(.minute)));  env.define("Time.minute", .fn(mkComponent(.minute)))
        env.define("timeSecond",  .fn(mkComponent(.second)));  env.define("Time.second", .fn(mkComponent(.second)))

        // MARK: JSON encode / decode
        env.define("jsonDecode", .fn(MarFn.native(1) { args in
            guard case .string(let raw) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            do {
                let any = try JSONSerialization.jsonObject(
                    with: Data(raw.utf8),
                    options: [.fragmentsAllowed]
                )
                return .ctor(tag: "Ok", args: [MarJSONCodec.jsonToMar(any)], origin: nil)
            } catch {
                return .ctor(tag: "Err",
                             args: [.string(error.localizedDescription)],
                             origin: nil)
            }
        }))
        env.define("JSON.decode", env.lookup("jsonDecode")!)

        env.define("jsonEncode", .fn(MarFn.native(1) { args in
            let any = MarJSONCodec.marToJSON(args[0])
            do {
                let data = try JSONSerialization.data(
                    withJSONObject: any,
                    options: [.fragmentsAllowed]
                )
                return .string(String(data: data, encoding: .utf8) ?? "")
            } catch {
                throw MarRuntimeError.message("JSON.encode failed: \(error.localizedDescription)")
            }
        }))
        env.define("JSON.encode", env.lookup("jsonEncode")!)

        // MARK: Entity / Repo / Endpoint stubs
        //
        // These exist server-side; the client never invokes them, but
        // shared modules may still mention them at top level. We give
        // each a value that survives evaluation without errors.
        let entityStub = MarFn.native(2) { _ in .ctor(tag: "__Entity", args: [], origin: nil) }
        env.define("entityDefine",  .fn(entityStub))
        env.define("Entity.define", .fn(entityStub))

        let colSerial = MarValue.ctor(tag: "__Column", args: [.string("serial")], origin: nil)
        env.define("entitySerial",  colSerial)
        env.define("Entity.serial", colSerial)

        let colCtor = MarFn.native(1) { _ in .ctor(tag: "__Column", args: [.string("?")], origin: nil) }
        env.define("entityInt",       .fn(colCtor)); env.define("Entity.int",       .fn(colCtor))
        env.define("entityText",      .fn(colCtor)); env.define("Entity.text",      .fn(colCtor))
        env.define("entityBool",      .fn(colCtor)); env.define("Entity.bool",      .fn(colCtor))
        env.define("entityTimestamp", .fn(colCtor)); env.define("Entity.timestamp", .fn(colCtor))

        let constraintStub = MarValue.ctor(tag: "__Constraint", args: [], origin: nil)
        env.define("entityNotNull",  constraintStub)
        env.define("Entity.notNull", constraintStub)

        let repoServerOnly: (String) -> MarFn = { name in
            MarFn.native(1) { _ in
                .effect(MarEffect(tag: name) {
                    throw MarRuntimeError.message("\(name) runs only server-side")
                })
            }
        }
        env.define("repoAll",          .fn(repoServerOnly("Repo.all")))
        env.define("Repo.all",         .fn(repoServerOnly("Repo.all")))
        env.define("repoFindById",     .fn(MarFn.native(2) { _ in
            .effect(MarEffect(tag: "Repo.findById") { throw MarRuntimeError.message("Repo.findById runs only server-side") })
        }))
        env.define("Repo.findById",    env.lookup("repoFindById")!)
        env.define("repoCreate",       .fn(MarFn.native(2) { _ in
            .effect(MarEffect(tag: "Repo.create") { throw MarRuntimeError.message("Repo.create runs only server-side") })
        }))
        env.define("Repo.create",      env.lookup("repoCreate")!)

        // Endpoint.* — typed handle for routes; Endpoint.call uses fetch.
        let endpointGet    = MarFn.native(1) { args in .ctor(tag: "__Ep", args: [.string("GET"),    args[0]], origin: nil) }
        let endpointPost   = MarFn.native(1) { args in .ctor(tag: "__Ep", args: [.string("POST"),   args[0]], origin: nil) }
        let endpointPatch  = MarFn.native(1) { args in .ctor(tag: "__Ep", args: [.string("PATCH"),  args[0]], origin: nil) }
        let endpointDelete = MarFn.native(1) { args in .ctor(tag: "__Ep", args: [.string("DELETE"), args[0]], origin: nil) }
        env.define("endpointGet",     .fn(endpointGet));    env.define("Endpoint.get",    .fn(endpointGet))
        env.define("endpointPost",    .fn(endpointPost));   env.define("Endpoint.post",   .fn(endpointPost))
        env.define("endpointPatch",   .fn(endpointPatch));  env.define("Endpoint.patch",  .fn(endpointPatch))
        env.define("endpointDelete",  .fn(endpointDelete)); env.define("Endpoint.delete", .fn(endpointDelete))

        // Endpoint.call : String -> Endpoint -> String -> (Result -> msg) -> Effect
        let endpointCall = MarFn.native(4) { args in
            let base = asString(args[0])
            guard case .ctor(_, let epArgs, _) = args[1],
                  epArgs.count == 2,
                  case .string(let method) = epArgs[0],
                  case .string(let path) = epArgs[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Endpoint", got: Eval.typeOf(args[1]))
            }
            let body = asString(args[2])
            let toMsg = args[3]
            return .effect(MarEffect(tag: "endpointCall") {
                MarHTTP.fire(
                    method: method,
                    url: base + path,
                    body: (method == "GET" || method == "DELETE") ? nil : body,
                    toMsg: toMsg
                )
                return .unit
            })
        }
        env.define("endpointCall",  .fn(endpointCall))
        env.define("Endpoint.call", .fn(endpointCall))

        // MARK: Service — RPC over HTTP

        // Service.declare : Service req resp — typed contract with no
        // handler. Each top-level binding gets its own stamped copy
        // via the loader's value-semantics path. The opaque __Service
        // ctor carries provenance so Service.call can derive the URL.
        let serviceDeclareValue: MarValue = .ctor(tag: "__Service", args: [], origin: nil)
        env.define("serviceDeclare",  serviceDeclareValue)
        env.define("Service.declare", serviceDeclareValue)

        // Service.implement : Service req resp -> (req -> Effect String resp) -> ExposedService
        // Browser/iOS-side, the handler never runs — service handlers
        // live on the server. We just return the contract back so the
        // value evaluates and Service.call still finds the origin.
        let serviceImplement = MarFn.native(2) { args in args[0] }
        env.define("serviceImplement",  .fn(serviceImplement))
        env.define("Service.implement", .fn(serviceImplement))

        // Service.call : Service req resp -> req -> (Result -> msg) -> Effect
        let serviceCall = MarFn.native(3) { args in
            let svc = args[0]
            let req = args[1]
            let toMsg = args[2]
            return .effect(MarEffect(tag: "serviceCall") {
                guard case .ctor(_, _, let origin?) = svc else {
                    throw MarRuntimeError.message("Service.call: anonymous service has no path")
                }
                let path = "/services/\(origin.module).\(origin.name)"
                let bodyAny = MarJSONCodec.marToJSON(req)
                let bodyData = try? JSONSerialization.data(
                    withJSONObject: bodyAny,
                    options: [.fragmentsAllowed]
                )
                let bodyString = bodyData.flatMap { String(data: $0, encoding: .utf8) } ?? "null"
                MarHTTP.fireService(path: path, body: bodyString, toMsg: toMsg)
                return .unit
            })
        }
        env.define("serviceCall",  .fn(serviceCall))
        env.define("Service.call", .fn(serviceCall))

        // MARK: Http.get / Http.post
        let httpGet = MarFn.native(2) { args in
            let url = asString(args[0])
            let toMsg = args[1]
            return .effect(MarEffect(tag: "httpGet") {
                MarHTTP.fire(method: "GET", url: url, body: nil, toMsg: toMsg)
                return .unit
            })
        }
        env.define("httpGet",  .fn(httpGet))
        env.define("Http.get", .fn(httpGet))

        let httpPost = MarFn.native(3) { args in
            let url = asString(args[0])
            let body = asString(args[1])
            let toMsg = args[2]
            return .effect(MarEffect(tag: "httpPost") {
                MarHTTP.fire(method: "POST", url: url, body: body, toMsg: toMsg)
                return .unit
            })
        }
        env.define("httpPost",  .fn(httpPost))
        env.define("Http.post", .fn(httpPost))

        // MARK: Auth — passwordless email-code

        // Auth.config: server-side this captures the user entity +
        // signup hook into a global. iOS-side we additionally pull the
        // `signInPage` field's path off — Page.protected reads it as
        // the redirect target when the user has no session. Stashed
        // into AppContext so StackShell can read it at render time.
        let authConfig = MarFn.native(1) { args in
            if case .record(let fs, _) = args[0],
               let pageVal = fs["signInPage"],
               case .ctor(_, let pageArgs, _) = pageVal,
               let firstArg = pageArgs.first,
               case .string(let path) = firstArg {
                MainActor.assumeIsolated {
                    AppContext.shared.signInPath = path
                }
            }
            return .ctor(tag: "__Auth", args: [args[0]], origin: nil)
        }
        env.define("authConfig",  .fn(authConfig))
        env.define("Auth.config", .fn(authConfig))

        // Auth.protect : Service -> (req -> user -> Effect) -> ExposedService
        // Server-side wraps the handler with a session-validating
        // middleware. iOS-side, just returns the contract — handlers
        // run on the server, never on the device.
        let authProtect = MarFn.native(2) { args in args[0] }
        env.define("authProtect",  .fn(authProtect))
        env.define("Auth.protect", .fn(authProtect))

        // PROPOSAL stubs (see docs/authorization-proposal.md).
        // Authorization always runs server-side; iOS just returns
        // the contract unchanged so the call chain evaluates.
        let authRequireRole = MarFn.native(2) { args in args[1] }
        env.define("authRequireRole",  .fn(authRequireRole))
        env.define("Auth.requireRole", .fn(authRequireRole))

        let authAuthorize = MarFn.native(3) { args in args[2] }
        env.define("authAuthorize",  .fn(authAuthorize))
        env.define("Auth.authorize", .fn(authAuthorize))

        let authRequireOwner = MarFn.native(3) { args in args[2] }
        env.define("authRequireOwner",  .fn(authRequireOwner))
        env.define("Auth.requireOwner", .fn(authRequireOwner))

        // Auth.requestCode : { email } -> (Result String () -> msg) -> Effect
        let authRequestCode = MarFn.native(2) { args in
            let req = args[0]
            let toMsg = args[1]
            return .effect(MarEffect(tag: "Auth.requestCode") {
                MarHTTP.fireAuth(path: "/_auth/request-code",
                                  body: req,
                                  decode: .ackUnit,
                                  toMsg: toMsg)
                return .unit
            })
        }
        env.define("authRequestCode",  .fn(authRequestCode))
        env.define("Auth.requestCode", .fn(authRequestCode))

        // Auth.verifyCode : { email, code } -> (Result String User -> msg) -> Effect
        let authVerifyCode = MarFn.native(2) { args in
            let req = args[0]
            let toMsg = args[1]
            return .effect(MarEffect(tag: "Auth.verifyCode") {
                MarHTTP.fireAuth(path: "/_auth/verify-code",
                                  body: req,
                                  decode: .userJSON,
                                  toMsg: toMsg)
                return .unit
            })
        }
        env.define("authVerifyCode",  .fn(authVerifyCode))
        env.define("Auth.verifyCode", .fn(authVerifyCode))

        // Auth.logout : (Result String () -> msg) -> Effect
        //
        // Optimistic logout: dispatches Ok(()) IMMEDIATELY and
        // fires the server POST in the background fire-and-forget.
        // See the JS counterpart for the full rationale; in short,
        // operators reported "Sign out feels stuck on slow networks"
        // because the previous flow waited for the server before
        // any UI update. Production apps (Gmail / Slack / Twitter)
        // all logout-then-navigate before the server responds.
        //
        // We post directly via URLSession rather than going through
        // MarHTTP.fireAuth — that helper couples the request to the
        // dispatch, which is exactly the coupling we want to break.
        let authLogout = MarFn.native(1) { args in
            let toMsg = args[0]
            return .effect(MarEffect(tag: "Auth.logout") {
                Task { @MainActor in
                    if let url = MarDispatcher.shared.resolve(path: "/_auth/logout") {
                        var req = URLRequest(url: url)
                        req.httpMethod = "POST"
                        // Carry the Bearer so the server can identify
                        // which session row to delete. Without this,
                        // the request would arrive anonymous and the
                        // server couldn't revoke the right token —
                        // session would linger until natural expiry.
                        if let tok = MarKeychain.load(forKey: MarKeychain.sessionTokenKey) {
                            req.setValue("Bearer \(tok)", forHTTPHeaderField: "Authorization")
                        }
                        // Fire-and-forget. Server-side session
                        // cleanup is best-effort; if it doesn't
                        // arrive the local credential is gone anyway
                        // (cleared below) so the user is locally
                        // logged out either way.
                        URLSession.shared.dataTask(with: req) { _, _, _ in }.resume()
                    }
                    // Drop the local credential first — covers the
                    // network-failure case where the server POST
                    // never lands. On the next request the absence
                    // of a Bearer + the server having seen no logout
                    // means a stale session row lingers until TTL,
                    // but the user is locally signed out and any
                    // subsequent call hits the 401 → sign-in path.
                    MarHTTP.clearStoredToken()
                    // Clear the cached user IMMEDIATELY so any
                    // Page.protected gate re-evaluating right after
                    // sees "no user" and bounces to sign-in.
                    AppContext.shared.currentUser = .ctor(tag: "Nothing", args: [], origin: nil)
                    // Dispatch Ok(()) so the mar update fires its
                    // post-logout action (typically Auth.completeSignIn).
                    let msg = (try? Eval.apply(toMsg, .ctor(tag: "Ok", args: [.unit], origin: nil))) ?? .unit
                    MarDispatcher.shared.dispatch(msg)
                }
                return .unit
            })
        }
        env.define("authLogout",  .fn(authLogout))
        env.define("Auth.logout", .fn(authLogout))

        // Auth.me : (Result String (Maybe User) -> msg) -> Effect
        // GET semantics, `null` body → Nothing, JSON object → Just user.
        let authMe = MarFn.native(1) { args in
            let toMsg = args[0]
            return .effect(MarEffect(tag: "Auth.me") {
                MarHTTP.fireAuthMe(toMsg: toMsg)
                return .unit
            })
        }
        env.define("authMe",  .fn(authMe))
        env.define("Auth.me", .fn(authMe))

        return env
    }

    // MARK: View builtins

    private static func registerViewBuiltins(_ env: Env) {
        // Helper to read attrs out of a `List Attr` argument. Each Attr
        // is a record { name: String, value: <opaque> } produced by
        // the modifier builtins below.
        func collectAttrs(_ list: MarValue) -> [MarView.Attr] {
            guard case .list(let xs) = list else { return [] }
            var out: [MarView.Attr] = []
            for x in xs {
                if case .record(let fs, _) = x,
                   case .string(let name) = fs["name"] ?? .unit {
                    out.append(MarView.Attr(name: name, value: fs["value"] ?? .unit))
                }
            }
            return out
        }

        func childrenList(_ list: MarValue) -> [MarView] {
            guard case .list(let xs) = list else { return [] }
            return xs.compactMap {
                if case .view(let v) = $0 { return v } else { return nil }
            }
        }

        // 2-arg native: (List Attr, List View) -> View. Used by every
        // UI.* container that accepts both a modifiers list and a
        // children list (navigationStack / uiSection / hstack / vstack).
        func container(_ tag: String) -> MarFn {
            MarFn.native(2) { args in
                .view(MarView(
                    tag: tag,
                    attrs: collectAttrs(args[0]),
                    children: childrenList(args[1]),
                    text: "",
                    msg: nil,
                    key: nil
                ))
            }
        }

        // Shared helper: builds a `{name, value}` record — the runtime
        // shape of an Attr. Used by submit / input-kind attrs below
        // and by the UI.* container modifiers further down.
        func makeAttr(_ name: String, _ value: MarValue) -> MarValue {
            .record(
                fields: ["name": .string(name), "value": value],
                order: ["name", "value"]
            )
        }
        let flagAttr: (String) -> MarValue = { name in makeAttr(name, .unit) }

        // UI.submit : msg -> Attr — declarative event hookup. The
        // renderer reads this attr and wires it to SwiftUI's
        // `.onSubmit` modifier (Return/Done/Go on the keyboard).
        let viewSubmit = MarFn.native(1) { args in makeAttr("submit", args[0]) }
        env.define("viewSubmit", .fn(viewSubmit))

        // Input-kind attrs (UI.email / .password / .newPassword /
        // .numeric / .oneTimeCode). MarRenderer translates these into
        // SwiftUI modifiers — `.keyboardType`, `.textContentType`,
        // `.autocapitalization`, etc. — so iOS keyboards and Keychain
        // behave the same way Safari/Chrome do on the web.
        env.define("viewEmail",       flagAttr("inputKindEmail"))
        env.define("viewPassword",    flagAttr("inputKindPassword"))
        env.define("viewNewPassword", flagAttr("inputKindNewPassword"))
        env.define("viewNumeric",     flagAttr("inputKindNumeric"))
        // iOS-only: enables the "Code from Mail" / SMS one-time-code
        // suggestion above the keyboard. Composes with `numeric` so a
        // 6-digit code field uses both: `[ numeric, oneTimeCode ]`.
        // The renderer (MarRenderer.swift) maps this to
        // `.textContentType = .oneTimeCode`.
        env.define("viewOneTimeCode", flagAttr("inputKindOneTimeCode"))

        // ---------- UI module: SwiftUI-style declarative vocabulary ----------
        //
        // Mirror of the Go runtime's UI builtins. Same VView tags as
        // the JS runtime emits ("navigationStack", "form", "uiList",
        // "uiSection", "hstack", "vstack", "textField"). The iOS
        // renderer (MarRenderer.swift) recognizes these tags and
        // produces real SwiftUI primitives — NavigationStack, Form,
        // List, Section, HStack/VStack, TextField — with platform
        // chrome (safe areas, swipe-back, table styling) for free.

        // Helper: 1-arg container that takes only children (no attrs).
        func contentOnlyContainer(_ tag: String) -> MarFn {
            MarFn.native(1) { args in
                .view(MarView(
                    tag: tag,
                    attrs: [],
                    children: childrenList(args[0]),
                    text: "",
                    msg: nil,
                    key: nil
                ))
            }
        }

        // Containers
        env.define("navigationStack",    .fn(container("navigationStack")))
        env.define("UI.navigationStack", .fn(container("navigationStack")))
        env.define("form",        .fn(contentOnlyContainer("form")))
        env.define("UI.form",     .fn(contentOnlyContainer("form")))
        env.define("list",        .fn(container("uiList")))
        env.define("UI.list",     .fn(container("uiList")))
        env.define("uiSection",   .fn(container("uiSection")))
        env.define("UI.section",  .fn(container("uiSection")))
        env.define("uiKeyedList", .fn(container("uiKeyedList")))
        env.define("UI.keyedList", .fn(container("uiKeyedList")))
        env.define("hstack",      .fn(container("hstack")))
        env.define("UI.hstack",   .fn(container("hstack")))
        env.define("vstack",      .fn(container("vstack")))
        env.define("UI.vstack",   .fn(container("vstack")))

        // textField : List Attr -> String placeholder -> String value -> (String -> msg) -> View msg
        let uiTextField = MarFn.native(4) { args in
            var attrs = collectAttrs(args[0])
            attrs.append(MarView.Attr(name: "placeholder", value: args[1]))
            let value: String
            if case .string(let s) = args[2] { value = s } else { value = "" }
            return .view(MarView(
                tag: "textField",
                attrs: attrs,
                children: [],
                text: value,
                msg: args[3],
                key: nil
            ))
        }
        env.define("textField",    .fn(uiTextField))
        env.define("UI.textField", .fn(uiTextField))

        // textArea : List Attr -> String placeholder -> String value -> (String -> msg) -> View msg
        // Multi-line variant of textField. Same wire shape, just a
        // different `tag` for MarRenderer to swap TextField for
        // TextEditor (or any platform-native multi-line control).
        let uiTextArea = MarFn.native(4) { args in
            var attrs = collectAttrs(args[0])
            attrs.append(MarView.Attr(name: "placeholder", value: args[1]))
            let value: String
            if case .string(let s) = args[2] { value = s } else { value = "" }
            return .view(MarView(
                tag: "textArea",
                attrs: attrs,
                children: [],
                text: value,
                msg: args[3],
                key: nil
            ))
        }
        env.define("textArea",    .fn(uiTextArea))
        env.define("UI.textArea", .fn(uiTextArea))

        // picker : List Attr -> a -> List a -> (a -> String) -> (a -> msg) -> View msg
        // Single-selection field for enum-like inputs that have
        // too many variants to render as a stack of toggles. The
        // selected value, option list, and labeling function ride
        // along as attrs so the renderer can rebuild the option
        // list and resolve the picked value at dispatch time.
        // iOS: SwiftUI Picker (menu / wheel). Web: <select>.
        let uiPicker = MarFn.native(5) { args in
            var attrs = collectAttrs(args[0])
            attrs.append(MarView.Attr(name: "selected", value: args[1]))
            attrs.append(MarView.Attr(name: "options", value: args[2]))
            attrs.append(MarView.Attr(name: "toLabel", value: args[3]))
            return .view(MarView(
                tag: "picker",
                attrs: attrs,
                children: [],
                text: "",
                msg: args[4],
                key: nil
            ))
        }
        env.define("picker",    .fn(uiPicker))
        env.define("UI.picker", .fn(uiPicker))

        // text — plain text leaf. The attrs list carries the
        // universal layout attrs (width / height); `text [width
        // fill] "..."` is the equal-columns idiom.
        let uiText = MarFn.native(2) { args in
            let attrs = collectAttrs(args[0])
            let s: String
            if case .string(let str) = args[1] { s = str } else { s = "" }
            return .view(MarView(
                tag: "text", attrs: attrs, children: [], text: s, msg: nil, key: nil
            ))
        }
        env.define("uiText",  .fn(uiText))
        env.define("UI.text", .fn(uiText))

        // UI.button : List Attr -> msg -> String -> View msg
        let uiButton = MarFn.native(3) { args in
            let label: String
            if case .string(let s) = args[2] { label = s } else { label = "" }
            return .view(MarView(
                tag: "button",
                attrs: collectAttrs(args[0]),
                children: [],
                text: label,
                msg: args[1],
                key: nil
            ))
        }
        env.define("uiButton",  .fn(uiButton))
        env.define("UI.button", .fn(uiButton))

        // UI.disabled : Bool -> Attr — greys out an interactive view
        // (today: button) and suppresses dispatch. Symmetric for
        // true/false so user code can pass a derived Bool without
        // building the attrs list conditionally.
        let uiDisabled = MarFn.native(1) { args in
            makeAttr("disabled", args[0])
        }
        env.define("uiDisabled",  .fn(uiDisabled))
        env.define("UI.disabled", .fn(uiDisabled))

        // UI.keyed : String -> View msg -> KeyedView msg
        // Wraps a regular View with a stable identity (the key
        // string) so it can be a child of UI.keyedList. The
        // distinction is compile-time only — at runtime we just
        // append a `key` attr to the inner MarView. MarRenderer
        // reads it back when feeding rows into SwiftUI's ForEach
        // as the row\'s `.id`, so .onMove / .onDelete animate the
        // correct row across mutations.
        //
        // Returns a copy with attrs extended so callers that hold
        // a reference to the unwrapped view don\'t see side-effects.
        let uiKeyed = MarFn.native(2) { args in
            guard case .string(let keyStr) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            guard case .view(let view) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "View", got: Eval.typeOf(args[1]))
            }
            var attrs = view.attrs
            attrs.append(MarView.Attr(name: "key", value: .string(keyStr)))
            return .view(MarView(
                tag: view.tag,
                attrs: attrs,
                children: view.children,
                text: view.text,
                msg: view.msg,
                key: view.key
            ))
        }
        env.define("uiKeyed",  .fn(uiKeyed))
        env.define("UI.keyed", .fn(uiKeyed))

        // UI.onMove : Bool -> (Int -> Int -> msg) -> Attr KeyedList
        // Reorder gesture for `list`. Bool = "edit mode active"
        // (rows show drag affordance), function = `(from, to) ->
        // msg` callback. Bundled into one attr so the type system
        // guarantees both are declared together.
        //
        // Stored as a record { editing, handler } so the renderer
        // can pull both off in one place. On iOS this maps to
        // `.onMove { ... }` + `editMode` environment.
        let uiOnMove = MarFn.native(2) { args in
            var fields: [String: MarValue] = [:]
            fields["editing"] = args[0]
            fields["handler"] = args[1]
            return makeAttr("onMove", .record(fields: fields, order: ["editing", "handler"]))
        }
        env.define("uiOnMove",  .fn(uiOnMove))
        env.define("UI.onMove", .fn(uiOnMove))

        // UI.onDelete : Bool -> (Int -> msg) -> Attr Section
        // Per-row delete affordance. Bool = "edit mode active"; when
        // True, SwiftUI's edit-mode minus button shows on every row.
        // When False, swipe-to-delete reveals the destructive action
        // (the standard iOS Mail/Notes pattern).
        //
        // Same packing shape as onMove so both can be set on the same
        // section without conflict. The Swift renderer wires this to
        // SwiftUI's native `.onDelete` modifier on ForEach — which
        // gives swipe + edit-mode + accessibility for free.
        let uiOnDelete = MarFn.native(2) { args in
            var fields: [String: MarValue] = [:]
            fields["editing"] = args[0]
            fields["handler"] = args[1]
            return makeAttr("onDelete", .record(fields: fields, order: ["editing", "handler"]))
        }
        env.define("uiOnDelete",  .fn(uiOnDelete))
        env.define("UI.onDelete", .fn(uiOnDelete))

        // title / subtitle — heading + secondary heading. Reuse the
        // existing "title" / "subtitle" tags so the renderer's
        // existing branches apply (.font(.title2) / .font(.headline)).
        let uiTitle = MarFn.native(1) { args in
            let s: String
            if case .string(let str) = args[0] { s = str } else { s = "" }
            return .view(MarView(tag: "title", attrs: [], children: [], text: s, msg: nil, key: nil))
        }
        env.define("uiTitle",  .fn(uiTitle))
        env.define("UI.title", .fn(uiTitle))

        let uiSubtitle = MarFn.native(1) { args in
            let s: String
            if case .string(let str) = args[0] { s = str } else { s = "" }
            return .view(MarView(tag: "subtitle", attrs: [], children: [], text: s, msg: nil, key: nil))
        }
        env.define("uiSubtitle",  .fn(uiSubtitle))
        env.define("UI.subtitle", .fn(uiSubtitle))

        // errorText — destructive-intent message (red + semi-bold).
        // Same leaf shape as text/title/subtitle; MarRenderer's
        // "errorText" case applies the visual treatment.
        let uiErrorText = MarFn.native(1) { args in
            let s: String
            if case .string(let str) = args[0] { s = str } else { s = "" }
            return .view(MarView(tag: "errorText", attrs: [], children: [], text: s, msg: nil, key: nil))
        }
        env.define("uiErrorText",  .fn(uiErrorText))
        env.define("UI.errorText", .fn(uiErrorText))

        // image : List (Attr Image) -> { src, alt } -> View msg
        // Carries src + alt (and any size/fit/fill attrs) on an "image"
        // view; MarRenderer's "image" case renders an AsyncImage. alt is
        // a required record field, never optional.
        let uiImage = MarFn.native(2) { args in
            var attrs = collectAttrs(args[0])
            var src = ""
            var alt = ""
            if case .record(let fields, _) = args[1] {
                if case .string(let s)? = fields["src"] { src = s }
                if case .string(let a)? = fields["alt"] { alt = a }
            }
            attrs.append(MarView.Attr(name: "src", value: .string(src)))
            attrs.append(MarView.Attr(name: "alt", value: .string(alt)))
            return .view(MarView(tag: "image", attrs: attrs, children: [], text: "", msg: nil, key: nil))
        }
        env.define("uiImage",  .fn(uiImage))
        env.define("UI.image", .fn(uiImage))

        // paragraph : List (Inline msg) -> View msg
        //
        // A flowing block of inline atoms. The `span` children carry
        // their own inline attrs; MarRenderer's `paragraph` case folds
        // them into one AttributedString (run styling + tappable
        // links), mirroring the .mar-paragraph / .mar-inline-* CSS on
        // web. `childrenList` unwraps the list of span views.
        let uiParagraph = MarFn.native(1) { args in
            return .view(MarView(
                tag: "paragraph",
                attrs: [],
                children: childrenList(args[0]),
                text: "",
                msg: nil, key: nil))
        }
        env.define("uiParagraph",  .fn(uiParagraph))
        env.define("UI.paragraph", .fn(uiParagraph))

        // span : List (Attr Inline) -> String -> Inline msg
        //
        // One styled run inside a paragraph: carries the inline attrs
        // (bold / italic / strikethrough / code / link) plus its text.
        // MarRenderer reads them when building the paragraph's
        // AttributedString.
        let uiSpan = MarFn.native(2) { args in
            let attrs = collectAttrs(args[0])
            let s: String
            if case .string(let str) = args[1] { s = str } else { s = "" }
            return .view(MarView(
                tag: "span",
                attrs: attrs,
                children: [],
                text: s,
                msg: nil, key: nil))
        }
        env.define("uiSpan",  .fn(uiSpan))
        env.define("UI.span", .fn(uiSpan))

        // Inline attrs. Bare markers (bold / italic / strikethrough /
        // code) carry no payload; `link` carries the destination URL.
        env.define("inlineBold",          flagAttr("inlineBold"))
        env.define("UI.bold",             flagAttr("inlineBold"))
        env.define("inlineItalic",        flagAttr("inlineItalic"))
        env.define("UI.italic",           flagAttr("inlineItalic"))
        env.define("inlineStrikethrough", flagAttr("inlineStrikethrough"))
        env.define("UI.strikethrough",    flagAttr("inlineStrikethrough"))
        env.define("inlineCode",          flagAttr("inlineCode"))
        env.define("UI.code",             flagAttr("inlineCode"))
        let inlineLink = MarFn.native(1) { args in makeAttr("inlineLink", args[0]) }
        env.define("inlineLink",  .fn(inlineLink))
        env.define("UI.link",     .fn(inlineLink))

        // chars / lines — sizing units. Build a record with __unit
        // and amount fields; the renderer dispatches on __unit to
        // pick .frame(maxWidth:) (chars) or .frame(idealHeight:)
        // (lines). Type-level the Mar code already prevents mixing
        // dimensions (chars only flows into width, lines only into
        // height), so the renderer just trusts the unit it sees.
        let uiChars = MarFn.native(1) { args in
            let n: Int
            if case .int(let i) = args[0] { n = i } else { n = 0 }
            return .record(
                fields: ["__unit": .string("chars"), "amount": .int(n)],
                order: ["__unit", "amount"]
            )
        }
        env.define("uiChars", .fn(uiChars))
        env.define("UI.chars", .fn(uiChars))
        let uiLines = MarFn.native(1) { args in
            let n: Int
            if case .int(let i) = args[0] { n = i } else { n = 0 }
            return .record(
                fields: ["__unit": .string("lines"), "amount": .int(n)],
                order: ["__unit", "amount"]
            )
        }
        env.define("uiLines", .fn(uiLines))
        env.define("UI.lines", .fn(uiLines))

        // fill — the axis-polymorphic "take the available space"
        // sizing value. Same __unit-tagged shape as chars/lines so
        // the renderer dispatches on one field; amount unused.
        let uiFillVal: MarValue = .record(
            fields: ["__unit": .string("fill"), "amount": .int(0)],
            order: ["__unit", "amount"]
        )
        env.define("uiFill", uiFillVal)
        env.define("UI.fill", uiFillVal)

        // width / height — the universal sizing attrs. The renderer
        // reads the Size value via attrLength helpers: chars/lines
        // map to .frame sizes on inputs, fill to .frame(maxWidth/
        // maxHeight: .infinity) on any view.
        let uiWidth = MarFn.native(1) { args in
            return makeAttr("width", args[0])
        }
        env.define("uiWidth", .fn(uiWidth))
        env.define("UI.width", .fn(uiWidth))
        let uiHeight = MarFn.native(1) { args in
            return makeAttr("height", args[0])
        }
        env.define("uiHeight", .fn(uiHeight))
        env.define("UI.height", .fn(uiHeight))

        // align — cross-axis alignment for a stack's hugging
        // children. Value is a plain alignment-name string; the
        // renderer maps it onto the VStack/HStack alignment
        // parameter, honoring only the axis that matches the stack.
        let uiAlign = MarFn.native(1) { args in
            return makeAttr("align", args[0])
        }
        env.define("uiAlign", .fn(uiAlign))
        env.define("UI.align", .fn(uiAlign))
        env.define("uiLeading", .string("leading"))
        env.define("UI.leading", .string("leading"))
        env.define("uiCenter", .string("center"))
        env.define("UI.center", .string("center"))
        env.define("uiTrailing", .string("trailing"))
        env.define("UI.trailing", .string("trailing"))
        env.define("uiTop", .string("top"))
        env.define("UI.top", .string("top"))
        env.define("uiBottom", .string("bottom"))
        env.define("UI.bottom", .string("bottom"))

        // px — pixel sizing unit for images (mirrors chars/lines,
        // tagged "px"). size — fixed width+height attr for an image.
        // fit/cover — content-mode flags (CSS object-fit vocabulary;
        // "cover", not "fill", which is the sizing value above).
        // MarRenderer's "image" case reads these.
        let uiPx = MarFn.native(1) { args in
            let n: Int
            if case .int(let i) = args[0] { n = i } else { n = 0 }
            return .record(
                fields: ["__unit": .string("px"), "amount": .int(n)],
                order: ["__unit", "amount"]
            )
        }
        env.define("uiPx", .fn(uiPx))
        env.define("UI.px", .fn(uiPx))
        let uiSize = MarFn.native(2) { args in
            return makeAttr("size", .record(fields: ["w": args[0], "h": args[1]], order: ["w", "h"]))
        }
        env.define("uiSize", .fn(uiSize))
        env.define("UI.size", .fn(uiSize))
        env.define("uiFit",  flagAttr("contentModeFit"))
        env.define("UI.fit",  flagAttr("contentModeFit"))
        env.define("uiCover", flagAttr("contentModeCover"))
        env.define("UI.cover", flagAttr("contentModeCover"))

        // navigationLink : Path r -> r -> View msg -> View msg
        // Mirror of SwiftUI's NavigationLink. The typed Path +
        // record build the destination URL via the same
        // path-pattern machinery as linkTo; the child View is the
        // tappable label. The renderer wires this to
        // `NavigationLink(value:){content}` which pushes onto the
        // ambient NavigationStack — swipe-back and the chevron come
        // for free.
        let uiNavigationLink = MarFn.native(4) { args in
            var attrs = collectAttrs(args[0])
            guard case .string(let src) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Path", got: Eval.typeOf(args[1]))
            }
            let pattern = MarPath.parse(src)
            let url = try MarPath.build(pattern, params: args[2])
            guard case .view(let child) = args[3] else {
                throw MarRuntimeError.typeMismatch(expected: "View", got: Eval.typeOf(args[3]))
            }
            attrs.append(MarView.Attr(name: "href", value: .string(url)))
            return .view(MarView(
                tag: "navigationLink",
                attrs: attrs,
                children: [child],
                text: "",
                msg: nil,
                key: nil
            ))
        }
        env.define("uiNavigationLink",  .fn(uiNavigationLink))
        env.define("UI.navigationLink", .fn(uiNavigationLink))

        // empty : View msg — no-op placeholder (renders as
        // EmptyView / display:none).
        let uiEmptyView = MarValue.view(MarView(
            tag: "empty", attrs: [], children: [], text: "", msg: nil, key: nil
        ))
        env.define("uiEmpty",  uiEmptyView)
        env.define("UI.empty", uiEmptyView)

        // spacer : View msg — SwiftUI's `Spacer()`. Expands along
        // the containing stack's main axis to push siblings apart.
        let uiSpacerView = MarValue.view(MarView(
            tag: "spacer", attrs: [], children: [], text: "", msg: nil, key: nil
        ))
        env.define("uiSpacer",  uiSpacerView)
        env.define("UI.spacer", uiSpacerView)

        // toggle : List Attr -> String -> Bool -> (Bool -> msg) -> View msg
        // Mirror of SwiftUI's Toggle. The current state lives in
        // the `isOn` attr; the label sits in `text`; the
        // `Bool -> msg` callback is the bound message. Renderer
        // builds an actual SwiftUI Toggle whose binding dispatches
        // `msg(newValue)` on flip. The leading attrs list carries
        // `disabled` (and future modifiers) so the API matches
        // every other interactive primitive.
        let uiToggle = MarFn.native(4) { args in
            var attrs = collectAttrs(args[0])
            let label: String
            if case .string(let s) = args[1] { label = s } else { label = "" }
            let isOn: Bool
            if case .bool(let b) = args[2] { isOn = b } else { isOn = false }
            attrs.append(MarView.Attr(name: "isOn", value: .bool(isOn)))
            return .view(MarView(
                tag: "toggle",
                attrs: attrs,
                children: [],
                text: label,
                msg: args[3],
                key: nil
            ))
        }
        env.define("uiToggle",  .fn(uiToggle))
        env.define("UI.toggle", .fn(uiToggle))

        // centered : View msg -> View msg
        // Wraps the child in a "centered" view tag — renderer maps
        // to .frame(maxWidth: .infinity, maxHeight: .infinity,
        // alignment: .center) on iOS, flex-center on web.
        let uiCentered = MarFn.native(1) { args in
            guard case .view(let child) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "View", got: Eval.typeOf(args[0]))
            }
            return .view(MarView(
                tag: "centered",
                attrs: [],
                children: [child],
                text: "",
                msg: nil,
                key: nil
            ))
        }
        env.define("uiCentered",  .fn(uiCentered))
        env.define("UI.centered", .fn(uiCentered))

        // sheet : { open, onDismiss, outlet } -> List (View msg) -> View msg
        //
        // iOS page-sheet modal. Mirrors SwiftUI's `.sheet(isPresented:)`.
        // The renderer (MarRenderer.swift) reads the open/outlet attrs
        // and the dismiss Msg, and applies `.sheet(isPresented:)` to
        // the parent view with the sheet's children as content. Same
        // semantic as web — parent owns open/closed state.
        let uiSheet = MarFn.native(2) { args in
            guard case .record(let fs, _) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "{ open, onDismiss, outlet }", got: Eval.typeOf(args[0]))
            }
            guard case .list(let kids) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "List (View msg)", got: Eval.typeOf(args[1]))
            }
            let children: [MarView] = kids.compactMap { v in
                if case .view(let mv) = v { return mv } else { return nil }
            }
            let attrs: [MarView.Attr] = [
                MarView.Attr(name: "open",   value: fs["open"] ?? .bool(false)),
                MarView.Attr(name: "outlet", value: fs["outlet"] ?? .string("")),
            ]
            return .view(MarView(
                tag: "sheet",
                attrs: attrs,
                children: children,
                text: "",
                msg: fs["onDismiss"],   // dispatched on dismissal
                key: nil
            ))
        }
        env.define("uiSheet",  .fn(uiSheet))
        env.define("UI.sheet", .fn(uiSheet))

        // UI.confirm : { title, confirmLabel, destructive,
        //                onConfirm, onCancel } -> View msg
        //
        // Modal destructive-action confirmation. iOS renderer maps
        // this to SwiftUI's `.confirmationDialog(title:isPresented:)`
        // modifier (which on iPhone slides up from the bottom, on
        // iPad anchors centered). The role: .destructive button
        // bubbles up the system red automatically.
        //
        // Both message handlers stashed as attrs because the view
        // has two dispatch paths (confirm / cancel) and MarView's
        // single `msg` slot can hold only one. The renderer reads
        // both off attrs when wiring the dialog buttons.
        let uiConfirm = MarFn.native(1) { args in
            guard case .record(let fs, _) = args[0] else {
                throw MarRuntimeError.typeMismatch(
                    expected: "{ title, confirmLabel, destructive, onConfirm, onCancel }",
                    got: Eval.typeOf(args[0]))
            }
            let attrs: [MarView.Attr] = [
                MarView.Attr(name: "title",        value: fs["title"]        ?? .string("")),
                MarView.Attr(name: "confirmLabel", value: fs["confirmLabel"] ?? .string("")),
                MarView.Attr(name: "destructive",  value: fs["destructive"]  ?? .bool(false)),
                MarView.Attr(name: "onConfirm",    value: fs["onConfirm"]    ?? .unit),
                MarView.Attr(name: "onCancel",     value: fs["onCancel"]     ?? .unit),
            ]
            return .view(MarView(
                tag: "confirmDialog",
                attrs: attrs,
                children: [],
                text: "",
                msg: nil,
                key: nil
            ))
        }
        env.define("uiConfirm",  .fn(uiConfirm))
        env.define("UI.confirm", .fn(uiConfirm))

        // Modifier attrs — produce VAttr values consumed by the
        // matching container's renderer.
        let navTitleCtor = MarFn.native(1) { args in makeAttr("navigationTitle", args[0]) }
        env.define("navigationTitle",    .fn(navTitleCtor))
        env.define("UI.navigationTitle", .fn(navTitleCtor))

        // topBarTrailing / topBarLeading — toolbar items at the
        // trailing / leading edge of the top bar. Match SwiftUI's
        // `.topBarTrailing` / `.topBarLeading` placement (iOS 17+).
        let topBarTrailingCtor = MarFn.native(1) { args in makeAttr("topBarTrailing", args[0]) }
        env.define("uiTopBarTrailing", .fn(topBarTrailingCtor))
        env.define("UI.topBarTrailing", .fn(topBarTrailingCtor))

        let topBarLeadingCtor = MarFn.native(1) { args in makeAttr("topBarLeading", args[0]) }
        env.define("uiTopBarLeading", .fn(topBarLeadingCtor))
        env.define("UI.topBarLeading", .fn(topBarLeadingCtor))

        let headerCtor = MarFn.native(1) { args in makeAttr("header", args[0]) }
        env.define("header",    .fn(headerCtor))
        env.define("UI.header", .fn(headerCtor))

        let footerCtor = MarFn.native(1) { args in makeAttr("footer", args[0]) }
        env.define("footer",    .fn(footerCtor))
        env.define("UI.footer", .fn(footerCtor))

        // numericCode bundles `numeric` + `oneTimeCode` as a single
        // flag — common OTP / 2FA case. Renderer expands it.
        env.define("numericCode",    flagAttr("inputKindNumericCode"))
        env.define("UI.numericCode", flagAttr("inputKindNumericCode"))

        // Re-expose existing input-kind / submit attrs under UI.*
        // so user code that lives entirely in the SwiftUI-style
        // vocabulary doesn't need a separate `import View`.
        env.define("UI.email",       flagAttr("inputKindEmail"))
        env.define("UI.password",    flagAttr("inputKindPassword"))
        env.define("UI.newPassword", flagAttr("inputKindNewPassword"))
        env.define("UI.numeric",     flagAttr("inputKindNumeric"))
        env.define("UI.oneTimeCode", flagAttr("inputKindOneTimeCode"))
        env.define("UI.submit",      .fn(viewSubmit))
    }

    // MARK: - Coercion helpers

    static func asInt(_ v: MarValue) -> Int {
        switch v {
        case .int(let n): return n
        case .float(let f): return Int(f)
        default: return 0
        }
    }
    static func asBool(_ v: MarValue) -> Bool {
        if case .bool(let b) = v { return b }
        return false
    }
    static func asString(_ v: MarValue) -> String {
        if case .string(let s) = v { return s }
        return ""
    }
}

