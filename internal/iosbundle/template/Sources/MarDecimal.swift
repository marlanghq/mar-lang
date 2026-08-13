// Exact base-10 arithmetic: the Swift mirror of the Go runtime's
// internal/runtime/decimal.go and the BigInt block in runtime.js.
// Semantics must match those two byte-for-byte (the conformance
// vectors in decimal_test.go / decimal_conformance_test.go are the
// contract).
//
// Swift has no arbitrary-precision integer in the stdlib, and
// Foundation.Decimal's rounding surface doesn't expose the integer
// quotient/remainder split our resolvers are specified in. So the
// coefficient is a plain big-endian base-10 digit array: the values
// are bounded at 34 significant digits and this code runs on user
// actions (money math), not in the frame loop, so schoolbook
// arithmetic is more than fast enough and trivially deterministic.

import Foundation

/// Exact decimal: sign + coefficient digits (big-endian, no leading
/// zeros, `[0]` for zero) + scale. `1.50` is digits [1,5,0], scale 2.
/// Equality is numeric; scale is display metadata.
struct MarDec: Sendable {
    var negative: Bool
    var digits: [UInt8]
    var scale: Int

    static let zero = MarDec(negative: false, digits: [0], scale: 0)

    static func fromInt(_ n: Int) -> MarDec {
        MarDec(negative: n < 0, digits: DecMath.digitsOf(n.magnitude), scale: 0)
    }

    var isZero: Bool { digits == [0] }
}

enum DecMath {
    static let maxDigits = 34

    // MARK: digit-array primitives (unsigned, big-endian)

    static func trim(_ d: [UInt8]) -> [UInt8] {
        var i = 0
        while i < d.count - 1 && d[i] == 0 { i += 1 }
        return Array(d[i...])
    }

    static func digitsOf(_ n: UInt) -> [UInt8] {
        if n == 0 { return [0] }
        var out: [UInt8] = []
        var v = n
        while v > 0 { out.append(UInt8(v % 10)); v /= 10 }
        return out.reversed()
    }

    static func isZero(_ d: [UInt8]) -> Bool { trim(d) == [0] }

    /// Significant digit count; zero counts as one digit (same as Go).
    static func digitCount(_ d: [UInt8]) -> Int { trim(d).count }

    static func cmpAbs(_ a: [UInt8], _ b: [UInt8]) -> Int {
        let ta = trim(a), tb = trim(b)
        if ta.count != tb.count { return ta.count < tb.count ? -1 : 1 }
        for (x, y) in zip(ta, tb) where x != y { return x < y ? -1 : 1 }
        return 0
    }

    static func addAbs(_ a: [UInt8], _ b: [UInt8]) -> [UInt8] {
        var out: [UInt8] = []
        var i = a.count - 1, j = b.count - 1, carry = 0
        while i >= 0 || j >= 0 || carry > 0 {
            var s = carry
            if i >= 0 { s += Int(a[i]); i -= 1 }
            if j >= 0 { s += Int(b[j]); j -= 1 }
            out.append(UInt8(s % 10))
            carry = s / 10
        }
        return trim(out.reversed())
    }

    /// a - b, requires a >= b.
    static func subAbs(_ a: [UInt8], _ b: [UInt8]) -> [UInt8] {
        var out: [UInt8] = []
        var i = a.count - 1, j = b.count - 1, borrow = 0
        while i >= 0 {
            var s = Int(a[i]) - borrow - (j >= 0 ? Int(b[j]) : 0)
            if s < 0 { s += 10; borrow = 1 } else { borrow = 0 }
            out.append(UInt8(s))
            i -= 1; j -= 1
        }
        return trim(out.reversed())
    }

    static func mulAbs(_ a: [UInt8], _ b: [UInt8]) -> [UInt8] {
        let ta = trim(a), tb = trim(b)
        if ta == [0] || tb == [0] { return [0] }
        var acc = [Int](repeating: 0, count: ta.count + tb.count)
        for i in stride(from: ta.count - 1, through: 0, by: -1) {
            for j in stride(from: tb.count - 1, through: 0, by: -1) {
                acc[i + j + 1] += Int(ta[i]) * Int(tb[j])
            }
        }
        for k in stride(from: acc.count - 1, through: 1, by: -1) {
            acc[k - 1] += acc[k] / 10
            acc[k] %= 10
        }
        return trim(acc.map { UInt8($0) })
    }

    static func shift10(_ a: [UInt8], _ n: Int) -> [UInt8] {
        if n <= 0 || isZero(a) { return trim(a) }
        return trim(a) + [UInt8](repeating: 0, count: n)
    }

    /// Schoolbook long division: returns (quotient, remainder).
    static func quoRemAbs(_ n: [UInt8], _ d: [UInt8]) -> ([UInt8], [UInt8]) {
        let td = trim(d)
        precondition(td != [0], "quoRemAbs: division by zero")
        var quo: [UInt8] = []
        var rem: [UInt8] = [0]
        for digit in trim(n) {
            rem = trim(rem + [digit])
            var q: UInt8 = 0
            while cmpAbs(rem, td) >= 0 {
                rem = subAbs(rem, td)
                q += 1
            }
            quo.append(q)
        }
        return (trim(quo), rem)
    }

    /// Digit array → Int, nil when it doesn't fit.
    static func toInt(_ d: [UInt8], negative: Bool) -> Int? {
        var v = 0
        for digit in trim(d) {
            let (m, o1) = v.multipliedReportingOverflow(by: 10)
            if o1 { return nil }
            let (s, o2) = m.addingReportingOverflow(Int(digit))
            if o2 { return nil }
            v = s
        }
        return negative ? -v : v
    }

    // MARK: MarDec operations (mirror decimal.go exactly)

    static func checkBound(_ op: String, _ d: [UInt8]) throws {
        if digitCount(d) > maxDigits {
            throw MarRuntimeError.message("\(op): Decimal overflow — the result exceeds \(maxDigits) significant digits")
        }
    }

    /// Common-scale coefficients as signed digit pairs.
    private static func align(_ a: MarDec, _ b: MarDec) -> (aDigits: [UInt8], bDigits: [UInt8], scale: Int) {
        if a.scale < b.scale {
            return (shift10(a.digits, b.scale - a.scale), trim(b.digits), b.scale)
        }
        if b.scale < a.scale {
            return (trim(a.digits), shift10(b.digits, a.scale - b.scale), a.scale)
        }
        return (trim(a.digits), trim(b.digits), a.scale)
    }

    private static func signedAdd(aNeg: Bool, a: [UInt8], bNeg: Bool, b: [UInt8]) -> (neg: Bool, digits: [UInt8]) {
        if aNeg == bNeg {
            return (aNeg, addAbs(a, b))
        }
        switch cmpAbs(a, b) {
        case 0:  return (false, [0])
        case 1:  return (aNeg, subAbs(a, b))
        default: return (bNeg, subAbs(b, a))
        }
    }

    static func decAdd(_ a: MarDec, _ b: MarDec) throws -> MarDec {
        let (da, db, scale) = align(a, b)
        let (neg, sum) = signedAdd(aNeg: a.negative, a: da, bNeg: b.negative, b: db)
        try checkBound("+", sum)
        return MarDec(negative: neg && sum != [0], digits: sum, scale: scale)
    }

    static func decSub(_ a: MarDec, _ b: MarDec) throws -> MarDec {
        let (da, db, scale) = align(a, b)
        let (neg, diff) = signedAdd(aNeg: a.negative, a: da, bNeg: !b.negative, b: db)
        try checkBound("-", diff)
        return MarDec(negative: neg && diff != [0], digits: diff, scale: scale)
    }

    static func decMul(_ a: MarDec, _ b: MarDec) throws -> MarDec {
        let prod = mulAbs(a.digits, b.digits)
        try checkBound("*", prod)
        let neg = (a.negative != b.negative) && prod != [0]
        return MarDec(negative: neg, digits: prod, scale: a.scale + b.scale)
    }

    static func decCompare(_ a: MarDec, _ b: MarDec) -> Int {
        let (da, db, _) = align(a, b)
        let aZero = da == [0], bZero = db == [0]
        let aNeg = a.negative && !aZero, bNeg = b.negative && !bZero
        if aNeg != bNeg { return aNeg ? -1 : 1 }
        let c = cmpAbs(da, db)
        return aNeg ? -c : c
    }

    /// Canonical scale-faithful rendering: "-12.50", "0.05", "7".
    static func decString(_ v: MarDec) -> String {
        var digits = trim(v.digits).map { Character(String($0)) }
        var out: String
        if v.scale == 0 {
            out = String(digits)
        } else {
            if digits.count <= v.scale {
                digits = [Character](repeating: "0", count: v.scale - digits.count + 1) + digits
            }
            let cut = digits.count - v.scale
            out = String(digits[0..<cut]) + "." + String(digits[cut...])
        }
        if v.negative && v.digits != [0] { out = "-" + out }
        return out
    }

    /// Canonical form only (optional sign, digits, optional point +
    /// digits): exponents and anything else return nil.
    static func parseDecimalString(_ s: String) -> MarDec? {
        var t = s.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty { return nil }
        var neg = false
        if t.hasPrefix("-") { neg = true; t.removeFirst() }
        else if t.hasPrefix("+") { t.removeFirst() }
        let parts = t.split(separator: ".", omittingEmptySubsequences: false)
        if parts.count > 2 { return nil }
        let intPart = parts.isEmpty ? "" : String(parts[0])
        let fracPart = parts.count == 2 ? String(parts[1]) : ""
        if intPart.isEmpty && fracPart.isEmpty { return nil }
        var digits: [UInt8] = []
        for ch in (intPart + fracPart) {
            guard let d = ch.wholeNumberValue, d >= 0, d <= 9 else { return nil }
            digits.append(UInt8(d))
        }
        let trimmed = trim(digits)
        if trimmed.count > maxDigits { return nil }
        return MarDec(negative: neg && trimmed != [0], digits: trimmed, scale: fracPart.count)
    }

    /// Rounding-mode tag from a Decimal.Rounding ctor value.
    static func roundingTag(_ v: MarValue) throws -> String {
        if case .ctor(let tag, _, _) = v,
           ["HalfEven", "HalfUp", "Down", "Up", "Floor", "Ceiling"].contains(tag) {
            return tag
        }
        throw MarRuntimeError.message("expected a Decimal.Rounding value")
    }

    /// num/den rounded to `scale` places under `mode`. den must be
    /// nonzero (callers handle the total-division zero case). Works in
    /// integers: value = numC * 10^(scale + denS - numS) / denC.
    static func roundQuotient(_ num: MarDec, _ den: MarDec, _ scale: Int, _ mode: String) throws -> MarDec {
        let shift = scale + den.scale - num.scale
        var n = trim(num.digits)
        var d = trim(den.digits)
        if shift > 0 { n = shift10(n, shift) }
        else if shift < 0 { d = shift10(d, -shift) }
        let negative = (num.negative && n != [0]) != (den.negative && d != [0])
        var (q, r) = quoRemAbs(n, d)
        var roundUp = false
        if r != [0] {
            switch mode {
            case "Down": break // toward zero: never up
            case "Up": roundUp = true
            case "Floor": roundUp = negative
            case "Ceiling": roundUp = !negative
            case "HalfUp", "HalfEven":
                let twice = mulAbs(r, [2])
                let c = cmpAbs(twice, d)
                if c > 0 { roundUp = true }
                else if c == 0 {
                    // banker's: to the even neighbour
                    roundUp = mode == "HalfUp" ? true : (q.last ?? 0) % 2 == 1
                }
            default: break
            }
        }
        if roundUp { q = addAbs(q, [1]) }
        try checkBound("Decimal.rounded", q)
        return MarDec(negative: negative && q != [0], digits: q, scale: scale)
    }

    static func decToScale(_ v: MarDec, _ scale: Int, _ mode: String) throws -> MarDec {
        if scale < 0 {
            throw MarRuntimeError.message("Decimal.toScale: negative scale \(scale)")
        }
        if scale >= v.scale {
            let coef = shift10(v.digits, scale - v.scale)
            try checkBound("Decimal.toScale", coef)
            return MarDec(negative: v.negative && coef != [0], digits: coef, scale: scale)
        }
        return try roundQuotient(v, MarDec.fromInt(1), scale, mode)
    }

    static func decToInt(_ v: MarDec, _ mode: String) throws -> Int {
        let r = try roundQuotient(v, MarDec.fromInt(1), 0, mode)
        guard let n = toInt(r.digits, negative: r.negative) else {
            throw MarRuntimeError.message("Decimal: value does not fit an Int")
        }
        return n
    }
}
