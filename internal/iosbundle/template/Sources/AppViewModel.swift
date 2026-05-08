// Top-level state for the runtime. Owns:
//
//   - APIClient (program.json fetch + service POSTs)
//   - Discovery (Bonjour browser)
//   - Program loader (fetches /_mar/program.json, decodes, runs main)
//
// The interesting work happens in `loadAll()`: fetch the JSON, decode
// into Program, build a fresh env via MarBuiltins.makeEnv, load the
// user's module, run `main` (an Effect that captures the page list
// into AppContext), then expose the captured pages so ContentView
// can render them.
//
// MainActor + @Observable so SwiftUI tracks property reads
// automatically. APIClient is an actor so network calls happen off
// the main thread.

import Foundation
import Observation
import SwiftUI

@MainActor
@Observable
final class AppViewModel {
    enum LoadState: Equatable {
        case idle
        case loading
        case loaded
        case failed(String)
    }

    private(set) var state: LoadState = .idle

    /// Pages decoded from the user's `main`. Empty → backend-only app
    /// (the iOS shell shows a placeholder; iOS apps without pages are
    /// uncommon).
    private(set) var pages: [DecodedPage] = []

    var baseURLString: String

    /// Whether the user pinned a baseURL in Settings. When false,
    /// Discovery results auto-pick the first found server.
    /// Always true in RELEASE builds (Bonjour is compiled out, so
    /// the only sources of a baseURL are the baked default or a
    /// manual override — both treated as "manual" since neither is
    /// going to change without explicit operator action).
    private(set) var hasManualBaseURL: Bool

    #if DEBUG
    let discovery = Discovery()
    #endif

    @ObservationIgnored
    private let api: APIClient

    init() {
        let resolved = AppViewModel.resolveInitialBaseURL()
        self.baseURLString = resolved.url
        #if DEBUG
        self.hasManualBaseURL = resolved.fromUser
        #else
        // RELEASE: no Bonjour, so the baked URL or stored override
        // IS the answer. Don't let auto-pick logic kick in even if
        // it somehow runs.
        self.hasManualBaseURL = true
        #endif
        let url = URL(string: resolved.url) ?? URL(string: "http://localhost:3000")!
        self.api = APIClient(baseURL: url)
        MarDispatcher.shared.baseURL = url

        #if DEBUG
        // Bonjour discovery is debug-only. App Store / TestFlight
        // builds never browse the local network — that would be
        // both wasteful (cellular networks have no _mar._tcp peer)
        // and a privacy / spoofing concern (a hostile WiFi could
        // advertise a fake mar backend).
        discovery.onServersChanged = { [weak self] in
            self?.maybeAutoPick()
        }
        discovery.start()
        #endif
    }

    private static func resolveInitialBaseURL() -> (url: String, fromUser: Bool) {
        if let stored = UserDefaults.standard.string(forKey: "MarBaseURL"),
           !stored.isEmpty {
            return (stored, true)
        }
        if let baked = Bundle.main.object(forInfoDictionaryKey: "MarBaseURL") as? String,
           !baked.isEmpty {
            return (baked, false)
        }
        return ("http://localhost:3000", false)
    }

    #if DEBUG
    private func maybeAutoPick() {
        guard !hasManualBaseURL else { return }
        let resolved = discovery.servers.compactMap { $0.url }
        guard let first = resolved.first else { return }
        let s = first.absoluteString
        guard s != baseURLString else { return }
        baseURLString = s
        Task {
            await api.setBaseURL(first)
            MarDispatcher.shared.baseURL = first
            await loadAll()
        }
    }
    #endif

    func loadAll() async {
        state = .loading
        do {
            let programData = try await api.fetchProgram()
            try await runProgram(programData)
            state = .loaded
        } catch {
            let msg = (error as? APIError)?.errorDescription
                ?? (error as? MarRuntimeError)?.errorDescription
                ?? error.localizedDescription
            state = .failed(msg)
        }
    }

    /// Decode + execute the user's mar program. Side-effect: fills
    /// `pages` with whatever `main` captured via App.frontend /
    /// App.fullstack.
    private func runProgram(_ data: Data) async throws {
        let program = try MarJSONCodec.decodeProgram(data)
        AppContext.shared.reset()
        // Auth metadata from the server-resolved Auth.config — the
        // mobile bundle doesn't include Main.mar, so the JS+Swift
        // runtimes can't run `auth = Auth.config { ... }` themselves.
        if !program.authSignInPath.isEmpty {
            AppContext.shared.signInPath = program.authSignInPath
        }
        let env = MarBuiltins.makeEnv()
        for module in program.modules {
            try MarLoader.load(module: module, into: env)
        }

        // Resolve the entry — typically `main`. The Go side stamps
        // entry as "main" or the synthetic "__entry" depending on
        // load path. Try both.
        let entry: MarValue
        if let v = env.lookup(program.entry) {
            entry = v
        } else if let v = env.lookup("main") {
            entry = v
        } else {
            throw MarRuntimeError.message("entry not found: \(program.entry)")
        }

        guard case .effect(let eff) = entry else {
            throw MarRuntimeError.message("entry is not an Effect")
        }
        // Run main — captures pages into AppContext.shared.
        _ = try eff.run()

        let decoded = AppContext.shared.decodedPages()
        self.pages = decoded
        // Seed the active path. If the user landed via deep link or
        // a previous nav, AppContext.currentPath may already be set
        // — only initialize when blank.
        if AppContext.shared.currentPath.isEmpty || AppContext.shared.currentPath == "/" {
            if let first = decoded.first {
                AppContext.shared.currentPath = first.path
            }
        }
    }

    func updateBaseURL(_ raw: String) async {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.isEmpty {
            UserDefaults.standard.removeObject(forKey: "MarBaseURL")
            hasManualBaseURL = false
            let fallback = AppViewModel.resolveInitialBaseURL()
            baseURLString = fallback.url
            if let url = URL(string: fallback.url) {
                await api.setBaseURL(url)
                MarDispatcher.shared.baseURL = url
            }
            await loadAll()
            return
        }
        guard let url = URL(string: trimmed), url.scheme != nil else {
            state = .failed("Invalid URL: \(trimmed)")
            return
        }
        baseURLString = trimmed
        hasManualBaseURL = true
        UserDefaults.standard.set(trimmed, forKey: "MarBaseURL")
        await api.setBaseURL(url)
        MarDispatcher.shared.baseURL = url
        await loadAll()
    }

    #if DEBUG
    func selectDiscovered(_ server: DiscoveredServer) async {
        guard let url = server.url else { return }
        await updateBaseURL(url.absoluteString)
    }
    #endif

}
