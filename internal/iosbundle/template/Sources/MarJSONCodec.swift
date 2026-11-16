// Two responsibilities here:
//
//  1. Bridge between raw JSON values (Foundation's JSONSerialization
//     output: NSNumber / String / Bool / Array / Dictionary / NSNull)
//     and MarValue. Used by JSON.encode / JSON.decode and by HTTP
//     response handling.
//
//  2. Decode the program.json wire format (Program / Module / Decl /
//     Expr / Pat) into typed Swift structures the interpreter can
//     consume. The shape is dictated by Go-side
//     `internal/jsserve.SerializeModule`; field names match exactly.
//
// Both directions are non-throwing where reasonable: malformed
// values fall back to `.unit` rather than crash, since we'd rather
// surface a runtime error in a specific place than blow up the
// whole load.

import Foundation

enum MarJSONCodec {

    // MARK: - Mar <-> JSON value bridge (used by JSON.encode/decode)

    /// Foundation JSON value → MarValue. Mirrors `jsToMar` in
    /// runtime.js: numbers → Int, strings → String, bools → Bool,
    /// null → unit, arrays → List, objects → Record.
    /// (null maps to unit, NOT to Nothing: Nothing tags uniformly
    /// via {"__ctor":"Nothing"} so it doesn\'t collide with VUnit\'s
    /// null encoding.)
    /// parseISO reads an ISO 8601 instant with OR without fractional
    /// seconds. `.withFractionalSeconds` REQUIRES them, so asking for it
    /// alone would refuse every value written before the wire form carried
    /// milliseconds — which is everything already stored or in flight.
    static func parseISO(_ iso: String) -> Date? {
        let withFraction = ISO8601DateFormatter()
        withFraction.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = withFraction.date(from: iso) { return d }
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        return plain.date(from: iso)
    }

    /// formatISO writes one, always with three decimal places, matching the
    /// Go encoder and JavaScript's toISOString.
    static func formatISO(_ millis: Int) -> String {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f.string(from: Date(timeIntervalSince1970: Double(millis) / 1000))
    }

    /// sameKindAsExisting reports whether a new key or item can share a
    /// collection with what is already there.
    ///
    /// A Set and a Dict are SORTED, so building one compares its members.
    /// Go and JS fail that comparison outright; Swift's search does not, so
    /// it built a set of mixed types and handed it on. All three now refuse
    /// the payload, with one sentence.
    static func sameKindAsExisting(_ v: MarValue, in collection: MarValue) -> Bool {
        var first: MarValue?
        switch collection {
        case .set(let items):  first = items.first
        case .dict(let pairs): first = pairs.first?.key
        default: return true
        }
        guard let existing = first else { return true }
        return kindTag(v) == kindTag(existing)
    }

    private static func kindTag(_ v: MarValue) -> String {
        switch v {
        case .int:     return "int"
        case .string:  return "string"
        case .bool:    return "bool"
        case .char:    return "char"
        case .decimal: return "decimal"
        case .time:    return "time"
        case .angle:   return "angle"
        case .list:    return "list"
        case .tuple:   return "tuple"
        case .record:  return "record"
        case .ctor:    return "ctor"
        default:       return "other"
        }
    }

    static func plainWire(_ any: Any?) -> String {
        if let s = any as? String { return s }
        if let n = any as? NSNumber { return formatWireNumber(n.doubleValue) }
        return "null"
    }

    /// formatWireNumber prints a rejected payload the way the other two
    /// runtimes do, so the corpus compares one sentence rather than three.
    static func formatWireNumber(_ d: Double) -> String {
        if d == d.rounded(.towardZero) && abs(d) < 1e15 {
            return String(Int(d))
        }
        return String(d)
    }

    static func jsonToMar(_ any: Any) throws -> MarValue {
        if any is NSNull { return .unit }
        if let n = any as? NSNumber {
            // Distinguish Bool from numeric: NSNumber wraps both
            // and `is Bool` only catches CFBoolean.
            if CFGetTypeID(n) == CFBooleanGetTypeID() {
                return .bool(n.boolValue)
            }
            let d = n.doubleValue
            if d == d.rounded(.towardZero) && abs(d) < 9_007_199_254_740_992 {
                return .int(n.intValue)
            }
            // A JSON number decodes into an exact Decimal, and the digits come
            // from decimalValue rather than from doubleValue.
            //
            // This used to render the DOUBLE back to a string, which loses the
            // number before Mar ever sees it. Measured on the same document:
            //
            //   {"v":1000000000000000000000000000000}
            //     doubleValue    9.999999999999999e+29   <- not the number sent
            //     decimalValue   1000000000000000000000000000000
            //
            // NSDecimal is base-10, so it keeps what the sender wrote, and
            // both spellings of one value -- 1e30 and the thirty-one digits --
            // arrive as the same Decimal. Notation is not information.
            if let dec = DecMath.parseDecimalString("\(n.decimalValue)") {
                return .decimal(dec)
            }
            // Nothing else is a number in Mar. `.float` used to catch this;
            // the case is gone now, since with this boundary closed nothing
            // could construct one.
            throw MarRuntimeError.message(
                "JSON: \(n.decimalValue) is not a number Mar can hold exactly:"
                + " an Int is at most 9007199254740991 and a Decimal at most 34 significant digits and 34 decimal places")
        }
        if let s = any as? String { return .string(s) }
        if let b = any as? Bool { return .bool(b) }
        if let arr = any as? [Any] {
            return .list(try arr.map(jsonToMar))
        }
        if let dict = any as? [String: Any] {
            // A number literal lifted out of the text by parseJSONExact, so
            // the digits the sender wrote are what gets read.
            if dict.count == 1, let lit = dict["__num"] as? String {
                return try numberFromLiteral(lit)
            }
            // Tagged constructor, round-trip from {__ctor: "Tag"} or
            // {__ctor: "Tag", __args: [...]}. Same convention as the
            // Go encoders and the JS jsToMar.
            if let tag = dict["__ctor"] as? String {
                let args: [MarValue]
                if let raw = dict["__args"] as? [Any] {
                    args = try raw.map(jsonToMar)
                } else {
                    args = []
                }
                return .ctor(tag: tag, args: args)
            }
            // Char round-trip, `{__char: "x"}`, and exactly one Unicode
            // scalar or nothing. Same convention as Go's convertJSON and
            // JS jsToMar.
            //
            // This used to take the first scalar and fall back to U+FFFD
            // for an empty payload, on the grounds that the producer is
            // trusted. It is not: the input is EXTERNAL, and a Mar
            // encoder cannot write more than one scalar. So
            // `{"__char":"abc"}` answered 'a' silently, and an empty
            // payload answered U+FFFD here and in the browser while the
            // server errored. Pinned by the Decode block of the corpus.
            if let s = dict["__char"] as? String, dict.count == 1 {
                let scalars = Array(s.unicodeScalars)
                if scalars.count != 1 {
                    throw MarRuntimeError.message(
                        "the value should be a single character, got \(scalars.count)")
                }
                return .char(scalars[0])
            }
            // Decimal round-trip, `{__dec: "1.50"}`. Rebuilt textually
            // so the exact coefficient + scale survive; same convention
            // as the Go and JS decoders.
            if dict["__dec"] != nil, dict.count == 1 {
                guard let s = dict["__dec"] as? String,
                      let dec = DecMath.parseDecimalString(s) else {
                    throw MarRuntimeError.message(
                        "the value should be a decimal number, got `\(plainWire(dict["__dec"]))`")
                }
                return .decimal(dec)
            }
            // Angle round-trip, `{__angle: 450}`, deci-degrees. Wrapped
            // rather than trusted: the payload crossed a network boundary,
            // and every other way to build an Angle normalizes.
            // Deci-degrees are integral by construction and no Mar encoder
            // writes a fraction, so a fractional payload is refused rather
            // than quietly falling through to a record — the same rule
            // `__char` follows, and the same sentence in all three
            // runtimes. Pinned by the Decode block of the corpus.
            // rawNumberOf, not `as? NSNumber`: after parseJSONExact every
            // number in the document is a lifted literal, so the tagged forms
            // that carry one read it the same way. __angle is the only one --
            // __dec, __char and __time are strings, __dict and __set arrays --
            // and missing it turned every Angle on the wire into "got an
            // object with __angle".
            if let d = rawNumberOf(dict["__angle"]), dict.count == 1 {
                guard d == d.rounded(.towardZero), let i = Int(exactly: d) else {
                    throw MarRuntimeError.message(
                        "the value should be a whole number of deci-degrees, got \(formatWireNumber(d))")
                }
                return .angle(MarMath.posMod(i, MarMath.deciFull))
            }
            // Time round-trip, `{__time: "ISO 8601"}` rebuilds a
            // VTime so user code typed as `createdAt : Time`
            // actually receives a Time, not a String.
            // A tagged value that will not rebuild is a decode failure, not
            // a record. Falling through left `{"__time":"nope"}` as a plain
            // record here and in the browser while the server errored — and
            // a Set or Dict that fell through CRASHED the first operation on
            // it, because the shape reduction has no kind for either.
            if dict["__time"] != nil {
                guard let iso = dict["__time"] as? String, let d = parseISO(iso) else {
                    throw MarRuntimeError.message(
                        "the value should be a date and time, got `\(plainWire(dict["__time"]))`")
                }
                return .time(Int(d.timeIntervalSince1970 * 1000))
            }
            // Dict / Set round-trip. Wire format mirrors the Go and JS
            // sides exactly: `{__dict: [[k, v], ...]}` and
            // `{__set: [k, ...]}`. We rebuild via the runtime's
            // insert helpers so the sorted invariant survives even
            // when the payload arrives out of order.
            if dict["__dict"] != nil {
                guard let pairs = dict["__dict"] as? [Any] else {
                    throw MarRuntimeError.message("the value should be a list of key/value pairs")
                }
                var d = MarValue.dict([])
                for p in pairs {
                    guard let arr = p as? [Any], arr.count == 2 else {
                        throw MarRuntimeError.message("the value should be a key/value pair of two items")
                    }
                    // A dictionary is sorted by key, so building one
                    // compares the keys; mixed types failed with a message
                    // about comparison rather than about the request.
                    let k = try jsonToMar(arr[0])
                    guard sameKindAsExisting(k, in: d) else {
                        throw MarRuntimeError.message(
                            "the value should be a list of key/value pairs whose keys are one type")
                    }
                    d = MarDict.insert(d, k, try jsonToMar(arr[1]))
                }
                return d
            }
            if dict["__set"] != nil {
                guard let items = dict["__set"] as? [Any] else {
                    throw MarRuntimeError.message("the value should be a list of items")
                }
                var s = MarValue.set([])
                for it in items {
                    let item = try jsonToMar(it)
                    guard sameKindAsExisting(item, in: s) else {
                        throw MarRuntimeError.message("the value should be a list of items of one type")
                    }
                    s = MarDict.setInsert(s, item)
                }
                return s
            }
            var fields: [String: MarValue] = [:]
            var order: [String] = []
            for k in dict.keys.sorted() {
                fields[k] = try jsonToMar(dict[k] as Any)
                order.append(k)
            }
            return .record(fields: fields, order: order)
        }
        return .string(String(describing: any))
    }

    /// The first value inside this one that has no JSON form, or nil.
    ///
    /// marToJSON cannot throw, so it DEGRADED: a division, a function, a View
    /// and an Effect each became `null` on the way out, silently. The server
    /// refuses all four. Four values that a receiver would read as "the field
    /// was null" when what happened is that the field could not be written.
    ///
    /// The refusal has to be decided before the walk rather than during it,
    /// which is why this exists as its own pass.
    static func unencodable(_ v: MarValue) -> MarValue? {
        switch v {
        case .division, .fn, .view, .effect:
            return v
        case .list(let xs), .tuple(let xs):
            for x in xs {
                if let bad = unencodable(x) { return bad }
            }
            return nil
        case .record(let fs, _):
            for f in fs.values {
                if let bad = unencodable(f) { return bad }
            }
            return nil
        case .ctor(_, let args):
            for a in args {
                if let bad = unencodable(a) { return bad }
            }
            return nil
        default:
            // `.unit` is NOT here: () encodes as null on every runtime, which
            // is a form and not an absence of one.
            return nil
        }
    }

    /// The sentence the other two runtimes give for a value with no JSON form.
    /// A division gets its own, because its answer is not "encode something
    /// else" but "resolve it first".
    static func noJSONFormMessage(_ v: MarValue) -> String {
        if case .division = v {
            return "JSON.encode: cannot encode a Decimal.Division — resolve it with "
                + "Decimal.rounded or Decimal.withRemainder first"
        }
        let kind: String
        switch v {
        case .fn:     kind = "a function"
        case .view:   kind = "a View"
        case .effect: kind = "a Cmd or Task"
        default:      kind = "this value"
        }
        return "JSON.encode: \(kind) has no JSON form.\n"
            + "  Encode the data it was built FROM, and build it on the side that uses it"
    }

    /// MarValue → Foundation JSON value. Mirrors `marToJs` in runtime.js
    /// and `appendValue` in internal/runtime/json.go: every constructor,
    /// Maybe and Result included, as `{"__args": [...], "__ctor": "Tag"}`.
    static func marToJSON(_ v: MarValue) -> Any {
        switch v {
        case .int(let n): return n
        // The value rides as a STRING under the __dec marker so no
        // JSON parser on any side routes the digits through binary
        // floating point.
        case .decimal(let d): return ["__dec": DecMath.decString(d)] as [String: Any]
        // Division never crosses a boundary (typed code can't ship
        // it); this codec is non-throwing, so degrade to null rather
        // than crash.
        case .division: return NSNull()
        case .string(let s): return s
        case .bool(let b): return b
        case .unit: return NSNull()
        case .duration(let s): return s     // Duration → seconds (whole renders as int, sub-second keeps the fraction)
        case .angle(let d): return ["__angle": d] as [String: Any]
        case .time(let ms):
            return ["__time": formatISO(ms)] as [String: Any]
        case .list(let xs): return xs.map(marToJSON)
        case .tuple(let xs): return xs.map(marToJSON)
        case .record(let fs, let order):
            var dict: [String: Any] = [:]
            let keys = order.isEmpty ? Array(fs.keys) : order
            for k in keys {
                dict[k] = marToJSON(fs[k] ?? .unit)
            }
            return dict
        case .ctor(let tag, let args):
            // Every ctor, Maybe and Result included, uses the
            // {"__ctor": ...} marker, as the Go and JS encoders do, so
            // the generic decoders on all three runtimes rebuild it
            // without type information. A transparent form collides:
            // Nothing → null with .unit, Just x → x with x, Ok x → x
            // and Err e → {"error": e} with x and with a record that
            // has an `error` field.
            if args.isEmpty {
                return ["__ctor": tag] as [String: Any]
            }
            return ["__ctor": tag, "__args": args.map(marToJSON)] as [String: Any]
        case .char(let scalar):
            // `{"__char": "x"}`, wraps the scalar in a 1-char string
            // so non-Mar JSON consumers can still read it as text.
            return ["__char": String(scalar)] as [String: Any]
        case .dict(let pairs):
            // `{"__dict": [[k, v], ...]}`, marker matches the Go and
            // JS encoders. Pairs ride as 2-element arrays so non-
            // String keys (Int / Float) round-trip too.
            let arr = pairs.map { [marToJSON($0.key), marToJSON($0.value)] as [Any] }
            return ["__dict": arr] as [String: Any]
        case .set(let items):
            return ["__set": items.map(marToJSON)] as [String: Any]
        case .fn, .view, .effect:
            return NSNull()
        }
    }

    // MARK: - Exact numbers

    /// Parses a document WITHOUT letting Foundation's number parser see the
    /// numbers.
    ///
    /// JSONSerialization hands back an NSNumber, and two things go wrong there.
    /// `doubleValue` is not the number that was sent -- measured, on the same
    /// document:
    ///
    ///     {"v":1000000000000000000000000000000}
    ///       doubleValue    9.999999999999999e+29
    ///
    /// And a number no Double can hold makes it refuse the WHOLE DOCUMENT, so
    /// the phone reported a valid document as invalid and could not say which
    /// number was the problem, because it never saw one.
    ///
    /// So the number literals are lifted out of the text first, as
    /// {"__num": "<the literal>"}, and read as digits. Structure, escapes and
    /// syntax errors stay Foundation's job. Mirrors parseJSONExact in
    /// internal/jsserve/runtime.js and the wire path in internal/runtime/json.go.
    ///
    /// The scan only has to know one thing to be correct: outside a string,
    /// JSON has no digits except in numbers. `true`, `false` and `null` have
    /// none.
    static func liftNumberLiterals(_ text: String) -> String {
        var out = ""
        let chars = Array(text)
        var i = 0
        while i < chars.count {
            let c = chars[i]
            if c == "\"" {
                var j = i + 1
                while j < chars.count {
                    if chars[j] == "\\" { j += 2; continue }
                    if chars[j] == "\"" { j += 1; break }
                    j += 1
                }
                out += String(chars[i..<min(j, chars.count)])
                i = j
                continue
            }
            if c == "-" || (c >= "0" && c <= "9") {
                var j = i
                if chars[j] == "-" { j += 1 }
                while j < chars.count, "0123456789.eE+-".contains(chars[j]) { j += 1 }
                let lit = String(chars[i..<j])
                out += "{\"__num\":\"" + lit + "\"}"
                i = j
                continue
            }
            out.append(c)
            i += 1
        }
        return out
    }

    /// Reads a JSON number that may have been lifted out of the text, and
    /// answers nil for anything that is not a number at all.
    static func rawNumberOf(_ any: Any?) -> Double? {
        if let n = any as? NSNumber, CFGetTypeID(n) != CFBooleanGetTypeID() {
            return n.doubleValue
        }
        if let d = any as? [String: Any], d.count == 1, let lit = d["__num"] as? String {
            return Double(lit)
        }
        return nil
    }

    static func parseJSONExact(_ text: String) throws -> Any {
        return try JSONSerialization.jsonObject(
            with: Data(liftNumberLiterals(text).utf8),
            options: [.fragmentsAllowed]
        )
    }

    /// Turns a JSON number's own text into the Mar value it names: an Int when
    /// it is whole and inside 53 bits, an exact Decimal when it fits 34
    /// significant digits and 34 places, and nothing otherwise.
    ///
    /// Nothing else is a number in Mar. `.float` used to catch the rest; the
    /// case is gone now, because with this boundary closed nothing could
    /// construct one. It had no type either -- a checked program could never
    /// hold one -- and existed only to reach a shape check and fail there.
    static func numberFromLiteral(_ lit: String) throws -> MarValue {
        if isPlainInteger(lit), let n = Int(lit), MarInt.inRange(n) {
            return .int(n)
        }
        if let dec = decimalFromWireNumber(lit) {
            return .decimal(dec)
        }
        throw MarRuntimeError.message(
            "JSON: \(lit) is not a number Mar can hold exactly:"
            + " an Int is at most \(MarInt.max) and a Decimal at most 34 significant digits and 34 decimal places")
    }

    private static func isPlainInteger(_ s: String) -> Bool {
        var body = Substring(s)
        if body.hasPrefix("-") { body = body.dropFirst() }
        return !body.isEmpty && body.allSatisfy { $0.isNumber }
    }

    /// Reads a JSON number's text into an exact Decimal, applying the exponent
    /// by MOVING THE POINT, never by multiplying a Double -- which is the
    /// approximation this path exists to avoid. 1e30 becomes a 1 and thirty
    /// zeros.
    ///
    /// The exponent is the sender's, so nothing is built from it before the
    /// bounds are known to hold: reading 1e-999999999 used to start by
    /// allocating a billion "0"s, and an exponent of Int.max or Int.min
    /// overflowed the point arithmetic, which traps in Swift and closed the
    /// app. Same steps and answers as decimalFromWireNumber in
    /// internal/runtime/decimal.go; nil where that one errors.
    static func decimalFromWireNumber(_ lit: String) -> MarDec? {
        var mantissa = Substring(lit)
        var exp = 0
        if let e = lit.firstIndex(where: { $0 == "e" || $0 == "E" }) {
            mantissa = lit[lit.startIndex..<e]
            // An exponent outside 64 bits is one the server cannot read.
            guard let n = Int(lit[lit.index(after: e)...]) else { return nil }
            exp = n
        }
        var neg = false
        // One sign, as Go's splitDecimalText takes it: two `if`s read -+5 as -5.
        if mantissa.hasPrefix("-") {
            neg = true
            mantissa = mantissa.dropFirst()
        } else if mantissa.hasPrefix("+") {
            mantissa = mantissa.dropFirst()
        }
        let parts = mantissa.split(separator: ".", maxSplits: 1, omittingEmptySubsequences: false)
        let intPart = parts.first ?? ""
        let frac = parts.count > 1 ? parts[1] : ""
        if intPart.isEmpty && frac.isEmpty { return nil }
        var digits: [UInt8] = []
        for ch in intPart + frac {
            guard let d = ch.wholeNumberValue, d >= 0, d <= 9 else { return nil }
            digits.append(UInt8(d))
        }
        let sig = DecMath.trim(digits)
        let sigCount = sig == [0] ? 0 : sig.count
        // Below -(34 + the digits) the number keeps more than 34 places
        // whatever zeros its digits end with; a zero fits, at 34. Asked before
        // the subtraction, which would trap for an exponent near Int.min.
        if exp < -(DecMath.maxDecimalPlaces + sigCount) {
            return sigCount == 0 ? MarDec(negative: false, digits: [0], scale: DecMath.maxDecimalPlaces) : nil
        }
        let scale = frac.count - exp
        if scale >= 0 { return DecMath.decimalFromDigits(negative: neg, sig, scale) }
        // The point moved past the last digit: zeros before the point count,
        // and the padding is refused before it is built. Zero needs none.
        if sigCount == 0 { return MarDec.zero }
        if -scale > DecMath.maxDigits - sigCount { return nil }
        return MarDec(negative: neg, digits: sig + [UInt8](repeating: 0, count: -scale), scale: 0)
    }

    // MARK: - Program decoding (program.json → typed Swift)

    /// Parse a top-level program.json blob into a Program value.
    /// Throws on malformed shape (missing required fields, wrong
    /// types): caller surfaces the error to the user via the
    /// AppViewModel's failed state.
    static func decodeProgram(_ data: Data) throws -> Program {
        let any = try JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed])
        guard let dict = any as? [String: Any] else {
            throw MarRuntimeError.message("program.json is not an object")
        }
        let entry = (dict["entry"] as? String) ?? "main"
        // Modern wire format: `modules` is a list. Backward-compat:
        // older bundles sent a single `module`.
        var rawModules: [[String: Any]] = []
        if let list = dict["modules"] as? [[String: Any]] {
            rawModules = list
        } else if let single = dict["module"] as? [String: Any] {
            rawModules = [single]
        } else {
            throw MarRuntimeError.message("program.json missing `modules` (or legacy `module`)")
        }
        let modules = try rawModules.map(decodeModule)
        // Auth metadata baked by the server (see makeProgramJSON in
        // internal/jsserve/livereload.go). Main.mar isn't in the
        // mobile bundle either, so the server resolves Auth.config
        // and ships just the bits the client needs.
        var signInPath = ""
        if let auth = dict["auth"] as? [String: Any],
           let path = auth["signInPath"] as? String {
            signInPath = path
        }
        return Program(modules: modules, entry: entry, authSignInPath: signInPath)
    }

    private static func decodeModule(_ any: [String: Any]) throws -> Module {
        let name = (any["name"] as? [String]) ?? []
        let imports = (any["imports"] as? [[String: Any]] ?? []).map(decodeImport)
        let decls = try (any["decls"] as? [[String: Any]] ?? []).map(decodeDecl)
        return Module(name: name, imports: imports, decls: decls)
    }

    private static func decodeImport(_ any: [String: Any]) -> Import {
        let module = (any["module"] as? [String]) ?? []
        let exposingArr = any["exposing"] as? [Any] ?? []
        let exposing: [String] = exposingArr.compactMap { item in
            if let s = item as? String { return s }
            // Server may emit either bare strings or {name: "..."}
            // shapes depending on version: accept both.
            if let dict = item as? [String: Any], let n = dict["name"] as? String { return n }
            return nil
        }
        let all = (any["all"] as? Bool) ?? false
        return Import(module: module, exposing: exposing, all: all)
    }

    private static func decodeDecl(_ any: [String: Any]) throws -> Decl {
        switch any["kind"] as? String {
        case "ValueDecl":
            let name = (any["name"] as? String) ?? ""
            let params = try (any["params"] as? [[String: Any]] ?? []).map(decodePat)
            let body = try decodeExpr(any["body"] ?? [String: Any]())
            return .value(name: name, params: params, body: body)
        case "CustomTypeDecl":
            let typeName = (any["name"] as? String) ?? ""
            let ctors = (any["constructors"] as? [[String: Any]] ?? []).map { c -> CtorDecl in
                CtorDecl(name: (c["name"] as? String) ?? "",
                         argCount: (c["argCount"] as? Int) ?? 0)
            }
            return .customType(name: typeName, constructors: ctors)
        default:
            return .other
        }
    }

    // MARK: - Expr / Pat

    static func decodeExpr(_ any: Any) throws -> Expr {
        guard let dict = any as? [String: Any] else {
            throw MarRuntimeError.message("expr is not an object")
        }
        let kind = dict["kind"] as? String ?? ""
        switch kind {
        case "EInt":
            return .int(intOf(dict["value"]))
        case "EDecimal":
            return .decimal(coef: stringOf(dict["coef"]),
                            scale: intOf(dict["scale"]))
        case "EString":
            return .string(stringOf(dict["value"]))
        case "EChar":
            // Go side ships the int code point; we sanitize and wrap
            // in Unicode.Scalar. Invalid (surrogate / out-of-range)
            // values fall back to U+FFFD: same contract as the JS
            // side's sanitizeCodePoint.
            return .char(scalarFromCode(intOf(dict["value"])))
        case "EUnit":
            return .unit
        case "EVar":
            if let impl = elaboratedImpl(dict) { return .var(impl) }
            return .var(stringOf(dict["name"]))
        case "ECtor":
            return .ctor(module: stringList(dict["module"]),
                         name: stringOf(dict["name"]))
        case "EQualified":
            if let impl = elaboratedImpl(dict) { return .var(impl) }
            // What the shape MEANS depends on which builtin this is, so the
            // name is checked and not merely the mark's presence: reading a
            // Service.call's response shape as a decoder would replace a
            // network call with a JSON parser.
            if let shape = MarWireShape.decode(dict["wire"]) {
                let module = stringList(dict["module"]).joined(separator: ".")
                let qualified = module.isEmpty ? stringOf(dict["name"]) : module + "." + stringOf(dict["name"])
                if qualified == "JSON.decode" { return .jsonDecode(shape: shape) }
                if qualified == "Service.call" { return .serviceCall(shape: shape) }
                if qualified == "Auth.verifyCode" { return .authVerify(shape: shape) }
                if qualified == "Auth.me" { return .authMe(shape: shape) }
            }
            return .qualified(module: stringList(dict["module"]),
                              name: stringOf(dict["name"]))
        case "ENegate":
            return .negate(try decodeExpr(dict["inner"] ?? [:]))
        case "EApp":
            return .app(fn: try decodeExpr(dict["fn"] ?? [:]),
                        arg: try decodeExpr(dict["arg"] ?? [:]))
        case "EBinop":
            return .binop(op: stringOf(dict["op"]),
                          left: try decodeExpr(dict["left"] ?? [:]),
                          right: try decodeExpr(dict["right"] ?? [:]))
        case "ELambda":
            let params = try (dict["params"] as? [[String: Any]] ?? []).map(decodePat)
            return .lambda(params: params, body: try decodeExpr(dict["body"] ?? [:]))
        case "EIf":
            return .ifExpr(cond: try decodeExpr(dict["cond"] ?? [:]),
                           then: try decodeExpr(dict["then"] ?? [:]),
                           else_: try decodeExpr(dict["else"] ?? [:]))
        case "ELet":
            let bindings = try (dict["bindings"] as? [[String: Any]] ?? []).map { b -> LetBinding in
                LetBinding(
                    pattern: try decodePat(b["pattern"] as? [String: Any] ?? [:]),
                    body: try decodeExpr(b["body"] ?? [:])
                )
            }
            return .letExpr(bindings: bindings, body: try decodeExpr(dict["body"] ?? [:]))
        case "ETuple":
            return .tuple(try (dict["members"] as? [Any] ?? []).map(decodeExpr))
        case "EList":
            return .list(try (dict["elements"] as? [Any] ?? []).map(decodeExpr))
        case "ERecord":
            let fields = try (dict["fields"] as? [[String: Any]] ?? []).map { f -> RecordField in
                RecordField(name: stringOf(f["name"]),
                            value: try decodeExpr(f["value"] ?? [:]))
            }
            return .record(fields)
        case "ERecordUpdate":
            let fields = try (dict["fields"] as? [[String: Any]] ?? []).map { f -> RecordField in
                RecordField(name: stringOf(f["name"]),
                            value: try decodeExpr(f["value"] ?? [:]))
            }
            return .recordUpdate(record: try decodeExpr(dict["record"] ?? [:]),
                                 fields: fields)
        case "EFieldAccess":
            return .fieldAccess(record: try decodeExpr(dict["record"] ?? [:]),
                                field: stringOf(dict["field"]))
        case "EFieldAccessor":
            return .fieldAccessor(field: stringOf(dict["field"]))
        case "ECase":
            let branches = try (dict["branches"] as? [[String: Any]] ?? []).map { b -> CaseBranch in
                CaseBranch(
                    pattern: try decodePat(b["pattern"] as? [String: Any] ?? [:]),
                    body: try decodeExpr(b["body"] ?? [:])
                )
            }
            return .caseExpr(subject: try decodeExpr(dict["subject"] ?? [:]),
                             branches: branches)
        default:
            throw MarRuntimeError.message("unknown expr kind: \(kind)")
        }
    }

    static func decodePat(_ any: [String: Any]) throws -> Pat {
        let kind = any["kind"] as? String ?? ""
        switch kind {
        case "PWildcard":   return .wildcard
        case "PVar":        return .var(stringOf(any["name"]))
        case "PInt":        return .int(intOf(any["value"]))
        case "PString":     return .string(stringOf(any["value"]))
        case "PChar":       return .char(scalarFromCode(intOf(any["value"])))
        case "PUnit":       return .unit
        case "PCtor":
            let args = try (any["args"] as? [[String: Any]] ?? []).map(decodePat)
            return .ctor(name: stringOf(any["name"]), args: args)
        case "PTuple":
            let members = try (any["members"] as? [[String: Any]] ?? []).map(decodePat)
            return .tuple(members)
        case "PList":
            let elements = try (any["elements"] as? [[String: Any]] ?? []).map(decodePat)
            return .list(elements)
        case "PCons":
            return .cons(head: try decodePat(any["head"] as? [String: Any] ?? [:]),
                         tail: try decodePat(any["tail"] as? [String: Any] ?? [:]))
        case "PRecord":
            // Field names arrive as a [String]; in the JSON they're
            // typed as Any because the wire format uses []any. Coerce
            // each to String, dropping any that aren't (defensive:
            // the server should only ever emit strings here).
            let raw = (any["fields"] as? [Any]) ?? []
            let fields = raw.compactMap { $0 as? String }
            return .record(fields: fields)
        default:
            throw MarRuntimeError.message("unknown pattern kind: \(kind)")
        }
    }

    // MARK: - Tiny coercion helpers

    private static func stringOf(_ v: Any?) -> String {
        v as? String ?? ""
    }
    private static func intOf(_ v: Any?) -> Int {
        if let n = v as? Int { return n }
        if let n = v as? NSNumber { return n.intValue }
        if let d = v as? Double { return Int(d) }
        return 0
    }
    private static func stringList(_ v: Any?) -> [String] {
        v as? [String] ?? []
    }

    /// `impl` is the native implementation the typechecker chose for this
    /// reference (ast.EVar.Impl / ast.EQualified.Impl): today only a
    /// Decimal List.sum / List.product, whose empty-list zero the values
    /// cannot reveal. The Go runtime reads the flag off the shared AST; the
    /// wire has no shared AST, so this side resolves the reference at decode
    /// and hands the evaluator a plain name. Same decision, one step earlier.
    private static func elaboratedImpl(_ dict: [String: Any]) -> String? {
        guard let impl = dict["impl"] as? String, !impl.isEmpty else { return nil }
        return impl
    }

    /// Build a Unicode.Scalar from an Int code point, substituting
    /// U+FFFD for out-of-range or surrogate values. Mirrors the
    /// runtime/char.go sanitizeCodePoint and JS sanitizeCodePoint.
    /// Force-unwrap on U+FFFD is safe: it's a valid scalar.
    static func scalarFromCode(_ n: Int) -> Unicode.Scalar {
        if n < 0 || n > 0x10FFFF { return Unicode.Scalar(0xFFFD)! }
        if n >= 0xD800 && n <= 0xDFFF { return Unicode.Scalar(0xFFFD)! }
        return Unicode.Scalar(UInt32(n)) ?? Unicode.Scalar(0xFFFD)!
    }
}
