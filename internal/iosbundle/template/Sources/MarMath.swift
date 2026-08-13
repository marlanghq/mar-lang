import Foundation

/// Math: deterministic integer trigonometry and roots.
///
/// Semantics mirror `internal/runtime/math.go` and the "Math (integer
/// trigonometry)" section of `runtime.js` exactly; the conformance vectors
/// hold all three to the same integers. Everything reads the one generated
/// quarter-wave table in MarMathTable.swift: no `sin` from Foundation
/// anywhere, because three libms would be three answers and a rules engine
/// replayed on both sides needs one.
enum MarMath {
    static let deciFull = 3600
    static let deciQuarter = 900
    /// brads: the 256-step turn seasons-gp and vortex already count in.
    static let bradsPerTurn = 256
    /// atan2 halves its legs below this so a product with a table entry
    /// (< 2^10) stays inside the 53 bits an Int has (ADR 0021).
    static let atanLegLimit = 1 << 40

    /// The positive modulo every Angle constructor wraps through, which is
    /// what lets any Int be a valid argument and makes a negative angle mean
    /// what it should.
    static func posMod(_ n: Int, _ m: Int) -> Int { ((n % m) + m) % m }

    /// Folds the whole circle onto the quarter-wave table: quadrants 1 and 3
    /// read it backwards, 2 and 3 negate. No interpolation, because the table
    /// already holds every representable input.
    static func sinDeci(_ a: Int) -> Int {
        let q = a / deciQuarter
        let r = a - q * deciQuarter
        let v = q & 1 == 0 ? MarMathTable.sinQuarter[r] : MarMathTable.sinQuarter[deciQuarter - r]
        return q & 2 == 0 ? v : -v
    }

    static func cosDeci(_ a: Int) -> Int { sinDeci(posMod(a + deciQuarter, deciFull)) }

    /// Integer Newton, floored: converges from above and stops the moment it
    /// would climb back, which is exactly floor(sqrt(n)) for n >= 1.
    static func isqrt(_ n: Int) -> Int {
        if n <= 0 { return 0 }
        var x = n
        var y = (x + 1) / 2
        while y < x {
            x = y
            y = (x + n / x) / 2
        }
        return x
    }

    /// The angle of the vector (x, y) in deci-degrees, 0..3599, searching the
    /// SAME table sin reads, which is what makes the two structurally unable
    /// to disagree about where 45.0° is. See math.go for the derivation of the
    /// nearest-by-cross-product rule; this is a line-for-line port.
    static func atan2Deci(_ y: Int, _ x: Int) -> Int {
        if x == 0 && y == 0 { return 0 }
        var ax = abs(x)
        var ay = abs(y)
        let swapped = ay > ax
        if swapped { swap(&ax, &ay) }
        while ax >= atanLegLimit {
            ax /= 2
            ay /= 2
        }
        var lo = 0
        var hi = 450
        while lo < hi {
            let mid = (lo + hi + 1) / 2
            if MarMathTable.sinQuarter[mid] * ax <= MarMathTable.sinQuarter[deciQuarter - mid] * ay {
                lo = mid
            } else {
                hi = mid - 1
            }
        }
        var inner = lo
        if inner < 450 {
            let d0 = MarMathTable.sinQuarter[deciQuarter - inner] * ay - MarMathTable.sinQuarter[inner] * ax
            let d1 = MarMathTable.sinQuarter[inner + 1] * ax - MarMathTable.sinQuarter[deciQuarter - inner - 1] * ay
            if d1 < d0 { inner += 1 }
        }
        if swapped { inner = deciQuarter - inner }
        let a: Int
        if x >= 0 {
            a = y >= 0 ? inner : deciFull - inner
        } else {
            a = y >= 0 ? 1800 - inner : 1800 + inner
        }
        return posMod(a, deciFull)
    }

    /// Registers Math.* under both the internal name the checker elaborates
    /// to and the qualified name, the same pairing MarBuiltins uses for Time.
    static func register(_ env: Env) {
        // Each constructor reduces into one turn BEFORE scaling, so
        // `Math.degrees` of a huge Int cannot overflow on the way to a value
        // that was always going to land in 0..3599.
        func angleFrom(_ perTurn: Int, _ scale: Int) -> MarFn {
            MarFn.native(1) { args in
                .angle(posMod(MarBuiltins.asInt(args[0]), perTurn) * scale)
            }
        }
        let degrees = angleFrom(360, 10)
        let deciDegrees = angleFrom(deciFull, 1)
        // 3600/256 is not a whole number of deci-degrees, so one brad floors;
        // a full turn is exact.
        let turns = MarFn.native(1) { args in
            .angle(posMod(MarBuiltins.asInt(args[0]), bradsPerTurn) * deciFull / bradsPerTurn)
        }
        env.define("mathDegrees", .fn(degrees));         env.define("Math.degrees", .fn(degrees))
        env.define("mathDeciDegrees", .fn(deciDegrees)); env.define("Math.deciDegrees", .fn(deciDegrees))
        env.define("mathTurns", .fn(turns));             env.define("Math.turns", .fn(turns))

        let add = MarFn.native(2) { args in .angle(posMod(args[0].asAngle + args[1].asAngle, deciFull)) }
        let subtract = MarFn.native(2) { args in .angle(posMod(args[0].asAngle - args[1].asAngle, deciFull)) }
        let opposite = MarFn.native(1) { args in .angle(posMod(args[0].asAngle + 1800, deciFull)) }
        env.define("mathAdd", .fn(add));            env.define("Math.add", .fn(add))
        env.define("mathSubtract", .fn(subtract));  env.define("Math.subtract", .fn(subtract))
        env.define("mathOpposite", .fn(opposite));  env.define("Math.opposite", .fn(opposite))

        let sinFn = MarFn.native(1) { args in .int(sinDeci(args[0].asAngle)) }
        let cosFn = MarFn.native(1) { args in .int(cosDeci(args[0].asAngle)) }
        let atan2Fn = MarFn.native(2) { args in .angle(atan2Deci(MarBuiltins.asInt(args[0]), MarBuiltins.asInt(args[1]))) }
        let isqrtFn = MarFn.native(1) { args in .int(isqrt(MarBuiltins.asInt(args[0]))) }
        env.define("mathSin", .fn(sinFn));      env.define("Math.sin", .fn(sinFn))
        env.define("mathCos", .fn(cosFn));      env.define("Math.cos", .fn(cosFn))
        env.define("mathAtan2", .fn(atan2Fn));  env.define("Math.atan2", .fn(atan2Fn))
        env.define("mathIsqrt", .fn(isqrtFn));  env.define("Math.isqrt", .fn(isqrtFn))
    }
}

extension MarValue {
    /// The deci-degree payload of an `.angle`, 0 for anything else. The
    /// checker has already proven the argument is an Angle, so the fallback
    /// only exists because the enum is open.
    var asAngle: Int {
        if case .angle(let d) = self { return d }
        return 0
    }
}
