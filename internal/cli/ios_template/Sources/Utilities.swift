import Foundation

enum MarDateCodec {
    static let utcTimeZone = TimeZone(secondsFromGMT: 0)!
    static let utcCalendar: Calendar = {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = utcTimeZone
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
        formatter.calendar = utcCalendar
        formatter.timeZone = utcTimeZone
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
        return input.replacingOccurrences(of: "T", with: " ") + " UTC"
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

enum PayloadEncodingError: LocalizedError, Equatable {
    case requiredField(String)
    case invalidInt(String)
    case invalidFloat(String)
    case invalidBool(String)
    case invalidDate(String)
    case invalidDateTime(String)

    var errorDescription: String? {
        switch self {
        case .requiredField(let name):
            return "Field \(name) is required"
        case .invalidInt(let name):
            return "Field \(name) expects Int"
        case .invalidFloat(let name):
            return "Field \(name) expects Float"
        case .invalidBool(let name):
            return "Field \(name) expects Bool (true/false)"
        case .invalidDate(let name):
            return "Field \(name) expects a date or Unix milliseconds"
        case .invalidDateTime(let name):
            return "Field \(name) expects a date/time or Unix milliseconds"
        }
    }
}

enum PayloadEncoder {
    static func buildPayload(fields: [Field], valuesByName: [String: String], forUpdate: Bool) throws -> [String: JSONValue] {
        var payload: [String: JSONValue] = [:]

        for field in fields where field.isEditable {
            guard let raw = valuesByName[field.name] else {
                if field.optional || forUpdate || field.defaultValue != nil {
                    continue
                }
                throw PayloadEncodingError.requiredField(field.name)
            }

            let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            if trimmed.isEmpty {
                if field.optional {
                    payload[field.name] = .null
                    continue
                }
                throw PayloadEncodingError.requiredField(field.name)
            }

            payload[field.name] = try encodeValue(field: field, raw: trimmed)
        }

        return payload
    }

    private static func encodeValue(field: Field, raw: String) throws -> JSONValue {
        switch field.fieldType {
        case .string:
            return .string(raw)
        case .int:
            guard let value = Int(raw) else { throw PayloadEncodingError.invalidInt(field.name) }
            return .number(Double(value))
        case .float:
            guard let value = Double(raw) else { throw PayloadEncodingError.invalidFloat(field.name) }
            return .number(value)
        case .bool:
            switch raw.lowercased() {
            case "true", "1", "yes":
                return .bool(true)
            case "false", "0", "no":
                return .bool(false)
            default:
                throw PayloadEncodingError.invalidBool(field.name)
            }
        case .date:
            guard let value = MarDateCodec.parseDateInput(raw) else { throw PayloadEncodingError.invalidDate(field.name) }
            return .number(value)
        case .dateTime:
            guard let value = MarDateCodec.parseDateTimeInput(raw) else { throw PayloadEncodingError.invalidDateTime(field.name) }
            return .number(value)
        }
    }
}

enum RowPresentation {
    static let preferredDisplayFieldNames = ["name", "title", "email", "label", "slug"]

    static func humanizeIdentifier(_ identifier: String) -> String {
        let words = splitIdentifierWords(identifier).map { $0.lowercased() }
        guard let first = words.first else { return identifier.trimmingCharacters(in: .whitespacesAndNewlines) }
        return ([capitalize(first)] + words.dropFirst()).joined(separator: " ")
    }

    static func fieldLabel(_ fieldName: String) -> String {
        humanizeIdentifier(fieldName)
    }

    static func rowID(entity: Entity, row: Row) -> String? {
        row[entity.primaryKey]?.stringValue.trimmingCharacters(in: .whitespacesAndNewlines).nilIfEmpty
    }

    static func relatedRowLabel(entity: Entity, row: Row) -> String {
        if let field = preferredDisplayField(from: entity.fields),
           let value = row[field.name]?.stringValue.trimmingCharacters(in: .whitespacesAndNewlines),
           !value.isEmpty,
           value != "null" {
            return value
        }

        return entity.displayName
    }

    static func rowTitle(entity: Entity, row: Row, relationLabelsByEntity: [String: [String: String]] = [:]) -> String {
        if let field = preferredDisplayField(from: entity.fields),
           let value = row[field.name]?.stringValue.trimmingCharacters(in: .whitespacesAndNewlines),
           !value.isEmpty,
           value != "null" {
            return value
        }

        if let primaryRelationField = primaryRelationFieldForTitle(entity: entity),
           let relationValue = row[primaryRelationField.name],
           let relationLabel = resolvedRelationLabel(for: primaryRelationField, value: relationValue, relationLabelsByEntity: relationLabelsByEntity),
           !relationLabel.isEmpty {
            return relationLabel
        }

        return entity.displayName
    }

    static func summaryRows(entity: Entity, row: Row, relationLabelsByEntity: [String: [String: String]] = [:]) -> [(label: String, value: String)] {
        var rows: [(label: String, value: String)] = []

        for field in entity.summaryFields {
            guard let value = row[field.name] else { continue }
            let text = displayString(for: field, value: value, relationLabelsByEntity: relationLabelsByEntity).trimmingCharacters(in: .whitespacesAndNewlines)
            guard !text.isEmpty else { continue }
            rows.append((fieldLabel(field.name), text))
        }

        if !rows.isEmpty {
            return rows
        }

        let titleFieldName = primaryRelationFieldForTitle(entity: entity)?.name
        for field in entity.detailFields where field.relationEntity != nil && field.name != titleFieldName {
            guard let value = row[field.name] else { continue }
            let text = displayString(for: field, value: value, relationLabelsByEntity: relationLabelsByEntity).trimmingCharacters(in: .whitespacesAndNewlines)
            guard !text.isEmpty else { continue }
            rows.append((fieldLabel(field.name), text))
        }

        return rows
    }

    static func displayString(for field: Field, value: JSONValue, relationLabelsByEntity: [String: [String: String]] = [:]) -> String {
        if let relationLabel = resolvedRelationLabel(for: field, value: value, relationLabelsByEntity: relationLabelsByEntity) {
            return relationLabel
        }

        switch field.fieldType {
        case .date:
            if let millis = value.doubleValue {
                return MarDateCodec.formatDateDisplay(milliseconds: millis)
            }
            return value.stringValue
        case .dateTime:
            if let millis = value.doubleValue {
                return MarDateCodec.formatDateTimeDisplay(milliseconds: millis)
            }
            return value.stringValue
        default:
            return value.stringValue
        }
    }

    static func defaultText(for field: Field) -> String {
        if let value = field.defaultValue {
            switch field.fieldType {
            case .date:
                if let millis = value.doubleValue {
                    return MarDateCodec.formatDateInput(milliseconds: millis)
                }
                return value.stringValue
            case .dateTime:
                if let millis = value.doubleValue {
                    return MarDateCodec.formatDateTimeInput(milliseconds: millis)
                }
                return value.stringValue
            default:
                return value.stringValue
            }
        }

        if field.fieldType == .bool && !field.optional {
            return "false"
        }

        return ""
    }

    static func formText(for field: Field, row: Row) -> String {
        guard let value = row[field.name] else {
            return defaultText(for: field)
        }

        switch field.fieldType {
        case .date:
            if let millis = value.doubleValue {
                return MarDateCodec.formatDateInput(milliseconds: millis)
            }
        case .dateTime:
            if let millis = value.doubleValue {
                return MarDateCodec.formatDateTimeInput(milliseconds: millis)
            }
        default:
            break
        }

        return value.stringValue
    }

    static func appShortHash(_ manifestHash: String) -> String? {
        let normalized = manifestHash.replacingOccurrences(of: "sha256:", with: "")
        guard !normalized.isEmpty else { return nil }
        return String(normalized.prefix(12))
    }

    private static func primaryRelationFieldForTitle(entity: Entity) -> Field? {
        entity.fields.first { !$0.primary && !$0.currentUser && $0.relationEntity != nil }
    }

    private static func resolvedRelationLabel(for field: Field, value: JSONValue, relationLabelsByEntity: [String: [String: String]]) -> String? {
        guard let relationEntity = field.relationEntity else { return nil }
        let raw = value.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty, raw != "null" else { return nil }
        return relationLabelsByEntity[relationEntity]?[raw]
    }

    private static func preferredDisplayField(from fields: [Field]) -> Field? {
        let candidates = fields.filter { field in
            field.name.lowercased() != "id" &&
                !field.primary &&
                !field.auto &&
                field.relationEntity == nil
        }

        for preferred in preferredDisplayFieldNames {
            if let match = candidates.first(where: { $0.name.lowercased() == preferred }) {
                return match
            }
        }

        if let stringField = candidates.first(where: { $0.fieldType == .string }) {
            return stringField
        }

        return candidates.first
    }

    private static func splitIdentifierWords(_ identifier: String) -> [String] {
        let trimmed = identifier.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return [] }

        var words: [String] = []
        var current = ""
        var previousWasLowerOrDigit = false

        for character in trimmed {
            if character == "_" || character == "-" || character.isWhitespace {
                if !current.isEmpty {
                    words.append(current)
                    current = ""
                }
                previousWasLowerOrDigit = false
                continue
            }

            if character.isUppercase && previousWasLowerOrDigit && !current.isEmpty {
                words.append(current)
                current = String(character)
            } else {
                current.append(character)
            }

            previousWasLowerOrDigit = character.isLowercase || character.isNumber
        }

        if !current.isEmpty {
            words.append(current)
        }

        return words
    }

    private static func capitalize(_ word: String) -> String {
        guard let first = word.first else { return word }
        return first.uppercased() + word.dropFirst()
    }
}

final class SessionStore {
    private let key = "MarRuntimeIOS.Session"

    func load() -> SessionSnapshot? {
        guard
            let data = UserDefaults.standard.data(forKey: key),
            let session = try? JSONDecoder().decode(SessionSnapshot.self, from: data)
        else {
            return nil
        }
        return session
    }

    func save(_ session: SessionSnapshot) {
        if let data = try? JSONEncoder().encode(session) {
            UserDefaults.standard.set(data, forKey: key)
        }
    }

    func clear() {
        UserDefaults.standard.removeObject(forKey: key)
    }
}

private extension String {
    var nilIfEmpty: String? {
        isEmpty ? nil : self
    }
}
