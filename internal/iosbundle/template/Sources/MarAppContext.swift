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
//   - navPath      : the navigation stack (paths from bottom to top).
//                    `navPath.last` is what the user is looking at;
//                    earlier entries are the swipe-back history.
//                    Bound directly into SwiftUI's `NavigationStack
//                    (path:)` so the native swipe-back gesture works
//                    out of the box.
//   - currentPath  : convenience computed property — the active
//                    page's path. Equal to `navPath.last` or `"/"`
//                    when the stack is empty (cold start).
//   - currentUser  : VCtor('Just', [user]) | VCtor('Nothing', []) | nil
//                    nil = not yet bootstrapped; the protected-page
//                    gate triggers Auth.me on first mount.
//
// Navigation effects (Nav.push / Nav.replace) call `navigate(path:)`
// which mutates navPath and triggers a SwiftUI re-render.

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

    /// Navigation stack (bottom to top). Bound directly into
    /// `NavigationStack(path:)` so native swipe-back pops the top
    /// entry without any extra wiring. `Nav.push` appends, `Nav.replace`
    /// resets to a single entry, `Auth.completeSignIn` resets to the
    /// pendingReturnPath.
    var navPath: [String] = []

    /// The page the user is currently looking at. Computed from
    /// `navPath` so there's a single source of truth for "where am I"
    /// across the runtime (router matching, Auth.me-bootstrap key,
    /// pendingReturnPath snapshot, etc.). Falls back to "/" on the
    /// brief window between cold-start and the initial `seedRoot`.
    var currentPath: String {
        navPath.last ?? "/"
    }

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
    /// the user to the sign-in screen — `Auth.completeSignIn` consumes it.
    /// Web uses a `?next=` URL parameter for the same purpose; on iOS
    /// we don't have a URL bar, so we keep it in memory. Lost on cold
    /// start (acceptable — there's no "where you were" anymore).
    var pendingReturnPath: String?

    /// Coalesces parallel Service.call 401s. Set true the moment the
    /// first 401 triggers a redirect; subsequent 401s during the same
    /// redirect window are dropped. Reset by `Auth.completeSignIn` after
    /// a successful login completes.
    var redirectingToSignIn: Bool = false

    /// Monotonic counter bumped every time `navigate(replace: true)`
    /// rewrites the stack. The `StackShell` view applies it as
    /// SwiftUI `.id()` on the NavigationStack so a replace tears down
    /// the old stack and mounts a new one — letting the wrapper
    /// cross-fade between them instead of replaying the
    /// slide-from-right push animation, which would visually lie
    /// about a destructive operation. Push and pop don't bump this;
    /// they animate inside the existing stack with SwiftUI's native
    /// transitions.
    var rootGeneration: Int = 0

    private init() {}

    func reset() {
        pages = []
        navPath = []
        currentUser = nil
        authPending = false
        signInPath = ""
        pendingReturnPath = nil
        redirectingToSignIn = false
    }

    /// Initial seed for the navigation stack — called once at app
    /// startup with the path of the first registered page. Idempotent:
    /// only sets when the stack is empty so a hot-reload or program
    /// refresh doesn't bulldoze the user's current location.
    func seedRoot(_ path: String) {
        if navPath.isEmpty {
            navPath = [path]
        }
    }

    /// Called by MarHTTP when a Service.call returns 401. Captures the
    /// current path so Auth.completeSignIn can return there, then routes
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

    /// Mutate the navigation stack. `replace` clears history and lands
    /// the user on `path` as the only entry (logout, sign-in landing,
    /// auth-expired redirect). `replace: false` appends, letting the
    /// user swipe back to where they were.
    ///
    /// `invalidateUser` opts out of clearing `currentUser` on replace,
    /// for the case where the caller has just installed a fresh user
    /// value and a navigate(replace:true) follows immediately —
    /// `Auth.completeSignIn` is the canonical example. Without this
    /// escape hatch, the just-set user would be wiped before the
    /// destination's protected-page gate could see it, forcing an
    /// extra Auth.me round-trip that's racy against the freshly-set
    /// session cookie.
    func navigate(path: String, replace: Bool, invalidateUser: Bool = true) {
        if replace {
            if invalidateUser {
                currentUser = nil
            }
            // Bump the generation BEFORE mutating navPath so SwiftUI
            // sees both observable changes in the same tick and treats
            // the NavigationStack as identity-swapped — triggering the
            // cross-fade transition instead of the default
            // slide-from-right push animation. Order matters: if the
            // path changed first, SwiftUI might already start animating
            // a stack-pop before the identity change supersedes it.
            rootGeneration += 1
            navPath = [path]
        } else {
            navPath.append(path)
        }
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
