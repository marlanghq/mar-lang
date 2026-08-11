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
// NOT `indirect enum` as a whole: a blanket `indirect` heap-boxes EVERY value,
// including `.int`, behind an allocation + ARC retain/release. A canvas frame
// builds millions of MarValues (every `rect x y w h c` is 5 scalar args, times
// hundreds of shapes), so that boxing dominated the interpreter — it's why iOS
// native (20fps) trailed the same tree-walk running on JavaScriptCore in the
// web view (30fps): JSC tags small ints and doesn't refcount them; we were
// heap-allocating them. Only `.view` embeds a MarValue BY VALUE directly
// (MarView.msg is `MarValue?`), so only it needs `indirect` to break the
// size recursion. Every other case holds MarValues through an Array /
// Dictionary / class (`.list`, `.record`, `.ctor`, `.fn`, …) — those are
// already references, so the enum stays finite-sized and scalars live inline
// on the stack with no allocation and no ARC traffic.
enum MarValue: @unchecked Sendable {
    case int(Int)
    case float(Double)
    case string(String)
    case bool(Bool)
    case unit
    /// Time interval normalized to (fractional) seconds. Built only via
    /// Time.* smart constructors (Time.millis gives sub-second); user
    /// code never coerces a number into one directly.
    case duration(Double)
    /// Absolute moment, Unix milliseconds. Built via Time.now or
    /// Time.fromIso. Wire format is `{"__time": "ISO 8601"}`.
    case time(Int)
    /// A rotation, carried as deci-degrees in 0..3599. Built only via
    /// Math.degrees / .deciDegrees / .turns, so the unit is named at
    /// construction and nowhere else (ADR 0029). The representation is
    /// normative — it fixes the resolution — but not observable, since no
    /// builtin hands it back. Wire format: `{"__angle": 450}`.
    case angle(Int)
    /// A single Unicode code point — Elm-style Char, NOT a grapheme
    /// cluster. We use `Unicode.Scalar` (not Swift's default
    /// `Character`) so semantics line up exactly with Go's `rune` and
    /// JS code points: `String.toList "🇧🇷"` yields two Chars, not
    /// one. Wire format: `{"__char": "x"}`.
    case char(Unicode.Scalar)
    /// Exact base-10 number (see MarDecimal.swift). Wire format:
    /// `{"__dec": "1.50"}` — a string, never a JSON number.
    case decimal(MarDec)
    /// The inert exact quotient `/` produces. Opaque: no codec, no
    /// arithmetic, not comparable — only Decimal.rounded / exact /
    /// withRemainder turn it into a value.
    case division(num: MarDec, den: MarDec)
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

    /// Tagged union constructor. `origin` is a vestigial provenance slot
    /// kept on the value shape so the many `.ctor(..., origin: nil)` call
    /// sites stay uniform; it is always nil now that Service contracts
    /// carry their own verb + path directly.
    case ctor(tag: String, args: [MarValue], origin: ServiceOrigin?)

    case fn(MarFn)
    /// The one case that embeds a MarValue by value (via MarView.msg :
    /// MarValue?), so it alone carries `indirect` to keep the enum finite.
    indirect case view(MarView)
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
        // Numeric equality: 1.50 == 1.5 (scale is display metadata).
        case (.decimal(let a), .decimal(let b)): return DecMath.decCompare(a, b) == 0
        case (.string(let a), .string(let b)): return a == b
        case (.char(let a),   .char(let b)):   return a == b
        // Every constructor wraps into 0..3599, so equality on the
        // representation IS equality of the rotation. Load-bearing: an
        // Angle lives in models, and models go through time travel,
        // App.shared and a rules engine replayed on both sides.
        case (.angle(let a),  .angle(let b)):  return a == b
        // Durations normalize to seconds at construction, so
        // Time.seconds 60 and Time.minutes 1 ARE the same interval. A
        // Time is the same moment when it is the same Unix
        // millisecond. Both fell through to `default: return false`
        // here, in Go and in JS too — the three runtimes agreed on the
        // wrong answer, which is exactly what a drift test cannot see.
        case (.duration(let a), .duration(let b)): return a == b
        case (.time(let a),   .time(let b)):   return a == b
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
        case (.decimal(let a), .decimal(let b)):
            return DecMath.decCompare(a, b)
        case (.string(let a), .string(let b)):
            return a < b ? -1 : (a > b ? 1 : 0)
        case (.char(let a), .char(let b)):
            return a.value < b.value ? -1 : (a.value > b.value ? 1 : 0)
        default:
            return 0
        }
    }
}
