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
//   - currentPath  : convenience computed property, the active
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
    /// element is a `__Page` or `__ProtectedPage` ctor: see
    /// `decodedPages()` for the per-tag positional layout.
    private(set) var pages: [MarValue] = []

    /// Navigation stack (bottom to top). Bound directly into
    /// `NavigationStack(path:)` so native swipe-back pops the top
    /// entry without any extra wiring. `Nav.push` appends, `Nav.replace`
    /// resets to a single entry, `Auth.completeSignIn` resets to the
    /// pendingReturnPath.
    var stack: [NavEntry] = []

    /// The paths on the stack, bottom first. Read-only: every write goes
    /// through `stack`, so an entry is minted once and keeps its identity.
    var navPath: [String] { stack.map(\.path) }

    @ObservationIgnored private var lastEntryID = 0

    private func mint(_ path: String) -> NavEntry {
        lastEntryID += 1
        return NavEntry(id: lastEntryID, path: path)
    }

    /// Whether an entry is still on the stack: what decides if a page's late
    /// result is delivered or dropped.
    func holds(_ id: Int) -> Bool { stack.contains { $0.id == id } }

    /// A whole new stack, every entry fresh.
    func setStack(_ paths: [String]) { stack = paths.map(mint) }

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
    /// the app doesn't declare any Auth: Page.protected then
    /// surfaces a clear runtime error rather than a silent loop.
    var signInPath: String = ""

    /// Where to send the user after a successful sign-in. Set by
    /// `handleAuthExpired()` when a 401 from a Service.call hijacks
    /// the user to the sign-in screen: `Auth.completeSignIn` consumes it.
    /// Web uses a `?next=` URL parameter for the same purpose; on iOS
    /// we don't have a URL bar, so we keep it in memory. Lost on cold
    /// start (acceptable: there's no "where you were" anymore).
    var pendingReturnPath: String?

    /// Coalesces parallel Service.call 401s. Set true the moment the
    /// first 401 triggers a redirect; subsequent 401s during the same
    /// redirect window are dropped. Reset by `Auth.completeSignIn` after
    /// a successful login completes.
    var redirectingToSignIn: Bool = false

    /// Monotonic counter bumped every time `navigate(replace: true)`
    /// rewrites the stack. The `StackShell` view applies it as
    /// SwiftUI `.id()` on the NavigationStack so a replace tears down
    /// the old stack and mounts a new one: letting the wrapper
    /// cross-fade between them instead of replaying the
    /// slide-from-right push animation, which would visually lie
    /// about a destructive operation. Push and pop don't bump this;
    /// they animate inside the existing stack with SwiftUI's native
    /// transitions.
    var rootGeneration: Int = 0

    /// Bumped whenever an answer to "who is signed in" stops being about
    /// the present: a replace (the next protected page asks again), a
    /// sign-out, a switch to another server. The protected-route gate
    /// records it before asking and keeps the answer only if it has not
    /// moved, so a late answer to an old question cannot overwrite the
    /// new one. rootGeneration is not the right clock: a sign-out does
    /// not replace the stack.
    var authEpoch: Int = 0

    private init() {}

    func reset() {
        pages = []
        stack = []
        currentUser = nil
        authPending = false
        authEpoch += 1
        signInPath = ""
        pendingReturnPath = nil
        redirectingToSignIn = false
    }

    /// Initial seed for the navigation stack: called once at app
    /// startup with `startPath` ("/" when a page answers it, else the
    /// first declared page). Idempotent:
    /// only sets when the stack is empty so a hot-reload or program
    /// refresh doesn't bulldoze the user's current location.
    func seedRoot(_ path: String, pages: [DecodedPage] = []) {
        if stack.isEmpty {
            setStack(Self.stackFor(path, pages: pages))
        }
    }

    /// The navigation stack a url should arrive as.
    ///
    /// For an ordinary route that is just the route. For a PRESENTED one it is
    /// two entries, the screen it covers, then itself, because a presented
    /// route opened cold used to render as a bare full screen, and that was
    /// worse than odd: `dismissTop` is a no-op on the first entry, so the
    /// sheet's own Done button did nothing and the screen was a dead end.
    ///
    /// The parent needs no new API. A presented route already nests in the url
    /// under the screen it covers: /classes/3/attendance sits under
    /// /classes/3, and the prefix of a CONCRETE url is itself concrete, so the
    /// parent's params come along for free. Routes that nest on screen nest in
    /// the url too; one that doesn't is a shape worth noticing in the route
    /// table rather than a case to paper over here.
    ///
    /// With no such parent the app's first page goes underneath instead,
    /// because a modal always has something behind it. The web runtime seeds
    /// the same two entries at boot, for the same reasons.
    static func stackFor(_ url: String, pages: [DecodedPage]) -> [String] {
        guard pages.isSheetRoute(url) else { return [url] }
        var segments = url.split(separator: "/").map(String.init)
        while segments.count > 1 {
            segments.removeLast()
            let candidate = "/" + segments.joined(separator: "/")
            if pages.contains(where: { $0.matchURL(candidate) != nil }),
               !pages.isSheetRoute(candidate) {
                return [candidate, url]
            }
        }
        if let first = pages.first, first.path != url, !first.isSheet {
            return [first.path, url]
        }
        return [url]
    }

    /// Called by MarHTTP when a Service.call returns 401. Captures the
    /// current path so Auth.completeSignIn can return there, then routes
    /// to the sign-in screen. Idempotent across parallel 401s thanks
    /// to `redirectingToSignIn`. Returns true if it took over (caller
    /// should NOT dispatch the Err); false when there's no signInPath
    /// configured (caller falls through to its default error path).
    func handleAuthExpired() -> Bool {
        // The decision is MarHTTP.decideAuthExpiry, shared with the browser and
        // compared against it; what is left here is this platform's half of it.
        let d = MarHTTP.decideAuthExpiry(signInPath: signInPath,
                                         alreadyRedirecting: redirectingToSignIn,
                                         currentPath: currentPath)
        guard d.handled else { return false }
        guard d.navigate else { return true }
        redirectingToSignIn = true
        if let capture = d.capture {
            pendingReturnPath = capture
        }
        navigate(path: signInPath, replace: true)
        return true
    }

    func capturePages(_ list: MarValue) {
        guard case .list(let xs) = list else { return }
        pages = xs
    }

    /// Close the top entry of the stack: what Nav.dismiss does, and what
    /// a presented route's own Cancel / Done button needs. Never pops the
    /// last entry: at the app's first screen there is nothing to close,
    /// and popping it would leave the renderer with no route at all.
    func dismissTop() {
        guard stack.count > 1 else { return }
        stack.removeLast()
    }

    /// Mutate the navigation stack. `replace` clears history and lands
    /// the user on `path` as the only entry (logout, sign-in landing,
    /// auth-expired redirect). `replace: false` appends, letting the
    /// user swipe back to where they were.
    ///
    /// A replace forgets the cached user, so the next protected page asks
    /// the server again: the browser's replace and replaceFresh do the
    /// same. There used to be an opt-out for Auth.completeSignIn, for a
    /// fresh user nothing ever installed (see the comment there).
    ///
    /// It also forgets the question in flight, which the browser does
    /// not: there one shared request answers every gate, so a replace
    /// made while it is out shows its answer. Here each gate asks for
    /// itself and the gate of the replaced stack is torn down, so its
    /// answer would land on the new stack.
    func navigate(path: String, replace: Bool) {
        if replace {
            forgetUser()
            // Bump the generation BEFORE mutating the stack so SwiftUI
            // sees both observable changes in the same tick and treats
            // the NavigationStack as identity-swapped: triggering the
            // cross-fade transition instead of the default
            // slide-from-right push animation. Order matters: if the
            // path changed first, SwiftUI might already start animating
            // a stack-pop before the identity change supersedes it.
            rootGeneration += 1
            stack = [mint(path)]
        } else {
            stack.append(mint(path))
        }
    }

    /// Who is signed in is no longer known: the cached answer and any
    /// question in flight are dropped (authEpoch), and the next protected
    /// page asks again.
    func forgetUser() {
        currentUser = nil
        authPending = false
        authEpoch += 1
    }

    /// Open the app again where it opens, as at launch, forgetting who is
    /// signed in and where a sign-in was going to return to. A DEBUG
    /// switch to another dev server does this: what the app learned
    /// came from the other one.
    func restart(pages: [DecodedPage]) {
        guard let start = pages.startPath else { return }
        forgetUser()
        pendingReturnPath = nil
        redirectingToSignIn = false
        rootGeneration += 1
        setStack(Self.stackFor(start, pages: pages))
    }

    /// Decoded view of the captured pages, ready for the renderer
    /// to mount. Recognizes all four page ctors:
    ///   - `__Page`                  : public, static path
    ///   - `__ProtectedPage`         : auth-gated, static path
    ///   - `__DynamicPage`           : public, `:param` pattern path
    ///   - `__DynamicProtectedPage`  : auth-gated, `:param` pattern path
    ///
    /// A seventh ctor arg (set by `Page.sheet`) marks the page as
    /// PRESENTED rather than pushed; it is orthogonal to the four tags,
    /// which is why it rides as an arg instead of multiplying them.
    func decodedPages() -> [DecodedPage] {
        pages.compactMap { v in
            // Page.withShared wraps a page. Unwrap it against the store's
            // CURRENT model to read the route, and keep the builder so the
            // runtime can re-resolve the four functions as the model moves.
            var v = v
            var binding: DecodedPage.SharedBinding?
            if case .ctor("__SharedPage", let wargs) = v, wargs.count >= 2,
               case .string(let key) = wargs[0] {
                // Say so when this fails. A dropped page does not crash and
                // does not draw anything: it simply is not in the app, and
                // the first symptom is the WRONG SCREEN at launch, because
                // the root is whichever page happened to survive.
                guard let store = MarSharedRegistry.lookup(key) else {
                    print("[mar] dropping a Page.withShared: no store for \(key)")
                    return nil
                }
                let inner: MarValue
                do {
                    inner = try Eval.apply(wargs[1], store.model)
                } catch {
                    print("[mar] dropping a Page.withShared (\(key)): "
                          + "the builder failed: \(error.localizedDescription)")
                    return nil
                }
                binding = DecodedPage.SharedBinding(key: key, builder: wargs[1])
                v = inner
            }
            guard case .ctor(let tag, let args) = v else { return nil }
            let isSheet = args.count >= 7 && boolOf(args[6]) == true
            switch tag {
            case "__Page":
                guard args.count >= 4 else { return nil }
                return DecodedPage(
                    shared: binding,
                    path: stringOf(args[0]) ?? "/",
                    title: args.count >= 5 ? (stringOf(args[4]) ?? "") : "",
                    initFn: args[1],
                    updateFn: args[2],
                    viewFn: args[3],
                    subscriptionsFn: args.count >= 6 ? args[5] : .unit,
                    isProtected: false,
                    isDynamic: false,
                    isSheet: isSheet
                )
            case "__ProtectedPage":
                guard args.count >= 5 else { return nil }
                return DecodedPage(
                    shared: binding,
                    path: stringOf(args[0]) ?? "/",
                    title: stringOf(args[4]) ?? "",
                    initFn: args[1],
                    updateFn: args[2],
                    viewFn: args[3],
                    subscriptionsFn: args.count >= 6 ? args[5] : .unit,
                    isProtected: true,
                    isDynamic: false,
                    isSheet: isSheet
                )
            case "__DynamicPage":
                guard args.count >= 5 else { return nil }
                return DecodedPage(
                    shared: binding,
                    path: stringOf(args[0]) ?? "/",
                    title: stringOf(args[4]) ?? "",
                    initFn: args[1],
                    updateFn: args[2],
                    viewFn: args[3],
                    subscriptionsFn: args.count >= 6 ? args[5] : .unit,
                    isProtected: false,
                    isDynamic: true,
                    isSheet: isSheet
                )
            case "__DynamicProtectedPage":
                guard args.count >= 5 else { return nil }
                return DecodedPage(
                    shared: binding,
                    path: stringOf(args[0]) ?? "/",
                    title: stringOf(args[4]) ?? "",
                    initFn: args[1],
                    updateFn: args[2],
                    viewFn: args[3],
                    subscriptionsFn: args.count >= 6 ? args[5] : .unit,
                    isProtected: true,
                    isDynamic: true,
                    isSheet: isSheet
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

    private func boolOf(_ v: MarValue) -> Bool? {
        if case .bool(let b) = v { return b }
        return nil
    }
}

/// One Page ready for mounting: its ctor args destructured into named
/// fields. The redirect path for protected pages comes from
/// `AppContext.signInPath` at render time (set by the Auth.config
/// builtin), not from the page itself. `isDynamic` indicates the
/// path is a `:param` pattern that the renderer matches at navigation
/// time. `isSheet` (Page.sheet) says this route is PRESENTED over the
/// screen it was reached from instead of pushed onto the stack.
extension Array where Element == DecodedPage {
    /// Does `url` resolve to a page declared with Page.sheet? Static paths
    /// first, then dynamic patterns, mirroring RouteView's own resolution
    /// order so both agree on which page a URL means.
    ///
    /// One definition, because two would drift: the renderer asks this to
    /// decide whether to present or push, and the lifecycle harness asks it
    /// to decide which of the mounted pages is the presented one. A harness
    /// answering that question its own way could report a pass while the
    /// renderer did something else.
    @MainActor
    func isSheetRoute(_ url: String) -> Bool {
        for pg in self where !pg.isDynamic {
            if pg.path == url { return pg.isSheet }
        }
        for pg in self where pg.isDynamic {
            if pg.matchURL(url) != nil { return pg.isSheet }
        }
        return false
    }

    /// Where the app opens: "/", when a page answers it, and the first
    /// declared page only when none does. That is the browser's answer for
    /// the address a person types, the bare domain, which arrives as "/"
    /// (currentPage in runtime.js; a "/" declared with Page.sheet is
    /// presented over the first page there, and stackFor does the same
    /// here). The browser's fallback takes the first page only when it is
    /// static, and paints nothing for a dynamic one; this opens it and
    /// shows "Page not found". Neither is an answer, and no app has that
    /// shape.
    ///
    /// The phone has no address bar, so it opens where the browser would.
    /// It used to open on the first declared page, which in every example
    /// with sign-in is the sign-in page, listed first: a person with a valid
    /// session was shown sign-in at every launch, while the browser, at
    /// "/", reached the protected page and its session. Where an app opens
    /// is lifecycle, not chrome.
    @MainActor
    var startPath: String? {
        if contains(where: { $0.matchURL("/") != nil }) { return "/" }
        return first?.path
    }
}

/// One entry of the navigation stack. The identity is the ENTRY, not the
/// path: /a -> /b -> /a holds two /a entries, each with its own page and
/// model (ADR 0009), and a late result for the first must not reach the
/// second.
struct NavEntry: Hashable {
    let id: Int
    let path: String

    /// The root shown in the moment before the stack is seeded on a cold
    /// start. Minted ids start at 1, so this one is never on the stack, and
    /// a page built for it must not be taken for an entry that left it.
    static let unseated = 0

    /// The entry a page runtime is told it belongs to: none for the unseated
    /// root, which is then treated like the one-page shell, always on top.
    var runtimeEntry: Int? { id == NavEntry.unseated ? nil : id }
}

struct DecodedPage: Identifiable {
    /// Set when the page came from `Page.withShared`. The page is a FUNCTION
    /// of the shared model, so the builder is kept unapplied and re-run on
    /// every read: see MarPageRuntime. Path, title and the flags are read
    /// once, because a route cannot change with the model.
    struct SharedBinding {
        let key: String
        let builder: MarValue
    }

    var shared: SharedBinding?
    let path: String
    let title: String
    let initFn: MarValue
    let updateFn: MarValue
    let viewFn: MarValue
    let subscriptionsFn: MarValue
    let isProtected: Bool
    let isDynamic: Bool
    let isSheet: Bool

    var id: String { path }

    /// Match a candidate URL against this page's path pattern.
    /// Returns the captured params record (typed per `{name:Type}`)
    /// on success, nil on miss. Static pages match by exact equality.
    /// Type-mismatch on a typed segment (e.g. `/notes/abc` against
    /// `{id:Int}`) returns nil so the router can fall through to
    /// the next page.
    ///
    /// Reads MarPath's cache, which is main-actor state. This already ran
    /// there; the annotation is what makes that checkable.
    @MainActor
    func matchURL(_ urlPath: String) -> MarValue? {
        if !isDynamic {
            return urlPath == path ? MarValue.record(fields: [:], order: []) : nil
        }
        let pattern = MarPath.parse(path)
        return MarPath.match(urlPath, pattern: pattern)
    }

    #if DEBUG
    /// Params good enough to render this page once, for the route smoke in
    /// AppViewModel. Fills each `{name:Type}` segment with a plausible value
    /// and runs it back through the real matcher, so whatever comes out is
    /// typed exactly the way a real URL's params would be. Returns nil when
    /// the pattern uses something this cannot fill: the caller says so
    /// rather than counting the page as checked.

    /// The concrete URL this page was rendered at, alongside its params.
    ///
    /// The URL matters as much as the params: the web half of the comparison
    /// has to be sent to the SAME address. Feeding it the raw pattern instead
    /// made it render `/verify/{email:String}` literally, so the two platforms
    /// were asked different questions and every dynamic route looked like a
    /// difference.
    @MainActor
    func sampleVisit() -> (url: String, params: MarValue)? {
        var url = ""
        for segment in path.split(separator: "/", omittingEmptySubsequences: false) {
            let s = String(segment)
            if s.hasPrefix("{") && s.hasSuffix("}") {
                let body = s.dropFirst().dropLast()
                let type = body.split(separator: ":").last.map(String.init) ?? "String"
                switch type {
                case "Int": url += "1"
                case "String": url += "sample"
                // A `{role:Role}` segment takes a ctor name; the registry the
                // matcher itself consults is the source of truth for those.
                default: url += MarPath.enumCtors(type)?.first ?? "sample"
                }
            } else {
                url += s
            }
            url += "/"
        }
        if url.count > 1 { url.removeLast() }
        guard let params = matchURL(url) else { return nil }
        return (url, params)
    }
    #endif
}
