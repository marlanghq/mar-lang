// Tree-walking interpreter for mar's expression AST. Direct
// translation of `evalExpr / apply / matchInto` in runtime.js.
//
// Why tree-walking and not bytecode: the JS runtime is tree-walking
// too, the AST is small, perf is fine for hand-written UI code, and
// staying line-by-line equivalent to the JS keeps the two
// implementations from drifting.

import Foundation

enum Eval {

    // MARK: - Recursion guard

    /// How deep the interpreter will recurse before refusing.
    ///
    /// A function that never reaches its base case used to run until the
    /// thread's stack was gone, which on iOS is a crash the app cannot catch
    /// or report — the user just sees it disappear.
    ///
    /// The number is MEASURED, not mirrored from the Go runtime, because the
    /// two platforms are not comparable: Go grows a goroutine stack to a
    /// gigabyte and survives 100_000 Mar frames, while this interpreter runs
    /// on an 8 MB thread stack. Built the way the template ships it
    /// (-O -wmo), it completes 1000 nested Mar calls and segfaults by 2000.
    /// 512 sits at half the known-good depth, which leaves room for frames
    /// fatter than the probe's — a deeply nested expression uses more stack
    /// per call than `1 + countDown (n - 1)` does.
    ///
    /// The asymmetry is honest rather than unfortunate: a program that
    /// recurses 5000 deep does not work on a phone today either. This turns
    /// the crash into a message.
    static let maxCallDepth = 512

    /// Ambient rather than threaded through every call, because unlike the Go
    /// server — where requests are handled concurrently and a shared counter
    /// would be wrong — everything that drives this interpreter is confined to
    /// the main actor: MarDispatcher, AppViewModel and MarPageRuntime are all
    /// @MainActor, and the effects that reach user code hop back onto it.
    ///
    /// Being ambient is also what makes it catch recursion routed through a
    /// higher-order builtin (`List.foldl (\_ _ -> loop n) 0 [1]`): the counter
    /// does not care whether the next call came from the evaluator or from
    /// Swift code inside a builtin.
    nonisolated(unsafe) static var callDepth = 0

    /// Enters one call frame. `body` is the closure body evaluation; the depth
    /// is restored however it returns, error included.
    @inline(__always)
    static func inCallFrame<T>(_ body: () throws -> T) throws -> T {
        if callDepth >= maxCallDepth {
            throw MarRuntimeError.message(
                "too much recursion: more than \(maxCallDepth) nested calls. "
                    + "A function is calling itself without reaching a base case")
        }
        callDepth += 1
        defer { callDepth -= 1 }
        return try body()
    }

    // MARK: - Expressions

    // Mirrors the JS runtime's message word for word, so the same mistake
    // reads the same on both platforms. `order` is the declaration order,
    // which makes the list stable rather than hash-shuffled.
    static func missingField(_ name: String, _ fields: [String: MarValue], _ order: [String]) -> MarRuntimeError {
        let had = (order.isEmpty ? Array(fields.keys).sorted() : order).joined(separator: ", ")
        return MarRuntimeError.message(
            "record has no field `\(name)`\n\n"
            + "this record has: \(had.isEmpty ? "(no fields)" : had)\n\n"
            + "reading a field that does not exist is a type error, so this record "
            + "did not come from this program's types.")
    }

    static func eval(_ expr: Expr, _ env: Env) throws -> MarValue {
        switch expr {
        case .int(let n):       return .int(n)
        case .float(let n):     return .float(n)
        case .decimal(let coef, let scale):
            var digits: [UInt8] = []
            digits.reserveCapacity(coef.count)
            for ch in coef {
                guard let d = ch.wholeNumberValue, d >= 0, d <= 9 else {
                    throw MarRuntimeError.message("invalid decimal literal")
                }
                digits.append(UInt8(d))
            }
            return .decimal(MarDec(negative: false, digits: DecMath.trim(digits), scale: scale))
        case .string(let s):    return .string(s)
        case .char(let c):      return .char(c)
        case .unit:             return .unit

        case .var(let name):
            guard let v = env.lookup(name) else {
                throw MarRuntimeError.unboundName(name)
            }
            return v

        case .ctor(let module, let name),
             .qualified(let module, let name):
            return try lookupQualified(env: env, module: module, name: name)

        case .negate(let inner):
            switch try eval(inner, env) {
            case .int(let n):   return .int(-n)
            case .decimal(let d):
                return .decimal(MarDec(negative: !d.negative && !d.isZero, digits: d.digits, scale: d.scale))
            default:
                throw MarRuntimeError.message("negate: unsupported type")
            }

        case .app:
            // Collapse the application spine `f a b c` into ONE saturated
            // call. Evaluating nested EApp nodes one at a time builds an
            // intermediate partial MarFn (plus a copied `applied` array)
            // per argument — the interpreter's hottest allocation site: a
            // canvas game's view frame is tens of thousands of
            // applications. Evaluation order matches the nested form (fn
            // first, then args left to right); in a typechecked program
            // the skipped intermediate applications are unobservable.
            var spine: [Expr] = []
            var head = expr
            while case .app(let f, let a) = head {
                spine.append(a)
                head = f
            }
            let fv = try eval(head, env)
            var args: [MarValue] = []
            args.reserveCapacity(spine.count)
            for e in spine.reversed() { args.append(try eval(e, env)) }
            return try applyMany(fv, args)

        case .binop(let op, let left, let right):
            let l = try eval(left, env)
            let r = try eval(right, env)
            // Fast paths for the hot operators: skip the env-chain lookup
            // (which hashes the operator string once per frame of the
            // scope chain) and the whole MarFn application machinery.
            // Each arm reproduces its native's semantics EXACTLY
            // (MarBuiltins.swift); operand shapes not handled here fall
            // through to the generic path so coercions keep working.
            switch op {
            case "+": if case .int(let a) = l, case .int(let b) = r { return .int(try MarInt.add(a, b)) }
            case "-": if case .int(let a) = l, case .int(let b) = r { return .int(try MarInt.sub(a, b)) }
            case "*": if case .int(let a) = l, case .int(let b) = r { return .int(try MarInt.mul(a, b)) }
            case "//": if case .int(let a) = l, case .int(let b) = r { return .int(b == 0 ? 0 : a / b) }
            case "/": if case .decimal(let a) = l, case .decimal(let b) = r { return .division(num: a, den: b) }
            case "==": return .bool(l.equalsMar(r))
            case "/=": return .bool(!l.equalsMar(r))
            case "<":  return .bool(l.compareMar(r) < 0)
            case ">":  return .bool(l.compareMar(r) > 0)
            case "<=": return .bool(l.compareMar(r) <= 0)
            case ">=": return .bool(l.compareMar(r) >= 0)
            case "&&": if case .bool(let a) = l, case .bool(let b) = r { return .bool(a && b) }
            case "||": if case .bool(let a) = l, case .bool(let b) = r { return .bool(a || b) }
            case "++":
                if case .string(let a) = l, case .string(let b) = r { return .string(a + b) }
                if case .list(let a) = l, case .list(let b) = r { return .list(a + b) }
            case "::": if case .list(let tail) = r { return .list([l] + tail) }
            case "|>": return try applyMany(r, [l])
            case "<|": return try applyMany(l, [r])
            default: break
            }
            guard let opFn = env.lookup(op) else {
                throw MarRuntimeError.unboundName("operator \(op)")
            }
            return try applyMany(opFn, [l, r])

        case .lambda(let params, let body):
            let names = try params.map { p -> String in
                switch p {
                case .var(let n):  return n
                case .wildcard:    return "__wild"
                default:
                    throw MarRuntimeError.message("lambda params must be names or _")
                }
            }
            return .fn(MarFn.closure(params: names, body: body, env: env))

        case .ifExpr(let cond, let thenE, let elseE):
            let c = try eval(cond, env)
            if case .bool(true) = c {
                return try eval(thenE, env)
            }
            return try eval(elseE, env)

        case .letExpr(let bindings, let body):
            var cur = env
            for b in bindings {
                let v = try eval(b.body, cur)
                var bag: [(String, MarValue)] = []
                if matchInto(b.pattern, v, &bag), !bag.isEmpty {
                    let frame = Env(parent: cur)
                    for (k, bv) in bag { frame.appendBinding(k, bv) }
                    cur = frame
                }
            }
            return try eval(body, cur)

        case .tuple(let xs):
            return .tuple(try xs.map { try eval($0, env) })

        case .list(let xs):
            return .list(try xs.map { try eval($0, env) })

        case .record(let fields):
            var fs: [String: MarValue] = [:]
            var order: [String] = []
            for f in fields {
                fs[f.name] = try eval(f.value, env)
                order.append(f.name)
            }
            return .record(fields: fs, order: order)

        case .recordUpdate(let recordE, let fields):
            let base = try eval(recordE, env)
            guard case .record(let baseFs, let baseOrder) = base else {
                throw MarRuntimeError.typeMismatch(expected: "record", got: typeOf(base))
            }
            var fs = baseFs
            for f in fields {
                fs[f.name] = try eval(f.value, env)
            }
            return .record(fields: fs, order: baseOrder)

        // A missing field THROWS. It used to fall back to `.unit`, which was
        // the worst of the three runtimes: a record without the field kept
        // running and carried a wrong value forward, silently, with nothing
        // to point at. The typechecker makes this unreachable for records
        // that came from this program's types — so if it fires, a record
        // arrived from outside them and that is worth stopping for.
        case .fieldAccess(let recordE, let field):
            let r = try eval(recordE, env)
            guard case .record(let fs, let order) = r else {
                throw MarRuntimeError.typeMismatch(expected: "record", got: typeOf(r))
            }
            guard let v = fs[field] else { throw Eval.missingField(field, fs, order) }
            return v

        case .fieldAccessor(let field):
            return .fn(MarFn.native(1) { args in
                guard case .record(let fs, let order) = args[0] else {
                    throw MarRuntimeError.typeMismatch(expected: "record", got: typeOf(args[0]))
                }
                guard let v = fs[field] else { throw Eval.missingField(field, fs, order) }
                return v
            })

        case .caseExpr(let subject, let branches):
            let subj = try eval(subject, env)
            for br in branches {
                var bag: [(String, MarValue)] = []
                if matchInto(br.pattern, subj, &bag) {
                    // Tag-only patterns (the common Msg dispatch) bind
                    // nothing — evaluate the body in the current env
                    // instead of pushing an empty frame every lookup in
                    // the body would have to walk through.
                    if bag.isEmpty { return try eval(br.body, env) }
                    let frame = Env(parent: env)
                    for (k, v) in bag { frame.appendBinding(k, v) }
                    return try eval(br.body, frame)
                }
            }
            throw MarRuntimeError.noMatch
        }
    }

    /// Looks a name up either by `Module.name` (qualified) or, if that
    /// fails, by the bare name. Mirrors how the JS interpreter falls
    /// back so `import UI exposing (form)` works AND fully
    /// qualified `UI.form` keeps working.
    private static func lookupQualified(env: Env, module: [String], name: String) throws -> MarValue {
        if !module.isEmpty {
            let key = module.joined(separator: ".") + "." + name
            if let v = env.lookup(key) { return v }
        }
        if let v = env.lookup(name) { return v }
        throw MarRuntimeError.unboundName(
            (module.isEmpty ? "" : module.joined(separator: ".") + ".") + name
        )
    }

    // MARK: - Function application

    /// Applies a function to one argument with currying. Kept as the
    /// single-argument entry point for taggers, List callbacks, etc.
    /// The exactly-saturating closure case runs WITHOUT building the
    /// one-element argument array `applyMany` would need — List.map /
    /// List.foldl call their user lambda once per element, so on a
    /// canvas frame this path fires millions of times.
    static func apply(_ fn: MarValue, _ arg: MarValue) throws -> MarValue {
        guard case .fn(let f) = fn else {
            throw MarRuntimeError.applyOnNonFunction
        }
        if f.applied.count + 1 == f.arity, f.native == nil,
           let params = f.params, let body = f.body, let fnEnv = f.env {
            let frame = Env(parent: fnEnv)
            for (p, v) in zip(params, f.applied) { frame.appendBinding(p, v) }
            frame.appendBinding(params[f.applied.count], arg)
            return try inCallFrame { try eval(body, frame) }
        }
        return try applyMany(fn, [arg])
    }

    /// Applies a function to N arguments at once. When the arguments
    /// saturate the arity, the call runs directly — no intermediate
    /// partial MarFns, no per-step `applied` array copies, and ONE Env
    /// frame for all params instead of a chain link per param (every
    /// lookup in the body walks that chain, so shorter is faster).
    /// Under-application still materializes a single partial carrying
    /// everything applied so far; over-application (a call returning
    /// another function) loops to feed the remaining args. Semantics
    /// mirror the JS runtime's one-at-a-time `apply` exactly.
    static func applyMany(_ fn: MarValue, _ args: [MarValue]) throws -> MarValue {
        var fv = fn
        var i = 0
        while i < args.count {
            guard case .fn(let f) = fv else {
                throw MarRuntimeError.applyOnNonFunction
            }
            let need = f.arity - f.applied.count
            if args.count - i >= need {
                // Saturated: run the native or the body directly.
                if let native = f.native {
                    if f.applied.isEmpty && i == 0 && need == args.count {
                        // Whole-call fast path: the args array IS the
                        // native's argument list — no copy.
                        fv = try native(args)
                    } else {
                        var all = f.applied
                        all.reserveCapacity(f.arity)
                        all.append(contentsOf: args[i ..< i + need])
                        fv = try native(all)
                    }
                } else {
                    guard let params = f.params, let body = f.body, let fnEnv = f.env else {
                        throw MarRuntimeError.message("malformed function: no native and no body")
                    }
                    // Bind straight into the frame — no intermediate
                    // `all` array; params of one call are distinct.
                    let frame = Env(parent: fnEnv)
                    for (p, v) in zip(params, f.applied) { frame.appendBinding(p, v) }
                    var j = 0
                    while j < need {
                        frame.appendBinding(params[f.applied.count + j], args[i + j])
                        j += 1
                    }
                    fv = try inCallFrame { try eval(body, frame) }
                }
                i += need
            } else {
                // Partial: one MarFn remembering everything applied so far.
                fv = .fn(MarFn(arity: f.arity,
                               applied: f.applied + args[i...],
                               params: f.params,
                               body: f.body,
                               env: f.env,
                               native: f.native))
                i = args.count
            }
        }
        return fv
    }

    // MARK: - Pattern matching

    /// Tries to match a pattern against a value, populating `bag`
    /// with bindings. Returns true on success. Mirrors `matchInto`
    /// in runtime.js. The bag is a flat pair list (not a Dictionary):
    /// one pattern's variables are distinct by construction, an empty
    /// array literal allocates nothing on the (common) tag-only match,
    /// and the caller adopts the pairs into an Env frame without
    /// re-hashing anything.
    static func matchInto(_ pat: Pat, _ v: MarValue, _ bag: inout [(String, MarValue)]) -> Bool {
        switch pat {
        case .wildcard:
            return true
        case .var(let name):
            bag.append((name, v))
            return true
        case .int(let i):
            if case .int(let n) = v, n == i { return true }
            return false
        case .string(let s):
            if case .string(let str) = v, str == s { return true }
            return false
        case .char(let c):
            if case .char(let other) = v, other == c { return true }
            return false
        case .unit:
            if case .unit = v { return true }
            return false
        case .ctor(let name, let args):
            guard case .ctor(let tag, let vargs, _) = v,
                  tag == name,
                  vargs.count == args.count else { return false }
            for (sp, sv) in zip(args, vargs) where !matchInto(sp, sv, &bag) {
                return false
            }
            return true
        case .tuple(let members):
            guard case .tuple(let xs) = v, xs.count == members.count else { return false }
            for (sp, sv) in zip(members, xs) where !matchInto(sp, sv, &bag) {
                return false
            }
            return true
        case .list(let elements):
            guard case .list(let xs) = v, xs.count == elements.count else { return false }
            for (sp, sv) in zip(elements, xs) where !matchInto(sp, sv, &bag) {
                return false
            }
            return true
        case .cons(let head, let tail):
            guard case .list(let xs) = v, !xs.isEmpty else { return false }
            if !matchInto(head, xs[0], &bag) { return false }
            return matchInto(tail, .list(Array(xs.dropFirst())), &bag)
        case .record(let fields):
            // `{ f1, f2 }` — bind each listed field's value. Partial
            // match: extra fields on the value are silently ignored.
            // Typecheck has already verified every listed field
            // exists on the value's type, so a missing field here is
            // a typechecker bug → return false rather than crashing.
            guard case .record(let recFields, _) = v else { return false }
            for fname in fields {
                guard let fv = recFields[fname] else { return false }
                bag.append((fname, fv))
            }
            return true
        }
    }

    // MARK: - Helpers

    static func typeOf(_ v: MarValue) -> String {
        switch v {
        case .int: return "Int"
        case .float: return "Float"
        case .decimal: return "Decimal"
        case .division: return "Decimal.Division"
        case .string: return "String"
        case .bool: return "Bool"
        case .unit: return "Unit"
        case .duration: return "Duration"
        case .time: return "Time"
        case .list: return "List"
        case .tuple: return "Tuple"
        case .record: return "Record"
        case .ctor(let tag, _, _): return "Ctor(\(tag))"
        case .char: return "Char"
        case .dict: return "Dict"
        case .set: return "Set"
        case .fn: return "Function"
        case .view: return "View"
        case .effect: return "Effect"
        }
    }
}
