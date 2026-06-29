// Top-level state for the runtime. Owns:
//
//   - APIClient (program.json fetch + service POSTs)
//   - Discovery (Bonjour browser, DEBUG only)
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
//
// Backend URL resolution:
//   - RELEASE: always the baked Info.plist MarBaseURL (set from
//     mar.json's `ios.serverUrl`). Bonjour is compiled out.
//   - DEBUG: same baked URL by default, but the Bonjour discovery
//     loop overrides it the moment a `_mar._tcp` service appears
//     on the LAN — typically `mar dev` running on the laptop. No
//     UI, no UserDefaults; the override is purely automatic for
//     the lifetime of the process.

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

    /// Currently-active backend URL as a string. Starts at the baked
    /// Info.plist value; in DEBUG, swaps to a discovered Bonjour
    /// endpoint when one appears.
    private(set) var baseURLString: String

    #if DEBUG
    let discovery = Discovery()
    #endif

    @ObservationIgnored
    private let api: APIClient

    /// Bytes of the program.json last successfully loaded. Lets the
    /// background refresh path detect "nothing changed" and skip the
    /// expensive (and state-resetting) re-run entirely. In typical
    /// usage the embedded snapshot and the fetched program are
    /// byte-identical (the user hasn't deployed since the .ipa was
    /// built), so this short-circuits ~every refresh.
    @ObservationIgnored
    private var lastLoadedProgramBytes: Data?

    init() {
        let initial = AppViewModel.bakedBaseURL()
        self.baseURLString = initial
        let url = URL(string: initial) ?? URL(string: "http://localhost:3000")!
        self.api = APIClient(baseURL: url)
        MarDispatcher.shared.baseURL = url

        // Instant cold-start. If `mar build --target ios` embedded a
        // program.json snapshot in the app bundle, decode and execute
        // it synchronously here so the first frame paints immediately.
        // The network fetch in loadAll() then refreshes the in-memory
        // program with whatever the server is serving right now.
        //
        // Failure to decode the embedded snapshot is logged and
        // swallowed: state stays .idle so the regular fetch path
        // takes over and surfaces any real error to the user.
        if let embedded = AppViewModel.loadEmbeddedProgram() {
            do {
                try self.runProgramSync(embedded)
                self.state = .loaded
            } catch {
                #if DEBUG
                print("[mar] embedded program.json decode failed: \(error)")
                #endif
            }
        }

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

    /// Reads the embedded program.json from Bundle.main if present.
    /// Returns nil when scaffolds without Resources/program.json are
    /// running (older builds or corrupt installs) — callers must
    /// gracefully fall through to the network path.
    private static func loadEmbeddedProgram() -> Data? {
        guard let url = Bundle.main.url(forResource: "program", withExtension: "json") else {
            return nil
        }
        return try? Data(contentsOf: url)
    }

    /// The baked Info.plist `MarBaseURL`, with a localhost fallback so
    /// the app still launches if someone shipped without setting one
    /// (the build emits a Warn in that case; this is just defensive).
    private static func bakedBaseURL() -> String {
        if let baked = Bundle.main.object(forInfoDictionaryKey: "MarBaseURL") as? String,
           !baked.isEmpty {
            return baked
        }
        return "http://localhost:3000"
    }

    #if DEBUG
    private func maybeAutoPick() {
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
        // If we already have a program loaded (typically from the
        // embedded snapshot, but also from a previous successful
        // fetch), this fetch is a *refresh* — don't flash the
        // loading screen. Failure stays silent: keep showing what we
        // already have rather than wiping it for an offline user.
        let hadProgram = !pages.isEmpty
        if !hadProgram {
            state = .loading
        }
        do {
            let programData = try await api.fetchProgram()
            try runProgramSync(programData)
            state = .loaded
        } catch {
            if hadProgram {
                #if DEBUG
                print("[mar] background refresh failed; keeping current program: \(error)")
                #endif
                return
            }
            let msg = (error as? APIError)?.errorDescription
                ?? (error as? MarRuntimeError)?.errorDescription
                ?? error.localizedDescription
            state = .failed(msg)
        }
    }

    /// Decode + execute the user's mar program. Side-effect: fills
    /// `pages` with whatever `main` captured via App.frontend /
    /// App.fullstack. Synchronous because the body is all CPU work
    /// (decode + interpreter eval) — the async wrapper is kept on
    /// the public loadAll caller for the network fetch.
    ///
    /// Cheap to call repeatedly: when `data` matches the bytes from
    /// the last successful load, we no-op entirely. This matters
    /// because the cold-start flow loads the embedded snapshot AND
    /// fires a background fetch of /_mar/program.json — the two are
    /// usually identical, and re-running main on identical bytes
    /// would needlessly tear down the user's navigation / auth
    /// state (currentPath, currentUser, etc. in AppContext).
    private func runProgramSync(_ data: Data) throws {
        if let prev = lastLoadedProgramBytes, prev == data {
            return
        }
        let program = try MarJSONCodec.decodeProgram(data)
        // First load (no pages yet) gets a clean slate so we don't
        // inherit stale state from a corrupted previous attempt.
        // Subsequent loads preserve navigation + auth state — losing
        // them on every background refresh would bounce a
        // mid-session user back to the sign-in screen.
        let isInitialLoad = AppContext.shared.pages.isEmpty
        if isInitialLoad {
            AppContext.shared.reset()
        }
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
        // Seed the navigation stack only on the initial load. On a
        // refresh (program changed but user was already navigating),
        // keep wherever they are — but if the user's current top-of-
        // stack no longer exists in the new program, reset to a
        // sensible root so the renderer doesn't blank out on a
        // missing match.
        if isInitialLoad {
            if let first = decoded.first {
                AppContext.shared.seedRoot(first.path)
            }
        } else {
            let current = AppContext.shared.currentPath
            let stillExists = decoded.contains { $0.matchURL(current) != nil }
            if !stillExists, let first = decoded.first {
                AppContext.shared.navigate(path: first.path, replace: true)
            }
        }
        lastLoadedProgramBytes = data
    }
}
