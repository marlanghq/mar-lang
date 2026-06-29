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
// Both directions are non-throwing where reasonable — malformed
// values fall back to `.unit` rather than crash, since we'd rather
// surface a runtime error in a specific place than blow up the
// whole load.

import Foundation

enum MarJSONCodec {

    // MARK: - Mar <-> JSON value bridge (used by JSON.encode/decode)

    /// Foundation JSON value → MarValue. Mirrors `jsToMar` in
    /// runtime.js: numbers → Int, strings → String, bools → Bool,
    /// null → unit, arrays → List, objects → Record.
    /// (null maps to unit, NOT to Nothing — Nothing tags uniformly
    /// via {"__ctor":"Nothing"} so it doesn\'t collide with VUnit\'s
    /// null encoding.)
    static func jsonToMar(_ any: Any) -> MarValue {
        if any is NSNull { return .unit }
        if let n = any as? NSNumber {
            // Distinguish Bool from numeric — NSNumber wraps both
            // and `is Bool` only catches CFBoolean.
            if CFGetTypeID(n) == CFBooleanGetTypeID() {
                return .bool(n.boolValue)
            }
            return .int(n.intValue)
        }
        if let s = any as? String { return .string(s) }
        if let b = any as? Bool { return .bool(b) }
        if let arr = any as? [Any] {
            return .list(arr.map(jsonToMar))
        }
        if let dict = any as? [String: Any] {
            // Tagged constructor — round-trip from {__ctor: "Tag"} or
            // {__ctor: "Tag", __args: [...]}. Same convention as the
            // Go encoders and the JS jsToMar.
            if let tag = dict["__ctor"] as? String {
                let args: [MarValue]
                if let raw = dict["__args"] as? [Any] {
                    args = raw.map(jsonToMar)
                } else {
                    args = []
                }
                return .ctor(tag: tag, args: args, origin: nil)
            }
            // Char round-trip — `{__char: "x"}` rebuilds a .char from
            // the first Unicode scalar of the payload. Same convention
            // as Go's convertJSON and JS jsToMar. If the scalar isn't
            // a valid Unicode.Scalar (shouldn't happen — the producer
            // is trusted), we fall back to U+FFFD.
            if let s = dict["__char"] as? String, dict.count == 1 {
                if let scalar = s.unicodeScalars.first {
                    return .char(scalar)
                }
                return .char(Unicode.Scalar(0xFFFD)!)
            }
            // Time round-trip — `{__time: "ISO 8601"}` rebuilds a
            // VTime so user code typed as `createdAt : Time`
            // actually receives a Time, not a String.
            if let iso = dict["__time"] as? String {
                let f = ISO8601DateFormatter()
                f.formatOptions = [.withInternetDateTime]
                if let d = f.date(from: iso) {
                    return .time(Int(d.timeIntervalSince1970 * 1000))
                }
            }
            // Dict / Set round-trip. Wire format mirrors the Go and JS
            // sides exactly: `{__dict: [[k, v], ...]}` and
            // `{__set: [k, ...]}`. We rebuild via the runtime's
            // insert helpers so the sorted invariant survives even
            // when the payload arrives out of order.
            if let pairs = dict["__dict"] as? [Any] {
                var d = MarValue.dict([])
                for p in pairs {
                    guard let arr = p as? [Any], arr.count == 2 else { continue }
                    d = MarDict.insert(d, jsonToMar(arr[0]), jsonToMar(arr[1]))
                }
                return d
            }
            if let items = dict["__set"] as? [Any] {
                var s = MarValue.set([])
                for it in items {
                    s = MarDict.setInsert(s, jsonToMar(it))
                }
                return s
            }
            var fields: [String: MarValue] = [:]
            var order: [String] = []
            for k in dict.keys.sorted() {
                fields[k] = jsonToMar(dict[k] as Any)
                order.append(k)
            }
            return .record(fields: fields, order: order)
        }
        return .string(String(describing: any))
    }

    /// MarValue → Foundation JSON value. Mirrors `marToJs` in
    /// runtime.js: Maybe.Just/Nothing/Result.Ok/Err unwrap; other
    /// ctors serialize as `{tag, args}`.
    static func marToJSON(_ v: MarValue) -> Any {
        switch v {
        case .int(let n): return n
        case .float(let f): return f
        case .string(let s): return s
        case .bool(let b): return b
        case .unit: return NSNull()
        case .duration(let s): return s     // Duration → seconds (Int)
        case .time(let ms):
            let d = Date(timeIntervalSince1970: TimeInterval(ms) / 1000)
            let f = ISO8601DateFormatter()
            f.formatOptions = [.withInternetDateTime]
            return ["__time": f.string(from: d)] as [String: Any]
        case .list(let xs): return xs.map(marToJSON)
        case .tuple(let xs): return xs.map(marToJSON)
        case .record(let fs, let order):
            var dict: [String: Any] = [:]
            let keys = order.isEmpty ? Array(fs.keys) : order
            for k in keys {
                dict[k] = marToJSON(fs[k] ?? .unit)
            }
            return dict
        case .ctor(let tag, let args, _):
            // Every ctor — Nothing and Just included — uses the
            // {"__ctor": ...} marker so generic decoders can
            // round-trip them without collisions (transparent
            // Nothing → null would clash with .unit\'s null
            // encoding; transparent Just x → x would break
            // generic decoders on record payloads). Ok/Err keep
            // the convenience shape because the server-side
            // decode for those tags is type-directed already.
            switch tag {
            case "Ok":  return args.first.map(marToJSON) ?? NSNull()
            case "Err": return ["error": args.first.map(marToJSON) ?? NSNull()]
            default:
                if args.isEmpty {
                    return ["__ctor": tag] as [String: Any]
                }
                return ["__ctor": tag, "__args": args.map(marToJSON)] as [String: Any]
            }
        case .char(let scalar):
            // `{"__char": "x"}` — wraps the scalar in a 1-char string
            // so non-Mar JSON consumers can still read it as text.
            return ["__char": String(scalar)] as [String: Any]
        case .dict(let pairs):
            // `{"__dict": [[k, v], ...]}` — marker matches the Go and
            // JS encoders. Pairs ride as 2-element arrays so non-
            // String keys (Int / Float) round-trip too.
            let arr = pairs.map { [marToJSON($0.0), marToJSON($0.1)] as [Any] }
            return ["__dict": arr] as [String: Any]
        case .set(let items):
            return ["__set": items.map(marToJSON)] as [String: Any]
        case .fn, .view, .effect:
            return NSNull()
        }
    }

    // MARK: - Program decoding (program.json → typed Swift)

    /// Parse a top-level program.json blob into a Program value.
    /// Throws on malformed shape (missing required fields, wrong
    /// types) — caller surfaces the error to the user via the
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
            // shapes depending on version — accept both.
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
        case "EFloat":
            if let d = dict["value"] as? Double { return .float(d) }
            return .float(Double(intOf(dict["value"])))
        case "EString":
            return .string(stringOf(dict["value"]))
        case "EChar":
            // Go side ships the int code point; we sanitize and wrap
            // in Unicode.Scalar. Invalid (surrogate / out-of-range)
            // values fall back to U+FFFD — same contract as the JS
            // side's sanitizeCodePoint.
            return .char(scalarFromCode(intOf(dict["value"])))
        case "EUnit":
            return .unit
        case "EVar":
            return .var(stringOf(dict["name"]))
        case "ECtor":
            return .ctor(module: stringList(dict["module"]),
                         name: stringOf(dict["name"]))
        case "EQualified":
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
            // each to String, dropping any that aren't (defensive —
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

    /// Build a Unicode.Scalar from an Int code point, substituting
    /// U+FFFD for out-of-range or surrogate values. Mirrors the
    /// runtime/char.go sanitizeCodePoint and JS sanitizeCodePoint.
    /// Force-unwrap on U+FFFD is safe — it's a valid scalar.
    static func scalarFromCode(_ n: Int) -> Unicode.Scalar {
        if n < 0 || n > 0x10FFFF { return Unicode.Scalar(0xFFFD)! }
        if n >= 0xD800 && n <= 0xDFFF { return Unicode.Scalar(0xFFFD)! }
        return Unicode.Scalar(UInt32(n)) ?? Unicode.Scalar(0xFFFD)!
    }
}
