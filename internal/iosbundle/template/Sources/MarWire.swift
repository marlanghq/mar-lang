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
/// Built once, by `decode`, and read for the life of the program. That was
/// true and unstated; the `private(set)`s make the compiler keep it, and the
/// unchecked conformance rests on it rather than on a promise. Without this
/// the shape was a mutable class crossing into a main-actor continuation,
/// which Swift 6 reports as "sending ... risks causing data races".
final class MarWireShape: @unchecked Sendable {
    let kind: String
    private(set) var fields: [(name: String, shape: MarWireShape)] = []
    private(set) var elem: MarWireShape?
    private(set) var members: [MarWireShape] = []
    private(set) var tags: [String] = []
    private(set) var args: [[MarWireShape]] = []
    /// The canonical type name a Ctor shape came from, and the one a Ref
    /// points back at.
    private(set) var name: String = ""

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
        shape.name = dict["name"] as? String ?? ""
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
        if let raw = dict["members"] as? [Any] {
            shape.members = raw.compactMap { MarWireShape.decode($0) }
        }
        if let raw = dict["tags"] as? [Any] {
            shape.tags = raw.compactMap { $0 as? String }
        }
        if let raw = dict["args"] as? [Any] {
            shape.args = raw.map { per in
                (per as? [Any] ?? []).compactMap { MarWireShape.decode($0) }
            }
        }
        return shape
    }
}

enum MarWire {
    /// Why JSONSerialization refused, in Mar's words.
    ///
    /// It reports every failure as NSCocoaErrorDomain 3840, so the CODE says
    /// nothing; what separates them is NSDebugDescription. Measured:
    ///
    ///   {"v":1e400}   Number wound up as NaN around line 1, column 5.
    ///   <html>        Invalid value around line 1, column 0.
    ///   {oops}        No string key for value in object around line 1, column 1.
    ///
    /// The first is VALID JSON that Foundation will not hold, and calling it
    /// "not valid JSON" is a false statement to whoever reads it -- the same
    /// defect as any wrong message, except this one sends a person looking for
    /// a syntax error that is not there.
    ///
    /// Reading a debug string is fragile, so it is used only to say something
    /// MORE precise: no match falls back to the sentence this always gave, and
    /// an OS that reworded it degrades to today's behaviour rather than worse.
    ///
    /// The three runtimes still answer this document differently, and that is
    /// recorded in internal/runtime/json.go: agreeing means deciding what each
    /// one ACCEPTS as a number, which is a decision and not a fix.
    static func whyNotJSON(_ error: Error) -> String {
        let debug = (error as NSError).userInfo["NSDebugDescription"] as? String ?? ""
        if debug.contains("NaN") || debug.contains("out of range") {
            return "the document holds a number too large for Mar to hold"
        }
        return "the document is not valid JSON"
    }

    /// What a document that parsed but could not become a Mar value says when
    /// the failure carries no sentence of its own. Fixed text, because the
    /// host's is localized and would make the answer depend on the device.
    static let notReadable = "the document could not be read"

    /// Answers nil when the value fits the shape, or the sentence that says
    /// how it does not. The wording is the Go runtime's, to the character.
    /// `seen` carries the named shapes met on the way down, so a Ref can point
    /// back at the type it is inside. A shape is a TREE and a recursive type is
    /// not: the point where one re-entered itself used to be Any, checking
    /// nothing from there down. Following the cycle against the DATA ends,
    /// because the data is finite.
    static func check(_ v: MarValue, _ s: MarWireShape, _ path: String = "") -> String? {
        check(v, s, path, [:])
    }

    // One branch per wire shape, and each one has to name what it wanted in a
    // sentence a user reads. The branch count is the shape vocabulary's size.
    // swiftlint:disable:next cyclomatic_complexity
    static func check(_ v: MarValue, _ s: MarWireShape, _ path: String, _ seen: [String: MarWireShape]) -> String? {
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
            guard case .ctor(let tag, let args) = v else {
                return bad("a Maybe (leave the field out for Nothing)")
            }
            if tag == "Nothing" { return nil }
            if tag == "Just", args.count == 1 {
                guard let elem = s.elem else { return nil }
                return check(args[0], elem, path)
            }
            return bad("a Maybe (leave the field out for Nothing)")

        case "Ref":
            // The type this position re-enters, remembered on the way down.
            // A Ref with nothing to point at came from a bundle older than
            // this kind: it asserts nothing rather than refusing.
            guard let target = seen[s.name] else { return nil }
            return check(v, target, path, seen)

        case "Ctor":
            // Remembered under its type name so a Ref below can find it.
            var seen = seen
            if !s.name.isEmpty { seen[s.name] = s }
            guard case .ctor(let tag, let carried) = v else {
                return bad("one of \(quoteEach(s.tags))")
            }
            guard let i = s.tags.firstIndex(of: tag) else {
                return "\(where_) should be one of \(quoteEach(s.tags)), got `\(tag)`"
            }
            // The right tag still has to carry the right things: a
            // constructor's arguments are part of its type.
            let want = i < s.args.count ? s.args[i] : []
            if carried.count != want.count {
                return "\(where_): `\(tag)` takes \(plural(want.count, "value")), got \(carried.count)"
            }
            for (j, arg) in carried.enumerated() {
                if let err = check(arg, want[j], "\(path).\(tag)[\(j)]", seen) { return err }
            }
            return nil

        case "Set":
            // Set, Dict and Angle had no arm here, the same hole the tuple
            // had: measured on the server, a plain `[1,2,3]` where the type
            // says `Set Int` sailed through the guard and answered 500 with
            // `Set.toList: not a Set`.
            guard case .set(let items) = v else { return bad("a set") }
            guard let elem = s.elem else { return nil }
            for (i, item) in items.enumerated() {
                if let err = check(item, elem, "\(path)[\(i)]", seen) { return err }
            }
            return nil

        case "Dict":
            guard case .dict(let pairs) = v else { return bad("a dictionary") }
            if s.members.count != 2 { return nil }
            for (i, pair) in pairs.enumerated() {
                if let err = check(pair.key, s.members[0], "\(path)[\(i)].key", seen) { return err }
                if let err = check(pair.value, s.members[1], "\(path)[\(i)].value", seen) { return err }
            }
            return nil

        case "Angle":
            if case .angle = v { return nil }
            return bad("an angle")

        case "Tuple":
            // A tuple travels as a JSON array and comes back as a LIST: an
            // array is all the wire has. Both forms arrive here, and the
            // arity is what separates a well-formed tuple from anything else.
            guard let xs = tupleMembers(v) else {
                return bad("a list of \(plural(s.members.count, "value"))")
            }
            if xs.count != s.members.count {
                return "\(where_) should be a list of \(plural(s.members.count, "value")), got \(xs.count)"
            }
            for (i, m) in xs.enumerated() {
                if let err = check(m, s.members[i], "\(path)[\(i)]", seen) { return err }
            }
            return nil

        case "List":
            guard case .list(let xs) = v else { return bad("a list") }
            guard let elem = s.elem else { return nil }
            for (i, item) in xs.enumerated() {
                if let err = check(item, elem, "\(path)[\(i)]", seen) { return err }
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
                if let err = check(sub, f.shape, next, seen) { return err }
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
            if isAbsent(v) { return .ctor(tag: "Nothing", args: []) }
            if case .ctor(let tag, let args) = v,
               tag == "Just", args.count == 1, let elem = s.elem {
                return .ctor(tag: "Just", args: [coerce(args[0], elem)])
            }
            return v

        case "List":
            guard case .list(let xs) = v, let elem = s.elem else { return v }
            return .list(xs.map { coerce($0, elem) })

        case "Tuple":
            // Accepting the array means producing the tuple.
            guard let xs = tupleMembers(v), xs.count == s.members.count else { return v }
            return .tuple(xs.enumerated().map { coerce($1, s.members[$0]) })

        case "Ctor":
            guard case .ctor(let tag, let carried) = v,
                  let i = s.tags.firstIndex(of: tag),
                  i < s.args.count,
                  carried.count == s.args[i].count else { return v }
            return .ctor(tag: tag,
                         args: carried.enumerated().map { coerce($1, s.args[i][$0]) })

        case "Record":
            guard case .record(var fields, var order) = v else { return v }
            for f in s.fields {
                guard let sub = fields[f.name] else {
                    if f.shape.kind == "Maybe" {
                        fields[f.name] = .ctor(tag: "Nothing", args: [])
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
    /// @MainActor: this returns a Mar function, and a Mar function runs on
    /// the main actor. Without it the shape was captured across an isolation
    /// boundary and Swift 6 called it what it was -- sending a value that is
    /// not Sendable.
    @MainActor
    static func decoder(_ shape: MarWireShape) -> MarValue {
        .fn(MarFn.native(1) { args in
            guard case .string(let raw) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "String", got: Eval.typeOf(args[0]))
            }
            // The two failures are kept apart on purpose. A document that is
            // not JSON gets Mar's own sentence, the same one the other two
            // runtimes give — Foundation's `localizedDescription` changes with
            // the PHONE'S LANGUAGE, so the value a program received depended on
            // the device. Everything after the parse (a tagged value that
            // cannot be one, a shape mismatch) already has a Mar sentence of
            // its own, and lumping them together told someone who sent
            // `{"__char":"abc"}` — valid JSON — that their document was not.
            let parsed: Any
            do {
                parsed = try MarJSONCodec.parseJSONExact(raw)
            } catch {
                return .ctor(tag: "Err",
                             args: [.string(whyNotJSON(error))])
            }
            do {
                let v = try MarJSONCodec.jsonToMar(parsed)
                if let err = check(v, shape) {
                    return .ctor(tag: "Err", args: [.string(err)])
                }
                return .ctor(tag: "Ok", args: [coerce(v, shape)])
            } catch let e as MarRuntimeError {
                // MarRuntimeError's errorDescription is MAR's sentence, so
                // this one is safe to pass through. Catching it by type rather
                // than relying on "only Mar errors get here" is what makes
                // that a guarantee instead of a comment.
                return .ctor(tag: "Err",
                             args: [.string(e.errorDescription ?? notReadable)])
            } catch {
                return .ctor(tag: "Err", args: [.string(notReadable)])
            }
        })
    }

    /// `Service.call` at a call site whose declared response type the checker
    /// knew. Same call, one more question asked of what comes back — and the
    /// only place the phone can ask it, since neither the service value nor
    /// the built program carries the type.
    /// @MainActor: this returns a Mar function, and a Mar function runs on
    /// the main actor. Without it the shape was captured across an isolation
    /// boundary and Swift 6 called it what it was -- sending a value that is
    /// not Sendable.
    @MainActor
    static func serviceCaller(_ shape: MarWireShape) -> MarValue {
        .fn(MarFn.native(3) { args in
            let svc = args[0]
            let toMsg = args[2]
            return .effect(MarEffect(tag: "serviceCall") {
                guard case .ctor("__Service", let svcArgs) = svc,
                      svcArgs.count == 2,
                      case .string(let verb) = svcArgs[0],
                      case .string(let pattern) = svcArgs[1] else {
                    throw MarRuntimeError.message("Service.call: not a service contract")
                }
                let built = MarBuiltins.buildServiceRequest(verb: verb, pattern: pattern, req: args[1])
                MarHTTP.fireService(method: verb, path: built.url, body: built.body,
                                    toMsg: toMsg, responseShape: shape)
                return .unit
            })
        })
    }

    /// `Auth.verifyCode` at a call site whose USER type the checker knew.
    /// @MainActor: this returns a Mar function, and a Mar function runs on
    /// the main actor. Without it the shape was captured across an isolation
    /// boundary and Swift 6 called it what it was -- sending a value that is
    /// not Sendable.
    @MainActor
    static func authVerifier(_ shape: MarWireShape) -> MarValue {
        .fn(MarFn.native(2) { args in
            let req = args[0]
            let toMsg = args[1]
            return .effect(MarEffect(tag: "Auth.verifyCode") {
                MarHTTP.fireAuthOutcome(
                    path: "/_auth/verify-code",
                    body: req,
                    okOutcome: { user in .ctor(tag: "SignedIn", args: [user]) },
                    mapCode: MarHTTP.verifyCodeOutcome,
                    toMsg: toMsg,
                    userShape: shape)
                return .unit
            })
        })
    }

    /// `Auth.me` at a call site whose user type the checker knew.
    /// @MainActor: this returns a Mar function, and a Mar function runs on
    /// the main actor. Without it the shape was captured across an isolation
    /// boundary and Swift 6 called it what it was -- sending a value that is
    /// not Sendable.
    @MainActor
    static func authMe(_ shape: MarWireShape) -> MarValue {
        .fn(MarFn.native(1) { args in
            let toMsg = args[0]
            return .effect(MarEffect(tag: "Auth.me") {
                MarHTTP.fireAuthMe(toMsg: toMsg, userShape: shape)
                return .unit
            })
        })
    }

    // MARK: - Helpers

    /// The positions of whichever of the two forms arrived: a tuple built
    /// inside the program, or the list a JSON array decodes to.
    private static func tupleMembers(_ v: MarValue) -> [MarValue]? {
        switch v {
        case .tuple(let xs): return xs
        case .list(let xs): return xs
        default: return nil
        }
    }

    private static func isAbsent(_ v: MarValue) -> Bool {
        if case .unit = v { return true }
        return false
    }

    private static func quoteEach(_ tags: [String]) -> String {
        tags.map { "`\($0)`" }.joined(separator: ", ")
    }

    /// Keeps the counts in these messages reading like English rather than
    /// like a format string.
    private static func plural(_ n: Int, _ noun: String) -> String {
        n == 1 ? "1 \(noun)" : "\(n) \(noun)s"
    }

    /// Names what actually arrived, in the vocabulary of whoever wrote the
    /// JSON: "a string", not "MarValue.string".
    private static func describe(_ v: MarValue) -> String {
        switch v {
        case .int: return "a whole number"
        case .decimal: return "a number"
        case .string: return "a string"
        case .bool: return "true or false"
        case .char: return "a character"
        case .list: return "a list"
        case .unit: return "nothing"
        case .time: return "a time"
        case .ctor(let tag, _): return "`\(tag)`"
        case .record(_, let order):
            return order.isEmpty ? "an empty object" : "an object with \(order.joined(separator: ", "))"
        default: return "something else"
        }
    }
}
