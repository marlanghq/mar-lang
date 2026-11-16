// Runtime values for the mar interpreter: direct port of the JS
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
// boundaries: the dispatch loop reads them on @MainActor and
// URLSession completion handlers re-enter @MainActor before
// touching them again. Letting Swift 6 infer Sendable would force
// us to refactor the runtime model unnecessarily; this conformance
// captures the invariant we actually rely on.
// NOT `indirect enum` as a whole: a blanket `indirect` heap-boxes EVERY value,
// including `.int`, behind an allocation + ARC retain/release. A canvas frame
// builds millions of MarValues (every `rect x y w h c` is 5 scalar args, times
// hundreds of shapes), so that boxing dominated the interpreter: it's why iOS
// native (20fps) trailed the same tree-walk running on JavaScriptCore in the
// web view (30fps): JSC tags small ints and doesn't refcount them; we were
// heap-allocating them. Only `.view` embeds a MarValue BY VALUE directly
// (MarView.msg is `MarValue?`), so only it needs `indirect` to break the
// size recursion. Every other case holds MarValues through an Array /
// Dictionary / class (`.list`, `.record`, `.ctor`, `.fn`, …): those are
// already references, so the enum stays finite-sized and scalars live inline
// on the stack with no allocation and no ARC traffic.
enum MarValue: @unchecked Sendable {
    case int(Int)
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
    /// normative, it fixes the resolution, but not observable, since no
    /// builtin hands it back. Wire format: `{"__angle": 450}`.
    case angle(Int)
    /// A single Unicode scalar value: Elm-style Char, NOT a grapheme
    /// cluster. We use `Unicode.Scalar` (not Swift's default
    /// `Character`) so semantics line up exactly with Go's `rune` and
    /// what JS yields when it spreads a string: `String.toList "🇧🇷"` gives two Chars, not
    /// one. Wire format: `{"__char": "x"}`.
    case char(Unicode.Scalar)
    /// Exact base-10 number (see MarDecimal.swift). Wire format:
    /// `{"__dec": "1.50"}`, a string, never a JSON number.
    case decimal(MarDec)
    /// The inert exact quotient `/` produces. Opaque: no codec, no
    /// arithmetic, not comparable: only Decimal.rounded / exact /
    /// withRemainder turn it into a value.
    ///
    /// `indirect` for SIZE, not recursion: an enum is as large as its
    /// largest case, and two inline MarDecs (2 x 24 bytes) made EVERY
    /// MarValue 48 bytes, an Int included. Boxed, this rare case costs one
    /// allocation per `/`, and every other value shrinks to 32 bytes (a
    /// stride of 32: 24 for `.decimal` / `.ctor` plus the tag), which is a
    /// third less to copy in every list, record, Dict pair and Env slot.
    indirect case division(num: MarDec, den: MarDec)
    case list([MarValue])
    case tuple([MarValue])
    case record(fields: [String: MarValue], order: [String])

    /// Ordered, polymorphic key-value map (Elm-style `Dict k v`). Pairs
    /// are kept sorted ascending by key per compareMar; the invariant
    /// is rebuilt on every insert / merge by Dict.swift's helpers.
    /// Wire format: `{"__dict": [[k, v], ...]}` (see MarJSONCodec).
    case dict([MarPair])

    /// Ordered set (Elm-style `Set k`). Same comparable-key constraint
    /// and sorted-on-mutation invariant as `.dict`. Wire format:
    /// `{"__set": [k, ...]}`.
    case set([MarValue])

    /// Tagged union constructor. The tag and the args are the whole identity,
    /// the same as Go's `VCtor{Tag, Args}` and the browser's
    /// `(tag, args) => ({k:'C', tag, args})`.
    case ctor(tag: String, args: [MarValue])

    case fn(MarFn)
    /// The one case that embeds a MarValue by value (via MarView.msg :
    /// MarValue?), so it alone carries `indirect` to keep the enum finite.
    indirect case view(MarView)
    case effect(MarEffect)
}

/// One entry of a `.dict`. A struct rather than a `(MarValue, MarValue)`
/// tuple, for speed alone: an Array of tuples is copied and destroyed through
/// the Swift runtime's GENERIC tuple witnesses, which walk the tuple's metadata
/// for every element, while a struct gets value witnesses compiled for it.
/// Dict.insert and Dict.update copy the whole pair array on every call (a Dict
/// is an immutable value, so the old one is still referenced), and that copy,
/// plus destroying the previous version, was most of the interpreter's time on
/// a Dict-heavy fold.
struct MarPair {
    var key: MarValue
    var value: MarValue
}

// MARK: - Functions (with native + interpreted variants)

/// Reference type so `applied` accumulates across partial applications
/// without copying the whole closure on every step. Mirrors the JS
/// `apply` function's `concat([arg])` pattern but in-place isn't safe
/// because the caller might re-apply the same partial twice, so we
/// always return a fresh MarFn from `apply` instead of mutating.
final class MarFn {
    let arity: Int
    let applied: [MarValue]
    let params: [String]?              // nil for natives
    let body: Expr?                    // nil for natives
    let env: Env?                      // nil for natives
    let native: (@MainActor ([MarValue]) throws -> MarValue)?

    init(arity: Int,
         applied: [MarValue],
         params: [String]?,
         body: Expr?,
         env: Env?,
         native: (@MainActor ([MarValue]) throws -> MarValue)?) {
        self.arity = arity
        self.applied = applied
        self.params = params
        self.body = body
        self.env = env
        self.native = native
    }

    /// Native function constructor: used by Builtins to register
    /// arithmetic, view ctors, effect helpers, etc.
    /// A builtin runs on the main actor. That is not a new constraint, it is
    /// the one that already held: the only dispatch anywhere in this template
    /// is DispatchQueue.main.async, and evaluation is entered from view bodies
    /// and from dispatch, both of which are main. Saying it here rather than
    /// at each of the ten call sites is the difference between a rule and ten
    /// reminders -- and it is what lets MarSharedRegistry and MarPath hold
    /// global state without a lock and without `nonisolated(unsafe)`, which
    /// would silence Swift 6 rather than answer it.
    static func native(_ arity: Int,
                       _ fn: @escaping @MainActor ([MarValue]) throws -> MarValue) -> MarFn {
        MarFn(arity: arity, applied: [], params: nil, body: nil, env: nil, native: fn)
    }

    /// Interpreted closure: produced by `ELambda` evaluation.
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

/// View AST: produced by UI.* builtins, consumed by the SwiftUI
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
/// MarDispatcher when the response arrives: `run` itself returns
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
    /// Something a CHECKED program cannot do. The Go evaluator drew this line
    /// and kept it -- of its twelve errors only two survived as real program
    /// errors -- and this side had no line at all: a bug in Mar and a mistake
    /// in the user's code arrived in the same voice, so the user read a
    /// sentence that sounded like their fault and the report never came.
    ///
    /// Keep the sentence identical to internalErrorf in internal/runtime and
    /// to marInternal in internal/jsserve/runtime.js; the parity fixture
    /// compares it.
    case internalError(String)

    var errorDescription: String? {
        switch self {
        case .unboundName(let n):           return MarRuntimeError.internalText("unbound name: \(n)")
        case .typeMismatch(let e, let g):   return MarRuntimeError.internalText("type mismatch: expected \(e), got \(g)")
        case .applyOnNonFunction:           return MarRuntimeError.internalText("tried to apply a non-function value")
        case .noMatch:                      return "no case branch matched"
        case .message(let m):               return m
        case .internalError(let m):         return MarRuntimeError.internalText(m)
        }
    }

    /// `noMatch` deliberately stays out of this: a case expression that matched
    /// nothing is the user's program, both clients match on its text, and
    /// there is a test that says so.
    static func internalText(_ what: String) -> String {
        "internal error: " + what +
            " — a checked program cannot do this, so this is a bug in Mar rather than in your code. Please report it."
    }
}

// MARK: - Equality / Comparison
//
// Mirrors `eqValues` and `cmpValues` in runtime.js: used to implement
// `==`, `/=`, `<`, `>`, `<=`, `>=` builtins.

extension MarValue {
    func equalsMar(_ other: MarValue) -> Bool {
        switch (self, other) {
        case (.int(let a),    .int(let b)):    return a == b
        // Numeric equality: 1.50 == 1.5 (scale is display metadata).
        case (.decimal(let a), .decimal(let b)): return DecMath.decCompare(a, b) == 0
        // Scalar-sequence equality, NOT Swift's ==, which is Unicode
        // canonical equivalence and would call the precomposed and combining
        // spellings of one word the same String. MarDict and MarSet key off
        // this, so it decides what a dictionary CONTAINS. UTF-8 encoding is a
        // bijection on scalar sequences, so comparing the bytes IS comparing
        // the scalars, and it is faster. ADR 0038 clause 4.
        case (.string(let a), .string(let b)): return a.utf8.elementsEqual(b.utf8)
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
        // here, in Go and in JS too: the three runtimes agreed on the
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
        case (.ctor(let ta, let aa), .ctor(let tb, let ab)):
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
                if !pa.key.equalsMar(pb.key) { return false }
                if !pa.value.equalsMar(pb.value) { return false }
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

    /// Three-way comparison used by `<`, `>`, `<=`, `>=`, by `max`/`min`,
    /// and — load-bearing — as the sort key that Dict and Set keep their
    /// entries in. Ordering is the Comparable set: Int, Decimal, String,
    /// Char, and tuples and lists of those. Tuples compare component-wise
    /// left to right; lists compare lexicographically with a proper prefix
    /// less than what extends it, so [1] < [1, 2].
    ///
    /// The default arm answers 0, which reads as "these are equal" and is
    /// the one place the three runtimes still differ: Go returns an error
    /// and the web runtime throws. Making this one throw would mean
    /// threading `throws` through the Dict and Set binary searches and
    /// merges, none of which can throw today, and the arm is unreachable
    /// from a checked program — the Comparable constraint is what keeps
    /// records, constructors and functions from arriving here. It is
    /// loud rather than silent so that if a compiler bug ever does reach
    /// it, there is something to find.
    func compareMar(_ other: MarValue) -> Int {
        switch (self, other) {
        case (.int(let a), .int(let b)):       return a < b ? -1 : (a > b ? 1 : 0)
        case (.decimal(let a), .decimal(let b)):
            return DecMath.decCompare(a, b)
        case (.string(let a), .string(let b)):
            // Lexicographic by scalar. UTF-8 byte order and scalar order are
            // the same order. Swift's own < is canonical and would call two
            // spellings equal. ADR 0038 clause 5.
            if a.utf8.elementsEqual(b.utf8) { return 0 }
            return a.utf8.lexicographicallyPrecedes(b.utf8) ? -1 : 1
        case (.char(let a), .char(let b)):
            return a.value < b.value ? -1 : (a.value > b.value ? 1 : 0)
        case (.tuple(let a), .tuple(let b)):
            // Two tuples that reach here always have the same arity:
            // different arities are different types and never unify.
            for (x, y) in zip(a, b) {
                let c = x.compareMar(y)
                if c != 0 { return c }
            }
            return 0
        case (.list(let a), .list(let b)):
            for (x, y) in zip(a, b) {
                let c = x.compareMar(y)
                if c != 0 { return c }
            }
            return a.count < b.count ? -1 : (a.count > b.count ? 1 : 0)
        default:
            print("mar: comparison: unsupported types (\(Eval.typeOf(self)) and \(Eval.typeOf(other))) — this is a bug in Mar rather than in your code")
            return 0
        }
    }
}
