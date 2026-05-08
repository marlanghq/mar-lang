// Settings tab. In DEBUG (Xcode debug-build), two ways to point at a
// backend:
//
//   1. Discovered (auto): Bonjour found one or more `_mar._tcp`
//      services on the LAN. Tap one to switch.
//   2. Manual (override): paste a URL. Persisted in UserDefaults so
//      the app keeps using it across launches and stops auto-picking
//      from discovery.
//
// In RELEASE (TestFlight / App Store), Bonjour is compiled out — the
// "Discovered" section disappears, only manual override remains. The
// baked URL from mar.json's ios.serverUrl is the default.

import SwiftUI

struct SettingsTab: View {
    @Environment(AppViewModel.self) private var viewModel
    @State private var draft: String = ""
    @State private var saving: Bool = false

    var body: some View {
        NavigationStack {
            Form {
                #if DEBUG
                Section {
                    if viewModel.discovery.servers.isEmpty {
                        HStack {
                            ProgressView().scaleEffect(0.8)
                            Text(viewModel.discovery.isBrowsing
                                 ? "Browsing local network…"
                                 : "Browser not running")
                                .foregroundStyle(.secondary)
                        }
                    } else {
                        ForEach(viewModel.discovery.servers) { server in
                            Button {
                                Task { await viewModel.selectDiscovered(server) }
                            } label: {
                                HStack {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(server.name)
                                            .foregroundStyle(.primary)
                                        if server.host.isEmpty {
                                            Text("Resolving…")
                                                .font(.caption)
                                                .foregroundStyle(.secondary)
                                        } else {
                                            Text("\(server.host):\(server.port)")
                                                .font(.caption.monospaced())
                                                .foregroundStyle(.secondary)
                                        }
                                    }
                                    Spacer()
                                    if let url = server.url, url.absoluteString == viewModel.baseURLString {
                                        Image(systemName: "checkmark")
                                            .foregroundStyle(.tint)
                                    }
                                }
                            }
                            .disabled(server.host.isEmpty)
                        }
                    }
                } header: {
                    Text("Discovered on the network")
                } footer: {
                    Text("mar servers on this network publish themselves via Bonjour. Tap one to connect. (Debug builds only.)")
                }
                #endif

                Section {
                    TextField("Base URL (e.g. https://my-app.fly.dev)", text: $draft)
                        .keyboardType(.URL)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled(true)
                    Button {
                        save()
                    } label: {
                        if saving {
                            ProgressView()
                        } else {
                            Text("Use this URL")
                        }
                    }
                    .disabled(saving || draft.isEmpty)

                    if viewModel.hasManualBaseURL {
                        Button("Reset to default", role: .destructive) {
                            saving = true
                            Task {
                                await viewModel.updateBaseURL("")
                                draft = viewModel.baseURLString
                                saving = false
                            }
                        }
                    }
                } header: {
                    Text("Backend URL")
                } footer: {
                    Text("Currently using: \(viewModel.baseURLString)")
                }

                Section {
                    Button("Reload program") {
                        Task { await viewModel.loadAll() }
                    }
                }
            }
            .navigationTitle("Settings")
            .onAppear {
                if draft.isEmpty {
                    draft = viewModel.baseURLString
                }
            }
        }
    }

    private func save() {
        saving = true
        Task {
            await viewModel.updateBaseURL(draft)
            saving = false
        }
    }
}
