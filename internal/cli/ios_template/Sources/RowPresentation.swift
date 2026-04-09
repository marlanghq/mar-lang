import Foundation

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
        let trimmed = row[entity.primaryKey]?.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return trimmed.isEmpty ? nil : trimmed
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
                return DateCodec.formatDateDisplay(milliseconds: millis)
            }
            return value.stringValue
        case .dateTime:
            if let millis = value.doubleValue {
                return DateCodec.formatDateTimeDisplay(milliseconds: millis)
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
                    return DateCodec.formatDateInput(milliseconds: millis)
                }
                return value.stringValue
            case .dateTime:
                if let millis = value.doubleValue {
                    return DateCodec.formatDateTimeInput(milliseconds: millis)
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
                return DateCodec.formatDateInput(milliseconds: millis)
            }
        case .dateTime:
            if let millis = value.doubleValue {
                return DateCodec.formatDateTimeInput(milliseconds: millis)
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

        if let stringField = candidates.first(where: { field in
            switch field.fieldType {
            case .string, .custom:
                return true
            default:
                return false
            }
        }) {
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
