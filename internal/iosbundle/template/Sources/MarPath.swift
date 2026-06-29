// Path-pattern parser + matcher + URL builder. Mirrors the JS
// runtime's IIFE-level helpers so the same `Path r` value behaves
// identically across the three runtimes.
//
// At runtime a `Path r` is just a String — the typechecker enforces
// the surface contract, and this helper lazily parses on demand
// (cached so repeat calls in a list render don't re-walk the same
// pattern N times).

import Foundation

enum MarPathSegment {
    case literal(String)
    case param(name: String, type: String)  // type ∈ { "String", "Int" }
}

struct MarPathPattern {
    let source: String
    let segments: [MarPathSegment]
}

enum MarPathError: Error, LocalizedError {
    case bareColon(path: String, name: String)
    case missingType(path: String, name: String)
    case unknownType(path: String, name: String, type: String)
    case duplicateParam(path: String, name: String)
    case malformed(path: String, segment: String)

    var errorDescription: String? {
        switch self {
        case .bareColon(let p, let n):
            return "path \"\(p)\": bare \":\(n)\" not supported. Use \"{\(n):Type}\"."
        case .missingType(let p, let n):
            return "path \"\(p)\": param \"{\(n)}\" requires a type, e.g. \"{\(n):String}\" or \"{\(n):Int}\"."
        case .unknownType(let p, let n, let t):
            return "path \"\(p)\": unknown type \"\(t)\" for param \"\(n)\". Allowed: String, Int."
        case .duplicateParam(let p, let n):
            return "path \"\(p)\": duplicate param \"\(n)\"."
        case .malformed(let p, let s):
            return "path \"\(p)\": malformed segment \"\(s)\" (use \"{name:Type}\")."
        }
    }
}

enum MarPath {
    /// Cache parsed patterns by source string. Bounded only by the
    /// number of distinct Path values in the app — typically dozens.
    private static var cache: [String: MarPathPattern] = [:]

    /// Custom-enum registry. Populated by MarLoader on every
    /// CustomTypeDecl whose ctors are all zero-arg. Path patterns
    /// look up these names to decode `{role:Role}` segments.
    /// Mirrors internal/runtime/path.go's EnumTypes.
    private static var enumTypes: [String: [String]] = [:]

    /// Register a custom enum type for use in `{name:Type}` segments.
    /// Idempotent — re-registration overwrites (so hot reload picks
    /// up renames cleanly).
    static func registerEnumType(_ typeName: String, ctorNames: [String]) {
        enumTypes[typeName] = ctorNames
    }

    static func parse(_ source: String) -> MarPathPattern {
        if let hit = cache[source] { return hit }
        // tryParse always succeeds for inputs that passed the
        // typechecker; if a runtime-built path string slips through
        // malformed, we fall back to a single-literal-segment pattern
        // that just won't match anything (matchURL returns nil).
        let parsed = (try? tryParse(source)) ?? MarPathPattern(source: source, segments: [.literal(source)])
        cache[source] = parsed
        return parsed
    }

    static func tryParse(_ source: String) throws -> MarPathPattern {
        let parts = source.split(separator: "/", omittingEmptySubsequences: true).map(String.init)
        var segs: [MarPathSegment] = []
        var seen = Set<String>()
        for part in parts {
            if part.hasPrefix(":") {
                throw MarPathError.bareColon(path: source, name: String(part.dropFirst()))
            }
            if part.hasPrefix("{") && part.hasSuffix("}") {
                let inner = String(part.dropFirst().dropLast())
                guard let colonIdx = inner.firstIndex(of: ":") else {
                    throw MarPathError.missingType(path: source, name: inner)
                }
                let name = inner[..<colonIdx].trimmingCharacters(in: .whitespaces)
                let type = inner[inner.index(after: colonIdx)...].trimmingCharacters(in: .whitespaces)
                if type != "String" && type != "Int" && enumTypes[type] == nil {
                    throw MarPathError.unknownType(path: source, name: name, type: type)
                }
                if seen.contains(name) {
                    throw MarPathError.duplicateParam(path: source, name: name)
                }
                seen.insert(name)
                segs.append(.param(name: name, type: type))
                continue
            }
            if part.contains("{") || part.contains("}") {
                throw MarPathError.malformed(path: source, segment: part)
            }
            segs.append(.literal(part))
        }
        return MarPathPattern(source: source, segments: segs)
    }

    /// Match a URL path against a parsed pattern. Returns the typed
    /// params record on success, nil on miss (count mismatch, literal
    /// mismatch, or type-decode failure).
    static func match(_ urlPath: String, pattern: MarPathPattern) -> MarValue? {
        let urlSegs = urlPath.split(separator: "/", omittingEmptySubsequences: true).map(String.init)
        guard urlSegs.count == pattern.segments.count else { return nil }
        var fields: [String: MarValue] = [:]
        var order: [String] = []
        for (seg, raw) in zip(pattern.segments, urlSegs) {
            switch seg {
            case .literal(let lit):
                if lit != raw { return nil }
            case .param(let name, let type):
                guard let v = decode(raw, type: type) else { return nil }
                fields[name] = v
                order.append(name)
            }
        }
        return .record(fields: fields, order: order)
    }

    /// Build a URL string from a parsed pattern + a params record.
    /// Used by linkTo / Nav.pushTo / Nav.replaceTo. Throws on
    /// missing or wrong-type fields so user code surfaces the
    /// problem at the call site instead of producing a silently
    /// malformed URL.
    static func build(_ pattern: MarPathPattern, params: MarValue) throws -> String {
        guard case .record(let fs, _) = params else {
            throw MarRuntimeError.typeMismatch(expected: "record", got: "non-record")
        }
        var out = ""
        for seg in pattern.segments {
            out += "/"
            switch seg {
            case .literal(let lit):
                out += lit
            case .param(let name, let type):
                guard let v = fs[name] else {
                    throw MarRuntimeError.message("linkTo \(pattern.source): missing param \"\(name)\"")
                }
                out += try encode(v, type: type, paramName: name, pathSource: pattern.source)
            }
        }
        return out.isEmpty ? "/" : out
    }

    // MARK: - Per-type codec

    private static func decode(_ raw: String, type: String) -> MarValue? {
        let decoded = raw.removingPercentEncoding ?? raw
        switch type {
        case "String":
            return .string(decoded)
        case "Int":
            // Reject leading zeros / spaces / non-numeric — same
            // rules as Go's strconv.ParseInt and JS's regex check.
            if let n = Int(decoded), String(n) == decoded || (decoded.hasPrefix("-") && String(n) == decoded) {
                return .int(n)
            }
            return nil
        default:
            // Custom enum: case-insensitive match against the
            // registered ctors. URLs are typically lowercased
            // (`/users/admin`), ctors are PascalCase (`Admin`).
            if let ctors = enumTypes[type] {
                let want = decoded.lowercased()
                for c in ctors where c.lowercased() == want {
                    return .ctor(tag: c, args: [], origin: nil)
                }
            }
            return nil
        }
    }

    private static func encode(_ v: MarValue, type: String, paramName: String, pathSource: String) throws -> String {
        switch type {
        case "String":
            guard case .string(let s) = v else {
                throw MarRuntimeError.message("linkTo \(pathSource): param \"\(paramName)\": expected String")
            }
            return s.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? s
        case "Int":
            guard case .int(let n) = v else {
                throw MarRuntimeError.message("linkTo \(pathSource): param \"\(paramName)\": expected Int")
            }
            return String(n)
        default:
            if let ctors = enumTypes[type] {
                guard case .ctor(let tag, _, _) = v else {
                    throw MarRuntimeError.message("linkTo \(pathSource): param \"\(paramName)\": expected \(type)")
                }
                // Defensive: confirm the ctor is a member of this
                // enum. The typechecker already enforces this; the
                // runtime check protects against handwritten VCtors.
                guard ctors.contains(tag) else {
                    throw MarRuntimeError.message("linkTo \(pathSource): ctor \"\(tag)\" is not a member of \(type)")
                }
                return tag.lowercased()
            }
            throw MarRuntimeError.message("linkTo \(pathSource): unknown type \"\(type)\"")
        }
    }
}
