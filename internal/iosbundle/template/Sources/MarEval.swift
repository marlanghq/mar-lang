// Tree-walking interpreter for mar's expression AST. Direct
// translation of `evalExpr / apply / matchInto` in runtime.js.
//
// Why tree-walking and not bytecode: the JS runtime is tree-walking
// too, the AST is small, perf is fine for hand-written UI code, and
// staying line-by-line equivalent to the JS keeps the two
// implementations from drifting.

import Foundation

enum Eval {

    // MARK: - Expressions

    static func eval(_ expr: Expr, _ env: Env) throws -> MarValue {
        switch expr {
        case .int(let n):       return .int(n)
        case .float(let n):     return .float(n)
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
            case .float(let f): return .float(-f)
            default:
                throw MarRuntimeError.message("negate: unsupported type")
            }

        case .app(let fn, let arg):
            let f = try eval(fn, env)
            let a = try eval(arg, env)
            return try apply(f, a)

        case .binop(let op, let left, let right):
            guard let opFn = env.lookup(op) else {
                throw MarRuntimeError.unboundName("operator \(op)")
            }
            let l = try eval(left, env)
            let r = try eval(right, env)
            let partial = try apply(opFn, l)
            return try apply(partial, r)

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
                var bag: [String: MarValue] = [:]
                if matchInto(b.pattern, v, &bag) {
                    let frame = Env(parent: cur)
                    for (k, val) in bag { frame.bindings[k] = val }
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

        case .fieldAccess(let recordE, let field):
            let r = try eval(recordE, env)
            guard case .record(let fs, _) = r else {
                throw MarRuntimeError.typeMismatch(expected: "record", got: typeOf(r))
            }
            return fs[field] ?? .unit

        case .fieldAccessor(let field):
            return .fn(MarFn.native(1) { args in
                guard case .record(let fs, _) = args[0] else {
                    throw MarRuntimeError.typeMismatch(expected: "record", got: typeOf(args[0]))
                }
                return fs[field] ?? .unit
            })

        case .caseExpr(let subject, let branches):
            let subj = try eval(subject, env)
            for br in branches {
                var bag: [String: MarValue] = [:]
                if matchInto(br.pattern, subj, &bag) {
                    let frame = Env(parent: env)
                    for (k, v) in bag { frame.bindings[k] = v }
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

    /// Applies a function to one argument with currying. If the
    /// resulting `applied` count is below arity, returns a fresh
    /// MarFn that remembers what's been applied. Otherwise either
    /// runs the native or evaluates the body.
    static func apply(_ fn: MarValue, _ arg: MarValue) throws -> MarValue {
        guard case .fn(let f) = fn else {
            throw MarRuntimeError.applyOnNonFunction
        }
        let next = f.applied + [arg]
        if next.count < f.arity {
            return .fn(MarFn(arity: f.arity,
                             applied: next,
                             params: f.params,
                             body: f.body,
                             env: f.env,
                             native: f.native))
        }
        if let native = f.native {
            return try native(next)
        }
        guard let params = f.params, let body = f.body, var fnEnv = f.env else {
            throw MarRuntimeError.message("malformed function: no native and no body")
        }
        for (p, v) in zip(params, next) {
            fnEnv = fnEnv.bind(p, v)
        }
        return try eval(body, fnEnv)
    }

    // MARK: - Pattern matching

    /// Tries to match a pattern against a value, populating `bag`
    /// with bindings. Returns true on success. Mirrors `matchInto`
    /// in runtime.js.
    static func matchInto(_ pat: Pat, _ v: MarValue, _ bag: inout [String: MarValue]) -> Bool {
        switch pat {
        case .wildcard:
            return true
        case .var(let name):
            bag[name] = v
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
                bag[fname] = fv
            }
            return true
        }
    }

    // MARK: - Helpers

    static func typeOf(_ v: MarValue) -> String {
        switch v {
        case .int: return "Int"
        case .float: return "Float"
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
