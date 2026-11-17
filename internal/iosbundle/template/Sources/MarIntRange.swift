// Int is 53 bits wide, and leaving that range is an error rather than a number
// nobody asked for. Mirrors internal/runtime/intrange.go and the checkedInt in
// internal/jsserve/runtime.js: including the message, which the conformance
// corpus compares across the three.
//
// Swift is the runtime that TRAPPED on overflow before this: an uncatchable
// crash, so the app vanished without saying anything. Go wrapped around and
// JavaScript quietly lost precision. Three behaviours for the same expression
// is what the bound exists to end.

import Foundation

enum MarInt {
    static let max: Int = (1 << 53) - 1  // 9007199254740991
    static let min: Int = -max           // -9007199254740991

    static func inRange(_ v: Int) -> Bool { v >= min && v <= max }

    static func overflow(_ a: Int, _ op: String, _ b: Int) -> MarRuntimeError {
        .message("Int overflow: \(a) \(op) \(b) is outside the range of Int (\(min) to \(max))")
    }

    // `outOfRange`, the twin of Go's intOutOfRange, lived here and nothing
    // called it: on the server that sentence is raised at ONE boundary, the
    // database, and a phone has none. Removed rather than kept for symmetry --
    // a message no code can produce is a claim about behaviour that does not
    // exist.

    /// What building a Duration raises when the interval does not fit in Int
    /// milliseconds. The same sentence as Go's durationOutOfRange
    /// (internal/runtime/intrange.go) and runtime.js's.
    static func durationOutOfRange(_ source: String) -> MarRuntimeError {
        .message("Duration out of range: \(source) is outside the range of a Duration"
            + " (\(min) to \(max) milliseconds)")
    }

    /// n of a unit, unitMs milliseconds each, or the refusal naming the
    /// constructor and its argument.
    static func duration(_ name: String, _ n: Int, _ unitMs: Int) throws -> Int {
        let (ms, didOverflow) = n.multipliedReportingOverflow(by: unitMs)
        guard !didOverflow, inRange(ms) else { throw durationOutOfRange("\(name) \(n)") }
        return ms
    }

    /// Both operands are already in range, so the sum reaches 2^54 and cannot
    /// trap. Only the range needs checking.
    static func add(_ a: Int, _ b: Int) throws -> Int {
        let c = a + b
        guard inRange(c) else { throw overflow(a, "+", b) }
        return c
    }

    static func sub(_ a: Int, _ b: Int) throws -> Int {
        let c = a - b
        guard inRange(c) else { throw overflow(a, "-", b) }
        return c
    }

    /// A product of two in-range operands reaches 2^106, which DOES overflow
    /// Int64, and in Swift overflowing is a trap, not a wrap, so the range
    /// check would never get to run. `multipliedReportingOverflow` is the only
    /// form that survives long enough to report.
    static func mul(_ a: Int, _ b: Int) throws -> Int {
        let (c, didOverflow) = a.multipliedReportingOverflow(by: b)
        guard !didOverflow, inRange(c) else { throw overflow(a, "*", b) }
        return c
    }
}
