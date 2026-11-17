// The calendar of a Time and of a Date, by hand.
//
// A Time is a millisecond from 0000-01-01T00:00:00.000Z to
// 9999-12-31T23:59:59.999Z in the proleptic Gregorian calendar, UTC, as on the
// server (internal/runtime/timerange.go) and in the browser (runtime.js), and
// every Time is built in range: through timeOf, or right after a check with
// inTimeRange where an out-of-range answer is not an error (a Nothing). A
// Mar Date is a day of the same calendar. Foundation cannot do this:
// Calendar(identifier: .gregorian), Calendar(identifier: .iso8601) and
// ISO8601DateFormatter switch to the Julian calendar before 1582-10-15 and
// count years by era, and a Foundation Date is a Double of seconds, which
// loses milliseconds on the way in and out. So the arithmetic is integers only:
// Howard Hinnant's days_from_civil / civil_from_days.

import Foundation

enum MarCivil {
    /// 0000-01-01T00:00:00.000Z and 9999-12-31T23:59:59.999Z.
    static let minMillis = -62_167_219_200_000
    static let maxMillis = 253_402_300_799_999
    static let msPerDay = 86_400_000
    static let minDay = fdiv(minMillis, msPerDay)
    static let maxDay = fdiv(maxMillis, msPerDay)

    static func inTimeRange(_ ms: Int) -> Bool { ms >= minMillis && ms <= maxMillis }

    /// The one sentence for a Time that would leave the range, as Go's
    /// timeOutOfRange and runtime.js's.
    static func timeOutOfRange(_ source: String) -> MarRuntimeError {
        .message("Time out of range: \(source) is outside the range of a Time"
            + " (0000-01-01T00:00:00.000Z to 9999-12-31T23:59:59.999Z)")
    }

    static func timeOf(_ source: String, _ ms: Int) throws -> MarValue {
        guard inTimeRange(ms) else { throw timeOutOfRange(source) }
        return .time(ms)
    }

    /// Floor division and modulo for a positive divisor.
    static func fdiv(_ a: Int, _ b: Int) -> Int {
        let q = a / b
        return (a % b < 0) ? q - 1 : q
    }

    static func fmod(_ a: Int, _ b: Int) -> Int { a - fdiv(a, b) * b }

    static func isLeapYear(_ y: Int) -> Bool { y % 4 == 0 && (y % 100 != 0 || y % 400 == 0) }

    static func daysInMonth(_ y: Int, _ m: Int) -> Int {
        switch m {
        case 2: return isLeapYear(y) ? 29 : 28
        case 4, 6, 9, 11: return 30
        default: return 31
        }
    }

    /// Days from 1970-01-01 to y-m-1, plus d-1. m is 1...12. Exact for every
    /// Mar Int year (|y| < 2^53, so era * 146097 < 3.3e18).
    static func daysFromCivil(_ y0: Int, _ m: Int, _ d: Int) -> Int {
        let y = m <= 2 ? y0 - 1 : y0
        let era = fdiv(y, 400)
        let yoe = y - era * 400
        let doy = (153 * ((m + 9) % 12) + 2) / 5
        let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy
        return era * 146_097 + doe - 719_468 + (d - 1)
    }

    /// The date of a day count from 1970-01-01.
    static func civilFromDays(_ days: Int) -> (y: Int, m: Int, d: Int) {
        let z = days + 719_468
        let era = fdiv(z, 146_097)
        let doe = z - era * 146_097
        let yoe = (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365
        let doy = doe - (365 * yoe + yoe / 4 - yoe / 100)
        let mp = (5 * doy + 2) / 153
        let d = doy - (153 * mp + 2) / 5 + 1
        let m = mp < 10 ? mp + 3 : mp - 9
        let y = yoe + era * 400
        return (m <= 2 ? y + 1 : y, m, d)
    }

    struct Fields { let y, m, d, hour, minute, second, milli: Int }

    static func timeFields(_ ms: Int) -> Fields {
        let days = fdiv(ms, msPerDay)
        let rem = ms - days * msPerDay
        let c = civilFromDays(days)
        return Fields(y: c.y, m: c.m, d: c.d,
                      hour: rem / 3_600_000, minute: rem / 60_000 % 60,
                      second: rem / 1000 % 60, milli: rem % 1000)
    }

    /// `2006-01-02T15:04:05.000Z`, the wire form and Time.toIso. Every Time
    /// is in range, so the year has four digits.
    static func formatTimeISO(_ ms: Int) -> String {
        let f = timeFields(ms)
        func pad(_ v: Int, _ w: Int) -> String {
            let s = String(v)
            return s.count >= w ? s : String(repeating: "0", count: w - s.count) + s
        }
        return "\(pad(f.y, 4))-\(pad(f.m, 2))-\(pad(f.d, 2))T"
            + "\(pad(f.hour, 2)):\(pad(f.minute, 2)):\(pad(f.second, 2)).\(pad(f.milli, 3))Z"
    }

    /// Time.fromYMD: midnight UTC, the month and the day carried over (month
    /// 13 is January of the next year, day 0 the last day of the month
    /// before).
    static func timeFromYMD(_ y: Int, _ m: Int, _ d: Int) throws -> MarValue {
        let source = "Time.fromYMD \(y) \(m) \(d)"
        let yy = y + fdiv(m - 1, 12)
        let mm = fmod(m - 1, 12) + 1
        let days = daysFromCivil(yy, mm, 1) + (d - 1)
        guard days >= minDay, days <= maxDay else { throw timeOutOfRange(source) }
        return try timeOf(source, days * msPerDay)
    }

    /// Time.addDays: n days of 86_400_000 ms, keeping the time of day.
    static func timeAddDays(_ ms: Int, _ n: Int) throws -> MarValue {
        let source = "Time.addDays \(n)"
        let day = fdiv(ms, msPerDay)
        let days = day + n
        guard days >= minDay, days <= maxDay else { throw timeOutOfRange(source) }
        return try timeOf(source, days * msPerDay + (ms - day * msPerDay))
    }

    /// Time.addMonths / Time.addYears: move the calendar month, keep the time
    /// of day, and land on the last day of a shorter month.
    static func timeAddMonths(_ ms: Int, _ n: Int, _ source: String) throws -> MarValue {
        let day = fdiv(ms, msPerDay)
        let c = civilFromDays(day)
        let total = c.y * 12 + (c.m - 1) + n
        let y = fdiv(total, 12)
        let m = fmod(total, 12) + 1
        guard y >= 0, y <= 9999 else { throw timeOutOfRange(source) }
        let d = min(c.d, daysInMonth(y, m))
        return try timeOf(source, daysFromCivil(y, m, d) * msPerDay + (ms - day * msPerDay))
    }

    /// The one text form of a Time the three runtimes accept, RFC 3339 with
    /// its optional parts and nothing else (Go's parseTimeISO is the same
    /// function):
    ///
    ///     YYYY-MM-DD 'T' HH:MM:SS [ '.' DIGIT+ ] ( 'Z' | ( '+' | '-' ) HH:MM )
    ///
    /// It answers the moment the text names, which can be outside the range
    /// (9999-12-31T23:59:59-01:00), or nil when the text is not in that form.
    static func parseTimeISO(_ s: String) -> Int? {
        let b = Array(s.utf8)
        var i = 0
        func num(_ n: Int) -> Int? {
            guard i + n <= b.count else { return nil }
            var v = 0
            for k in 0..<n {
                let c = b[i + k]
                guard c >= 0x30, c <= 0x39 else { return nil }
                v = v * 10 + Int(c - 0x30)
            }
            i += n
            return v
        }
        func lit(_ c: UInt8) -> Bool {
            guard i < b.count, b[i] == c else { return false }
            i += 1
            return true
        }
        func isDigit(_ k: Int) -> Bool { k < b.count && b[k] >= 0x30 && b[k] <= 0x39 }
        guard let y = num(4), lit(0x2D), let mo = num(2), lit(0x2D), let d = num(2), lit(0x54),
              let h = num(2), lit(0x3A), let mi = num(2), lit(0x3A), let sec = num(2) else { return nil }
        var milli = 0
        if lit(0x2E) {
            var k = 0
            while isDigit(i) {
                if k < 3 { milli = milli * 10 + Int(b[i] - 0x30) }
                k += 1
                i += 1
            }
            guard k > 0 else { return nil }
            while k < 3 {
                milli *= 10
                k += 1
            }
        }
        var offset = 0
        if lit(0x5A) {
            offset = 0
        } else if i < b.count, b[i] == 0x2B || b[i] == 0x2D {
            let sign = b[i] == 0x2D ? -1 : 1
            i += 1
            guard let oh = num(2), lit(0x3A), let om = num(2), oh <= 23, om <= 59 else { return nil }
            offset = sign * (oh * 3600 + om * 60)
        } else {
            return nil
        }
        guard i == b.count, (1...12).contains(mo), d >= 1, d <= daysInMonth(y, mo),
              h <= 23, mi <= 59, sec <= 59 else { return nil }
        return (daysFromCivil(y, mo, d) * 86_400 + h * 3600 + mi * 60 + sec - offset) * 1000 + milli
    }

    // MARK: Date

    /// A Date is a day of the calendar, with no time of day and no time zone:
    /// days from 1970-01-01, from 0000-01-01 to 9999-12-31 (the days of a
    /// Time), as on the server (internal/runtime/date.go) and in the browser
    /// (runtime.js). The one sentence for a Date that would leave the range.
    static func dateOutOfRange(_ source: String) -> MarRuntimeError {
        .message("Date out of range: \(source) is outside the range of a Date (0000-01-01 to 9999-12-31)")
    }

    static func dateOf(_ source: String, _ days: Int) throws -> MarValue {
        guard days >= minDay, days <= maxDay else { throw dateOutOfRange(source) }
        return .date(days)
    }

    /// Date.fromYMD: the month and the day carried over as Time.fromYMD
    /// carries them.
    static func dateFromYMD(_ y: Int, _ m: Int, _ d: Int) throws -> MarValue {
        let yy = y + fdiv(m - 1, 12)
        let mm = fmod(m - 1, 12) + 1
        return try dateOf("Date.fromYMD \(y) \(m) \(d)", daysFromCivil(yy, mm, 1) + (d - 1))
    }

    /// Date.addMonths / Date.addYears: move the calendar month and land on the
    /// last day of a shorter month.
    static func dateAddMonths(_ days: Int, _ n: Int, _ source: String) throws -> MarValue {
        let c = civilFromDays(days)
        let total = c.y * 12 + (c.m - 1) + n
        let ny = fdiv(total, 12)
        let nm = fmod(total, 12) + 1
        guard ny >= 0, ny <= 9999 else { throw dateOutOfRange(source) }
        return try dateOf(source, daysFromCivil(ny, nm, min(c.d, daysInMonth(ny, nm))))
    }

    /// 1 for Monday through 7 for Sunday; 1970-01-01 was a Thursday.
    static func weekday(_ days: Int) -> Int { fmod(days + 3, 7) + 1 }

    /// `2026-11-17`, Date.toIso and the wire form. Every Date is in range, so
    /// the year has four digits.
    static func formatDateISO(_ days: Int) -> String {
        let c = civilFromDays(days)
        func pad(_ v: Int, _ w: Int) -> String {
            let s = String(v)
            return s.count >= w ? s : String(repeating: "0", count: w - s.count) + s
        }
        return "\(pad(c.y, 4))-\(pad(c.m, 2))-\(pad(c.d, 2))"
    }

    /// The one text form of a Date the three runtimes accept, YYYY-MM-DD and
    /// nothing else (Go's parseDateISO is the same function): the day count,
    /// or nil.
    static func parseDateISO(_ s: String) -> Int? {
        let b = Array(s.utf8)
        guard b.count == 10, b[4] == UInt8(ascii: "-"), b[7] == UInt8(ascii: "-") else { return nil }
        func num(_ from: Int, _ to: Int) -> Int? {
            var v = 0
            for i in from..<to {
                guard b[i] >= UInt8(ascii: "0"), b[i] <= UInt8(ascii: "9") else { return nil }
                v = v * 10 + Int(b[i] - UInt8(ascii: "0"))
            }
            return v
        }
        guard let y = num(0, 4), let m = num(5, 7), let d = num(8, 10),
              m >= 1, m <= 12, d >= 1, d <= daysInMonth(y, m) else { return nil }
        return daysFromCivil(y, m, d)
    }

    /// The day the device's wall clock shows at ms: the instant moved by the
    /// offset in force there, as runtime.js's localDay. Date.today reads it.
    static func localDay(_ ms: Int, _ tz: TimeZone) -> Int {
        fdiv(ms + localOffset(ms, tz), msPerDay)
    }

    /// The Time a Time.every subscription delivers: the clock, through timeOf
    /// like every other Time (nil for a clock outside the range).
    static func everyTick() -> MarValue? {
        try? timeOf("Time.every", Int((Date().timeIntervalSince1970 * 1000).rounded(.down)))
    }

    // MARK: The date picker

    /// The date picker's calendar: Foundation's Gregorian calendar in the
    /// given time zone, laid out for the device's locale (its week starts on
    /// the locale's first weekday, as the browser's popover does). It labels
    /// the days before 1582-10-15 by the Julian calendar, so the picker offers
    /// the days from 1582-10-15, the first day of the Gregorian calendar, to
    /// 9999-12-31, as the browser's date input does; on those days every
    /// calendar agrees.
    static func pickerCalendar(_ tz: TimeZone) -> Calendar {
        var c = Calendar(identifier: .gregorian)
        c.timeZone = tz
        c.locale = Locale.autoupdatingCurrent
        return c
    }

    /// The time zone the picker runs in: the device's offset from UTC at now,
    /// held fixed. The day crosses the picker as its label, so the zone only
    /// decides which day the picker marks as today, and a fixed offset has no
    /// transition that could skip a label (Pacific/Apia has no 2011-12-30).
    static func pickerZone(_ device: TimeZone = .autoupdatingCurrent, at now: Date = Date()) -> TimeZone {
        TimeZone(secondsFromGMT: device.secondsFromGMT(for: now)) ?? .gmt
    }

    static let pickerFirstDay = daysFromCivil(1582, 10, 15)

    /// The days the picker offers, from midnight of the first to the last
    /// minute of the last.
    static func pickerRange(_ tz: TimeZone) -> ClosedRange<Date> {
        let cal = pickerCalendar(tz)
        let lo = cal.date(from: DateComponents(year: 1582, month: 10, day: 15)) ?? .distantPast
        let hi = cal.date(from: DateComponents(year: 9999, month: 12, day: 31, hour: 23, minute: 59)) ?? .distantFuture
        return lo...hi
    }

    /// The time zone's offset from UTC at ms, in ms. Offsets change on whole
    /// seconds, so the instant is read as a whole second, exactly.
    static func localOffset(_ ms: Int, _ tz: TimeZone) -> Int {
        tz.secondsFromGMT(for: Date(timeIntervalSince1970: TimeInterval(fdiv(ms, 1000)))) * 1000
    }

    /// The Date the picker shows for a day: noon of it, under its label. A day
    /// before the first the picker offers shows as that first day, as in the
    /// browser.
    static func pickerDate(_ days: Int, _ tz: TimeZone) -> Date {
        let c = civilFromDays(max(days, pickerFirstDay))
        return pickerCalendar(tz).date(from: DateComponents(year: c.y, month: c.m, day: c.d, hour: 12))
            ?? pickerRange(tz).lowerBound
    }

    /// The day a picked Date stands for: the label the picker gives it; nil
    /// for a day the picker does not offer.
    static func pickedDay(_ date: Date, _ tz: TimeZone) -> Int? {
        let c = pickerCalendar(tz).dateComponents([.era, .year, .month, .day], from: date)
        guard c.era == 1, let y = c.year, let m = c.month, let d = c.day, y <= 9999 else { return nil }
        let days = daysFromCivil(y, m, d)
        return days >= pickerFirstDay ? days : nil
    }
}
