import Foundation

/// A decoded value was not produced by the type system, and the code that
/// receives it was checked on the assumption that it has the declared type.
/// This file is the comparison nobody was making.
///
/// It is a port of `CheckWire` / `CoerceWire` in internal/runtime/wire.go,
/// arm for arm and sentence for sentence. Three runtimes answering the same
/// question have to answer it the same way, and the shared conformance corpus
/// is what holds them to it — a drift test can only see that each layer
/// DEFINES a decoder, never that the layers agree about what it means.
///
/// The shape itself is written by the typechecker onto the tree
/// (`ast.EQualified.Wire`) and arrives here as ordinary JSON, because the
/// call site's type is the only place a `JSON.decode` result type exists.
/// A class rather than a struct only because `elem` makes the shape
/// recursive, and a Swift value type cannot store itself. Nothing mutates one
/// after it is decoded.
final class MarWireShape {
    let kind: String
    var fields: [(name: String, shape: MarWireShape)] = []
    var elem: MarWireShape? = nil
    var tags: [String] = []

    init(kind: String) {
        self.kind = kind
    }

    /// Absent on every node but a `JSON.decode` whose result type somebody
    /// pinned, so an older bundle simply has no `wire` key and this side
    /// behaves exactly as it did before the check existed.
    static func decode(_ any: Any?) -> MarWireShape? {
        guard let dict = any as? [String: Any],
              let kind = dict["kind"] as? String else { return nil }
        let shape = MarWireShape(kind: kind)
        if let elem = dict["elem"] {
            shape.elem = MarWireShape.decode(elem)
        }
        if let raw = dict["fields"] as? [Any] {
            for entry in raw {
                guard let f = entry as? [String: Any],
                      let name = f["name"] as? String,
                      let sub = MarWireShape.decode(f["shape"]) else { continue }
                shape.fields.append((name: name, shape: sub))
            }
        }
        if let raw = dict["tags"] as? [Any] {
            shape.tags = raw.compactMap { $0 as? String }
        }
        return shape
    }
}

enum MarWire {

    /// Answers nil when the value fits the shape, or the sentence that says
    /// how it does not. The wording is the Go runtime's, to the character.
    static func check(_ v: MarValue, _ s: MarWireShape, _ path: String = "") -> String? {
        let where_ = path.isEmpty ? "the value" : "`\(path)`"
        func bad(_ want: String) -> String {
            "\(where_) should be \(want), got \(describe(v))"
        }

        switch s.kind {
        case "Any":
            return nil

        case "Int":
            if case .int = v { return nil }
            return bad("a whole number")

        case "String":
            if case .string = v { return nil }
            return bad("a string")

        case "Bool":
            if case .bool = v { return nil }
            return bad("true or false")

        case "Char":
            if case .char = v { return nil }
            return bad("a single character")

        case "Decimal":
            // An Int is a legal Decimal on the wire: JSON has one number type,
            // and a client sending 3 for a Decimal field means 3.
            if case .decimal = v { return nil }
            if case .int = v { return nil }
            return bad("a number")

        case "Time":
            if case .time = v { return nil }
            return bad("a time (an ISO-8601 string under \"__time\")")

        case "()":
            // Records ignore what they do not declare, so `()` does too: it is
            // one rule, and the two halves used to disagree.
            if case .unit = v { return nil }
            if case .record = v { return nil }
            return bad("empty")

        case "Maybe":
            if isAbsent(v) { return nil }
            guard case .ctor(let tag, let args, _) = v else {
                return bad("a Maybe (leave the field out for Nothing)")
            }
            if tag == "Nothing" { return nil }
            if tag == "Just", args.count == 1 {
                guard let elem = s.elem else { return nil }
                return check(args[0], elem, path)
            }
            return bad("a Maybe (leave the field out for Nothing)")

        case "Ctor":
            guard case .ctor(let tag, let args, _) = v, args.isEmpty else {
                return bad("one of \(quoteEach(s.tags))")
            }
            if s.tags.contains(tag) { return nil }
            return "\(where_) should be one of \(quoteEach(s.tags)), got `\(tag)`"

        case "List":
            guard case .list(let xs) = v else { return bad("a list") }
            guard let elem = s.elem else { return nil }
            for (i, item) in xs.enumerated() {
                if let err = check(item, elem, "\(path)[\(i)]") { return err }
            }
            return nil

        case "Record":
            guard case .record(let fields, _) = v else { return bad("an object") }
            for f in s.fields {
                guard let sub = fields[f.name] else {
                    // A missing Maybe is Nothing; a missing anything else is
                    // the single most common wire bug, so it gets its own
                    // message.
                    if f.shape.kind == "Maybe" { continue }
                    return "\(where_) is missing the field `\(f.name)`"
                }
                let next = path.isEmpty ? f.name : "\(path).\(f.name)"
                if let err = check(sub, f.shape, next) { return err }
            }
            // Extra fields are allowed on purpose: a newer client sending a
            // field this build does not know about should not be a rejection.
            return nil

        default:
            return nil
        }
    }

    /// Accepting a value means producing the value that was accepted. A JSON
    /// `3` for a Decimal field is legal and has to arrive as a Decimal, and a
    /// declared optional field nobody sent still has to BE there, because the
    /// code reads it whether or not it was written.
    static func coerce(_ v: MarValue, _ s: MarWireShape) -> MarValue {
        switch s.kind {
        case "Decimal":
            if case .int(let i) = v {
                return .decimal(MarDec.fromInt(i))
            }
            return v

        case "Maybe":
            if isAbsent(v) { return .ctor(tag: "Nothing", args: [], origin: nil) }
            if case .ctor(let tag, let args, let origin) = v,
               tag == "Just", args.count == 1, let elem = s.elem {
                return .ctor(tag: "Just", args: [coerce(args[0], elem)], origin: origin)
            }
            return v

        case "List":
            guard case .list(let xs) = v, let elem = s.elem else { return v }
            return .list(xs.map { coerce($0, elem) })

        case "Record":
            guard case .record(var fields, var order) = v else { return v }
            for f in s.fields {
                guard let sub = fields[f.name] else {
                    if f.shape.kind == "Maybe" {
                        fields[f.name] = .ctor(tag: "Nothing", args: [], origin: nil)
                        order.append(f.name)
                    }
                    continue
                }
                fields[f.name] = coerce(sub, f.shape)
            }
            return .record(fields: fields, order: order)

        default:
            return v
        }
    }

    /// `JSON.decode` at a call site whose result type the checker knew. Same
    /// decoder, one more question asked afterwards, and the answer goes where
    /// a decode failure already went: `Err`.
    static func decoder(_ shape: MarWireShape) -> MarValue {
        .fn(MarFn.native(1) { args in
            guard case .string(let raw) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            do {
                let any = try JSONSerialization.jsonObject(
                    with: Data(raw.utf8),
                    options: [.fragmentsAllowed]
                )
                let v = MarJSONCodec.jsonToMar(any)
                if let err = check(v, shape) {
                    return .ctor(tag: "Err", args: [.string(err)], origin: nil)
                }
                return .ctor(tag: "Ok", args: [coerce(v, shape)], origin: nil)
            } catch {
                return .ctor(tag: "Err",
                             args: [.string(error.localizedDescription)],
                             origin: nil)
            }
        })
    }

    // MARK: - Helpers

    private static func isAbsent(_ v: MarValue) -> Bool {
        if case .unit = v { return true }
        return false
    }

    private static func quoteEach(_ tags: [String]) -> String {
        tags.map { "`\($0)`" }.joined(separator: ", ")
    }

    /// Names what actually arrived, in the vocabulary of whoever wrote the
    /// JSON: "a string", not "MarValue.string".
    private static func describe(_ v: MarValue) -> String {
        switch v {
        case .int: return "a whole number"
        case .float: return "a number"
        case .decimal: return "a number"
        case .string: return "a string"
        case .bool: return "true or false"
        case .char: return "a character"
        case .list: return "a list"
        case .unit: return "nothing"
        case .time: return "a time"
        case .ctor(let tag, _, _): return "`\(tag)`"
        case .record(_, let order):
            return order.isEmpty ? "an empty object" : "an object with \(order.joined(separator: ", "))"
        default: return "something else"
        }
    }
}
