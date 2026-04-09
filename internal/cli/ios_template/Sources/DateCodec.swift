import Foundation

enum DateCodec {
    static let utcTimeZone = TimeZone(secondsFromGMT: 0)!
    static let localTimeZone = TimeZone.autoupdatingCurrent
    static let utcCalendar: Calendar = {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = utcTimeZone
        return calendar
    }()
    static let localCalendar: Calendar = {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = localTimeZone
        return calendar
    }()

    private static let dateInputFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.calendar = utcCalendar
        formatter.timeZone = utcTimeZone
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter
    }()

    private static let dateTimeInputFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.calendar = localCalendar
        formatter.timeZone = localTimeZone
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd'T'HH:mm"
        return formatter
    }()

    private static let humanBuildFormatter: DateFormatter = {
        let formatter = DateFormatter()
        formatter.calendar = utcCalendar
        formatter.timeZone = utcTimeZone
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "MMM d, yyyy, HH:mm 'UTC'"
        return formatter
    }()

    static func normalizeDateMilliseconds(_ millis: Double) -> Double {
        let date = Date(timeIntervalSince1970: millis / 1000)
        let start = utcCalendar.startOfDay(for: date)
        return start.timeIntervalSince1970 * 1000
    }

    static func parseDateInput(_ raw: String) -> Double? {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if let whole = Double(trimmed), whole.rounded() == whole {
            return normalizeDateMilliseconds(whole)
        }
        guard let date = dateInputFormatter.date(from: trimmed) else {
            return nil
        }
        return normalizeDateMilliseconds(date.timeIntervalSince1970 * 1000)
    }

    static func parseDateTimeInput(_ raw: String) -> Double? {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if let whole = Double(trimmed), whole.rounded() == whole {
            return whole
        }
        guard let date = dateTimeInputFormatter.date(from: trimmed) else {
            return nil
        }
        return date.timeIntervalSince1970 * 1000
    }

    static func formatDateInput(milliseconds: Double) -> String {
        let date = Date(timeIntervalSince1970: normalizeDateMilliseconds(milliseconds) / 1000)
        return dateInputFormatter.string(from: date)
    }

    static func formatDateTimeInput(milliseconds: Double) -> String {
        let date = Date(timeIntervalSince1970: milliseconds / 1000)
        return dateTimeInputFormatter.string(from: date)
    }

    static func formatDateDisplay(milliseconds: Double) -> String {
        formatDateInput(milliseconds: milliseconds)
    }

    static func formatDateTimeDisplay(milliseconds: Double) -> String {
        let input = formatDateTimeInput(milliseconds: milliseconds)
        return input.replacingOccurrences(of: "T", with: " ")
    }

    static func parseBuildTime(_ raw: String) -> String {
        let formatter = ISO8601DateFormatter()
        formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let date = formatter.date(from: raw) {
            return humanBuildFormatter.string(from: date)
        }
        let fallback = ISO8601DateFormatter()
        fallback.formatOptions = [.withInternetDateTime]
        if let date = fallback.date(from: raw) {
            return humanBuildFormatter.string(from: date)
        }
        return raw
    }
}
