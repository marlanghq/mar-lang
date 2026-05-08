// Runtime context shared between the interpreter and async effects.
//
// Mirrors the JS `currentDispatch` global pattern: each page mount
// updates the closure here so HTTP responses (Service.call, Http.get)
// can post a Msg back into the running update loop. baseURL lets
// builtins build absolute URLs from the relative paths the user
// writes (`/services/Foo.bar`).
//
// Singleton because an app has at most one MVU page running at any
// time. Updated synchronously on the main actor.

import Foundation

@MainActor
final class MarDispatcher {
    static let shared = MarDispatcher()

    /// The Msg sink for the currently-mounted page. Set on mount,
    /// cleared on unmount. Async effects check this before firing —
    /// a stale closure would dispatch into a torn-down page.
    var current: ((MarValue) -> Void)?

    /// Backend URL for service / Http calls. Updated whenever
    /// AppViewModel resolves a new baseURL (Bonjour pick, manual
    /// override, baked default).
    var baseURL: URL = URL(string: "http://localhost:3000")!

    private init() {}

    /// Convenience: fire `msg` into the live page if there is one.
    /// Async effects call this from URLSession completion handlers.
    func dispatch(_ msg: MarValue) {
        current?(msg)
    }

    /// Resolve a relative path (e.g. "/services/Foo.bar") against
    /// the current baseURL. Returns nil for malformed inputs.
    func resolve(path: String) -> URL? {
        URL(string: path, relativeTo: baseURL)
    }
}
