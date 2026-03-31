import Foundation
import SwiftUI

private let generatedServerURL = "__MAR_IOS_SERVER_URL__"

struct LoginAlertState: Identifiable {
    let id = UUID()
    let title: String
    let message: String
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
        case more
    }

    @Published var phase: Phase = .connecting
    @Published var authEmail: String = ""
    @Published var authCode: String = ""
    @Published var loginStep: LoginStep = .email
    @Published var selectedTab: Tab = .profile
    @Published var schema: Schema?
    @Published var authenticatedEmail: String?
    @Published var authenticatedRole: String?
    @Published var isBusy = false
    @Published var errorMessage: String?
    @Published var errorDetails: String?
    @Published var infoMessage: String?
    @Published var loginAlert: LoginAlertState?

    private let sessionStore = SessionStore()
    private let configuredBaseURL: URL?
    private(set) var client: MarAPIClient?

    init() {
        configuredBaseURL = Self.normalizedBaseURL(from: generatedServerURL)
        if let saved = sessionStore.load() {
            authenticatedEmail = saved.email
            authenticatedRole = saved.role
        }
    }

    var isAdmin: Bool {
        authenticatedRole?.lowercased() == "admin"
    }

    var authEnabled: Bool {
        schema?.auth?.enabled == true
    }

    var availableTabs: [Tab] {
        var tabs = businessEntityTabs
        if !(schema?.actions.isEmpty ?? true) {
            tabs.append(.actions)
        }
        if isAdmin {
            tabs.append(.admin)
        }
        if authEnabled {
            tabs.append(.profile)
        }
        if shouldShowUserEntityTabs {
            tabs.append(contentsOf: userEntityTabs)
        }
        if tabs.count > 4 {
            return Array(tabs.prefix(4)) + [.more]
        }
        return tabs
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

        isBusy = true
        phase = .connecting
        let existing = sessionStore.load()
        let client = MarAPIClient(baseURL: normalizedURL, token: existing?.baseURL == normalizedURL.absoluteString ? existing?.token : nil)
        self.client = client

        do {
            let schema = try await client.fetchSchema()
            self.schema = schema

            if schema.auth?.enabled == true {
                if let stored = existing, stored.baseURL == normalizedURL.absoluteString, !stored.token.isEmpty {
                    do {
                        let me = try await client.authMe()
                        authenticatedEmail = me.email
                        authenticatedRole = me.role
                        saveSession(baseURL: normalizedURL.absoluteString, token: stored.token, email: me.email, role: me.role)
                        ensureValidSelectedTab()
                        phase = .ready
                    } catch {
                        authenticatedEmail = nil
                        authenticatedRole = nil
                        authCode = ""
                        loginStep = .email
                        phase = .authenticationRequired
                        sessionStore.clear()
                    }
                } else {
                    authenticatedEmail = nil
                    authenticatedRole = nil
                    authCode = ""
                    loginStep = .email
                    phase = .authenticationRequired
                }
            } else {
                authenticatedEmail = nil
                authenticatedRole = nil
                ensureValidSelectedTab()
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
            authenticatedEmail = me.email
            authenticatedRole = me.role
            saveSession(baseURL: configuredBaseURL?.absoluteString ?? generatedServerURL, token: response.token, email: me.email, role: me.role)
            ensureValidSelectedTab()
            phase = .ready
            authCode = ""
            loginStep = .email
        } catch {
            setLoginError(error)
        }

        isBusy = false
    }

    func logout() async {
        defer {
            authenticatedEmail = nil
            authenticatedRole = nil
            authCode = ""
            loginStep = .email
            sessionStore.clear()
            phase = authEnabled ? .authenticationRequired : .ready
        }

        guard let client else { return }
        try? await client.logout()
        await client.setToken(nil)
    }

    func refreshSchema() async {
        guard let client else { return }
        errorDetails = nil
        do {
            schema = try await client.fetchSchema()
            ensureValidSelectedTab()
        } catch {
            setError(error)
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

    private func saveSession(baseURL: String, token: String, email: String?, role: String?) {
        sessionStore.save(SessionSnapshot(baseURL: baseURL, token: token, email: email, role: role))
    }

    private func ensureValidSelectedTab() {
        guard let first = availableTabs.first else {
            selectedTab = .profile
            return
        }
        if !availableTabs.contains(selectedTab) {
            selectedTab = first
        }
    }

    private func setStartupError(_ error: Error) {
        if let clientError = error as? MarClientError {
            switch clientError {
            case .transport(_, let message, _):
                errorMessage = "We’re having trouble reaching the app."
                errorDetails = message + " Check your internet connection and try again."
                return
            case .decoding:
                errorMessage = "This app could not load its latest schema."
                errorDetails = "The server responded with unexpected data. Try again in a moment or regenerate the iOS project."
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
        if let clientError = error as? MarClientError {
            errorDetails = clientError.debugDetails
        } else {
            errorDetails = String(reflecting: error)
        }
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

    private var businessEntityTabs: [Tab] {
        (schema?.entities ?? [])
            .filter { $0.name.caseInsensitiveCompare("User") != .orderedSame }
            .map { Tab.entity($0.name) }
    }

    private var userEntityTabs: [Tab] {
        (schema?.entities ?? [])
            .filter { $0.name.caseInsensitiveCompare("User") == .orderedSame }
            .map { Tab.entity($0.name) }
    }

    private var shouldShowUserEntityTabs: Bool {
        isAdmin
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
        isLoading = true
        errorMessage = nil
        defer { isLoading = false }

        do {
            rows = try await client.listRows(entity: entity)
            relationLabelsByEntity = try await loadRelationLabels()
        } catch {
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

    func deleteRow(_ row: Row) async throws {
        guard let id = RowPresentation.rowID(entity: entity, row: row) else { return }
        try await client.deleteRow(entity: entity, id: id)
        rows.removeAll { RowPresentation.rowID(entity: entity, row: $0) == id }
    }

    private func loadRelationLabels() async throws -> [String: [String: String]] {
        let relationNames = Set(entity.fields.compactMap(\.relationEntity))
        guard !relationNames.isEmpty else { return [:] }

        var result: [String: [String: String]] = [:]
        for relationName in relationNames {
            guard let relationEntity = schema.entities.first(where: { $0.name == relationName }) else { continue }
            let relationRows = try await client.listRows(entity: relationEntity)
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
    private let client: MarAPIClient

    @Published var values: [String: String]
    @Published var relationRows: [String: [Row]] = [:]
    @Published var isSaving = false
    @Published var isLoadingRelations = false
    @Published var errorMessage: String?

    init(entity: Entity, schema: Schema, client: MarAPIClient, mode: Mode) {
        self.entity = entity
        self.schema = schema
        self.client = client
        self.mode = mode

        switch mode {
        case .create:
            self.values = Dictionary(uniqueKeysWithValues: entity.visibleFields.map { ($0.name, RowPresentation.defaultText(for: $0)) })
        case .edit(let row):
            self.values = Dictionary(uniqueKeysWithValues: entity.visibleFields.map { ($0.name, RowPresentation.formText(for: $0, row: row)) })
        }
    }

    func loadRelations() async {
        let relationNames = Set(entity.visibleFields.compactMap(\.relationEntity))
        guard !relationNames.isEmpty else { return }

        isLoadingRelations = true
        defer { isLoadingRelations = false }

        for name in relationNames {
            guard relationRows[name] == nil, let relationEntity = schema.entities.first(where: { $0.name == name }) else { continue }
            do {
                relationRows[name] = try await client.listRows(entity: relationEntity)
            } catch {
                errorMessage = error.localizedDescription
            }
        }
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
}

@MainActor
final class AdminViewModel: ObservableObject {
    struct DownloadedBackup: Identifiable {
        let id = UUID()
        let url: URL
        let name: String
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
            downloadedBackup = DownloadedBackup(url: url, name: backup.name)
            backupsInfoMessage = "Backup ready to share."
        } catch {
            backupsErrorMessage = error.localizedDescription
        }
    }

}
