// Loads a parsed Module into an Env: re-exposes imported names,
// registers ctor functions for custom types, then evaluates each
// value declaration. Direct port of `loadModule` in runtime.js.
//
// Service contracts carry their own verb + path (stamped by
// Service.declare), so the loader treats them like any other value —
// no provenance stamping needed.

import Foundation

enum MarLoader {

    /// Run a module's declarations into the given environment. The
    /// env is mutated in place — typical use is the global env
    /// returned by `MarBuiltins.makeEnv()`.
    static func load(module: Module, into env: Env) throws {
        // Pass 0: imports — re-bind exposed names from already-known
        // qualified bindings. Without this, code that compiles
        // (e.g. `column [...]` after `import View exposing (column)`)
        // explodes at runtime with "unbound name: column".
        for imp in module.imports {
            let modName = imp.module.joined(separator: ".")
            // `exposing (..)`: bind every export of the module bare,
            // values and ctors registered as `modName.x` in the env
            // chain (for builtin modules like UI, the whole
            // vocabulary). Mirrors the typechecker's wildcard.
            if imp.all {
                for (name, v) in env.exportsOf(modName) {
                    env.define(name, v)
                }
            }
            for item in imp.exposing {
                let qualified = modName + "." + item
                if let v = env.lookup(qualified) {
                    env.define(item, v)
                }
            }
        }

        let modName = module.name.joined(separator: ".")

        // Pass 1: register custom-type constructors as values. Nullary
        // ctors become VCtor values directly; n-ary ctors become
        // native functions that, when applied, produce a VCtor. Each
        // ctor is bound bare (intra-module use) AND under the
        // qualified `Module.Ctor` form (so other modules can import
        // them via `import M exposing (T(..))` or reference them as
        // `M.Ctor` directly).
        for d in module.decls {
            guard case .customType(let typeName, let ctors) = d else { continue }
            var ctorNames: [String] = []
            var allZeroArg = true
            for c in ctors {
                let val: MarValue
                if c.argCount == 0 {
                    val = .ctor(tag: c.name, args: [], origin: nil)
                } else {
                    let ctorName = c.name
                    let arity = c.argCount
                    val = .fn(MarFn.native(arity) { args in
                        .ctor(tag: ctorName, args: args, origin: nil)
                    })
                    allZeroArg = false
                }
                env.define(c.name, val)
                if !modName.isEmpty {
                    env.define(modName + "." + c.name, val)
                }
                ctorNames.append(c.name)
            }
            // Path-pattern enum registry: a `{role:Role}` segment in
            // some Page.dynamic looks up the type here. Only zero-arg
            // ctor types are eligible (mirrors Entity.enum's
            // restriction); the typechecker rejects others, this is
            // a defensive filter.
            if allZeroArg && !typeName.isEmpty {
                MarPath.registerEnumType(typeName, ctorNames: ctorNames)
                if !modName.isEmpty {
                    MarPath.registerEnumType(modName + "." + typeName, ctorNames: ctorNames)
                }
            }
        }

        // Pass 2: pre-bind value names with placeholders so mutually
        // recursive references can resolve through env.lookup. The
        // actual value gets defined for real in pass 3.
        for d in module.decls {
            if case .value(let name, _, _) = d {
                env.define(name, .unit)
                if !modName.isEmpty {
                    env.define(modName + "." + name, .unit)
                }
            }
        }

        // Pass 3: evaluate each value declaration. Each value is
        // bound BOTH bare and qualified — bare for intra-module
        // references, qualified so EQualified lookups from other
        // modules resolve. Without the qualified alias, two
        // modules that both define `page` would silently overwrite
        // each other in the bare slot.
        for d in module.decls {
            guard case .value(let name, let params, let body) = d else { continue }

            // ValueDecl with parameters is sugar for ELambda. Wrap
            // before evaluating so `def foo a b = body` becomes a
            // proper closure.
            let bodyExpr: Expr
            if params.isEmpty {
                bodyExpr = body
            } else {
                bodyExpr = .lambda(params: params, body: body)
            }

            let val = try Eval.eval(bodyExpr, env)

            env.define(name, val)
            if !modName.isEmpty {
                env.define(modName + "." + name, val)
            }
        }
    }
}
