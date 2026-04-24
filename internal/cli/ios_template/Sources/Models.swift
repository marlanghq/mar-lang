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

enum FieldType: Codable, Hashable {
    case int
    case string
    case bool
    case decimal
    case date
    case dateTime
    case custom(String)

    var rawValue: String {
        switch self {
        case .int:
            return "Int"
        case .string:
            return "String"
        case .bool:
            return "Bool"
        case .decimal:
            return "Decimal"
        case .date:
            return "Date"
        case .dateTime:
            return "DateTime"
        case .custom(let value):
            return value
        }
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        let raw = try container.decode(String.self)
        switch raw {
        case "Int":
            self = .int
        case "String":
            self = .string
        case "Bool":
            self = .bool
        case "Decimal":
            self = .decimal
        case "Date":
            self = .date
        case "DateTime":
            self = .dateTime
        default:
            self = .custom(raw)
        }
    }

    func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        try container.encode(rawValue)
    }
}

struct Field: Codable, Hashable, Identifiable {
    let name: String
    let fieldType: FieldType
    let enumValues: [String]
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
        case enumValues
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
        enumValues: [String],
        relationEntity: String?,
        currentUser: Bool,
        primary: Bool,
        auto: Bool,
        optional: Bool,
        defaultValue: JSONValue?
    ) {
        self.name = name
        self.fieldType = fieldType
        self.enumValues = enumValues
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
        enumValues = try container.decodeIfPresent([String].self, forKey: .enumValues) ?? []
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
    let unique: [UniqueConstraintInfo]

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

struct UniqueConstraintInfo: Codable, Hashable {
    let fields: [String]
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
    let queries: [QueryInfo]
    let records: [RecordInfo]
    let screens: FrontendInfo?

    enum CodingKeys: String, CodingKey {
        case appName
        case port
        case database
        case entities
        case auth
        case systemAuth
        case inputAliases
        case actions
        case queries
        case records
        case screens
    }

    init(
        appName: String,
        port: Int,
        database: String,
        entities: [Entity],
        auth: AuthInfo?,
        systemAuth: SystemAuthInfo?,
        inputAliases: [InputAliasInfo],
        actions: [ActionInfo],
        queries: [QueryInfo],
        records: [RecordInfo],
        screens: FrontendInfo?
    ) {
        self.appName = appName
        self.port = port
        self.database = database
        self.entities = entities
        self.auth = auth
        self.systemAuth = systemAuth
        self.inputAliases = inputAliases
        self.actions = actions
        self.queries = queries
        self.records = records
        self.screens = screens
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
        queries = try container.decodeIfPresent([QueryInfo].self, forKey: .queries) ?? []
        records = try container.decodeIfPresent([RecordInfo].self, forKey: .records) ?? []
        screens = try container.decodeIfPresent(FrontendInfo.self, forKey: .screens)
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
    let path: String
    let inputAlias: String
    let steps: Int

    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name
        case path
        case inputAlias
        case steps
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        path = try container.decodeIfPresent(String.self, forKey: .path) ?? "/actions/" + name.replacingOccurrences(of: "_", with: "-")
        inputAlias = try container.decodeIfPresent(String.self, forKey: .inputAlias) ?? ""
        steps = try container.decodeIfPresent(Int.self, forKey: .steps) ?? 0
    }
}

struct QueryInfo: Codable, Hashable, Identifiable {
    let name: String
    let path: String
    let parameters: [String]
    let entity: String

    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name
        case path
        case parameters
        case entity
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        path = try container.decodeIfPresent(String.self, forKey: .path) ?? "/queries/" + name.replacingOccurrences(of: "_", with: "-")
        parameters = try container.decodeIfPresent([String].self, forKey: .parameters) ?? []
        entity = try container.decodeIfPresent(String.self, forKey: .entity) ?? ""
    }
}

struct RecordInfo: Codable, Hashable, Identifiable {
    let name: String
    let fields: [RecordFieldInfo]

    var id: String { name }
}

struct RecordFieldInfo: Codable, Hashable, Identifiable {
    let name: String
    let fieldType: String

    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name
        case fieldType = "type"
    }
}

struct InputAliasInfo: Codable, Hashable, Identifiable {
    let name: String
    let fields: [InputAliasField]

    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name
        case fields
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        fields = try container.decodeIfPresent([InputAliasField].self, forKey: .fields) ?? []
    }
}

struct InputAliasField: Codable, Hashable, Identifiable {
    let name: String
    let fieldType: String
    let enumValues: [String]
    let relationEntity: String?

    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name
        case fieldType = "type"
        case enumValues
        case relationEntity
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        fieldType = try container.decode(String.self, forKey: .fieldType)
        enumValues = try container.decodeIfPresent([String].self, forKey: .enumValues) ?? []
        relationEntity = try container.decodeIfPresent(String.self, forKey: .relationEntity)
    }
}

struct FrontendInfo: Codable, Hashable {
    let screens: [FrontendScreenInfo]

    enum CodingKeys: String, CodingKey {
        case screens
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        screens = try container.decodeIfPresent([FrontendScreenInfo].self, forKey: .screens) ?? []
    }
}

struct FrontendScreenInfo: Codable, Hashable, Identifiable {
    let name: String
    let parameters: [String]
    let title: String?
    let titleExpression: String?
    let view: FrontendViewNodeInfo?
    let messages: [FrontendMessageInfo]
    let initExpression: String?
    let updateMessage: String?
    let updateModel: String?
    let updateBody: String?
    let viewModel: String?
    let toolbarItems: [FrontendToolbarItemInfo]
    let sections: [FrontendSectionInfo]

    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name
        case parameters
        case title
        case titleExpression
        case view
        case messages
        case initExpression
        case updateMessage
        case updateModel
        case updateBody
        case viewModel
        case toolbarItems
        case sections
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        parameters = try container.decodeIfPresent([String].self, forKey: .parameters) ?? []
        title = try container.decodeIfPresent(String.self, forKey: .title)
        titleExpression = try container.decodeIfPresent(String.self, forKey: .titleExpression)
        view = try container.decodeIfPresent(FrontendViewNodeInfo.self, forKey: .view)
        messages = try container.decodeIfPresent([FrontendMessageInfo].self, forKey: .messages) ?? []
        initExpression = try container.decodeIfPresent(String.self, forKey: .initExpression)
        updateMessage = try container.decodeIfPresent(String.self, forKey: .updateMessage)
        updateModel = try container.decodeIfPresent(String.self, forKey: .updateModel)
        updateBody = try container.decodeIfPresent(String.self, forKey: .updateBody)
        viewModel = try container.decodeIfPresent(String.self, forKey: .viewModel)
        toolbarItems = try container.decodeIfPresent([FrontendToolbarItemInfo].self, forKey: .toolbarItems) ?? []
        sections = try container.decodeIfPresent([FrontendSectionInfo].self, forKey: .sections) ?? []
    }
}

struct FrontendMessageInfo: Codable, Hashable {
    let name: String
    let parameters: [String]

    enum CodingKeys: String, CodingKey {
        case name
        case parameters
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        parameters = try container.decodeIfPresent([String].self, forKey: .parameters) ?? []
    }
}

struct FrontendViewNodeInfo: Codable, Hashable, Identifiable {
    let kind: String
    let title: String?
    let text: String?
    let label: String?
    let message: String?
    let children: [FrontendViewNodeInfo]

    var id: String {
        [
            kind,
            title ?? "",
            text ?? "",
            label ?? "",
            message ?? "",
            children.map(\.id).joined(separator: ",")
        ].joined(separator: ":")
    }

    enum CodingKeys: String, CodingKey {
        case kind
        case title
        case text
        case label
        case message
        case children
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        kind = try container.decode(String.self, forKey: .kind)
        title = try container.decodeIfPresent(String.self, forKey: .title)
        text = try container.decodeIfPresent(String.self, forKey: .text)
        label = try container.decodeIfPresent(String.self, forKey: .label)
        message = try container.decodeIfPresent(String.self, forKey: .message)
        children = try container.decodeIfPresent([FrontendViewNodeInfo].self, forKey: .children) ?? []
    }
}

struct FrontendToolbarItemInfo: Codable, Hashable {
    let placement: String
    let item: FrontendItemInfo
}

struct FrontendSectionInfo: Codable, Hashable, Identifiable {
    let title: String?
    let items: [FrontendItemInfo]

    var id: String {
        "\(title ?? ""):\(items.map(\.id).joined(separator: ","))"
    }

    enum CodingKeys: String, CodingKey {
        case title
        case items
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        title = try container.decodeIfPresent(String.self, forKey: .title)
        items = try container.decodeIfPresent([FrontendItemInfo].self, forKey: .items) ?? []
    }
}

struct FrontendItemInfo: Codable, Hashable, Identifiable {
    let kind: String
    let condition: String?
    let label: String?
    let text: String?
    let message: String?
    let modelField: String?
    let disabled: String?
    let options: [FrontendOptionInfo]
    let target: String?
    let entity: String?
    let relationField: String?
    let filter: String?
    let titleField: String?
    let subtitleField: String?
    let destination: String?
    let action: String?
    let reportGroup: String?
    let reportMetrics: [FrontendReportMetricInfo]
    let values: [FrontendActionValueInfo]
    let formFields: [FrontendFormFieldInfo]

    var id: String {
        [
            kind,
            condition ?? "",
            label ?? "",
            text ?? "",
            message ?? "",
            modelField ?? "",
            disabled ?? "",
            options.map(\.value).joined(separator: ","),
            target ?? "",
            entity ?? "",
            relationField ?? "",
            action ?? ""
        ].joined(separator: ":")
    }

    enum CodingKeys: String, CodingKey {
        case kind
        case condition
        case label
        case text
        case message
        case modelField
        case disabled
        case options
        case target
        case entity
        case relationField
        case filter
        case titleField
        case subtitleField
        case destination
        case action
        case reportGroup
        case reportMetrics
        case values
        case formFields
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        kind = try container.decode(String.self, forKey: .kind)
        condition = try container.decodeIfPresent(String.self, forKey: .condition)
        label = try container.decodeIfPresent(String.self, forKey: .label)
        text = try container.decodeIfPresent(String.self, forKey: .text)
        message = try container.decodeIfPresent(String.self, forKey: .message)
        modelField = try container.decodeIfPresent(String.self, forKey: .modelField)
        disabled = try container.decodeIfPresent(String.self, forKey: .disabled)
        options = try container.decodeIfPresent([FrontendOptionInfo].self, forKey: .options) ?? []
        target = try container.decodeIfPresent(String.self, forKey: .target)
        entity = try container.decodeIfPresent(String.self, forKey: .entity)
        relationField = try container.decodeIfPresent(String.self, forKey: .relationField)
        filter = try container.decodeIfPresent(String.self, forKey: .filter)
        titleField = try container.decodeIfPresent(String.self, forKey: .titleField)
        subtitleField = try container.decodeIfPresent(String.self, forKey: .subtitleField)
        destination = try container.decodeIfPresent(String.self, forKey: .destination)
        action = try container.decodeIfPresent(String.self, forKey: .action)
        reportGroup = try container.decodeIfPresent(String.self, forKey: .reportGroup)
        reportMetrics = try container.decodeIfPresent([FrontendReportMetricInfo].self, forKey: .reportMetrics) ?? []
        values = try container.decodeIfPresent([FrontendActionValueInfo].self, forKey: .values) ?? []
        formFields = try container.decodeIfPresent([FrontendFormFieldInfo].self, forKey: .formFields) ?? []
    }
}

struct FrontendOptionInfo: Codable, Hashable {
    let value: String
    let label: String
}

struct FrontendReportMetricInfo: Codable, Hashable {
    let aggregate: String
    let field: String?
    let label: String?
}

struct FrontendActionValueInfo: Codable, Hashable {
    let field: String
    let expression: String
}

struct FrontendFormFieldInfo: Codable, Hashable {
    let field: String
    let filter: String?
}

struct RequestCodeResponse: Decodable, Hashable {
    let ok: Bool?
    let message: String
}

struct LoginResponse: Decodable, Hashable {
    let token: String
    let role: String?
    let email: String?
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

struct PublicVersionPayload: Decodable, Hashable {
    let app: VersionApp
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
    let userID: String?
}

struct SchemaCacheSnapshot: Codable, Equatable {
    let baseURL: String
    let version: String?
    let schema: Schema
}
