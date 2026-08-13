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

    static func outOfRange(_ source: String, _ v: Int) -> MarRuntimeError {
        .message("Int out of range: \(v) from \(source) is outside the range of Int (\(min) to \(max))")
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
