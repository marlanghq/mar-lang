import Foundation

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
        case .custom:
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
            guard let value = DateCodec.parseDateInput(raw) else { throw PayloadEncodingError.invalidDate(field.name) }
            return .number(value)
        case .dateTime:
            guard let value = DateCodec.parseDateTimeInput(raw) else { throw PayloadEncodingError.invalidDateTime(field.name) }
            return .number(value)
        }
    }
}
