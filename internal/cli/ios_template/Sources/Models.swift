import Foundation

typealias Row = [String: JSONValue]

enum JSONValue: Codable, Hashable, Sendable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case object([String: JSONValue])
    case array([JSONValue])
    case null

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()

        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([String: JSONValue].self) {
            self = .object(value)
        } else if let value = try? container.decode([JSONValue].self) {
            self = .array(value)
        } else {
            throw DecodingError.dataCorruptedError(in: container, debugDescription: "Unsupported JSON value")
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()

        switch self {
        case .string(let value):
            try container.encode(value)
        case .number(let value):
            try container.encode(value)
        case .bool(let value):
            try container.encode(value)
        case .object(let value):
            try container.encode(value)
        case .array(let value):
            try container.encode(value)
        case .null:
            try container.encodeNil()
        }
    }

    var stringValue: String {
        switch self {
        case .string(let value):
            return value
        case .number(let value):
            if value.rounded() == value {
                return String(Int(value))
            }
            return value.formatted(.number)
        case .bool(let value):
            return value ? "true" : "false"
        case .null:
            return ""
        case .array, .object:
            return encodedJSONString ?? ""
        }
    }

    var doubleValue: Double? {
        switch self {
        case .number(let value):
            return value
        case .string(let value):
            return Double(value)
        default:
            return nil
        }
    }

    var boolValue: Bool? {
        switch self {
        case .bool(let value):
            return value
        case .string(let value):
            switch value.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
            case "true", "1", "yes":
                return true
            case "false", "0", "no":
                return false
            default:
                return nil
            }
        default:
            return nil
        }
    }

    var encodedJSONString: String? {
        guard let data = try? JSONEncoder().encode(self) else { return nil }
        return String(data: data, encoding: .utf8)
    }
}

enum FieldType: String, Codable, Hashable, CaseIterable {
    case int = "Int"
    case string = "String"
    case bool = "Bool"
    case float = "Float"
    case date = "Date"
    case dateTime = "DateTime"
}

struct Field: Codable, Hashable, Identifiable {
    let name: String
    let fieldType: FieldType
    let relationEntity: String?
    let currentUser: Bool
    let primary: Bool
    let auto: Bool
    let optional: Bool
    let defaultValue: JSONValue?

    var id: String { name }
    var isEditable: Bool { !primary && !currentUser && !auto }

    enum CodingKeys: String, CodingKey {
        case name
        case fieldType = "type"
        case relationEntity
        case currentUser
        case primary
        case auto
        case optional
        case defaultValue = "default"
    }

    init(
        name: String,
        fieldType: FieldType,
        relationEntity: String?,
        currentUser: Bool,
        primary: Bool,
        auto: Bool,
        optional: Bool,
        defaultValue: JSONValue?
    ) {
        self.name = name
        self.fieldType = fieldType
        self.relationEntity = relationEntity
        self.currentUser = currentUser
        self.primary = primary
        self.auto = auto
        self.optional = optional
        self.defaultValue = defaultValue
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        fieldType = try container.decode(FieldType.self, forKey: .fieldType)
        relationEntity = try container.decodeIfPresent(String.self, forKey: .relationEntity)
        currentUser = try container.decodeIfPresent(Bool.self, forKey: .currentUser) ?? false
        primary = try container.decode(Bool.self, forKey: .primary)
        auto = try container.decode(Bool.self, forKey: .auto)
        optional = try container.decode(Bool.self, forKey: .optional)
        defaultValue = try container.decodeIfPresent(JSONValue.self, forKey: .defaultValue)
    }
}

struct Entity: Codable, Hashable, Identifiable {
    let name: String
    let table: String
    let resource: String
    let primaryKey: String
    let fields: [Field]

    var id: String { name }
    var displayName: String { RowPresentation.humanizeIdentifier(name) }
    var visibleFields: [Field] { fields.filter(\.isEditable) }
    var summaryFields: [Field] {
        fields.filter { !$0.primary && !$0.currentUser && !$0.auto && $0.relationEntity == nil }
    }
    var detailFields: [Field] {
        fields.filter { !$0.primary && !$0.currentUser && !$0.auto }
    }
    var displayFields: [Field] {
        let visible = visibleFields
        if visible.isEmpty {
            return fields.filter { !$0.currentUser }
        }
        return visible
    }
}

struct Schema: Codable, Hashable {
    let appName: String
    let port: Int
    let database: String
    let entities: [Entity]
    let auth: AuthInfo?
    let systemAuth: SystemAuthInfo?
    let inputAliases: [InputAliasInfo]
    let actions: [ActionInfo]

    enum CodingKeys: String, CodingKey {
        case appName
        case port
        case database
        case entities
        case auth
        case systemAuth
        case inputAliases
        case actions
    }

    init(
        appName: String,
        port: Int,
        database: String,
        entities: [Entity],
        auth: AuthInfo?,
        systemAuth: SystemAuthInfo?,
        inputAliases: [InputAliasInfo],
        actions: [ActionInfo]
    ) {
        self.appName = appName
        self.port = port
        self.database = database
        self.entities = entities
        self.auth = auth
        self.systemAuth = systemAuth
        self.inputAliases = inputAliases
        self.actions = actions
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        appName = try container.decode(String.self, forKey: .appName)
        port = try container.decode(Int.self, forKey: .port)
        database = try container.decode(String.self, forKey: .database)
        entities = try container.decode([Entity].self, forKey: .entities)
        auth = try container.decodeIfPresent(AuthInfo.self, forKey: .auth)
        systemAuth = try container.decodeIfPresent(SystemAuthInfo.self, forKey: .systemAuth)
        inputAliases = try container.decodeIfPresent([InputAliasInfo].self, forKey: .inputAliases) ?? []
        actions = try container.decodeIfPresent([ActionInfo].self, forKey: .actions) ?? []
    }
}

struct AuthInfo: Codable, Hashable {
    let enabled: Bool
    let userEntity: String
    let emailField: String
    let roleField: String
    let needsBootstrap: Bool
}

struct SystemAuthInfo: Codable, Hashable {
    let enabled: Bool
    let needsBootstrap: Bool
}

struct ActionInfo: Codable, Hashable, Identifiable {
    let name: String
    let inputAlias: String
    let steps: Int

    var id: String { name }
}

struct InputAliasInfo: Codable, Hashable, Identifiable {
    let name: String
    let fields: [InputAliasField]

    var id: String { name }
}

struct InputAliasField: Codable, Hashable, Identifiable {
    let name: String
    let fieldType: String

    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name
        case fieldType = "type"
    }
}

struct RequestCodeResponse: Decodable, Hashable {
    let ok: Bool?
    let message: String
}

struct LoginResponse: Decodable, Hashable {
    let token: String
    let role: String?
    let email: String?

    private enum CodingKeys: String, CodingKey {
        case token
        case role
        case email
        case user
    }

    private enum UserCodingKeys: String, CodingKey {
        case role
        case email
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        token = try container.decode(String.self, forKey: .token)

        if let directRole = try container.decodeIfPresent(String.self, forKey: .role) {
            role = directRole
        } else if let userContainer = try? container.nestedContainer(keyedBy: UserCodingKeys.self, forKey: .user) {
            role = try userContainer.decodeIfPresent(String.self, forKey: .role)
        } else {
            role = nil
        }

        if let directEmail = try container.decodeIfPresent(String.self, forKey: .email) {
            email = directEmail
        } else if let userContainer = try? container.nestedContainer(keyedBy: UserCodingKeys.self, forKey: .user) {
            email = try userContainer.decodeIfPresent(String.self, forKey: .email)
        } else {
            email = nil
        }
    }
}

struct AuthMeResponse: Decodable, Hashable {
    let authenticated: Bool?
    let email: String
    let userId: JSONValue?
    let role: String?
    let user: Row?
}

struct PerfPayload: Decodable, Hashable {
    let uptimeSeconds: Double
    let goroutines: Int
    let memoryBytes: Double
    let sqliteBytes: Double
    let http: PerfHTTP
}

struct PerfHTTP: Decodable, Hashable {
    let totalRequests: Int
    let success2xx: Int
    let errors4xx: Int
    let errors5xx: Int
    let routes: [PerfRoute]
}

struct PerfRoute: Decodable, Hashable, Identifiable {
    let method: String
    let route: String
    let count: Int
    let avgMs: Double
    let countsByCode: [PerfStatusCount]

    var id: String { "\(method) \(route)" }
}

struct PerfStatusCount: Decodable, Hashable {
    let code: Int
    let count: Int
}

struct AdminVersionPayload: Decodable, Hashable {
    let app: VersionApp
    let mar: VersionMar
    let runtime: VersionRuntime
}

struct VersionApp: Decodable, Hashable {
    let name: String
    let buildTime: String
    let manifestHash: String
}

struct VersionMar: Decodable, Hashable {
    let version: String
    let commit: String
    let buildTime: String
}

struct VersionRuntime: Decodable, Hashable {
    let goVersion: String
    let platform: String
}

struct BackupsPayload: Decodable, Hashable {
    let backupDir: String
    let backups: [BackupFile]
}

struct BackupFile: Decodable, Hashable, Identifiable {
    let path: String
    let name: String
    let sizeBytes: Double
    let createdAt: String

    var id: String { name }
}

struct BackupResponse: Decodable, Hashable {
    let path: String
    let backupDir: String
    let removed: [String]
}

struct RequestLogsPayload: Decodable, Hashable {
    let buffer: Int
    let totalCaptured: Int
    let logs: [RequestLogRecord]
}

struct RequestLogRecord: Decodable, Hashable, Identifiable {
    let id: String
    let method: String
    let path: String
    let route: String
    let userEmail: String?
    let userRole: String?
    let status: Int
    let durationMs: Double
    let timestamp: String
    let queryCount: Int
    let queryTimeMs: Double
    let errorMessage: String?
    let queries: [RequestLogQueryRecord]
}

struct RequestLogQueryRecord: Decodable, Hashable, Identifiable {
    let sql: String
    let reason: String?
    let durationMs: Double
    let rowCount: Int
    let error: String?

    var id: String { "\(reason ?? "")|\(sql)" }
}

struct APIErrorResponse: Decodable, Error {
    let errorCode: String?
    let message: String
    let details: JSONValue?
}

struct SessionSnapshot: Codable, Equatable {
    let baseURL: String
    let token: String
    let email: String?
    let role: String?
}
