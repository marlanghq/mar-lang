// Runtime values for the mar interpreter — direct port of the JS
// `VInt / VString / VRecord / VCtor / VFn / VView / VEffect / ...`
// constructors in internal/jsserve/runtime.js.
//
// Indirect enum so we can store recursive shapes (records of records,
// list of values, ctor args, etc.) without manual boxing. MarFn and
// MarEffect are reference types because they carry mutable state
// (partial application, native closures over the environment).

import Foundation

// `@unchecked Sendable` because MarValue carries reference types
// (MarFn / MarEffect) that hold native closures the compiler can't
// prove are concurrency-safe. In practice they're treated as
// immutable values: once constructed, no mutation crosses actor
// boundaries — the dispatch loop reads them on @MainActor and
// URLSession completion handlers re-enter @MainActor before
// touching them again. Letting Swift 6 infer Sendable would force
// us to refactor the runtime model unnecessarily; this conformance
// captures the invariant we actually rely on.
indirect enum MarValue: @unchecked Sendable {
    case int(Int)
    case float(Double)
    case string(String)
    case bool(Bool)
    case unit
    /// Time interval normalized to seconds. Built only via Time.*
    /// smart constructors; user code never coerces an Int into one
    /// directly.
    case duration(Int)
    /// Absolute moment, Unix milliseconds. Built via Time.now or
    /// Time.fromIso. Wire format is `{"__time": "ISO 8601"}`.
    case time(Int)
    /// A single Unicode code point — Elm-style Char, NOT a grapheme
    /// cluster. We use `Unicode.Scalar` (not Swift's default
    /// `Character`) so semantics line up exactly with Go's `rune` and
    /// JS code points: `String.toList "🇧🇷"` yields two Chars, not
    /// one. Wire format: `{"__char": "x"}`.
    case char(Unicode.Scalar)
    case list([MarValue])
    case tuple([MarValue])
    case record(fields: [String: MarValue], order: [String])

    /// Ordered, polymorphic key-value map (Elm-style `Dict k v`). Pairs
    /// are kept sorted ascending by key per compareMar; the invariant
    /// is rebuilt on every insert / merge by Dict.swift's helpers.
    /// Wire format: `{"__dict": [[k, v], ...]}` (see MarJSONCodec).
    case dict([(MarValue, MarValue)])

    /// Ordered set (Elm-style `Set k`). Same comparable-key constraint
    /// and sorted-on-mutation invariant as `.dict`. Wire format:
    /// `{"__set": [k, ...]}`.
    case set([MarValue])

    /// Tagged union constructor. `origin` is non-nil for `__Service`
    /// values once they're stamped by `loadModule` so `Service.call`
    /// can derive the wire path.
    case ctor(tag: String, args: [MarValue], origin: ServiceOrigin?)

    case fn(MarFn)
    case view(MarView)
    case effect(MarEffect)
}

struct ServiceOrigin: Hashable {
    let module: String
    let name: String
}

// MARK: - Functions (with native + interpreted variants)

/// Reference type so `applied` accumulates across partial applications
/// without copying the whole closure on every step. Mirrors the JS
/// `apply` function's `concat([arg])` pattern but in-place isn't safe
/// because the caller might re-apply the same partial twice — so we
/// always return a fresh MarFn from `apply` instead of mutating.
final class MarFn {
    let arity: Int
    let applied: [MarValue]
    let params: [String]?              // nil for natives
    let body: Expr?                    // nil for natives
    let env: Env?                      // nil for natives
    let native: (([MarValue]) throws -> MarValue)?

    init(arity: Int,
         applied: [MarValue],
         params: [String]?,
         body: Expr?,
         env: Env?,
         native: (([MarValue]) throws -> MarValue)?) {
        self.arity = arity
        self.applied = applied
        self.params = params
        self.body = body
        self.env = env
        self.native = native
    }

    /// Native function constructor — used by Builtins to register
    /// arithmetic, view ctors, effect helpers, etc.
    static func native(_ arity: Int,
                       _ fn: @escaping ([MarValue]) throws -> MarValue) -> MarFn {
        MarFn(arity: arity, applied: [], params: nil, body: nil, env: nil, native: fn)
    }

    /// Interpreted closure — produced by `ELambda` evaluation.
    static func closure(params: [String], body: Expr, env: Env) -> MarFn {
        MarFn(arity: params.count,
              applied: [],
              params: params,
              body: body,
              env: env,
              native: nil)
    }
}

// MARK: - View nodes

/// View AST — produced by UI.* builtins, consumed by the SwiftUI
/// renderer. Mirrors the JS `VView` shape exactly so the wire
/// semantics (which lives entirely in the user's mar code) translates
/// to native rendering without surprises.
struct MarView {
    let tag: String
    let attrs: [Attr]
    let children: [MarView]
    let text: String
    /// For `button`: the Msg to dispatch on tap.
    /// For `textField`: a `String -> Msg` function applied to the
    ///   new text on each keystroke.
    /// nil for everything else.
    let msg: MarValue?
    /// Reserved for future keyed-diff support; currently always nil.
    let key: String?

    struct Attr {
        let name: String
        let value: MarValue
    }
}

// MARK: - Effects

/// An Effect is a thunk that may have side-effects (HTTP, DB, etc.)
/// and produces a value. Async effects (Service.call, Http.get) start
/// a background task in `run` and dispatch a Msg via the global
/// MarDispatcher when the response arrives — `run` itself returns
/// `.unit` synchronously.
final class MarEffect {
    let tag: String
    let run: () throws -> MarValue

    init(tag: String, run: @escaping () throws -> MarValue) {
        self.tag = tag
        self.run = run
    }
}

// MARK: - Errors

enum MarRuntimeError: Error, LocalizedError {
    case unboundName(String)
    case typeMismatch(expected: String, got: String)
    case applyOnNonFunction
    case noMatch
    case message(String)

    var errorDescription: String? {
        switch self {
        case .unboundName(let n):           return "unbound name: \(n)"
        case .typeMismatch(let e, let g):   return "type mismatch: expected \(e), got \(g)"
        case .applyOnNonFunction:           return "tried to apply a non-function value"
        case .noMatch:                      return "no case branch matched"
        case .message(let m):               return m
        }
    }
}

// MARK: - Equality / Comparison
//
// Mirrors `eqValues` and `cmpValues` in runtime.js — used to implement
// `==`, `/=`, `<`, `>`, `<=`, `>=` builtins.

extension MarValue {
    func equalsMar(_ other: MarValue) -> Bool {
        switch (self, other) {
        case (.int(let a),    .int(let b)):    return a == b
        case (.float(let a),  .float(let b)):  return a == b
        case (.int(let a),    .float(let b)):  return Double(a) == b
        case (.float(let a),  .int(let b)):    return a == Double(b)
        case (.string(let a), .string(let b)): return a == b
        case (.char(let a),   .char(let b)):   return a == b
        case (.bool(let a),   .bool(let b)):   return a == b
        case (.unit, .unit):                   return true
        case (.list(let a),   .list(let b)),
             (.tuple(let a),  .tuple(let b)):
            guard a.count == b.count else { return false }
            for (x, y) in zip(a, b) where !x.equalsMar(y) { return false }
            return true
        case (.ctor(let ta, let aa, _), .ctor(let tb, let ab, _)):
            guard ta == tb, aa.count == ab.count else { return false }
            for (x, y) in zip(aa, ab) where !x.equalsMar(y) { return false }
            return true
        case (.record(let fa, _), .record(let fb, _)):
            guard fa.keys == fb.keys else { return false }
            for k in fa.keys where !(fa[k]?.equalsMar(fb[k] ?? .unit) ?? false) {
                return false
            }
            return true
        case (.dict(let a), .dict(let b)):
            guard a.count == b.count else { return false }
            for (pa, pb) in zip(a, b) {
                if !pa.0.equalsMar(pb.0) { return false }
                if !pa.1.equalsMar(pb.1) { return false }
            }
            return true
        case (.set(let a), .set(let b)):
            guard a.count == b.count else { return false }
            for (x, y) in zip(a, b) where !x.equalsMar(y) { return false }
            return true
        default:
            return false
        }
    }

    /// Three-way comparison used by `<`, `>`, `<=`, `>=`. JS limits
    /// this to numbers + strings (other types return 0); we follow.
    func compareMar(_ other: MarValue) -> Int {
        switch (self, other) {
        case (.int(let a), .int(let b)):       return a < b ? -1 : (a > b ? 1 : 0)
        case (.float(let a), .float(let b)):
            if a < b { return -1 }; if a > b { return 1 }; return 0
        case (.int(let a), .float(let b)):
            let da = Double(a)
            if da < b { return -1 }; if da > b { return 1 }; return 0
        case (.float(let a), .int(let b)):
            let db = Double(b)
            if a < db { return -1 }; if a > db { return 1 }; return 0
        case (.string(let a), .string(let b)):
            return a < b ? -1 : (a > b ? 1 : 0)
        case (.char(let a), .char(let b)):
            return a.value < b.value ? -1 : (a.value > b.value ? 1 : 0)
        default:
            return 0
        }
    }
}
