import Foundation

actor MarAPIClient {
    private let baseURL: URL
    private let session: URLSession
    private var token: String?
    private var schemaVersionObserver: (@Sendable (String) -> Void)?

    init(baseURL: URL, token: String? = nil, session: URLSession = MarAPIClient.defaultSession()) {
        self.baseURL = baseURL
        self.token = token
        self.session = session
    }

    private static func defaultSession() -> URLSession {
        let configuration = URLSessionConfiguration.default
        configuration.waitsForConnectivity = true
        configuration.timeoutIntervalForRequest = 10
        configuration.timeoutIntervalForResource = 20
        return URLSession(configuration: configuration)
    }

    func setToken(_ token: String?) {
        self.token = token
    }

    func setSchemaVersionObserver(_ observer: (@Sendable (String) -> Void)?) {
        schemaVersionObserver = observer
    }

    func fetchSchema() async throws -> SchemaFetchResult {
        let data = try await requestRaw("/_mar/schema", method: "GET", authorized: false, publishVersion: false)
        let decoder = JSONDecoder()

        do {
            let schema = try decoder.decode(Schema.self, from: data)
            return SchemaFetchResult(schema: schema, version: nil)
        } catch {
            throw MarClientError.decoding(
                path: "/_mar/schema",
                details: String(reflecting: error),
                responsePreview: String(data: data, encoding: .utf8) ?? "<non-utf8>"
            )
        }
    }

    func fetchPublicVersion() async throws -> PublicVersionPayload {
        try await request("/_mar/version", method: "GET", authorized: false)
    }

    func requestCode(email: String) async throws -> RequestCodeResponse {
        try await request(
            "/auth/request-code",
            method: "POST",
            authorized: false,
            body: ["email": .string(email.trimmingCharacters(in: .whitespacesAndNewlines))]
        )
    }

    func login(email: String, code: String) async throws -> LoginResponse {
        try await request(
            "/auth/login",
            method: "POST",
            authorized: false,
            additionalHeaders: ["X-Mar-Admin-UI": "true"],
            body: [
                "email": .string(email.trimmingCharacters(in: .whitespacesAndNewlines)),
                "code": .string(code.trimmingCharacters(in: .whitespacesAndNewlines))
            ]
        )
    }

    func authMe() async throws -> AuthMeResponse {
        try await request("/auth/me", method: "GET")
    }

    func logout() async throws {
        _ = try await requestUnit("/auth/logout", method: "POST")
    }

    func listRows(entity: Entity, filters: [String: String] = [:]) async throws -> [Row] {
        let path: String
        if filters.isEmpty {
            path = entity.resource
        } else {
            let query = filters
                .sorted { $0.key < $1.key }
                .map { key, value in
                    let encodedKey = key.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? key
                    let encodedValue = value.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? value
                    return "\(encodedKey)=\(encodedValue)"
                }
                .joined(separator: "&")
            path = "\(entity.resource)?\(query)"
        }
        let rows: [Row] = try await request(path, method: "GET")
        return rows
    }

    func createRow(entity: Entity, payload: [String: JSONValue]) async throws -> Row {
        try await request(entity.resource, method: "POST", body: payload)
    }

    func updateRow(entity: Entity, id: String, payload: [String: JSONValue]) async throws -> Row {
        try await request("\(entity.resource)/\(id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? id)", method: "PUT", body: payload)
    }

    func deleteRow(entity: Entity, id: String) async throws {
        _ = try await requestUnit("\(entity.resource)/\(id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? id)", method: "DELETE")
    }

    func runAction(action: ActionInfo, payload: [String: JSONValue]) async throws -> Row {
        try await request("/actions/\(action.name)", method: "POST", body: payload)
    }

    func fetchAdminVersion() async throws -> AdminVersionPayload {
        try await request("/_mar/admin/version", method: "GET")
    }

    func fetchPerformance() async throws -> PerfPayload {
        try await request("/_mar/admin/perf", method: "GET")
    }

    func fetchBackups() async throws -> BackupsPayload {
        try await request("/_mar/admin/backups", method: "GET")
    }

    func fetchRequestLogs(limit: Int = 50) async throws -> RequestLogsPayload {
        try await request("/_mar/admin/request-logs?limit=\(max(1, limit))", method: "GET")
    }

    func createBackup() async throws -> BackupResponse {
        try await request("/_mar/admin/backups", method: "POST")
    }

    func downloadBackup(named name: String) async throws -> URL {
        let encodedName = name.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? name
        let destinationName = name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "backup.db" : name
        let data = try await requestRaw("/_mar/admin/backups/download?name=\(encodedName)", method: "GET")
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        let fileName = ((destinationName as NSString).lastPathComponent).isEmpty ? "backup.db" : (destinationName as NSString).lastPathComponent
        let destination = directory.appendingPathComponent(fileName)

        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: nil)
        try data.write(to: destination, options: .atomic)

        let renamed = destination.pathExtension.isEmpty
            ? destination.appendingPathExtension("db")
            : destination
        try? FileManager.default.removeItem(at: renamed)
        if renamed != destination {
            try FileManager.default.moveItem(at: destination, to: renamed)
        }
        return renamed
    }

    private func requestUnit(_ path: String, method: String, authorized: Bool = true, body: [String: JSONValue]? = nil) async throws -> VoidResponse {
        try await request(path, method: method, authorized: authorized, body: body)
    }

    private func requestRaw(
        _ path: String,
        method: String,
        authorized: Bool = true,
        additionalHeaders: [String: String] = [:],
        publishVersion: Bool = true
    ) async throws -> Data {
        let (data, httpResponse) = try await performRequest(
            path: path,
            method: method,
            authorized: authorized,
            additionalHeaders: additionalHeaders,
            body: nil
        )
        if publishVersion {
            publishSchemaVersion(from: httpResponse)
        }

        guard (200 ... 299).contains(httpResponse.statusCode) else {
            let decoder = JSONDecoder()
            if let apiError = try? decoder.decode(APIErrorResponse.self, from: data) {
                throw apiError
            }
            let message = String(data: data, encoding: .utf8) ?? "Unexpected server response"
            throw APIErrorResponse(errorCode: nil, message: message, details: nil)
        }

        return data
    }

    private func request<T: Decodable>(
        _ path: String,
        method: String,
        authorized: Bool = true,
        additionalHeaders: [String: String] = [:],
        body: [String: JSONValue]? = nil
    ) async throws -> T {
        let (data, httpResponse) = try await performRequest(
            path: path,
            method: method,
            authorized: authorized,
            additionalHeaders: additionalHeaders,
            body: body
        )
        let decoder = JSONDecoder()

        logRequestResponse(path: path, method: method, statusCode: httpResponse.statusCode)
        publishSchemaVersion(from: httpResponse)

        guard (200 ... 299).contains(httpResponse.statusCode) else {
            if let apiError = try? decoder.decode(APIErrorResponse.self, from: data) {
                throw apiError
            }
            let message = String(data: data, encoding: .utf8) ?? "Unexpected server response"
            throw APIErrorResponse(errorCode: nil, message: message, details: nil)
        }

        do {
            return try decoder.decode(T.self, from: data)
        } catch let decodingError as DecodingError {
            throw MarClientError.decoding(
                path: path,
                details: describe(decodingError),
                responsePreview: responsePreview(from: data)
            )
        } catch {
            throw marTransportError(path: path, error: error)
        }
    }

    private func performRequest(
        path: String,
        method: String,
        authorized: Bool,
        additionalHeaders: [String: String],
        body: [String: JSONValue]?
    ) async throws -> (Data, HTTPURLResponse) {
        let request = try buildRequest(
            path: path,
            method: method,
            authorized: authorized,
            additionalHeaders: additionalHeaders,
            body: body
        )
        logRequestStart(request, path: path, method: method)

        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            logRequestFailure(path: path, method: method, error: error)
            throw marTransportError(path: path, error: error)
        }

        guard let httpResponse = response as? HTTPURLResponse else {
            throw MarClientError.transport(
                path: path,
                message: "The server returned an invalid HTTP response.",
                details: "error: badServerResponse"
            )
        }

        return (data, httpResponse)
    }

    private func buildRequest(
        path: String,
        method: String,
        authorized: Bool,
        additionalHeaders: [String: String],
        body: [String: JSONValue]?
    ) throws -> URLRequest {
        var request = URLRequest(url: resolvedURL(path: path))
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")

        if body != nil {
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }

        for (key, value) in additionalHeaders {
            request.setValue(value, forHTTPHeaderField: key)
        }

        if authorized, let token, !token.isEmpty {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }

        if let body {
            request.httpBody = try JSONEncoder().encode(body)
        }

        return request
    }

    private func publishSchemaVersion(from response: HTTPURLResponse) {
        guard let version = schemaVersion(from: response),
              !version.isEmpty
        else {
            return
        }

        schemaVersionObserver?(version)
    }

    private func schemaVersion(from response: HTTPURLResponse) -> String? {
        response.value(forHTTPHeaderField: "X-Mar-Schema-Version")?.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    private func resolvedURL(path: String) -> URL {
        let raw = path.hasPrefix("/") ? String(path.dropFirst()) : path
        let parts = raw.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: false)
        let pathPart = String(parts.first ?? "")
        let queryPart = parts.count > 1 ? String(parts[1]) : nil

        var components = URLComponents(url: baseURL.appending(path: pathPart), resolvingAgainstBaseURL: false)
        if let queryPart, !queryPart.isEmpty {
            components?.percentEncodedQuery = queryPart
        }
        return components?.url ?? baseURL.appending(path: pathPart)
    }

    private func logRequestStart(_ request: URLRequest, path: String, method: String) {
        print("Mar iOS request start \(method) \(request.url?.absoluteString ?? path)")
    }

    private func logRequestResponse(path: String, method: String, statusCode: Int) {
        print("Mar iOS request response \(method) \(path) status=\(statusCode)")
    }

    private func logRequestFailure(path: String, method: String, error: Error) {
        print("Mar iOS request failure \(method) \(path) error=\(String(reflecting: error))")
    }
}

private struct VoidResponse: Decodable {}

struct SchemaFetchResult {
    let schema: Schema
    let version: String?
}

extension APIErrorResponse: LocalizedError {
    var errorDescription: String? { message }
}

enum MarClientError: LocalizedError {
    case decoding(path: String, details: String, responsePreview: String)
    case transport(path: String, message: String, details: String)

    var errorDescription: String? {
        switch self {
        case .decoding:
            return "The data couldn't be read because it is missing or invalid."
        case .transport(_, let message, _):
            return message
        }
    }

}

func marTransportError(path: String, error: Error) -> MarClientError {
    if let urlError = error as? URLError {
        return MarClientError.transport(
            path: path,
            message: transportMessage(for: urlError),
            details: transportDetails(for: urlError)
        )
    }

    return MarClientError.transport(
        path: path,
        message: "The app could not complete the request.",
        details: String(reflecting: error)
    )
}

private func transportMessage(for error: URLError) -> String {
    if isLocalNetworkProhibited(error) {
        return "Local network access is disabled for this app."
    }

    switch error.code {
    case .secureConnectionFailed,
         .serverCertificateHasBadDate,
         .serverCertificateHasUnknownRoot,
         .serverCertificateNotYetValid,
         .serverCertificateUntrusted,
         .clientCertificateRejected,
         .clientCertificateRequired:
        return "A secure connection to the server could not be established."
    case .cannotConnectToHost:
        return "The app server is not responding."
    case .cannotFindHost, .dnsLookupFailed:
        return "The server address could not be resolved."
    case .notConnectedToInternet, .networkConnectionLost, .internationalRoamingOff, .dataNotAllowed:
        return "The device could not reach the internet."
    case .timedOut:
        return "This is taking longer than expected. Please try again in a moment."
    default:
        return "The app could not complete the request."
    }
}

private func transportDetails(for error: URLError) -> String {
    if isLocalNetworkProhibited(error) {
        return "Allow Local Network access for this app in iOS Settings, then try again."
    }

    var lines = [
        "code: \(error.code.rawValue) (\(error.code))"
    ]

    if let failingURL = error.failingURL {
        lines.append("url: \(failingURL.absoluteString)")
    }

    if let underlying = error.userInfo[NSUnderlyingErrorKey] {
        lines.append("underlying: \(String(reflecting: underlying))")
    } else {
        lines.append("details: \(error.localizedDescription)")
    }

    return lines.joined(separator: "\n")
}

private func isLocalNetworkProhibited(_ error: URLError) -> Bool {
    if String(reflecting: error).localizedCaseInsensitiveContains("local network prohibited") {
        return true
    }
    if let underlying = error.userInfo[NSUnderlyingErrorKey],
       String(reflecting: underlying).localizedCaseInsensitiveContains("local network prohibited") {
        return true
    }
    return false
}

private func responsePreview(from data: Data) -> String {
    let raw = String(data: data, encoding: .utf8) ?? "<non-utf8 \(data.count) bytes>"
    let normalized = raw.replacingOccurrences(of: "\n", with: " ")
    return String(normalized.prefix(500))
}

private func describe(_ error: DecodingError) -> String {
    switch error {
    case .typeMismatch(let type, let context):
        return "typeMismatch(\(type), path: \(codingPathString(context.codingPath)), \(context.debugDescription))"
    case .valueNotFound(let type, let context):
        return "valueNotFound(\(type), path: \(codingPathString(context.codingPath)), \(context.debugDescription))"
    case .keyNotFound(let key, let context):
        return "keyNotFound(\(key.stringValue), path: \(codingPathString(context.codingPath)), \(context.debugDescription))"
    case .dataCorrupted(let context):
        return "dataCorrupted(path: \(codingPathString(context.codingPath)), \(context.debugDescription))"
    @unknown default:
        return String(reflecting: error)
    }
}

private func codingPathString(_ codingPath: [CodingKey]) -> String {
    if codingPath.isEmpty {
        return "<root>"
    }
    return codingPath.map(\.stringValue).joined(separator: ".")
}
