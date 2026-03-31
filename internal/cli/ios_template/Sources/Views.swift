import SwiftUI
import UIKit

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
            .navigationTitle("Mar Runtime")
        }
    }
}

struct LoginView: View {
    @ObservedObject var model: AppViewModel

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    VStack(alignment: .leading, spacing: 8) {
                        if let schema = model.schema {
                            Text(schema.appName)
                                .font(.title.bold())
                        }

                        Text(model.loginStep == .email ? "Sign in with your email." : "Enter the code sent to your email.")
                            .foregroundStyle(.secondary)
                    }
                    .padding(.vertical, 4)
                }

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

                            if let details = model.errorDetails, !details.isEmpty {
                                Text(details)
                                    .font(.footnote)
                                    .foregroundStyle(.secondary)
                                    .textSelection(.enabled)
                            }
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
            .navigationTitle("Sign In")
            .navigationBarTitleDisplayMode(.inline)
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
}

struct ErrorSections: View {
    let message: String?
    let details: String?

    var body: some View {
        if let message {
            Section {
                VStack(alignment: .leading, spacing: 8) {
                    Text(message)
                        .foregroundStyle(.red)

                    if let details, !details.isEmpty {
                        Text(details)
                            .font(.footnote.monospaced())
                            .foregroundStyle(.secondary)
                            .textSelection(.enabled)
                    }
                }
                .padding(.vertical, 2)
            }
        }
    }
}

struct ShellView: View {
    @ObservedObject var model: AppViewModel
    let schema: Schema
    let client: MarAPIClient

    var body: some View {
        TabView(selection: $model.selectedTab) {
            ForEach(primaryTabSpecs) { spec in
                Tab(spec.title, systemImage: spec.systemImage, value: spec.tab) {
                    NavigationStack {
                        ShellTabDestinationView(spec: spec, schema: schema, client: client, model: model)
                    }
                }
            }

            if !overflowTabSpecs.isEmpty {
                Tab("More", systemImage: "ellipsis.circle", value: AppViewModel.Tab.more) {
                    NavigationStack {
                        MoreMenuView(model: model, schema: schema, client: client, specs: overflowTabSpecs)
                    }
                }
            }
        }
        .tabViewStyle(.sidebarAdaptable)
        .tint(.blue)
    }

    private var primaryTabSpecs: [ShellTabSpec] {
        let specs = orderedTabSpecs
        if specs.count > 4 {
            return Array(specs.prefix(4))
        }
        return specs
    }

    private var overflowTabSpecs: [ShellTabSpec] {
        let specs = orderedTabSpecs
        guard specs.count > 4 else { return [] }
        return Array(specs.dropFirst(4))
    }

    private var orderedTabSpecs: [ShellTabSpec] {
        var specs = businessEntitySpecs
        if !schema.actions.isEmpty {
            specs.append(ShellTabSpec(tab: .actions, title: "Actions", systemImage: "bolt"))
        }
        if model.isAdmin {
            specs.append(ShellTabSpec(tab: .admin, title: "Admin", systemImage: "gauge"))
        }
        if model.authEnabled {
            specs.append(ShellTabSpec(tab: .profile, title: "Profile", systemImage: "person.crop.circle"))
        }
        specs.append(contentsOf: userEntitySpecs)
        return specs
    }

    private var businessEntitySpecs: [ShellTabSpec] {
        schema.entities
            .filter { $0.name.caseInsensitiveCompare("User") != .orderedSame }
            .map { entity in
                ShellTabSpec(
                    tab: .entity(entity.name),
                    title: entity.displayName,
                    systemImage: symbolName(for: entity),
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
                    systemImage: symbolName(for: entity),
                    entity: entity
                )
            }
    }

    private func symbolName(for entity: Entity) -> String {
        switch entity.name.lowercased() {
        case "student":
            return "person"
        case "class", "course":
            return "book.closed"
        case "enrollment":
            return "link"
        case "user":
            return "person.2"
        default:
            return "square.text.square"
        }
    }
}

private struct ShellTabSpec: Identifiable {
    let tab: AppViewModel.Tab
    let title: String
    let systemImage: String
    let entity: Entity?

    init(tab: AppViewModel.Tab, title: String, systemImage: String, entity: Entity? = nil) {
        self.tab = tab
        self.title = title
        self.systemImage = systemImage
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
        case .more:
            return "more"
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
        case .more:
            EmptyView()
        }
    }
}

private struct MoreMenuView: View {
    let model: AppViewModel
    let schema: Schema
    let client: MarAPIClient
    let specs: [ShellTabSpec]

    var body: some View {
        List(specs) { spec in
            NavigationLink {
                ShellTabDestinationView(spec: spec, schema: schema, client: client, model: model)
            } label: {
                Label(spec.title, systemImage: spec.systemImage)
            }
        }
        .navigationTitle("More")
    }
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
            if model.isLoading {
                ProgressView()
            }
        }
        .navigationTitle(entity.displayName)
        .toolbar {
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
    let onSaved: (Row) -> Void

    @Environment(\.dismiss) private var dismiss
    @StateObject private var model: EntityFormViewModel

    init(entity: Entity, schema: Schema, client: MarAPIClient, mode: EntityFormViewModel.Mode, onSaved: @escaping (Row) -> Void) {
        self.entity = entity
        self.schema = schema
        self.client = client
        self.onSaved = onSaved
        _model = StateObject(wrappedValue: EntityFormViewModel(entity: entity, schema: schema, client: client, mode: mode))
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
                ForEach(entity.visibleFields) { field in
                    DynamicFieldView(field: field, value: binding(for: field), relationRows: model.relationRows[field.relationEntity ?? ""] ?? [], relationEntity: relationEntity(named: field.relationEntity))
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
    }

    private func relationEntity(named name: String?) -> Entity? {
        guard let name else { return nil }
        return schema.entities.first(where: { $0.name == name })
    }

    private func binding(for field: Field) -> Binding<String> {
        Binding(
            get: { model.values[field.name, default: RowPresentation.defaultText(for: field)] },
            set: { model.values[field.name] = $0 }
        )
    }

    private func submit() async {
        do {
            let row = try await model.submit()
            onSaved(row)
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

    var body: some View {
        switch (field.relationEntity, field.fieldType) {
        case (.some, _):
            relationPicker
        case (_, .bool):
            boolPicker
        case (_, .date):
            dateDisclosure(label: RowPresentation.fieldLabel(field.name), includesTime: false)
        case (_, .dateTime):
            dateDisclosure(label: "\(RowPresentation.fieldLabel(field.name)) (UTC)", includesTime: true)
        case (_, .int):
            TextField(RowPresentation.fieldLabel(field.name), text: $value)
                .keyboardType(.numberPad)
        case (_, .float):
            TextField(RowPresentation.fieldLabel(field.name), text: $value)
                .keyboardType(.decimalPad)
        default:
            TextField(RowPresentation.fieldLabel(field.name), text: $value)
                .textInputAutocapitalization(.sentences)
        }
    }

    private var relationPicker: some View {
        Picker(RowPresentation.fieldLabel(field.name), selection: $value) {
            Text(field.optional ? "No selection" : "Select \(RowPresentation.humanizeIdentifier(field.relationEntity ?? ""))")
                .tag("")

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
        Picker(RowPresentation.fieldLabel(field.name), selection: $value) {
            if field.optional {
                Text("No selection").tag("")
            }
            Text("False").tag("false")
            Text("True").tag("true")
        }
        .pickerStyle(.segmented)
    }

    @ViewBuilder
    private func dateDisclosure(label: String, includesTime: Bool) -> some View {
        let currentDate = bindingDate(includesTime: includesTime)
        DisclosureGroup {
            if let currentDate {
                DatePicker(
                    "",
                    selection: currentDate,
                    displayedComponents: includesTime ? [.date, .hourAndMinute] : [.date]
                )
                .labelsHidden()
                .environment(\.timeZone, MarDateCodec.utcTimeZone)

                HStack {
                    if field.optional {
                        Button("Clear", role: .destructive) {
                            value = ""
                        }
                    }
                    Spacer()
                }
            } else {
                Button(includesTime ? "Select date and time" : "Select date") {
                    let now = Date()
                    value = includesTime
                        ? MarDateCodec.formatDateTimeInput(milliseconds: now.timeIntervalSince1970 * 1000)
                        : MarDateCodec.formatDateInput(milliseconds: now.timeIntervalSince1970 * 1000)
                }
            }
        } label: {
            LabeledContent(label, value: displayDateText(includesTime: includesTime))
        }
    }

    private func bindingDate(includesTime: Bool) -> Binding<Date>? {
        let parsed: Double? = includesTime
            ? MarDateCodec.parseDateTimeInput(value)
            : MarDateCodec.parseDateInput(value)

        guard let parsed else { return nil }

        return Binding<Date>(
            get: { Date(timeIntervalSince1970: parsed / 1000) },
            set: { newDate in
                let millis = newDate.timeIntervalSince1970 * 1000
                value = includesTime
                    ? MarDateCodec.formatDateTimeInput(milliseconds: millis)
                    : MarDateCodec.formatDateInput(milliseconds: millis)
            }
        )
    }

    private func displayDateText(includesTime: Bool) -> String {
        guard !value.isEmpty else { return includesTime ? "Not set" : "Not set" }
        if includesTime, let parsed = MarDateCodec.parseDateTimeInput(value) {
            return MarDateCodec.formatDateTimeDisplay(milliseconds: parsed)
        }
        if !includesTime, let parsed = MarDateCodec.parseDateInput(value) {
            return MarDateCodec.formatDateDisplay(milliseconds: parsed)
        }
        return value
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
            ActionFormView(action: action, alias: schema.inputAliases.first(where: { $0.name == action.inputAlias }), client: client)
        }
        .navigationTitle("Actions")
    }
}

struct ActionFormView: View {
    let action: ActionInfo
    let alias: InputAliasInfo?
    let client: MarAPIClient

    @State private var values: [String: String] = [:]
    @State private var resultRow: Row?
    @State private var errorMessage: String?
    @State private var isRunning = false

    var body: some View {
        Form {
            if let alias {
                Section("Input") {
                    ForEach(alias.fields) { field in
                        TextField(RowPresentation.fieldLabel(field.name), text: binding(for: field.name))
                            .keyboardType(field.fieldType == "Int" ? .numberPad : (field.fieldType == "Float" ? .decimalPad : .default))
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
                .disabled(isRunning || alias == nil)
            }
        }
    }

    private func binding(for fieldName: String) -> Binding<String> {
        Binding(
            get: { values[fieldName, default: ""] },
            set: { values[fieldName] = $0 }
        )
    }

    private func run() async {
        guard let alias else { return }
        isRunning = true
        errorMessage = nil
        defer { isRunning = false }

        do {
            let payload = try buildActionPayload(fields: alias.fields, valuesByName: values)
            resultRow = try await client.runAction(action: action, payload: payload)
        } catch {
            errorMessage = error.localizedDescription
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
                guard let value = MarDateCodec.parseDateInput(raw) else {
                    throw APIErrorResponse(errorCode: "invalid_field", message: "\(field.name) expects a date or Unix milliseconds", details: nil)
                }
                payload[field.name] = .number(value)
            case "DateTime":
                guard let value = MarDateCodec.parseDateTimeInput(raw) else {
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
                if let appName = model.schema?.appName {
                    LabeledContent("App", value: appName)
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
    let client: MarAPIClient
    @StateObject private var model: AdminViewModel

    init(client: MarAPIClient) {
        self.client = client
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
        let buildTime = MarDateCodec.parseBuildTime(app.buildTime)
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
