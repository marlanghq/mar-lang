// Captured state from running `main` once. Mirrors what `mar dev`
// and `mar build` do server-side: override App.frontend / fullstack
// so the user's `main` doesn't try to start a real server, just
// records what its arguments were.
//
// Singleton because there's exactly one mar app loaded at a time.
// Reset on each `loadProgram` so a hot-reload (or baseURL switch)
// rebuilds from scratch.
//
// Also holds the running navigation + auth state:
//
//   - currentPath  : which page is active (path-based router)
//   - currentUser  : VCtor('Just', [user]) | VCtor('Nothing', []) | nil
//                    nil = not yet bootstrapped; the LoadedShell
//                    triggers Auth.me when the app first mounts a
//                    Page.protected.
//
// Navigation effects (Nav.push / Nav.replace) call `navigate(path:)`
// which mutates currentPath and triggers a SwiftUI re-render.

import Foundation
import Observation

@MainActor
@Observable
final class AppContext {
    // The actual `static let shared` lives outside the @Observable
    // class body to avoid the macro complaining about non-stored
    // properties on a MainActor-isolated type.
    static let shared = AppContext()

    /// Pages list produced by App.frontend / App.fullstack. Each
    /// element is a `__Page` or `__ProtectedPage` ctor — see
    /// `decodedPages()` for the per-tag positional layout.
    private(set) var pages: [MarValue] = []

    /// Active page path (drives the path-based router in stack mode).
    /// Defaults to whatever the first decoded page's path is.
    var currentPath: String = "/"

    /// Cached Auth.me result, loaded on demand the first time a
    /// Page.protected becomes the active page. nil = not yet fetched.
    /// `Nav.replace` clears this so logout + re-login flows force a
    /// fresh check.
    var currentUser: MarValue?

    /// True while an Auth.me request is in flight. The LoadedShell
    /// uses this to show a spinner instead of flashing the redirect
    /// page during the bootstrap.
    var authPending: Bool = false

    /// Path to redirect unauthenticated users to. Set by the
    /// Auth.config builtin from its `signInPage` field. Empty when
    /// the app doesn't declare any Auth — Page.protected then
    /// surfaces a clear runtime error rather than a silent loop.
    var signInPath: String = ""

    /// Where to send the user after a successful sign-in. Set by
    /// `handleAuthExpired()` when a 401 from a Service.call hijacks
    /// the user to the sign-in screen — `Nav.afterSignIn` consumes it.
    /// Web uses a `?next=` URL parameter for the same purpose; on iOS
    /// we don't have a URL bar, so we keep it in memory. Lost on cold
    /// start (acceptable — there's no "where you were" anymore).
    var pendingReturnPath: String?

    /// Coalesces parallel Service.call 401s. Set true the moment the
    /// first 401 triggers a redirect; subsequent 401s during the same
    /// redirect window are dropped. Reset by `Nav.afterSignIn` after
    /// a successful login completes.
    var redirectingToSignIn: Bool = false

    private init() {}

    func reset() {
        pages = []
        currentPath = "/"
        currentUser = nil
        authPending = false
        signInPath = ""
        pendingReturnPath = nil
        redirectingToSignIn = false
    }

    /// Called by MarHTTP when a Service.call returns 401. Captures the
    /// current path so Nav.afterSignIn can return there, then routes
    /// to the sign-in screen. Idempotent across parallel 401s thanks
    /// to `redirectingToSignIn`. Returns true if it took over (caller
    /// should NOT dispatch the Err); false when there's no signInPath
    /// configured (caller falls through to its default error path).
    func handleAuthExpired() -> Bool {
        guard !signInPath.isEmpty else { return false }
        if redirectingToSignIn { return true }
        redirectingToSignIn = true
        if currentPath != signInPath {
            pendingReturnPath = currentPath
        }
        navigate(path: signInPath, replace: true)
        return true
    }

    func capturePages(_ list: MarValue) {
        guard case .list(let xs) = list else { return }
        pages = xs
    }

    /// Mutate the active path. `replace` is a hint that the caller
    /// expects the previous URL not to be reachable via "back" — on
    /// iOS we don't model browser history, so push/replace differ
    /// only in that replace also invalidates the cached auth state
    /// (logout / post-login flows).
    func navigate(path: String, replace: Bool) {
        if replace {
            currentUser = nil
        }
        currentPath = path
    }

    /// Decoded view of the captured pages, ready for the renderer
    /// to mount. Recognizes all four page ctors:
    ///   - `__Page`                  : public, static path
    ///   - `__ProtectedPage`         : auth-gated, static path
    ///   - `__DynamicPage`           : public, `:param` pattern path
    ///   - `__DynamicProtectedPage`  : auth-gated, `:param` pattern path
    func decodedPages() -> [DecodedPage] {
        pages.compactMap { v in
            guard case .ctor(let tag, let args, _) = v else { return nil }
            switch tag {
            case "__Page":
                guard args.count >= 4 else { return nil }
                return DecodedPage(
                    path: stringOf(args[0]) ?? "/",
                    title: args.count >= 5 ? (stringOf(args[4]) ?? "") : "",
                    initFn: args[1],
                    updateFn: args[2],
                    viewFn: args[3],
                    isProtected: false,
                    isDynamic: false
                )
            case "__ProtectedPage":
                guard args.count >= 5 else { return nil }
                return DecodedPage(
                    path: stringOf(args[0]) ?? "/",
                    title: stringOf(args[4]) ?? "",
                    initFn: args[1],
                    updateFn: args[2],
                    viewFn: args[3],
                    isProtected: true,
                    isDynamic: false
                )
            case "__DynamicPage":
                guard args.count >= 5 else { return nil }
                return DecodedPage(
                    path: stringOf(args[0]) ?? "/",
                    title: stringOf(args[4]) ?? "",
                    initFn: args[1],
                    updateFn: args[2],
                    viewFn: args[3],
                    isProtected: false,
                    isDynamic: true
                )
            case "__DynamicProtectedPage":
                guard args.count >= 5 else { return nil }
                return DecodedPage(
                    path: stringOf(args[0]) ?? "/",
                    title: stringOf(args[4]) ?? "",
                    initFn: args[1],
                    updateFn: args[2],
                    viewFn: args[3],
                    isProtected: true,
                    isDynamic: true
                )
            default:
                return nil
            }
        }
    }

    private func stringOf(_ v: MarValue) -> String? {
        if case .string(let s) = v { return s }
        return nil
    }
}

/// One Page ready for mounting — its ctor args destructured into named
/// fields. The redirect path for protected pages comes from
/// `AppContext.signInPath` at render time (set by the Auth.config
/// builtin), not from the page itself. `isDynamic` indicates the
/// path is a `:param` pattern that the renderer matches at navigation
/// time.
struct DecodedPage: Identifiable {
    let path: String
    let title: String
    let initFn: MarValue
    let updateFn: MarValue
    let viewFn: MarValue
    let isProtected: Bool
    let isDynamic: Bool

    var id: String { path }

    /// Display title for the tab bar / navigation bar. Falls back to
    /// the path when the user didn't pass `title` to Page.create.
    var displayTitle: String {
        title.isEmpty ? path : title
    }

    /// Match a candidate URL against this page's path pattern.
    /// Returns the captured params record (typed per `{name:Type}`)
    /// on success, nil on miss. Static pages match by exact equality.
    /// Type-mismatch on a typed segment (e.g. `/notes/abc` against
    /// `{id:Int}`) returns nil so the router can fall through to
    /// the next page.
    func matchURL(_ urlPath: String) -> MarValue? {
        if !isDynamic {
            return urlPath == path ? MarValue.record(fields: [:], order: []) : nil
        }
        let pattern = MarPath.parse(path)
        return MarPath.match(urlPath, pattern: pattern)
    }
}
