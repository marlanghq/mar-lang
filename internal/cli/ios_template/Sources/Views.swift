import SwiftUI
import UIKit

private extension Notification.Name {
    static let marRowsChanged = Notification.Name("marRowsChanged")
}

struct RootView: View {
    @ObservedObject var model: AppViewModel

    var body: some View {
        Group {
            switch model.phase {
            case .setup:
                StartupErrorView(model: model)
            case .connecting:
                StartupLoadingView()
            case .authenticationRequired:
                LoginView(model: model)
            case .ready:
                if let schema = model.schema, let client = model.client {
                    ShellView(model: model, schema: schema, client: client)
                } else {
                    ProgressView("Loading…")
                }
            }
        }
        .animation(.easeInOut, value: model.phase)
        .alert(item: $model.schemaRefreshAlert) { alert in
            Alert(
                title: Text(alert.title),
                message: Text(alert.message),
                dismissButton: .default(Text("OK"))
            )
        }
    }
}

struct StartupLoadingView: View {
    var body: some View {
        VStack(spacing: 16) {
            ProgressView()
                .controlSize(.large)
            Text("Loading latest app data…")
                .font(.headline)
        }
        .padding(24)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color(.systemGroupedBackground))
    }
}

private struct LatestSchemaDestination<Content: View>: View {
    @ObservedObject var model: AppViewModel
    let fallbackSchema: Schema
    let build: (Schema) -> Content

    var body: some View {
        build(model.schema ?? fallbackSchema)
            .onAppear {
                model.activatePendingSchemaIfNeeded()
            }
    }
}

struct StartupErrorView: View {
    @ObservedObject var model: AppViewModel

    var body: some View {
        NavigationStack {
            VStack(spacing: 20) {
                Spacer()

                Image(systemName: "wifi.exclamationmark")
                    .font(.system(size: 42))
                    .foregroundStyle(.blue)

                VStack(spacing: 8) {
                    Text("Unable to open this app")
                        .font(.title3.bold())

                    if let message = model.errorMessage {
                        Text(message)
                            .multilineTextAlignment(.center)
                    }

                    if let details = model.errorDetails, !details.isEmpty {
                        Text(details)
                            .font(.subheadline)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                    }
                }

                Button {
                    Task { await model.connect() }
                } label: {
                    if model.isBusy {
                        ProgressView()
                            .frame(maxWidth: .infinity)
                    } else {
                        Text("Try again")
                            .frame(maxWidth: .infinity)
                    }
                }
                .buttonStyle(.borderedProminent)
                .disabled(model.isBusy)
                .frame(maxWidth: 320)

                Spacer()
            }
            .padding(24)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Color(.systemGroupedBackground))
            .navigationTitle(model.displayAppName)
        }
    }
}

struct LoginView: View {
    @ObservedObject var model: AppViewModel

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 0) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(loginSubtitle)
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
                .padding(.horizontal, 20)
                .padding(.top, 8)
                .padding(.bottom, 8)

                Form {
                    if model.loginStep == .email {
                        Section("Email") {
                            TextField("name@example.com", text: $model.authEmail)
                                .textInputAutocapitalization(.never)
                                .keyboardType(.emailAddress)
                                .textContentType(.emailAddress)
                                .autocorrectionDisabled()
                                .disabled(model.isBusy)
                        }
                    } else {
                        Section("Code") {
                            TextField("123456", text: $model.authCode)
                                .textInputAutocapitalization(.never)
                                .keyboardType(.numberPad)
                                .textContentType(.oneTimeCode)
                                .font(.system(.body, design: .rounded).monospacedDigit())
                                .disabled(model.isBusy)
                        }

                        Section("Email") {
                            Text(model.authEmail)
                                .foregroundStyle(.primary)

                            Button("Use a Different Email") {
                                model.editLoginEmail()
                            }
                            .disabled(model.isBusy)

                            Button("Send Another Code") {
                                Task { await model.requestCode() }
                            }
                            .disabled(model.isBusy)
                        }
                    }

                    if model.loginStep == .email, let message = model.infoMessage {
                        Section {
                            Text(message)
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                        }
                    }

                    if let message = model.errorMessage {
                        Section {
                            VStack(alignment: .leading, spacing: 8) {
                                Text(message)
                                    .foregroundStyle(.red)
                            }
                            .padding(.vertical, 2)
                        }
                    }

                    if let auth = model.schema?.auth, auth.needsBootstrap {
                        Section("Not implemented yet") {
                            Text("Bootstrap-first-admin flow is not implemented in this iOS runtime yet. Use the web Admin for the first admin creation.")
                                .font(.footnote)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
            .navigationTitle(model.loginStep == .email ? "Sign In" : "Enter Code")
            .navigationBarTitleDisplayMode(.large)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        if model.loginStep == .email {
                            Task { await model.requestCode() }
                        } else {
                            Task { await model.login() }
                        }
                    } label: {
                        if model.isBusy {
                            ProgressView()
                        } else {
                            Text(model.loginStep == .email ? "Next" : "Sign In")
                        }
                    }
                    .disabled(model.isBusy || primaryActionDisabled)
                }
            }
            .alert(item: $model.loginAlert) { alert in
                Alert(
                    title: Text(alert.title),
                    message: Text(alert.message),
                    dismissButton: .default(Text("OK"))
                )
            }
        }
    }

    private var primaryActionDisabled: Bool {
        switch model.loginStep {
        case .email:
            model.authEmail.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        case .code:
            model.authCode.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
    }

    private var loginSubtitle: String {
        switch model.loginStep {
        case .email:
            "We will send you a 6-digit access code."
        case .code:
            "Enter the 6-digit access code we sent to your email."
        }
    }
}

struct ShellView: View {
    @ObservedObject var model: AppViewModel
    let schema: Schema
    let client: MarAPIClient

    var body: some View {
        if let frontend = schema.screens, let firstScreen = frontend.screens.first {
            NavigationStack {
                FrontendScreenView(screen: firstScreen, row: nil, parentEntity: nil, parentRow: nil, schema: schema, client: client, model: model)
            }
        } else {
            NavigationStack {
                List {
                    ForEach(primarySpecs) { spec in
                        NavigationLink {
                            LatestSchemaDestination(model: model, fallbackSchema: schema) { liveSchema in
                                ShellTabDestinationView(spec: spec, schema: liveSchema, client: client, model: model)
                            }
                        } label: {
                            Text(spec.title)
                        }
                    }

                    if !adminSpecs.isEmpty {
                        Section("Admin") {
                            ForEach(adminSpecs) { spec in
                                NavigationLink {
                                    LatestSchemaDestination(model: model, fallbackSchema: schema) { liveSchema in
                                        ShellTabDestinationView(spec: spec, schema: liveSchema, client: client, model: model)
                                    }
                                } label: {
                                    Text(spec.title)
                                }
                            }
                        }
                    }
                }
                .listStyle(.insetGrouped)
                .contentMargins(.top, 18, for: .scrollContent)
                .listSectionSpacing(.custom(24))
                .navigationTitle(schema.appName)
                .toolbar {
                    if model.authEnabled {
                        ToolbarItem(placement: .topBarTrailing) {
                            NavigationLink {
                                ProfileView(model: model)
                            } label: {
                                Image(systemName: "person.crop.circle")
                            }
                            .accessibilityLabel("Profile")
                        }
                    }
                }
            }
        }
    }

    private var primarySpecs: [ShellTabSpec] {
        var specs = businessEntitySpecs
        if !schema.actions.isEmpty {
            specs.append(ShellTabSpec(tab: .actions, title: "Actions"))
        }
        return specs
    }

    private var adminSpecs: [ShellTabSpec] {
        var specs: [ShellTabSpec] = []
        specs.append(contentsOf: userEntitySpecs)
        if model.isAdmin {
            specs.append(ShellTabSpec(tab: .admin, title: "Admin"))
        }
        return specs
    }

    private var businessEntitySpecs: [ShellTabSpec] {
        schema.entities
            .filter { $0.name.caseInsensitiveCompare("User") != .orderedSame }
            .map { entity in
                ShellTabSpec(
                    tab: .entity(entity.name),
                    title: entity.displayName,
                    entity: entity
                )
            }
    }

    private var userEntitySpecs: [ShellTabSpec] {
        schema.entities
            .filter { $0.name.caseInsensitiveCompare("User") == .orderedSame }
            .map { entity in
                ShellTabSpec(
                    tab: .entity(entity.name),
                    title: entity.displayName,
                    entity: entity
                )
            }
    }
}

private struct ShellTabSpec: Identifiable {
    let tab: AppViewModel.Tab
    let title: String
    let entity: Entity?

    init(tab: AppViewModel.Tab, title: String, entity: Entity? = nil) {
        self.tab = tab
        self.title = title
        self.entity = entity
    }

    var id: String {
        switch tab {
        case .entity(let name):
            return "entity:\(name)"
        case .actions:
            return "actions"
        case .admin:
            return "admin"
        case .profile:
            return "profile"
        }
    }
}

private struct ShellTabDestinationView: View {
    let spec: ShellTabSpec
    let schema: Schema
    let client: MarAPIClient
    let model: AppViewModel

    var body: some View {
        switch spec.tab {
        case .entity:
            if let entity = spec.entity {
                EntityRowsView(entity: entity, schema: schema, client: client)
            } else {
                EmptyView()
            }
        case .actions:
            ActionsHomeView(schema: schema, client: client)
        case .admin:
            AdminHomeView(client: client)
        case .profile:
            ProfileView(model: model)
        }
    }
}

private struct FrontendScreenView: View {
    let screen: FrontendScreenInfo
    let row: Row?
    let parentEntity: Entity?
    let parentRow: Row?
    let schema: Schema
    let client: MarAPIClient
    let model: AppViewModel
    @Environment(\.dismiss) private var dismiss
    @State private var editingListEntities: Set<String> = []

    var body: some View {
        List {
            if screen.forEntity != nil && row == nil {
                Section {
                    ContentUnavailableView("No record selected", systemImage: "questionmark.circle")
                }
            } else {
                ForEach(Array(screen.sections.enumerated()), id: \.offset) { _, section in
                    if frontendCondition(section.when, row: row, model: model) {
                        Section(section.title ?? "") {
                            ForEach(Array(section.items.enumerated()), id: \.offset) { _, item in
                                FrontendItemView(
                                    item: item,
                                    screen: screen,
                                    sectionTitle: section.title,
                                    row: row,
                                    parentEntity: parentEntity,
                                    parentRow: parentRow,
                                    schema: schema,
                                    client: client,
                                    model: model,
                                    editingListEntities: editingListEntities
                                )
                            }
                        }
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
        .navigationTitle(screenTitle)
        .toolbar {
            ToolbarItemGroup(placement: .topBarTrailing) {
                ForEach(Array(trailingToolbarItems.enumerated()), id: \.offset) { _, toolbarItem in
                    toolbarView(for: toolbarItem)
                }
            }
            ToolbarItemGroup(placement: .primaryAction) {
                ForEach(Array(primaryToolbarItems.enumerated()), id: \.offset) { _, toolbarItem in
                    toolbarView(for: toolbarItem)
                }
            }
            if model.authEnabled && screen.name == "Home" {
                ToolbarItem(placement: .topBarTrailing) {
                    NavigationLink {
                        ProfileView(model: model)
                    } label: {
                        Image(systemName: "person.crop.circle")
                    }
                    .accessibilityLabel("Profile")
                }
            }
        }
    }

    private var trailingToolbarItems: [FrontendToolbarItemInfo] {
        screen.toolbarItems.filter { $0.placement != "primary" }
    }

    private var primaryToolbarItems: [FrontendToolbarItemInfo] {
        screen.toolbarItems.filter { $0.placement == "primary" }
    }

    private var screenTitle: String {
        if let expression = screen.titleExpression,
           let row,
           let resolved = frontendActionExpression(expression, binding: frontendScreenBindingName(screen), row: row, model: model),
           !resolved.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return resolved
        }
        if let title = screen.title, !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return title
        }
        if let forEntity = screen.forEntity, let entity = schema.entities.first(where: { $0.name == forEntity }), let row {
            return RowPresentation.rowTitle(entity: entity, row: row)
        }
        return RowPresentation.humanizeIdentifier(screen.name)
    }

    @ViewBuilder
    private func toolbarView(for toolbarItem: FrontendToolbarItemInfo) -> some View {
        switch toolbarItem.item.kind {
        case "edit":
            if let entity = toolbarScreenEntity, let row {
                NavigationLink {
                    LatestSchemaDestination(model: model, fallbackSchema: schema) { liveSchema in
                        EntityFormView(
                            entity: entity,
                            schema: liveSchema,
                            client: client,
                            mode: .edit(row)
                        ) { _ in } onDeleted: { _ in
                            dismiss()
                        }
                    }
                } label: {
                    Text("Edit")
                }
            }
        case "editList":
            if let entityName = toolbarItem.item.entity {
                let isEditing = editingListEntities.contains(entityName)
                Button(isEditing ? "Done" : "Edit") {
                    if isEditing {
                        editingListEntities.remove(entityName)
                    } else {
                        editingListEntities.insert(entityName)
                    }
                }
            }
        case "create":
            if let entityName = toolbarItem.item.entity,
               let entity = schema.entities.first(where: { $0.name == entityName }) {
                NavigationLink {
                    LatestSchemaDestination(model: model, fallbackSchema: schema) { liveSchema in
                        EntityFormView(
                            entity: entity,
                            schema: liveSchema,
                            client: client,
                            mode: .create
                        ) { _ in }
                    }
                } label: {
                    if toolbarItem.placement == "primary" {
                        Image(systemName: "plus")
                    } else {
                        Text("New")
                    }
                }
            }
        default:
            EmptyView()
        }
    }

    private var toolbarScreenEntity: Entity? {
        guard let name = screen.forEntity else { return nil }
        return schema.entities.first(where: { $0.name == name })
    }
}

private struct FrontendItemView: View {
    let item: FrontendItemInfo
    let screen: FrontendScreenInfo
    let sectionTitle: String?
    let row: Row?
    let parentEntity: Entity?
    let parentRow: Row?
    let schema: Schema
    let client: MarAPIClient
    let model: AppViewModel
    let editingListEntities: Set<String>
    @Environment(\.dismiss) private var dismiss
    @State private var confirmingDelete = false
    @State private var deleteErrorMessage: String?

    @ViewBuilder
    var body: some View {
        switch item.kind {
        case "link":
            if frontendCondition(item.filter, row: row, model: model),
               let targetName = item.target,
               let target = schema.screens?.screens.first(where: { $0.name == targetName }) {
                NavigationLink {
                    LatestSchemaDestination(model: model, fallbackSchema: schema) { liveSchema in
                        FrontendScreenView(screen: target, row: nil, parentEntity: screenEntity, parentRow: row, schema: liveSchema, client: client, model: model)
                    }
                } label: {
                    Text(item.label ?? RowPresentation.humanizeIdentifier(target.name))
                }
            }
        case "field":
            if let fieldName = item.field,
               let entity = screenEntity,
               let (labelText, text) = frontendFieldDisplay(entity: entity, fieldPath: fieldName, row: row) {
                if !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    LabeledContent(labelText, value: text)
                }
            }
        case "create":
            if let entityName = item.entity, let entity = schema.entities.first(where: { $0.name == entityName }) {
                NavigationLink("Create \(entity.displayName)") {
                    LatestSchemaDestination(model: model, fallbackSchema: schema) { liveSchema in
                        EntityFormView(
                            entity: entity,
                            schema: liveSchema,
                            client: client,
                            mode: .create,
                            initialValues: frontendActionValues(item.values, binding: frontendScreenBindingName(screen), row: row, model: model),
                            formFields: item.formFields
                        ) { _ in }
                    }
                }
            }
        case "edit":
            if let entity = screenEntity, let row {
                NavigationLink {
                    LatestSchemaDestination(model: model, fallbackSchema: schema) { liveSchema in
                        EntityFormView(
                            entity: entity,
                            schema: liveSchema,
                            client: client,
                            mode: .edit(row),
                            formFields: item.formFields
                        ) { _ in
                            dismiss()
                        } onDeleted: { _ in
                            dismiss()
                        }
                    }
                } label: {
                    Text("Edit \(entity.displayName)")
                }
            }
        case "delete":
            if let entity = screenEntity, row != nil {
                VStack(alignment: .leading, spacing: 8) {
                    Button {
                        confirmingDelete = true
                    } label: {
                        HStack {
                            Text("Delete \(entity.displayName)")
                                .foregroundStyle(.red)
                            Spacer()
                        }
                    }
                    .buttonStyle(.plain)
                    if let deleteErrorMessage, !deleteErrorMessage.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                        Text(deleteErrorMessage)
                            .font(.footnote)
                            .foregroundStyle(.red)
                    }
                }
                .alert("Delete \(entity.displayName)?", isPresented: $confirmingDelete) {
                    Button("Delete", role: .destructive) {
                        Task { await deleteCurrentRow(entity: entity) }
                    }
                    Button("Cancel", role: .cancel) {}
                } message: {
                    Text("This cannot be undone.")
                }
            }
        case "list", "children":
            if let entityName = item.entity, let entity = schema.entities.first(where: { $0.name == entityName }) {
                FrontendRowsView(
                    item: item,
                    entity: entity,
                    sectionTitle: sectionTitle,
                    screenTitle: screen.title,
                    parentEntity: screenEntity,
                    parentRow: row,
                    schema: schema,
                    client: client,
                    model: model,
                    isEditingList: editingListEntities.contains(entity.name)
                )
            }
        case "report":
            if let entityName = item.entity, let entity = schema.entities.first(where: { $0.name == entityName }) {
                FrontendReportView(
                    item: item,
                    entity: entity,
                    schema: schema,
                    client: client,
                    model: model
                )
            }
        case "action":
            if let actionName = item.action,
               let action = schema.actions.first(where: { $0.name == actionName }) {
                NavigationLink(RowPresentation.humanizeIdentifier(action.name)) {
                    LatestSchemaDestination(model: model, fallbackSchema: schema) { liveSchema in
                        ActionFormView(
                            action: action,
                            alias: liveSchema.inputAliases.first(where: { $0.name == action.inputAlias }),
                            schema: liveSchema,
                            client: client,
                            initialValues: frontendActionValues(item.values, binding: frontendScreenBindingName(screen), row: row, model: model),
                            formFields: item.formFields
                        )
                    }
                }
            }
        default:
            EmptyView()
        }
    }

    private var screenEntity: Entity? {
        guard let name = screen.forEntity else { return nil }
        return schema.entities.first(where: { $0.name == name })
    }

    private func frontendFieldDisplay(entity: Entity, fieldPath: String, row: Row?) -> (String, String)? {
        guard let row else { return nil }
        let (fieldName, _) = parseFrontendDisplayFieldPath(fieldPath)
        guard let field = entity.fields.first(where: { $0.name == fieldName }),
              let value = row[field.name] else {
            return nil
        }
        let text = frontendFieldText(field: field, value: value)
        return (RowPresentation.fieldLabel(field.name), text)
    }

    private func parseFrontendDisplayFieldPath(_ raw: String) -> (String, String?) {
        let parts = raw.split(separator: ".", omittingEmptySubsequences: false).map(String.init)
        if parts.count == 2, !parts[0].isEmpty, !parts[1].isEmpty {
            return (parts[0], parts[1])
        }
        return (raw, nil)
    }

    private func frontendFieldText(field: Field, value: JSONValue) -> String {
        guard let relationEntityName = field.relationEntity else {
            return RowPresentation.displayString(for: field, value: value)
        }
        let rawID = value.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !rawID.isEmpty, rawID != "null",
              schema.entities.contains(where: { $0.name == relationEntityName }) else {
            return RowPresentation.displayString(for: field, value: value)
        }
        if let parentEntity, let parentRow,
           parentEntity.name == relationEntityName,
           RowPresentation.rowID(entity: parentEntity, row: parentRow) == rawID {
            return RowPresentation.relatedRowLabel(entity: parentEntity, row: parentRow)
        }
        return RowPresentation.displayString(for: field, value: value)
    }

    private func deleteCurrentRow(entity: Entity) async {
        guard let row, let id = RowPresentation.rowID(entity: entity, row: row) else {
            deleteErrorMessage = "Could not remove this record."
            return
        }

        do {
            try await client.deleteRow(entity: entity, id: id)
            NotificationCenter.default.post(name: .marRowsChanged, object: entity.name)
            dismiss()
        } catch {
            deleteErrorMessage = error.localizedDescription
        }
    }
}

private struct FrontendComputedReport: Identifiable {
    let sortKey: String
    let label: String
    let metrics: [(String, String)]

    var id: String { sortKey }
}

private enum FrontendReportGroupSpec {
    case field(String)
    case month(String)
}

private struct FrontendReportView: View {
    let item: FrontendItemInfo
    let entity: Entity
    let schema: Schema
    let client: MarAPIClient
    let model: AppViewModel

    @State private var rows: [Row] = []
    @State private var isLoading = false
    @State private var hasStartedLoading = false
    @State private var errorMessage: String?

    var body: some View {
        Group {
            if let errorMessage, !errorMessage.isEmpty {
                Text(errorMessage)
                    .foregroundStyle(.red)
            } else if isLoading && rows.isEmpty {
                HStack {
                    ProgressView()
                    Text("Loading...")
                        .foregroundStyle(.secondary)
                }
            } else if reportRows.isEmpty {
                Text("No data yet.")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(reportRows) { report in
                    VStack(alignment: .leading, spacing: 8) {
                        Text(report.label)
                            .font(.headline)
                        ForEach(Array(report.metrics.enumerated()), id: \.offset) { _, metric in
                            LabeledContent(metric.0, value: metric.1)
                        }
                    }
                }
            }
        }
        .onAppear {
            guard !hasStartedLoading else { return }
            hasStartedLoading = true
            Task { await load() }
        }
        .onReceive(NotificationCenter.default.publisher(for: .marRowsChanged)) { notification in
            let changedEntity = notification.object as? String
            guard changedEntity == nil || changedEntity == entity.name else { return }
            Task { await load() }
        }
    }

    private var reportRows: [FrontendComputedReport] {
        guard let groupSpec = parseReportGroup(item.reportGroup, entity: entity) else { return [] }

        var grouped: [String: (label: String, rows: [Row])] = [:]
        for row in rows where frontendCondition(item.filter, row: row, model: model) {
            guard let group = reportGroupKey(groupSpec, row: row) else { continue }
            var entry = grouped[group.sortKey] ?? (label: group.label, rows: [])
            entry.rows.append(row)
            grouped[group.sortKey] = entry
        }

        return grouped
            .map { sortKey, entry in
                FrontendComputedReport(
                    sortKey: sortKey,
                    label: entry.label,
                    metrics: item.reportMetrics.compactMap { metricValue($0, rows: entry.rows) }
                )
            }
            .sorted { $0.sortKey < $1.sortKey }
    }

    private func load() async {
        guard !isLoading else { return }
        isLoading = true
        errorMessage = nil

        do {
            rows = try await client.listRows(entity: entity)
            isLoading = false
        } catch {
            isLoading = false
            errorMessage = error.localizedDescription
        }
    }

    private func parseReportGroup(_ raw: String?, entity: Entity) -> FrontendReportGroupSpec? {
        let trimmed = raw?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard !trimmed.isEmpty else { return nil }

        if trimmed.hasPrefix("month("), trimmed.hasSuffix(")") {
            let fieldName = String(trimmed.dropFirst("month(".count).dropLast()).trimmingCharacters(in: .whitespacesAndNewlines)
            guard entity.fields.contains(where: { $0.name == fieldName }) else { return nil }
            return .month(fieldName)
        }

        guard entity.fields.contains(where: { $0.name == trimmed }) else { return nil }
        return .field(trimmed)
    }

    private func reportGroupKey(_ spec: FrontendReportGroupSpec, row: Row) -> (sortKey: String, label: String)? {
        switch spec {
        case .field(let fieldName):
            let label = reportFieldLabel(fieldName: fieldName, row: row)
            return label.map { ($0, $0) }
        case .month(let fieldName):
            guard let value = row[fieldName]?.doubleValue else { return nil }
            let date = Date(timeIntervalSince1970: value / 1000)
            let year = DateCodec.localCalendar.component(.year, from: date)
            let month = DateCodec.localCalendar.component(.month, from: date)
            let sortKey = String(format: "%04d-%02d", year, month)
            return (sortKey, monthLabel(month) + " " + String(year))
        }
    }

    private func reportFieldLabel(fieldName: String, row: Row) -> String? {
        guard let field = entity.fields.first(where: { $0.name == fieldName }),
              let value = row[field.name] else {
            return nil
        }
        let text = RowPresentation.displayString(for: field, value: value)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return text.isEmpty ? nil : text
    }

    private func metricValue(_ metric: FrontendReportMetricInfo, rows: [Row]) -> (String, String)? {
        let aggregate = metric.aggregate.trimmingCharacters(in: .whitespacesAndNewlines)
        let label = (metric.label?.trimmingCharacters(in: .whitespacesAndNewlines)).flatMap { $0.isEmpty ? nil : $0 }
            ?? defaultMetricLabel(aggregate: aggregate, fieldName: metric.field)

        switch aggregate {
        case "count":
            return (label, String(rows.count))
        case "avg":
            guard let value = aggregateField(metric.field, rows: rows, reducer: average) else { return nil }
            return (label, formatMetricNumber(value))
        case "sum":
            guard let value = aggregateField(metric.field, rows: rows, reducer: sum) else { return nil }
            return (label, formatMetricNumber(value))
        case "min":
            guard let value = aggregateField(metric.field, rows: rows, reducer: { $0.min() }) else { return nil }
            return (label, formatMetricNumber(value))
        case "max":
            guard let value = aggregateField(metric.field, rows: rows, reducer: { $0.max() }) else { return nil }
            return (label, formatMetricNumber(value))
        default:
            return nil
        }
    }

    private func aggregateField(_ fieldName: String?, rows: [Row], reducer: ([Double]) -> Double?) -> Double? {
        guard let fieldName,
              entity.fields.contains(where: { $0.name == fieldName }) else {
            return nil
        }
        let values = rows.compactMap { $0[fieldName]?.doubleValue }
        return reducer(values)
    }

    private func average(_ values: [Double]) -> Double? {
        guard !values.isEmpty else { return nil }
        return values.reduce(0, +) / Double(values.count)
    }

    private func sum(_ values: [Double]) -> Double? {
        guard !values.isEmpty else { return nil }
        return values.reduce(0, +)
    }

    private func defaultMetricLabel(aggregate: String, fieldName: String?) -> String {
        if aggregate == "count" {
            return "Count"
        }
        if let fieldName, !fieldName.isEmpty {
            return RowPresentation.humanizeIdentifier(fieldName)
        }
        return RowPresentation.humanizeIdentifier(aggregate)
    }

    private func formatMetricNumber(_ value: Double) -> String {
        let rounded = (value * 100).rounded() / 100
        if rounded.rounded() == rounded {
            return String(Int(rounded))
        }
        return rounded.formatted(.number.precision(.fractionLength(2)))
    }

    private func monthLabel(_ month: Int) -> String {
        switch month {
        case 1: return "Jan"
        case 2: return "Feb"
        case 3: return "Mar"
        case 4: return "Apr"
        case 5: return "May"
        case 6: return "Jun"
        case 7: return "Jul"
        case 8: return "Aug"
        case 9: return "Sep"
        case 10: return "Oct"
        case 11: return "Nov"
        case 12: return "Dec"
        default: return String(month)
        }
    }
}

private struct FrontendRowsView: View {
    let item: FrontendItemInfo
    let entity: Entity
    let sectionTitle: String?
    let screenTitle: String?
    let parentEntity: Entity?
    let parentRow: Row?
    let schema: Schema
    let client: MarAPIClient
    let model: AppViewModel
    let isEditingList: Bool

    @State private var rows: [Row] = []
    @State private var relationLabelsByEntity: [String: [String: String]] = [:]
    @State private var isLoading = false
    @State private var hasStartedLoading = false
    @State private var errorMessage: String?

    var body: some View {
        Group {
            if let message = errorMessage {
                Text(message)
                    .foregroundStyle(.red)
            } else if isLoading && rows.isEmpty {
                HStack {
                    ProgressView()
                    Text("Loading...")
                        .foregroundStyle(.secondary)
                }
            } else if filteredRows.isEmpty {
                Text("No \(emptyRowsLabel) yet")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(Array(filteredRows.enumerated()), id: \.offset) { _, row in
                    if isEditingList {
                        VStack(alignment: .leading, spacing: 10) {
                            FrontendRowSummaryView(item: item, entity: entity, row: row, relationLabelsByEntity: relationLabelsByEntity)
                            HStack(spacing: 10) {
                                NavigationLink("Edit") {
                                    LatestSchemaDestination(model: model, fallbackSchema: schema) { liveSchema in
                                        EntityFormView(
                                            entity: entity,
                                            schema: liveSchema,
                                            client: client,
                                            mode: .edit(row)
                                        ) { _ in } onDeleted: { deletedRow in
                                            rows.removeAll { candidate in
                                                RowPresentation.rowID(entity: entity, row: candidate) ==
                                                    RowPresentation.rowID(entity: entity, row: deletedRow)
                                            }
                                        }
                                    }
                                }
                                Button("Delete", role: .destructive) {
                                    Task { await deleteRow(row) }
                                }
                            }
                            .font(.subheadline)
                        }
                    } else if let destination = destinationScreen {
                        NavigationLink {
                            LatestSchemaDestination(model: model, fallbackSchema: schema) { liveSchema in
                                FrontendScreenView(screen: destination, row: row, parentEntity: parentEntity ?? entity, parentRow: parentRow ?? row, schema: liveSchema, client: client, model: model)
                            }
                        } label: {
                            FrontendRowSummaryView(item: item, entity: entity, row: row, relationLabelsByEntity: relationLabelsByEntity)
                        }
                    } else {
                        NavigationLink {
                            LatestSchemaDestination(model: model, fallbackSchema: schema) { liveSchema in
                                RowDetailView(
                                    entity: entity,
                                    schema: liveSchema,
                                    client: client,
                                    row: row,
                                    onSaved: { _ in },
                                    onDelete: { _ in },
                                    relationLabelsByEntity: relationLabelsByEntity
                                )
                            }
                        } label: {
                            FrontendRowSummaryView(item: item, entity: entity, row: row, relationLabelsByEntity: relationLabelsByEntity)
                        }
                    }
                }
            }
        }
        .onAppear {
            guard !hasStartedLoading else { return }
            hasStartedLoading = true
            Task { await load() }
        }
        .onReceive(NotificationCenter.default.publisher(for: .marRowsChanged)) { notification in
            let changedEntity = notification.object as? String
            guard changedEntity == nil || changedEntity == entity.name else { return }
            Task { await load() }
        }
    }

    private var filteredRows: [Row] {
        rows.filter { row in
            if item.kind == "children" {
                guard let relationField = item.relationField,
                      let parentEntity,
                      let parentRow,
                      let parentID = RowPresentation.rowID(entity: parentEntity, row: parentRow) else {
                    return false
                }
                let childValue = row[relationField]?.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
                if childValue != parentID {
                    return false
                }
            }
            return frontendCondition(item.filter, row: row, model: model)
        }
    }

    private var destinationScreen: FrontendScreenInfo? {
        guard let destinationName = item.destination else { return nil }
        return schema.screens?.screens.first(where: { $0.name == destinationName })
    }

    private var emptyRowsLabel: String {
        let label = nonEmpty(item.label) ?? nonEmpty(sectionTitle) ?? nonEmpty(screenTitle) ?? entity.displayName
        let lowercased = label.lowercased()
        if lowercased.hasPrefix("my ") {
            return String(lowercased.dropFirst(3))
        }
        return lowercased
    }

    private func load() async {
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

    private func deleteRow(_ row: Row) async {
        guard let id = RowPresentation.rowID(entity: entity, row: row) else { return }
        do {
            try await client.deleteRow(entity: entity, id: id)
            rows.removeAll { RowPresentation.rowID(entity: entity, row: $0) == id }
            NotificationCenter.default.post(name: .marRowsChanged, object: entity.name)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func loadRelationLabelsBestEffort() async -> [String: [String: String]] {
        let relationNames = relationNamesNeededForSummary()
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

    private func relationNamesNeededForSummary() -> Set<String> {
        var fieldNames: [String] = []

        if let titleField = item.titleField {
            fieldNames.append(titleField)
        }

        if let subtitleField = item.subtitleField {
            fieldNames.append(subtitleField)
        }

        if fieldNames.isEmpty {
            fieldNames.append(contentsOf: entity.summaryFields.prefix(1).map(\.name))
        }

        return Set(fieldNames.compactMap { fieldName in
            entity.fields.first(where: { $0.name == fieldName })?.relationEntity
        })
    }

    private func nonEmpty(_ value: String?) -> String? {
        let trimmed = value?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return trimmed.isEmpty ? nil : trimmed
    }
}

private struct FrontendRowSummaryView: View {
    let item: FrontendItemInfo
    let entity: Entity
    let row: Row
    let relationLabelsByEntity: [String: [String: String]]

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text(rowText(fieldName: item.titleField) ?? RowPresentation.rowTitle(entity: entity, row: row, relationLabelsByEntity: relationLabelsByEntity))
                .font(.headline)

            if let subtitle = rowText(fieldName: item.subtitleField), !subtitle.isEmpty {
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            } else if item.titleField == nil {
                let summaryRows = Array(RowPresentation.summaryRows(entity: entity, row: row, relationLabelsByEntity: relationLabelsByEntity).prefix(1))
                ForEach(Array(summaryRows.enumerated()), id: \.offset) { _, item in
                    Text("\(item.label): \(item.value)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private func rowText(fieldName: String?) -> String? {
        guard let fieldName,
              let field = entity.fields.first(where: { $0.name == fieldName }),
              let value = row[field.name] else {
            return nil
        }
        let text = RowPresentation.displayString(for: field, value: value, relationLabelsByEntity: relationLabelsByEntity)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return text.isEmpty ? nil : text
    }
}

@MainActor
private func frontendCondition(_ raw: String?, row: Row?, model: AppViewModel) -> Bool {
    let expression = raw?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    guard !expression.isEmpty else { return true }

    switch expression {
    case "user_authenticated":
        return model.authenticatedEmail != nil || model.authenticatedUserID != nil
    case "anonymous":
        return model.authenticatedEmail == nil && model.authenticatedUserID == nil
    default:
        break
    }

    let parts = expression.split(separator: " ", omittingEmptySubsequences: true).map(String.init)
    guard parts.count == 3, let row else { return true }
    let left = parts[0]
    let op = parts[1]
    let right = parts[2]
    let leftValue = row[left]?.stringValue.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
    let rightValue: String
    switch right {
    case "user_id":
        rightValue = model.authenticatedUserID ?? ""
    default:
        rightValue = right.trimmingCharacters(in: CharacterSet(charactersIn: "\""))
    }

    switch op {
    case "==":
        return leftValue == rightValue
    case "!=":
        return leftValue != rightValue
    default:
        return true
    }
}

@MainActor
private func frontendActionValues(_ values: [FrontendActionValueInfo], binding: String?, row: Row?, model: AppViewModel) -> [String: String] {
    Dictionary(uniqueKeysWithValues: values.compactMap { value in
        let resolved = frontendActionExpression(value.expression, binding: binding, row: row, model: model)
        return resolved == nil ? nil : (value.field, resolved!)
    })
}

@MainActor
private func frontendActionExpression(_ raw: String, binding: String?, row: Row?, model: AppViewModel) -> String? {
    let expression = raw.trimmingCharacters(in: .whitespacesAndNewlines)
    if expression == "user_id" {
        return model.authenticatedUserID
    }
    if let fieldName = frontendBoundFieldName(expression, binding: binding) {
        return row?[fieldName]?.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
    }
    if expression.hasPrefix("\""), expression.hasSuffix("\""), expression.count >= 2 {
        return String(expression.dropFirst().dropLast())
    }
    return expression
}

private func frontendBoundFieldName(_ expression: String, binding: String?) -> String? {
    guard let binding, !binding.isEmpty else { return nil }
    let prefix = binding + "."
    guard expression.hasPrefix(prefix) else { return nil }
    return String(expression.dropFirst(prefix.count))
}

private func frontendScreenBindingName(_ screen: FrontendScreenInfo) -> String? {
    guard let entityName = screen.forEntity, let first = entityName.first else { return nil }
    return String(first).lowercased() + entityName.dropFirst()
}

struct EntityRowsView: View {
    let entity: Entity
    let schema: Schema
    let client: MarAPIClient

    @StateObject private var model: EntityRowsViewModel
    @State private var showingCreate = false

    init(entity: Entity, schema: Schema, client: MarAPIClient) {
        self.entity = entity
        self.schema = schema
        self.client = client
        _model = StateObject(wrappedValue: EntityRowsViewModel(entity: entity, schema: schema, client: client))
    }

    var body: some View {
        List {
            if let message = model.errorMessage {
                Section {
                    Text(message)
                        .foregroundStyle(.red)
                }
            }

            if model.rows.isEmpty, !model.isLoading {
                Section {
                    ContentUnavailableView("No \(entity.displayName.lowercased()) yet", systemImage: "tray")
                }
            } else {
                ForEach(model.rows.indices, id: \.self) { index in
                    let row = model.rows[index]
                    NavigationLink {
                        RowDetailView(
                            entity: entity,
                            schema: schema,
                            client: client,
                            row: row,
                            onSaved: { updatedRow in
                                model.insertOrReplace(updatedRow)
                            },
                            onDelete: { deletedRow in
                                if let deletedID = RowPresentation.rowID(entity: entity, row: deletedRow) {
                                    model.rows.removeAll { RowPresentation.rowID(entity: entity, row: $0) == deletedID }
                                }
                            },
                            relationLabelsByEntity: model.relationLabelsByEntity
                        )
                    } label: {
                        EntityRowSummaryView(entity: entity, row: row, relationLabelsByEntity: model.relationLabelsByEntity)
                    }
                }
            }
        }
        .overlay {
            if model.isLoading && model.rows.isEmpty {
                ProgressView()
            }
        }
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                HStack(spacing: 8) {
                    Text(entity.displayName)
                        .font(.headline)

                    if model.isLoading && !model.rows.isEmpty {
                        ProgressView()
                            .controlSize(.small)
                    }
                }
            }

            ToolbarItemGroup(placement: .topBarTrailing) {
                Button {
                    showingCreate = true
                } label: {
                    Image(systemName: "plus")
                }
            }
        }
        .task {
            await model.load()
        }
        .refreshable {
            await model.reload()
        }
        .sheet(isPresented: $showingCreate) {
            NavigationStack {
                EntityFormView(entity: entity, schema: schema, client: client, mode: .create) { row in
                    model.insertOrReplace(row)
                }
            }
        }
    }
}

struct EntityRowSummaryView: View {
    let entity: Entity
    let row: Row
    let relationLabelsByEntity: [String: [String: String]]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(RowPresentation.rowTitle(entity: entity, row: row, relationLabelsByEntity: relationLabelsByEntity))
                .font(.headline)

            let summaryRows = Array(RowPresentation.summaryRows(entity: entity, row: row, relationLabelsByEntity: relationLabelsByEntity).prefix(2))
            ForEach(Array(summaryRows.enumerated()), id: \.offset) { _, item in
                Text("\(item.label): \(item.value)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }
}

struct RowDetailView: View {
    let entity: Entity
    let schema: Schema
    let client: MarAPIClient
    let row: Row
    let onSaved: (Row) -> Void
    let onDelete: (Row) -> Void
    let relationLabelsByEntity: [String: [String: String]]

    @Environment(\.dismiss) private var dismiss
    @State private var showingEdit = false
    @State private var confirmingDelete = false
    @State private var errorMessage: String?
    @State private var isDeleting = false

    var body: some View {
        Form {
            Section {
                ForEach(entity.detailFields) { field in
                    if let value = row[field.name] {
                        let text = RowPresentation.displayString(for: field, value: value, relationLabelsByEntity: relationLabelsByEntity)
                        if !text.isEmpty {
                            LabeledContent(RowPresentation.fieldLabel(field.name), value: text)
                        }
                    }
                }
            }

            if let message = errorMessage {
                Section {
                    Text(message)
                        .foregroundStyle(.red)
                }
            }

            Section {
                Button(role: .destructive) {
                    confirmingDelete = true
                } label: {
                    HStack {
                        if isDeleting {
                            ProgressView()
                        }
                        Text("Delete \(entity.displayName)")
                    }
                    .frame(maxWidth: .infinity, alignment: .center)
                }
                .disabled(isDeleting)
            }
        }
        .navigationTitle(RowPresentation.rowTitle(entity: entity, row: row, relationLabelsByEntity: relationLabelsByEntity))
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button("Edit") {
                    showingEdit = true
                }
            }
        }
        .sheet(isPresented: $showingEdit) {
            NavigationStack {
                EntityFormView(entity: entity, schema: schema, client: client, mode: .edit(row)) { updated in
                    onSaved(updated)
                } onDeleted: { deletedRow in
                    onDelete(deletedRow)
                    dismiss()
                }
            }
        }
        .alert("Delete \(entity.displayName)?", isPresented: $confirmingDelete) {
            Button("Delete", role: .destructive) {
                Task { await deleteRow() }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This cannot be undone.")
        }
    }

    private func deleteRow() async {
        isDeleting = true
        defer { isDeleting = false }

        do {
            guard let id = RowPresentation.rowID(entity: entity, row: row) else { return }
            try await client.deleteRow(entity: entity, id: id)
            onDelete(row)
            dismiss()
        } catch {
            errorMessage = error.localizedDescription
        }
    }
}

struct EntityFormView: View {
    let entity: Entity
    let schema: Schema
    let client: MarAPIClient
    let initialValues: [String: String]
    let formFields: [FrontendFormFieldInfo]
    let onSaved: (Row) -> Void
    let onDeleted: (Row) -> Void

    @Environment(\.dismiss) private var dismiss
    @StateObject private var model: EntityFormViewModel
    @State private var confirmingDelete = false
    @State private var isDeleting = false

    init(
        entity: Entity,
        schema: Schema,
        client: MarAPIClient,
        mode: EntityFormViewModel.Mode,
        initialValues: [String: String] = [:],
        formFields: [FrontendFormFieldInfo] = [],
        onSaved: @escaping (Row) -> Void,
        onDeleted: @escaping (Row) -> Void = { _ in }
    ) {
        self.entity = entity
        self.schema = schema
        self.client = client
        self.initialValues = initialValues
        self.formFields = formFields
        self.onSaved = onSaved
        self.onDeleted = onDeleted
        _model = StateObject(
            wrappedValue: EntityFormViewModel(
                entity: entity,
                schema: schema,
                client: client,
                mode: mode,
                initialValues: initialValues,
                formFields: formFields
            )
        )
    }

    var title: String {
        switch model.mode {
        case .create:
            return "New \(entity.displayName)"
        case .edit:
            return "Edit \(entity.displayName)"
        }
    }

    var body: some View {
        Form {
            if let message = model.errorMessage {
                Section {
                    Text(message)
                        .foregroundStyle(.red)
                }
            }

            Section {
                ForEach(model.visibleFields) { field in
                    if initialValues[field.name] == nil {
                        DynamicFieldView(
                            field: field,
                            value: binding(for: field),
                            relationRows: model.relationRows(for: field),
                            relationEntity: relationEntity(named: field.relationEntity),
                            relationHelperText: model.relationHelperText(for: field)
                        )
                    }
                }
            }

            if case .edit = model.mode {
                Section("Manage") {
                    Button(role: .destructive) {
                        confirmingDelete = true
                    } label: {
                        HStack {
                            if isDeleting {
                                ProgressView()
                            }
                            Text("Delete \(entity.displayName)")
                        }
                        .frame(maxWidth: .infinity, alignment: .center)
                    }
                    .disabled(isDeleting || model.isSaving)
                }
            }
        }
        .navigationTitle(title)
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button("Cancel") { dismiss() }
            }
            ToolbarItem(placement: .confirmationAction) {
                Button {
                    Task { await submit() }
                } label: {
                    if model.isSaving {
                        ProgressView()
                    } else {
                        Text("Save")
                    }
                }
                .disabled(model.isSaving)
            }
        }
        .task {
            await model.loadRelations()
        }
        .alert("Delete \(entity.displayName)?", isPresented: $confirmingDelete) {
            Button("Delete", role: .destructive) {
                Task { await deleteCurrentRow() }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This cannot be undone.")
        }
    }

    private func relationEntity(named name: String?) -> Entity? {
        guard let name else { return nil }
        return schema.entities.first(where: { $0.name == name })
    }

    private func binding(for field: Field) -> Binding<String> {
        Binding(
            get: { model.values[field.name, default: RowPresentation.defaultText(for: field)] },
            set: { newValue in
                model.setValue(newValue, for: field.name)
                Task { await model.loadRelations() }
            }
        )
    }

    private func submit() async {
        do {
            let row = try await model.submit()
            onSaved(row)
            NotificationCenter.default.post(name: .marRowsChanged, object: entity.name)
            dismiss()
        } catch {
            model.errorMessage = error.localizedDescription
        }
    }

    private func deleteCurrentRow() async {
        guard case let .edit(row) = model.mode else { return }
        guard let id = RowPresentation.rowID(entity: entity, row: row) else { return }

        isDeleting = true
        defer { isDeleting = false }

        do {
            try await client.deleteRow(entity: entity, id: id)
            NotificationCenter.default.post(name: .marRowsChanged, object: entity.name)
            onDeleted(row)
            dismiss()
        } catch {
            model.errorMessage = error.localizedDescription
        }
    }
}

struct DynamicFieldView: View {
    let field: Field
    @Binding var value: String
    let relationRows: [Row]
    let relationEntity: Entity?
    let relationHelperText: String?

    @ViewBuilder
    var body: some View {
        if field.relationEntity != nil {
            VStack(alignment: .leading, spacing: 8) {
                relationPicker
                if let relationHelperText {
                    Text(relationHelperText)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
        } else if !field.enumValues.isEmpty {
            enumPicker
        } else {
            switch field.fieldType {
            case .bool:
            boolPicker
            case .date:
            dateNavigationLink(label: RowPresentation.fieldLabel(field.name), includesTime: false)
            case .dateTime:
            dateNavigationLink(label: RowPresentation.fieldLabel(field.name), includesTime: true)
            case .int:
            TextField(RowPresentation.fieldLabel(field.name), text: $value)
                .keyboardType(.numberPad)
            case .float:
            TextField(RowPresentation.fieldLabel(field.name), text: $value)
                .keyboardType(.decimalPad)
            default:
                TextField(RowPresentation.fieldLabel(field.name), text: $value)
                    .textInputAutocapitalization(.sentences)
            }
        }
    }

    private var relationPicker: some View {
        Picker(RowPresentation.fieldLabel(field.name), selection: $value) {
            if field.optional {
                Text("No selection")
                    .tag("")
            }

            ForEach(relationRows.indices, id: \.self) { index in
                let row = relationRows[index]
                if let relationEntity, let id = RowPresentation.rowID(entity: relationEntity, row: row) {
                    Text(RowPresentation.relatedRowLabel(entity: relationEntity, row: row))
                        .tag(id)
                }
            }
        }
        .pickerStyle(.navigationLink)
    }

    private var boolPicker: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(RowPresentation.fieldLabel(field.name))
                .font(.subheadline)
                .foregroundStyle(.secondary)

            Picker("", selection: $value) {
                if field.optional {
                    Text("No selection").tag("")
                }
                Text("No").tag("false")
                Text("Yes").tag("true")
            }
            .labelsHidden()
            .pickerStyle(.segmented)
        }
    }

    private var enumPicker: some View {
        Picker(RowPresentation.fieldLabel(field.name), selection: $value) {
            if field.optional {
                Text("No selection")
                    .tag("")
            }

            ForEach(field.enumValues, id: \.self) { enumValue in
                Text(RowPresentation.humanizeIdentifier(enumValue))
                    .tag(enumValue)
            }
        }
        .pickerStyle(.navigationLink)
    }

    @ViewBuilder
    private func dateNavigationLink(label: String, includesTime: Bool) -> some View {
        NavigationLink {
            DateFieldEditorView(
                title: label,
                includesTime: includesTime,
                isOptional: field.optional,
                value: $value
            )
        } label: {
            LabeledContent(label, value: displayDateText(includesTime: includesTime))
        }
    }

    private func displayDateText(includesTime: Bool) -> String {
        guard !value.isEmpty else { return includesTime ? "Not set" : "Not set" }
        if includesTime, let parsed = DateCodec.parseDateTimeInput(value) {
            return DateCodec.formatDateTimeDisplay(milliseconds: parsed)
        }
        if !includesTime, let parsed = DateCodec.parseDateInput(value) {
            return DateCodec.formatDateDisplay(milliseconds: parsed)
        }
        return value
    }
}

struct DateFieldEditorView: View {
    let title: String
    let includesTime: Bool
    let isOptional: Bool
    @Binding var value: String

    @Environment(\.dismiss) private var dismiss
    @State private var selectedDate: Date

    init(title: String, includesTime: Bool, isOptional: Bool, value: Binding<String>) {
        self.title = title
        self.includesTime = includesTime
        self.isOptional = isOptional
        _value = value

        let parsed: Double? = includesTime
            ? DateCodec.parseDateTimeInput(value.wrappedValue)
            : DateCodec.parseDateInput(value.wrappedValue)
        let initialDate = parsed.map { Date(timeIntervalSince1970: $0 / 1000) } ?? Date()
        _selectedDate = State(initialValue: initialDate)
    }

    var body: some View {
        Form {
            Section {
                DatePicker(
                    "",
                    selection: $selectedDate,
                    displayedComponents: includesTime ? [.date, .hourAndMinute] : [.date]
                )
                .labelsHidden()
                .environment(\.timeZone, includesTime ? DateCodec.localTimeZone : DateCodec.utcTimeZone)
            }

            if isOptional {
                Section {
                    Button("Clear", role: .destructive) {
                        value = ""
                        dismiss()
                    }
                }
            }
        }
        .navigationTitle(title)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Done") {
                    let millis = selectedDate.timeIntervalSince1970 * 1000
                    value = includesTime
                        ? DateCodec.formatDateTimeInput(milliseconds: millis)
                        : DateCodec.formatDateInput(milliseconds: millis)
                    dismiss()
                }
            }
        }
    }
}

struct ActionsHomeView: View {
    let schema: Schema
    let client: MarAPIClient

    var body: some View {
        List(schema.actions) { action in
            NavigationLink(value: action) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(RowPresentation.humanizeIdentifier(action.name))
                        .font(.headline)
                    Text("Input: \(action.inputAlias)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
        .navigationDestination(for: ActionInfo.self) { action in
            ActionFormView(action: action, alias: schema.inputAliases.first(where: { $0.name == action.inputAlias }), schema: schema, client: client)
        }
        .navigationTitle("Actions")
    }
}

struct ActionFormView: View {
    let action: ActionInfo
    let alias: InputAliasInfo?
    let schema: Schema
    let client: MarAPIClient
    let initialValues: [String: String]
    let formFields: [FrontendFormFieldInfo]

    @State private var values: [String: String]
    @State private var relationRows: [String: [Row]] = [:]
    @State private var resultRow: Row?
    @State private var errorMessage: String?
    @State private var isRunning = false

    init(action: ActionInfo, alias: InputAliasInfo?, schema: Schema, client: MarAPIClient, initialValues: [String: String] = [:], formFields: [FrontendFormFieldInfo] = []) {
        self.action = action
        self.alias = alias
        self.schema = schema
        self.client = client
        self.initialValues = initialValues
        self.formFields = formFields
        _values = State(initialValue: initialValues)
    }

    var body: some View {
        Form {
            if let alias {
                Section("Input") {
                    ForEach(visibleFields(alias: alias)) { field in
                        if initialValues[field.name] == nil {
                            actionFieldView(field)
                        }
                    }
                }
            } else {
                Section {
                    Text("Input alias \(action.inputAlias) is unavailable.")
                        .foregroundStyle(.secondary)
                }
            }

            if let message = errorMessage {
                Section {
                    Text(message)
                        .foregroundStyle(.red)
                }
            }

            if let resultRow {
                Section("Response") {
                    ForEach(resultRow.keys.sorted(), id: \.self) { key in
                        LabeledContent(RowPresentation.fieldLabel(key), value: resultRow[key]?.stringValue ?? "")
                    }
                }
            }
        }
        .navigationTitle(RowPresentation.humanizeIdentifier(action.name))
        .task {
            await loadRelations()
        }
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button {
                    Task { await run() }
                } label: {
                    if isRunning {
                        ProgressView()
                    } else {
                        Text("Run")
                    }
                }
                .disabled(isRunning || alias == nil || !canRun)
            }
        }
    }

    private var canRun: Bool {
        guard let alias else { return false }
        return visibleFields(alias: alias).allSatisfy { field in
            guard let relationEntityName = field.relationEntity else { return true }
            guard let filters = relationFilters(for: field) else { return false }
            return relationRows[relationRowsCacheKey(entityName: relationEntityName, filters: filters)] != nil
        }
    }

    private func binding(for fieldName: String) -> Binding<String> {
        Binding(
            get: { values[fieldName, default: ""] },
            set: { newValue in
                updateValue(newValue, for: fieldName)
                Task { await loadRelations() }
            }
        )
    }

    @ViewBuilder
    private func actionFieldView(_ field: InputAliasField) -> some View {
        if let relationEntityName = field.relationEntity {
            if let relationEntity = relationEntity(named: relationEntityName),
               let filters = relationFilters(for: field),
               let rows = relationRows[relationRowsCacheKey(entityName: relationEntityName, filters: filters)] {
                Picker(RowPresentation.fieldLabel(field.name), selection: binding(for: field.name)) {
                    Text("Select \(relationEntity.displayName)")
                        .tag("")

                    ForEach(rows.indices, id: \.self) { index in
                        let row = rows[index]
                        if let id = RowPresentation.rowID(entity: relationEntity, row: row) {
                            Text(RowPresentation.relatedRowLabel(entity: relationEntity, row: row))
                                .tag(id)
                        }
                    }
                }
                .pickerStyle(.navigationLink)
            } else {
                actionRelationState(field)
            }
        } else if !field.enumValues.isEmpty {
            Picker(RowPresentation.fieldLabel(field.name), selection: binding(for: field.name)) {
                Text("Select \(RowPresentation.fieldLabel(field.name))")
                    .tag("")

                ForEach(field.enumValues, id: \.self) { enumValue in
                    Text(RowPresentation.humanizeIdentifier(enumValue))
                        .tag(enumValue)
                }
            }
            .pickerStyle(.navigationLink)
        } else if field.fieldType == "Bool" {
            VStack(alignment: .leading, spacing: 10) {
                Text(RowPresentation.fieldLabel(field.name))
                    .font(.subheadline)
                    .foregroundStyle(.secondary)

                Picker("", selection: binding(for: field.name)) {
                    Text("No").tag("false")
                    Text("Yes").tag("true")
                }
                .labelsHidden()
                .pickerStyle(.segmented)
            }
        } else if field.fieldType == "Date" {
            actionDateNavigationLink(field: field, includesTime: false)
        } else if field.fieldType == "DateTime" {
            actionDateNavigationLink(field: field, includesTime: true)
        } else {
            TextField(RowPresentation.fieldLabel(field.name), text: binding(for: field.name))
                .keyboardType(field.fieldType == "Int" ? .numberPad : (field.fieldType == "Float" ? .decimalPad : .default))
        }
    }

    @ViewBuilder
    private func actionDateNavigationLink(field: InputAliasField, includesTime: Bool) -> some View {
        let label = RowPresentation.fieldLabel(field.name)
        NavigationLink {
            DateFieldEditorView(
                title: label,
                includesTime: includesTime,
                isOptional: false,
                value: binding(for: field.name)
            )
        } label: {
            LabeledContent(label, value: actionDateDisplayText(fieldName: field.name, includesTime: includesTime))
        }
    }

    private func actionDateDisplayText(fieldName: String, includesTime: Bool) -> String {
        let raw = values[fieldName, default: ""].trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty else { return "Not set" }
        if includesTime, let parsed = DateCodec.parseDateTimeInput(raw) {
            return DateCodec.formatDateTimeDisplay(milliseconds: parsed)
        }
        if !includesTime, let parsed = DateCodec.parseDateInput(raw) {
            return DateCodec.formatDateDisplay(milliseconds: parsed)
        }
        return raw
    }

    private func relationEntity(named name: String) -> Entity? {
        schema.entities.first(where: { $0.name == name })
    }

    private func loadRelations() async {
        guard let alias else { return }

        for field in visibleFields(alias: alias) {
            guard let relationName = field.relationEntity else { continue }
            guard let filters = relationFilters(for: field) else { continue }
            let cacheKey = relationRowsCacheKey(entityName: relationName, filters: filters)
            guard relationRows[cacheKey] == nil, let relationEntity = relationEntity(named: relationName) else { continue }
            do {
                relationRows[cacheKey] = try await client.listRows(entity: relationEntity, filters: filters)
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    private func run() async {
        guard let alias else { return }
        isRunning = true
        errorMessage = nil
        defer { isRunning = false }

        do {
            let payload = try buildActionPayload(fields: alias.fields, valuesByName: values)
            resultRow = try await client.runAction(action: action, payload: payload)
            NotificationCenter.default.post(name: .marRowsChanged, object: nil)
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func visibleFields(alias: InputAliasInfo) -> [InputAliasField] {
        let baseFields = alias.fields.filter { initialValues[$0.name] == nil }
        guard !formFields.isEmpty else { return baseFields }
        return formFields.compactMap { formField in
            baseFields.first(where: { $0.name == formField.field })
        }
    }

    private func formFieldConfig(for fieldName: String) -> FrontendFormFieldInfo? {
        formFields.first(where: { $0.field == fieldName })
    }

    private func relationFilters(for field: InputAliasField) -> [String: String]? {
        guard let config = formFieldConfig(for: field.name),
              let (relationField, parentField) = frontendDependentFilter(config)
        else {
            return [:]
        }
        let parentValue = values[parentField, default: ""].trimmingCharacters(in: .whitespacesAndNewlines)
        guard !parentValue.isEmpty else { return nil }
        return [relationField: parentValue]
    }

    private func updateValue(_ value: String, for fieldName: String) {
        let dependentFields = formFields.compactMap { formField -> String? in
            guard let (_, parentField) = frontendDependentFilter(formField), parentField == fieldName else {
                return nil
            }
            return formField.field
        }
        for dependentField in dependentFields {
            values.removeValue(forKey: dependentField)
        }
        values[fieldName] = value
    }

    @ViewBuilder
    private func actionRelationState(_ field: InputAliasField) -> some View {
        if let config = formFieldConfig(for: field.name),
           let (_, parentField) = frontendDependentFilter(config),
           values[parentField, default: ""].trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            LabeledContent(RowPresentation.fieldLabel(field.name)) {
                Text("Select \(RowPresentation.fieldLabel(parentField)) first.")
                    .foregroundStyle(.secondary)
            }
        } else {
            LabeledContent(RowPresentation.fieldLabel(field.name)) {
                Text("Loading options...")
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func buildActionPayload(fields: [InputAliasField], valuesByName: [String: String]) throws -> [String: JSONValue] {
        var payload: [String: JSONValue] = [:]
        for field in fields {
            let raw = valuesByName[field.name, default: ""].trimmingCharacters(in: .whitespacesAndNewlines)
            guard !raw.isEmpty else {
                throw APIErrorResponse(errorCode: "missing_field", message: "\(field.name) is required", details: nil)
            }
            switch field.fieldType {
            case "Int":
                guard let value = Int(raw) else { throw APIErrorResponse(errorCode: "invalid_field", message: "\(field.name) expects Int", details: nil) }
                payload[field.name] = .number(Double(value))
            case "Float":
                guard let value = Double(raw) else { throw APIErrorResponse(errorCode: "invalid_field", message: "\(field.name) expects Float", details: nil) }
                payload[field.name] = .number(value)
            case "Bool":
                switch raw.lowercased() {
                case "true", "1", "yes":
                    payload[field.name] = .bool(true)
                case "false", "0", "no":
                    payload[field.name] = .bool(false)
                default:
                    throw APIErrorResponse(errorCode: "invalid_field", message: "\(field.name) expects Bool", details: nil)
                }
            case "Date":
                guard let value = DateCodec.parseDateInput(raw) else {
                    throw APIErrorResponse(errorCode: "invalid_field", message: "\(field.name) expects a date or Unix milliseconds", details: nil)
                }
                payload[field.name] = .number(value)
            case "DateTime":
                guard let value = DateCodec.parseDateTimeInput(raw) else {
                    throw APIErrorResponse(errorCode: "invalid_field", message: "\(field.name) expects a date/time or Unix milliseconds", details: nil)
                }
                payload[field.name] = .number(value)
            default:
                payload[field.name] = .string(raw)
            }
        }
        return payload
    }
}

struct ProfileView: View {
    @ObservedObject var model: AppViewModel

    var body: some View {
        Form {
            Section("Account") {
                if let email = model.authenticatedEmail {
                    LabeledContent("Email", value: email)
                }
                if let role = model.authenticatedRole {
                    LabeledContent("Role", value: role)
                }
                if let app = model.publicVersion?.app {
                    LabeledContent("Version", value: AdminFormatting.appBuildLabel(app))
                }
            }

            Section {
                Button("Logout", role: .destructive) {
                    Task { await model.logout() }
                }
            }

            if let message = model.errorMessage {
                Section {
                    Text(message)
                        .foregroundStyle(.red)
                }
            }
        }
        .navigationTitle("Profile")
        .refreshable {
            await model.refreshSchema()
        }
    }
}

struct AdminHomeView: View {
    @StateObject private var model: AdminViewModel

    init(client: MarAPIClient) {
        _model = StateObject(wrappedValue: AdminViewModel(client: client))
    }

    var body: some View {
        List {
            NavigationLink {
                AdminRuntimeView(model: model)
            } label: {
                Label("Runtime", systemImage: "gauge")
            }

            NavigationLink {
                AdminRequestLogsView(model: model)
            } label: {
                Label("Request Logs", systemImage: "list.bullet.rectangle")
            }

            NavigationLink {
                AdminBackupsView(model: model)
            } label: {
                Label("Backups", systemImage: "externaldrive")
            }
        }
        .navigationTitle("Admin")
        .sheet(item: $model.downloadedBackup) { item in
            ShareSheet(items: [item.url])
        }
    }
}

private struct AdminRuntimeView: View {
    @ObservedObject var model: AdminViewModel

    var body: some View {
        List {
            if let message = model.runtimeErrorMessage {
                Section {
                    Text(message)
                        .foregroundStyle(.red)
                }
            }

            if let version = model.version {
                Section("Identity") {
                    LabeledContent("App", value: version.app.name)
                    LabeledContent("App build", value: AdminFormatting.appBuildLabel(version.app))
                    LabeledContent("Mar version", value: AdminFormatting.marVersionLabel(version.mar))
                    LabeledContent("Go version", value: version.runtime.goVersion)
                    LabeledContent("Platform", value: version.runtime.platform)
                }
            }

            if let perf = model.perf {
                Section("Runtime") {
                    LabeledContent("Uptime", value: AdminFormatting.formatSeconds(perf.uptimeSeconds))
                    LabeledContent("Memory", value: AdminFormatting.formatBytes(perf.memoryBytes))
                    LabeledContent("SQLite file", value: AdminFormatting.formatBytes(perf.sqliteBytes))
                    LabeledContent("Goroutines", value: "\(perf.goroutines)")
                }

                Section("Traffic") {
                    LabeledContent("Requests", value: "\(perf.http.totalRequests)")
                    LabeledContent("2xx responses", value: "\(perf.http.success2xx)")
                    LabeledContent("4xx errors", value: "\(perf.http.errors4xx)")
                    LabeledContent("5xx errors", value: "\(perf.http.errors5xx)")
                }

                Section("Route Metrics") {
                    ForEach(perf.http.routes) { route in
                        VStack(alignment: .leading, spacing: 4) {
                            Text("\(route.method) \(route.route)")
                                .font(.headline)
                            Text("Count \(route.count) • Avg \(AdminFormatting.formatMilliseconds(route.avgMs))")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            if !route.countsByCode.isEmpty {
                                Text(route.countsByCode.map { "\($0.code): \($0.count)" }.joined(separator: ", "))
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
        }
        .overlay {
            if model.isLoadingRuntime {
                ProgressView()
            }
        }
        .navigationTitle("Runtime")
        .task {
            await model.loadRuntime()
        }
        .refreshable {
            await model.loadRuntime()
        }
    }
}

private struct AdminRequestLogsView: View {
    @ObservedObject var model: AdminViewModel

    var body: some View {
        List {
            if let message = model.requestLogsErrorMessage {
                Section {
                    Text(message)
                        .foregroundStyle(.red)
                }
            }

            if let requestLogs = model.requestLogs {
                Section("Request Logs") {
                    LabeledContent("Buffer", value: "\(requestLogs.buffer)")
                    LabeledContent("Captured", value: "\(requestLogs.totalCaptured)")
                }

                if !requestLogs.logs.isEmpty {
                    Section("Recent Requests") {
                        ForEach(requestLogs.logs) { log in
                            VStack(alignment: .leading, spacing: 6) {
                                Text(AdminFormatting.requestLogTitle(log))
                                    .font(.headline)

                                Text(AdminFormatting.requestLogMeta(log))
                                    .font(.caption)
                                    .foregroundStyle(.secondary)

                                if let userLine = AdminFormatting.requestLogUserLine(log) {
                                    Text(userLine)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }

                                if log.queryCount > 0 {
                                    Text("Queries \(log.queryCount) • SQL \(AdminFormatting.formatMilliseconds(log.queryTimeMs))")
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                }

                                if let errorMessage = log.errorMessage?.trimmingCharacters(in: .whitespacesAndNewlines), !errorMessage.isEmpty {
                                    Text(errorMessage)
                                        .font(.caption)
                                        .foregroundStyle(.red)
                                }
                            }
                            .padding(.vertical, 2)
                        }
                    }
                }
            }
        }
        .overlay {
            if model.isLoadingRequestLogs {
                ProgressView()
            }
        }
        .navigationTitle("Request Logs")
        .task {
            await model.loadRequestLogs()
        }
        .refreshable {
            await model.loadRequestLogs()
        }
    }
}

private struct AdminBackupsView: View {
    @ObservedObject var model: AdminViewModel

    var body: some View {
        List {
            if let message = model.backupsErrorMessage {
                Section {
                    Text(message)
                        .foregroundStyle(.red)
                }
            }

            if let message = model.backupsInfoMessage {
                Section {
                    Text(message)
                        .foregroundStyle(.secondary)
                }
            }

            Section {
                Button {
                    Task { await model.createBackup() }
                } label: {
                    if model.isCreatingBackup {
                        ProgressView()
                    } else {
                        Text("Create backup")
                    }
                }
            }

            if let backups = model.backups {
                Section("Available Backups") {
                    ForEach(backups.backups) { backup in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(backup.createdAt)
                                .font(.headline)
                            Text(backup.name)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            HStack {
                                Text(AdminFormatting.formatBytes(backup.sizeBytes))
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)

                                Spacer()

                                Button {
                                    Task { await model.downloadBackup(backup) }
                                } label: {
                                    if model.downloadingBackupName == backup.name {
                                        ProgressView()
                                    } else {
                                        Label("Download", systemImage: "arrow.down.circle")
                                    }
                                }
                                .buttonStyle(.borderless)
                            }
                        }
                    }
                }
            }
        }
        .overlay {
            if model.isLoadingBackups {
                ProgressView()
            }
        }
        .navigationTitle("Backups")
        .task {
            await model.loadBackups()
        }
        .refreshable {
            await model.loadBackups()
        }
    }
}

private enum AdminFormatting {
    static func appBuildLabel(_ app: VersionApp) -> String {
        let buildTime = DateCodec.parseBuildTime(app.buildTime)
        if let shortHash = RowPresentation.appShortHash(app.manifestHash) {
            return "\(buildTime) (\(shortHash))"
        }
        return buildTime
    }

    static func marVersionLabel(_ mar: VersionMar) -> String {
        let commit = mar.commit.trimmingCharacters(in: .whitespacesAndNewlines)
        if commit.isEmpty {
            return mar.version
        }
        return "\(mar.version) (\(commit))"
    }

    static func formatBytes(_ value: Double) -> String {
        ByteCountFormatter.string(fromByteCount: Int64(value), countStyle: .file)
    }

    static func formatSeconds(_ value: Double) -> String {
        if value < 60 {
            return "\(Int(value.rounded())) s"
        }
        let formatter = DateComponentsFormatter()
        formatter.allowedUnits = [.hour, .minute, .second]
        formatter.unitsStyle = .abbreviated
        return formatter.string(from: value) ?? "\(Int(value.rounded())) s"
    }

    static func formatMilliseconds(_ value: Double) -> String {
        if value.rounded() == value {
            return "\(Int(value)) ms"
        }
        return "\(String(format: "%.1f", value)) ms"
    }

    static func requestLogTitle(_ log: RequestLogRecord) -> String {
        let route = log.route.trimmingCharacters(in: .whitespacesAndNewlines)
        if !route.isEmpty {
            return "\(log.method) \(route)"
        }
        return "\(log.method) \(log.path)"
    }

    static func requestLogMeta(_ log: RequestLogRecord) -> String {
        "\(log.status) • \(formatMilliseconds(log.durationMs)) • \(log.timestamp)"
    }

    static func requestLogUserLine(_ log: RequestLogRecord) -> String? {
        let email = log.userEmail?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        let role = log.userRole?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""

        switch (email.isEmpty, role.isEmpty) {
        case (false, false):
            return "\(email) • \(role)"
        case (false, true):
            return email
        case (true, false):
            return role
        case (true, true):
            return nil
        }
    }
}

struct ShareSheet: UIViewControllerRepresentable {
    let items: [Any]

    func makeUIViewController(context: Context) -> UIActivityViewController {
        UIActivityViewController(activityItems: items, applicationActivities: nil)
    }

    func updateUIViewController(_ uiViewController: UIActivityViewController, context: Context) {}
}
