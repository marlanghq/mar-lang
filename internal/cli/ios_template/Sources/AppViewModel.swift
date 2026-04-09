import Foundation
import Network
import SwiftUI

private let generatedServerURL = "__MAR_IOS_SERVER_URL__"
private let generatedDisplayName = "__MAR_IOS_DISPLAY_NAME__"
// swiftlint:disable line_length
private let generatedEmbeddedSchemaJSON = #"""
__MAR_IOS_EMBEDDED_SCHEMA__
"""#
// swiftlint:enable line_length

struct LoginAlertState: Identifiable {
    let id = UUID()
    let title: String
    let message: String
}

private final class LocalNetworkPermissionProbe: @unchecked Sendable {
    private let lock = NSLock()
    private let queue = DispatchQueue(label: "mar.local-network-permission")
    private var browser: NWBrowser?
    private var continuation: CheckedContinuation<Void, Never>?
    private var didResume = false

    func request() async {
        await withCheckedContinuation { (continuation: CheckedContinuation<Void, Never>) in
            let parameters = NWParameters()
            parameters.includePeerToPeer = true
            let browser = NWBrowser(for: .bonjour(type: "_mar._tcp", domain: nil), using: parameters)

            lock.lock()
            self.browser = browser
            self.continuation = continuation
            lock.unlock()

            browser.stateUpdateHandler = { [weak self] state in
                print("Mar iOS local network probe state=\(state)")
                switch state {
                case .ready:
                    guard let probe = self else { return }
                    probe.queue.asyncAfter(deadline: .now() + 0.5) {
                        probe.finish()
                    }
                case .failed, .cancelled:
                    self?.finish()
                default:
                    break
                }
            }

            browser.start(queue: queue)
            queue.asyncAfter(deadline: .now() + 8.0) { [weak self] in
                self?.finish()
            }
        }
    }

    private func finish() {
        lock.lock()
        guard !didResume else {
            lock.unlock()
            return
        }

        didResume = true
        let continuation = continuation
        let browser = browser
        self.continuation = nil
        self.browser = nil
        lock.unlock()

        browser?.cancel()
        continuation?.resume()
    }
}

@MainActor
final class AppViewModel: ObservableObject {
    enum Phase: Equatable {
        case setup
        case connecting
        case authenticationRequired
        case ready
    }

    enum LoginStep: Equatable {
        case email
        case code
    }

    enum Tab: Hashable {
        case entity(String)
        case actions
        case admin
        case profile
    }

    @Published var phase: Phase = .connecting
    @Published var authEmail: String = ""
    @Published var authCode: String = ""
    @Published var loginStep: LoginStep = .email
    @Published var schema: Schema?
    @Published var publicVersion: PublicVersionPayload?
    @Published var authenticatedEmail: String?
    @Published var authenticatedRole: String?
    @Published var authenticatedUserID: String?
    @Published var isBusy = false
    @Published var errorMessage: String?
    @Published var errorDetails: String?
    @Published var infoMessage: String?
    @Published var loginAlert: LoginAlertState?
    @Published var schemaRefreshAlert: LoginAlertState?

    private let sessionStore = SessionStore()
    private let configuredBaseURL: URL?
    private(set) var client: MarAPIClient?
    private var currentSchemaVersion: String?
    private var pendingSchema: Schema?
    private var pendingSchemaVersion: String?
    private var schemaRefreshInFlight = false

    init() {
        configuredBaseURL = Self.normalizedBaseURL(from: generatedServerURL)
        if let normalizedURL = configuredBaseURL {
            let baseURLString = normalizedURL.absoluteString
            let savedSession = sessionStore.load()

            if let saved = savedSession, saved.baseURL == baseURLString {
                authenticatedEmail = saved.email
                authenticatedRole = saved.role
                authenticatedUserID = saved.userID
            }

            client = MarAPIClient(
                baseURL: normalizedURL,
                token: (savedSession?.baseURL == baseURLString) ? savedSession?.token : nil
            )

            if let startupSnapshot = startupSnapshot(baseURL: baseURLString) {
                schema = startupSnapshot.schema
                currentSchemaVersion = startupSnapshot.version
                applyStartupPhase(using: startupSnapshot.schema, existingSession: savedSession, baseURL: baseURLString)
            } else {
                phase = .connecting
            }
        } else if let saved = sessionStore.load() {
            authenticatedEmail = saved.email
            authenticatedRole = saved.role
            authenticatedUserID = saved.userID
            phase = .setup
        } else {
            phase = .setup
        }
    }

    var isAdmin: Bool {
        authenticatedRole?.lowercased() == "admin"
    }

    var authEnabled: Bool {
        schema?.auth?.enabled == true
    }

    var displayAppName: String {
        let configuredName = generatedDisplayName.trimmingCharacters(in: .whitespacesAndNewlines)
        if let appName = schema?.appName, !appName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return appName
        }
        if !configuredName.isEmpty {
            return configuredName
        }
        return "Mar Runtime"
    }

    func start() async {
        await connect()
    }

    func connect() async {
        errorMessage = nil
        errorDetails = nil
        infoMessage = nil

        guard let normalizedURL = configuredBaseURL else {
            errorMessage = "This app is missing its server configuration."
            errorDetails = "Regenerate the iOS project from the .mar file so it includes a valid server_url."
            phase = .setup
            return
        }

        logStartupConfiguration(baseURL: normalizedURL)
        await requestLocalNetworkPermissionIfNeeded(for: normalizedURL)

        let existing = sessionStore.load()
        let client = MarAPIClient(baseURL: normalizedURL, token: existing?.baseURL == normalizedURL.absoluteString ? existing?.token : nil)
        self.client = client
        await client.setSchemaVersionObserver { [weak self] version in
            Task { @MainActor in
                self?.observeSchemaVersion(version)
            }
        }

        let baseURLString = normalizedURL.absoluteString

        if let startupSnapshot = startupSnapshot(baseURL: baseURLString) {
            schema = startupSnapshot.schema
            currentSchemaVersion = startupSnapshot.version
            applyStartupPhase(using: startupSnapshot.schema, existingSession: existing, baseURL: baseURLString)

            Task { [weak self] in
                await self?.finishConnect(using: client, existingSession: existing, baseURL: baseURLString, hasImmediateSchema: true)
            }
            return
        }

        isBusy = true
        phase = .connecting

        do {
            async let schemaTask = client.fetchSchema()
            async let publicVersionTask = client.fetchPublicVersion()

            let schemaResponse = try await schemaTask
            let schema = schemaResponse.schema
            self.schema = schema
            currentSchemaVersion = schemaResponse.version
            saveSchemaSnapshot(baseURL: baseURLString, schema: schema, version: schemaResponse.version)
            publicVersion = try? await publicVersionTask

            if schema.auth?.enabled == true {
                if let stored = existing, stored.baseURL == baseURLString, !stored.token.isEmpty {
                    await restoreStoredSession(using: client, session: stored, baseURL: baseURLString)
                } else {
                    transitionToAuthenticationRequired(clearSession: false)
                }
            } else {
                setAuthenticatedUser(email: nil, role: nil, userID: nil)
                phase = .ready
            }
        } catch {
            phase = .setup
            setStartupError(error)
        }

        isBusy = false
    }

    func requestCode() async {
        guard let client else { return }
        let email = authEmail.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !email.isEmpty else {
            errorMessage = "Enter your email before requesting a code."
            errorDetails = nil
            return
        }

        isBusy = true
        errorMessage = nil
        errorDetails = nil
        loginAlert = nil
        do {
            let response = try await client.requestCode(email: email)
            authEmail = email
            authCode = ""
            loginStep = .code
            infoMessage = response.message
        } catch {
            setError(error)
        }
        isBusy = false
    }

    func login() async {
        guard let client else { return }
        let email = authEmail.trimmingCharacters(in: .whitespacesAndNewlines)
        let code = authCode.trimmingCharacters(in: .whitespacesAndNewlines)

        guard !email.isEmpty, !code.isEmpty else {
            errorMessage = "Email and code are required."
            errorDetails = nil
            return
        }

        isBusy = true
        errorMessage = nil
        errorDetails = nil
        infoMessage = nil
        loginAlert = nil

        do {
            let response = try await client.login(email: email, code: code)
            await client.setToken(response.token)

            let me = try await client.authMe()
            applyAuthenticatedUser(me, token: response.token, baseURL: configuredBaseURL?.absoluteString ?? generatedServerURL)
            activatePendingSchemaIfNeeded()
            phase = .ready
            authCode = ""
            loginStep = .email
        } catch {
            setLoginError(error)
        }

        isBusy = false
    }

    func logout() async {
        let currentClient = client

        setAuthenticatedUser(email: nil, role: nil, userID: nil)
        authCode = ""
        loginStep = .email
        errorMessage = nil
        errorDetails = nil
        infoMessage = nil
        loginAlert = nil
        sessionStore.clear()
        phase = authEnabled ? .authenticationRequired : .ready

        guard let currentClient else { return }

        await currentClient.setToken(nil)
        Task {
            try? await currentClient.logout()
        }
    }

    func refreshSchema() async {
        guard let client else { return }
        errorDetails = nil
        do {
            let response = try await client.fetchSchema()
            pendingSchema = response.schema
            pendingSchemaVersion = response.version
            saveSchemaSnapshot(baseURL: configuredBaseURL?.absoluteString ?? generatedServerURL, schema: response.schema, version: response.version)
        } catch {
            setError(error)
        }
    }

    private func observeSchemaVersion(_ version: String) {
        let normalized = version.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !normalized.isEmpty else { return }

        if currentSchemaVersion == nil {
            currentSchemaVersion = normalized
            return
        }

        if currentSchemaVersion == normalized {
            return
        }

        if schemaRefreshInFlight {
            pendingSchemaVersion = normalized
            return
        }

        pendingSchemaVersion = normalized
        schemaRefreshInFlight = true
        Task { await refreshSchemaInBackground() }
    }

    private func refreshSchemaInBackground() async {
        guard let client else {
            schemaRefreshInFlight = false
            pendingSchemaVersion = nil
            return
        }

        defer {
            schemaRefreshInFlight = false
        }

        do {
            let response = try await client.fetchSchema()
            pendingSchema = response.schema
            pendingSchemaVersion = response.version ?? pendingSchemaVersion
            saveSchemaSnapshot(baseURL: configuredBaseURL?.absoluteString ?? generatedServerURL, schema: response.schema, version: response.version ?? pendingSchemaVersion)
            schemaRefreshAlert = nil
        } catch {
            pendingSchemaVersion = nil
            schemaRefreshAlert = LoginAlertState(title: "Update Available", message: "This app may need an update.")
        }
    }

    func editLoginEmail() {
        authCode = ""
        infoMessage = nil
        errorMessage = nil
        errorDetails = nil
        loginAlert = nil
        loginStep = .email
    }

    private static func normalizedBaseURL(from raw: String) -> URL? {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        let candidate = trimmed.hasPrefix("http://") || trimmed.hasPrefix("https://") ? trimmed : "http://\(trimmed)"
        guard var components = URLComponents(string: candidate), components.host != nil else {
            return nil
        }
        components.path = ""
        components.query = nil
        components.fragment = nil
        return components.url
    }

    private func saveSession(baseURL: String, token: String, email: String?, role: String?, userID: String?) {
        sessionStore.save(SessionSnapshot(baseURL: baseURL, token: token, email: email, role: role, userID: userID))
    }

    private func saveSchemaSnapshot(baseURL: String, schema: Schema, version: String?) {
        sessionStore.saveSchema(SchemaCacheSnapshot(baseURL: baseURL, version: version, schema: schema))
    }

    private func setAuthenticatedUser(email: String?, role: String?, userID: String?) {
        authenticatedEmail = email
        authenticatedRole = role
        authenticatedUserID = userID
    }

    private func applyAuthenticatedUser(_ response: AuthMeResponse, token: String? = nil, baseURL: String? = nil) {
        let userID = response.userId?.stringValue
        setAuthenticatedUser(email: response.email, role: response.role, userID: userID)

        if let token, let baseURL {
            saveSession(baseURL: baseURL, token: token, email: response.email, role: response.role, userID: userID)
        }
    }

    private func restoreStoredSession(using client: MarAPIClient, session: SessionSnapshot, baseURL: String) async {
        do {
            let me = try await client.authMe()
            applyAuthenticatedUser(me, token: session.token, baseURL: baseURL)
            phase = .ready
        } catch {
            transitionToAuthenticationRequired(clearSession: true)
        }
    }

    func activatePendingSchemaIfNeeded() {
        guard let pendingSchema else { return }
        schema = pendingSchema
        if let pendingSchemaVersion, !pendingSchemaVersion.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            currentSchemaVersion = pendingSchemaVersion
        }
        self.pendingSchema = nil
        self.pendingSchemaVersion = nil
        schemaRefreshAlert = nil
    }

    private func embeddedSchemaSnapshot(baseURL: String) -> SchemaCacheSnapshot? {
        let raw = generatedEmbeddedSchemaJSON.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty else { return nil }
        guard let data = raw.data(using: .utf8), let schema = try? JSONDecoder().decode(Schema.self, from: data) else {
            return nil
        }
        return SchemaCacheSnapshot(baseURL: baseURL, version: nil, schema: schema)
    }

    private func startupSnapshot(baseURL: String) -> SchemaCacheSnapshot? {
        sessionStore.loadSchema(baseURL: baseURL) ?? embeddedSchemaSnapshot(baseURL: baseURL)
    }

    private func applyStartupPhase(using schema: Schema, existingSession: SessionSnapshot?, baseURL: String) {
        if schema.auth?.enabled == true {
            if let stored = existingSession, stored.baseURL == baseURL, !stored.token.isEmpty {
                setAuthenticatedUser(email: stored.email, role: stored.role, userID: stored.userID)
                phase = .ready
            } else {
                transitionToAuthenticationRequired(clearSession: false)
            }
        } else {
            setAuthenticatedUser(email: nil, role: nil, userID: nil)
            phase = .ready
        }
        isBusy = false
    }

    private func finishConnect(using client: MarAPIClient, existingSession: SessionSnapshot?, baseURL: String, hasImmediateSchema: Bool) async {
        do {
            async let schemaTask = client.fetchSchema()
            async let publicVersionTask = client.fetchPublicVersion()

            let schemaResponse = try await schemaTask
            let latestSchema = schemaResponse.schema
            publicVersion = try? await publicVersionTask

            if hasImmediateSchema {
                pendingSchema = latestSchema
                pendingSchemaVersion = schemaResponse.version
            } else {
                schema = latestSchema
                currentSchemaVersion = schemaResponse.version
            }
            saveSchemaSnapshot(baseURL: baseURL, schema: latestSchema, version: schemaResponse.version)

            if latestSchema.auth?.enabled == true {
                if let stored = existingSession, stored.baseURL == baseURL, !stored.token.isEmpty {
                    await restoreStoredSession(using: client, session: stored, baseURL: baseURL)
                } else {
                    transitionToAuthenticationRequired(clearSession: false)
                }
            } else {
                setAuthenticatedUser(email: nil, role: nil, userID: nil)
                phase = .ready
            }
        } catch {
            if !hasImmediateSchema {
                phase = .setup
                setStartupError(error)
            }
        }

        if !hasImmediateSchema {
            isBusy = false
        }
    }

    private func transitionToAuthenticationRequired(clearSession: Bool) {
        setAuthenticatedUser(email: nil, role: nil, userID: nil)
        authCode = ""
        loginStep = .email
        phase = .authenticationRequired
        if clearSession {
            sessionStore.clear()
        }
    }

    private func logStartupConfiguration(baseURL: URL) {
        let bundleID = Bundle.main.bundleIdentifier ?? "<missing>"
        let localNetworkUsageDescription = Bundle.main.object(forInfoDictionaryKey: "NSLocalNetworkUsageDescription") as? String ?? "<missing>"
        let bonjourServices = Bundle.main.object(forInfoDictionaryKey: "NSBonjourServices") ?? "<missing>"
        let transportSecurity = Bundle.main.object(forInfoDictionaryKey: "NSAppTransportSecurity") ?? "<missing>"
        print("Mar iOS startup bundle=\(bundleID) baseURL=\(baseURL.absoluteString)")
        print("Mar iOS startup NSLocalNetworkUsageDescription=\(localNetworkUsageDescription)")
        print("Mar iOS startup NSBonjourServices=\(bonjourServices)")
        print("Mar iOS startup NSAppTransportSecurity=\(transportSecurity)")
    }

    private func requestLocalNetworkPermissionIfNeeded(for baseURL: URL) async {
        guard Self.needsLocalNetworkPermission(for: baseURL) else { return }
        await LocalNetworkPermissionProbe().request()
        try? await Task.sleep(nanoseconds: 500_000_000)
    }

    private static func needsLocalNetworkPermission(for baseURL: URL) -> Bool {
        guard let host = baseURL.host?.lowercased() else { return false }
        if host == "localhost" || host.hasSuffix(".local") {
            return true
        }
        if host.hasPrefix("192.168.") || host.hasPrefix("10.") {
            return true
        }

        let parts = host.split(separator: ".").compactMap { Int($0) }
        if parts.count == 4, parts[0] == 172, (16...31).contains(parts[1]) {
            return true
        }

        return false
    }

    private func setStartupError(_ error: Error) {
        if let clientError = error as? MarClientError {
            switch clientError {
            case .transport(_, let message, _):
                errorMessage = "We’re having trouble reaching the app."
                errorDetails = message + " Make sure the Mar server is running and try again."
                return
            case .decoding:
                errorMessage = "This app could not load its latest schema."
                errorDetails = "Try again in a moment."
                return
            }
        }

        if let apiError = error as? APIErrorResponse {
            errorMessage = "This app could not load its latest schema."
            errorDetails = apiError.message
            return
        }

        errorMessage = "This app could not finish loading."
        errorDetails = "Try again in a moment."
    }

    private func setError(_ error: Error) {
        errorMessage = error.localizedDescription
        errorDetails = nil
    }

    private func setLoginError(_ error: Error) {
        if error is APIErrorResponse {
            loginAlert = LoginAlertState(title: "Invalid Code", message: "The code is invalid or expired.")
            return
        }

        if let clientError = error as? MarClientError {
            loginAlert = LoginAlertState(title: "Could Not Sign In", message: clientError.localizedDescription)
            return
        }

        loginAlert = LoginAlertState(title: "Could Not Sign In", message: "Please try again.")
    }
}

@MainActor
final class EntityRowsViewModel: ObservableObject {
    let entity: Entity
    let schema: Schema
    private let client: MarAPIClient

    @Published var rows: [Row] = []
    @Published var relationLabelsByEntity: [String: [String: String]] = [:]
    @Published var isLoading = false
    @Published var errorMessage: String?

    init(entity: Entity, schema: Schema, client: MarAPIClient) {
        self.entity = entity
        self.schema = schema
        self.client = client
    }

    func load() async {
        guard !isLoading else { return }
        isLoading = true
        errorMessage = nil

        do {
            let loadedRows = try await client.listRows(entity: entity)
            rows = loadedRows
            isLoading = false
            guard !loadedRows.isEmpty else {
                relationLabelsByEntity = [:]
                return
            }
            relationLabelsByEntity = await loadRelationLabelsBestEffort()
        } catch {
            isLoading = false
            errorMessage = error.localizedDescription
        }
    }

    func reload() async {
        await load()
    }

    func insertOrReplace(_ row: Row) {
        guard let newID = RowPresentation.rowID(entity: entity, row: row) else {
            rows.insert(row, at: 0)
            return
        }

        if let index = rows.firstIndex(where: { RowPresentation.rowID(entity: entity, row: $0) == newID }) {
            rows[index] = row
        } else {
            rows.insert(row, at: 0)
        }
    }

    private func loadRelationLabelsBestEffort() async -> [String: [String: String]] {
        let relationNames = Set(entity.fields.compactMap(\.relationEntity))
        guard !relationNames.isEmpty else { return [:] }

        var result: [String: [String: String]] = [:]
        for relationName in relationNames {
            guard let relationEntity = schema.entities.first(where: { $0.name == relationName }) else { continue }
            guard let relationRows = try? await client.listRows(entity: relationEntity) else { continue }
            result[relationName] = Dictionary(
                uniqueKeysWithValues: relationRows.compactMap { row in
                    guard let id = RowPresentation.rowID(entity: relationEntity, row: row) else { return nil }
                    return (id, RowPresentation.relatedRowLabel(entity: relationEntity, row: row))
                }
            )
        }
        return result
    }
}

@MainActor
final class EntityFormViewModel: ObservableObject {
    enum Mode {
        case create
        case edit(Row)
    }

    let entity: Entity
    let schema: Schema
    let mode: Mode
    let formFields: [FrontendFormFieldInfo]
    private let client: MarAPIClient

    @Published var values: [String: String]
    @Published var relationRows: [String: [Row]] = [:]
    @Published var isSaving = false
    @Published var isLoadingRelations = false
    @Published var errorMessage: String?

    init(entity: Entity, schema: Schema, client: MarAPIClient, mode: Mode, initialValues: [String: String] = [:], formFields: [FrontendFormFieldInfo] = []) {
        self.entity = entity
        self.schema = schema
        self.client = client
        self.mode = mode
        self.formFields = formFields

        let resolvedVisibleFields: [Field]
        if formFields.isEmpty {
            resolvedVisibleFields = entity.visibleFields
        } else {
            resolvedVisibleFields = formFields.compactMap { formField in
                entity.visibleFields.first(where: { $0.name == formField.field })
            }
        }

        let defaults: [String: String]
        switch mode {
        case .create:
            defaults = Dictionary(uniqueKeysWithValues: resolvedVisibleFields.map { ($0.name, RowPresentation.defaultText(for: $0)) })
        case .edit(let row):
            defaults = Dictionary(uniqueKeysWithValues: resolvedVisibleFields.map { ($0.name, RowPresentation.formText(for: $0, row: row)) })
        }
        self.values = defaults.merging(initialValues) { _, preset in preset }
    }

    func loadRelations() async {
        isLoadingRelations = true
        defer { isLoadingRelations = false }

        for field in visibleFields {
            guard let relationEntityName = field.relationEntity else { continue }
            guard let filters = relationFilters(for: field) else { continue }
            let cacheKey = relationRowsCacheKey(entityName: relationEntityName, filters: filters)
            guard relationRows[cacheKey] == nil, let relationEntity = schema.entities.first(where: { $0.name == relationEntityName }) else { continue }
            do {
                relationRows[cacheKey] = try await client.listRows(entity: relationEntity, filters: filters)
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    func setValue(_ value: String, for fieldName: String) {
        let dependentFields = dependentFieldNames(for: fieldName)
        for dependentField in dependentFields {
            values.removeValue(forKey: dependentField)
        }
        values[fieldName] = value
    }

    func relationRows(for field: Field) -> [Row] {
        guard let relationEntityName = field.relationEntity else { return [] }
        guard let filters = relationFilters(for: field) else { return [] }
        return relationRows[relationRowsCacheKey(entityName: relationEntityName, filters: filters)] ?? []
    }

    func relationHelperText(for field: Field) -> String? {
        guard let config = formFieldConfig(for: field.name),
              let (_, parentField) = frontendDependentFilter(config)
        else {
            return nil
        }
        let parentValue = values[parentField, default: ""].trimmingCharacters(in: .whitespacesAndNewlines)
        return parentValue.isEmpty ? "Select \(RowPresentation.fieldLabel(parentField)) first." : nil
    }

    func submit() async throws -> Row {
        isSaving = true
        errorMessage = nil
        defer { isSaving = false }

        let payload = try PayloadEncoder.buildPayload(fields: entity.fields, valuesByName: values, forUpdate: isUpdate)

        switch mode {
        case .create:
            return try await client.createRow(entity: entity, payload: payload)
        case .edit(let row):
            guard let id = RowPresentation.rowID(entity: entity, row: row) else {
                throw APIErrorResponse(errorCode: "missing_primary_key", message: "Missing primary key for update", details: nil)
            }
            return try await client.updateRow(entity: entity, id: id, payload: payload)
        }
    }

    var isUpdate: Bool {
        if case .edit = mode { return true }
        return false
    }

    var visibleFields: [Field] {
        if formFields.isEmpty {
            return entity.visibleFields
        }
        return formFields.compactMap { formField in
            entity.visibleFields.first(where: { $0.name == formField.field })
        }
    }

    private func formFieldConfig(for fieldName: String) -> FrontendFormFieldInfo? {
        formFields.first(where: { $0.field == fieldName })
    }

    private func relationFilters(for field: Field) -> [String: String]? {
        guard let config = formFieldConfig(for: field.name),
              let (relationField, parentField) = frontendDependentFilter(config)
        else {
            return [:]
        }
        let parentValue = values[parentField, default: ""].trimmingCharacters(in: .whitespacesAndNewlines)
        guard !parentValue.isEmpty else { return nil }
        return [relationField: parentValue]
    }

    private func dependentFieldNames(for parentFieldName: String) -> [String] {
        formFields.compactMap { formField in
            guard let (_, parentField) = frontendDependentFilter(formField), parentField == parentFieldName else {
                return nil
            }
            return formField.field
        }
    }
}

func frontendDependentFilter(_ formField: FrontendFormFieldInfo) -> (String, String)? {
    guard let rawFilter = formField.filter?.trimmingCharacters(in: .whitespacesAndNewlines), !rawFilter.isEmpty else {
        return nil
    }
    let parts = rawFilter.components(separatedBy: "==")
    guard parts.count == 2 else { return nil }
    let relationField = parts[0].trimmingCharacters(in: .whitespacesAndNewlines)
    let rightSide = parts[1].trimmingCharacters(in: .whitespacesAndNewlines)
    guard rightSide.hasPrefix("form.") else { return nil }
    return (relationField, String(rightSide.dropFirst("form.".count)))
}

func relationRowsCacheKey(entityName: String, filters: [String: String]) -> String {
    guard !filters.isEmpty else { return entityName }
    let query = filters
        .sorted { $0.key < $1.key }
        .map { key, value in "\(key)=\(value)" }
        .joined(separator: "&")
    return "\(entityName)?\(query)"
}

@MainActor
final class AdminViewModel: ObservableObject {
    struct DownloadedBackup: Identifiable {
        let id = UUID()
        let url: URL
    }

    private let client: MarAPIClient

    @Published var version: AdminVersionPayload?
    @Published var perf: PerfPayload?
    @Published var backups: BackupsPayload?
    @Published var requestLogs: RequestLogsPayload?
    @Published var isLoadingRuntime = false
    @Published var isLoadingRequestLogs = false
    @Published var isLoadingBackups = false
    @Published var isCreatingBackup = false
    @Published var downloadingBackupName: String?
    @Published var downloadedBackup: DownloadedBackup?
    @Published var runtimeErrorMessage: String?
    @Published var requestLogsErrorMessage: String?
    @Published var backupsErrorMessage: String?
    @Published var backupsInfoMessage: String?

    init(client: MarAPIClient) {
        self.client = client
    }

    func loadRuntime() async {
        isLoadingRuntime = true
        runtimeErrorMessage = nil
        defer { isLoadingRuntime = false }

        version = nil
        perf = nil

        do {
            async let versionPayload = client.fetchAdminVersion()
            async let perfPayload = client.fetchPerformance()
            version = try await versionPayload
            perf = try await perfPayload
        } catch {
            runtimeErrorMessage = error.localizedDescription
        }
    }

    func loadRequestLogs() async {
        isLoadingRequestLogs = true
        requestLogsErrorMessage = nil
        defer { isLoadingRequestLogs = false }

        requestLogs = nil

        do {
            requestLogs = try await client.fetchRequestLogs()
        } catch {
            requestLogsErrorMessage = error.localizedDescription
        }
    }

    func loadBackups() async {
        isLoadingBackups = true
        backupsErrorMessage = nil
        defer { isLoadingBackups = false }

        backups = nil

        do {
            backups = try await client.fetchBackups()
        } catch {
            backupsErrorMessage = error.localizedDescription
        }
    }

    func createBackup() async {
        isCreatingBackup = true
        backupsErrorMessage = nil
        backupsInfoMessage = nil
        defer { isCreatingBackup = false }

        do {
            _ = try await client.createBackup()
            backups = try await client.fetchBackups()
            backupsInfoMessage = "Backup created."
        } catch {
            backupsErrorMessage = error.localizedDescription
        }
    }

    func downloadBackup(_ backup: BackupFile) async {
        downloadingBackupName = backup.name
        backupsErrorMessage = nil
        backupsInfoMessage = nil
        defer { downloadingBackupName = nil }

        do {
            let url = try await client.downloadBackup(named: backup.name)
            downloadedBackup = DownloadedBackup(url: url)
            backupsInfoMessage = "Backup ready to share."
        } catch {
            backupsErrorMessage = error.localizedDescription
        }
    }

}
