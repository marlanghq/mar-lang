// Top-level UI. Interprets the user's mar program via MarLoader +
// MarBuiltins, captures the page list, and mounts each Page natively
// as a SwiftUI screen via MarPageHost.
//
// States:
//
//   - loading  → spinner
//   - loaded   → user's Pages, mounted in a SwiftUI NavigationStack
//     driven by AppContext.navPath. Native swipe-back pops the top
//     entry; Nav.push/Nav.replace append or rewrite the stack.
//     Page.protected destinations gate on Auth.me. A lone public
//     static page skips the stack and mounts directly.
//
//   - failed   → error banner with retry.
//
// Connectivity: in DEBUG, Bonjour discovers `_mar._tcp` services on
// the LAN and auto-connects to a `mar dev` advertising THIS app's
// name (see AppViewModel.maybeAutoPick), showing an orange "Dev
// server" banner while the override is active; in RELEASE, the
// baked-in `ios.serverUrl` from mar.json is the only target. No
// user-facing "settings" UI: the backend is configuration, not
// preference.

import SwiftUI

struct ContentView: View {
    @Environment(AppViewModel.self) private var viewModel

    var body: some View {
        Group {
            switch viewModel.state {
            case .idle, .loading:
                ProgressView("Loading…")
            case .loaded:
                LoadedShell(pages: viewModel.pages)
            case .failed(let message):
                FailedView(message: message)
            }
        }
        .animation(.default, value: viewModel.state)
        // DEBUG-only dev-server banner. When Bonjour swaps the backend
        // to a LAN `mar dev`, say so on screen: sign-in codes then go
        // to that terminal's stdout, not to real email, and without
        // this banner that reads as "login is broken".
        .overlay(alignment: .bottom) {
            if let label = viewModel.devOverrideLabel {
                Text("Dev server: \(label)")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.black)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(.orange.opacity(0.92), in: Capsule())
                    .padding(.bottom, 6)
                    .allowsHitTesting(false)
            }
        }
        // The server is running a different mar than this binary, so
        // the program it is serving was refused (see loadAll). The app
        // keeps working on the program it already has, but everything
        // deployed since is invisible, which would otherwise look like
        // "my deploy didn't go out". Say it on screen.
        //
        // A banner rather than a blocking dialog: the app is still
        // usable, just frozen at a known-good version, and the fix is
        // an App Store update the user may not be able to do this
        // second. Top edge so it does not collide with the DEBUG dev
        // banner at the bottom.
        .overlay(alignment: .top) {
            if let serverVersion = viewModel.incompatibleServerRuntime {
                Text("Update needed — the server is on mar \(serverVersion)")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.white)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(.red.opacity(0.92), in: Capsule())
                    .padding(.top, 6)
                    .allowsHitTesting(false)
            }
        }
        .task {
            // Always fire loadAll on first appear. If state is .idle
            // (no embedded snapshot), this is the only fetch and it
            // shows the loading indicator. If state is already .loaded
            // (embedded paint succeeded in init), loadAll detects
            // pages.isEmpty == false and runs as a silent background
            // refresh: the user sees the embedded UI immediately and
            // it updates if the server has a fresher version.
            await viewModel.loadAll()
        }
    }
}

private struct LoadedShell: View {
    let pages: [DecodedPage]

    var body: some View {
        if pages.isEmpty {
            BackendOnlyPlaceholder()
        } else if pages.count == 1 {
            // Single-page: just mount it. If it's protected or
            // dynamic, the gating layer below still applies.
            if pages[0].isProtected || pages[0].isDynamic {
                StackShell(pages: pages)
            } else {
                MarSinglePageView(page: pages[0])
            }
        } else {
            // A stack, because that is what iPhone navigation looks like:
            // not because the program demands one. The chrome is this
            // platform's to choose, and on another one (an iPad sidebar, an
            // Android drawer) it should look different.
            //
            // What is NOT free is the lifecycle. An all-public, all-static
            // app used to become a TabView here, with an icon guessed from
            // the path, and the problem was not the tab bar: it mounted every
            // page at once and switched by visibility, so the stack never moved
            // and ADR-0009 had no push to re-init from. The model an app left
            // behind on a page came back on iOS and did not on the web: the
            // same program behaving two ways.
            //
            // A tab bar whose selection writes the stack would keep the
            // lifecycle intact and belongs here just as much. See
            // shell_shape_test.go, which guards the lifecycle and says
            // nothing about the chrome.
            StackShell(pages: pages)
        }
    }
}

/// Stack-mode renderer backed by SwiftUI's native NavigationStack.
///
/// `AppContext.navPath` is the source of truth for the navigation
/// stack: it's a `[String]` where each entry is the path of a
/// page on the stack (bottom-first). Bound directly into
/// `NavigationStack(path:)` so:
///
///   - `Nav.push "/foo"`  → appends → SwiftUI pushes a new screen.
///   - `Nav.replace "/x"` → replaces the whole array → SwiftUI
///     drops the stack and lands on the new page.
///   - Native swipe-back / nav-bar back → SwiftUI mutates the
///     binding, which writes back to `navPath`, popping the top
///     entry. Mar code doesn't need to know about the pop.
///
/// The "root" of NavigationStack is the first entry of `navPath`;
/// the rest are pushed via `.navigationDestination(for: String.self)`.
/// Keeping the first entry as the root (instead of an invisible
/// placeholder) means swipe-back from the only entry has nothing to
/// pop, which is the correct behavior: there's no "blank screen"
/// the user could end up on.
private struct StackShell: View {
    let pages: [DecodedPage]

    @State private var ctx = AppContext.shared

    var body: some View {
        // Custom binding so SwiftUI's pop gestures update the shared
        // stack. Get exposes everything after the root (the "pushed"
        // portion); set rewrites that suffix while preserving the root
        // entry. When the user swipes back the only pushed entry, set
        // is called with [], leaving the stack with just the root.
        // The Page.sheet routes at the TOP of the stack are presented, not
        // pushed: they come off the pushed array and go to `.sheet` below,
        // so the screen they were reached from stays on screen underneath.
        // As the ROOT a sheet route has nothing to present over and mounts
        // normally: the same graceful fallback the web gives a cold load of
        // a sheet route.
        let chain = presentedChain(ctx.stack, pages)
        #if DEBUG
        // Publish the decision this shell actually made. The lifecycle
        // harness asks THIS rather than working it out from the route
        // table again: a shell that resolved a sheet route and then
        // pushed it anyway would agree with a re-derivation and disagree
        // with the screen. A plain box, not observable state, so writing
        // it during a body pass invalidates nothing.
        let _ = { MarPresentation.current = chain.last?.path }()
        #endif

        let pushedBinding = Binding<[NavEntry]>(
            get: {
                let stack = ctx.stack
                return Array(stack.dropFirst().dropLast(presentedChain(stack, pages).count))
            },
            set: { newPushed in
                // While a route is presented the stack underneath is
                // covered and cannot pop, so the presented entries are
                // preserved here; each `.sheet`'s own binding is what
                // removes its entry (swipe down, or a Nav effect from it).
                let stack = ctx.stack
                let tail = presentedChain(stack, pages)
                ctx.stack = Array(stack.prefix(1)) + newPushed + tail
            }
        )

        // Cross-fade between root swaps. SwiftUI's NavigationStack
        // animates push/pop internally with the standard slide-from-
        // right; that's the right cue for "going deeper / coming back".
        // But `Nav.replace` is destructive: the previous stack is
        // discarded, and the slide animation would visually lie
        // about that. We swap the entire NavigationStack identity on
        // each replace (via `.id(rootGeneration)`), and the surrounding
        // `.animation(_:value:)` triggers an opacity transition for
        // the identity change. Net effect: push/pop = slide; replace
        // = cross-fade.
        //
        // The opacity transition is applied with .animation rather
        // than withAnimation at the call site because the call sites
        // (Auth.completeSignIn, handleAuthExpired, Nav.replace) are
        // scattered and we want the animation policy centralized
        // here, in the view that owns the stack.
        NavigationStack(path: pushedBinding) {
            RouteView(entry: rootEntry, pages: pages)
                .navigationDestination(for: NavEntry.self) { entry in
                    RouteView(entry: entry, pages: pages)
                }
        }
        .sheet(isPresented: dismissing(chain.first)) {
            if let entry = chain.first {
                PresentedRoute(entry: entry, pages: pages)
            }
        }
        .id(ctx.rootGeneration)
        .transition(.opacity)
        .animation(.easeInOut(duration: 0.25), value: ctx.rootGeneration)
        // Only the page on top subscribes, so the top moving changes who
        // listens: a page under a sheet that closed listens again, and one a
        // push covered stops. It stays mounted either way, so its own
        // onAppear / onDisappear cannot say so.
        .onChange(of: ctx.stack.last?.id) { MarSubscriptions.shared.refresh() }
    }

    /// The entry SwiftUI renders as the root view of NavigationStack.
    /// In normal operation this is the stack's first entry; the empty-stack
    /// fallback covers the brief window during cold-start before
    /// `seedRoot` runs in AppViewModel: falling back to where the app
    /// opens (`startPath`) keeps the renderer from blanking out.
    private var rootEntry: NavEntry {
        ctx.stack.first ?? NavEntry(id: NavEntry.unseated, path: pages.startPath ?? "/")
    }

}

/// The entries the shell presents instead of pushing: the run of Page.sheet
/// routes at the top of the stack, bottom first. Never the root, which has
/// nothing to present over. Empty when the top is an ordinary page.
@MainActor
private func presentedChain(_ stack: [NavEntry], _ pages: [DecodedPage]) -> [NavEntry] {
    var start = stack.count
    while start > 1, pages.isSheetRoute(stack[start - 1].path) { start -= 1 }
    return Array(stack[start...])
}

/// Whether `shown` is presented, as a binding a `.sheet` can close. Closing
/// drops the entry from the stack, so the route and what is on screen can
/// never disagree: the same rule the web follows by routing every dismissal
/// through history. Only while that entry is still on top: when Nav.dismiss
/// or Back has already taken it off, SwiftUI reports the sheet closing too,
/// and a second removal would take the page underneath with it.
@MainActor
private func dismissing(_ shown: NavEntry?) -> Binding<Bool> {
    Binding(
        get: { shown != nil },
        set: { showing in
            let ctx = AppContext.shared
            if !showing, let shown, ctx.stack.count > 1, ctx.stack.last?.id == shown.id {
                ctx.stack.removeLast()
            }
        }
    )
}

/// One presented route, and the presented route over it, if any. A sheet
/// route that opens another sheet route presents it over itself, so the one
/// underneath stays alive with its model, as a covered page does, and Back
/// from the second finds the first as it was left.
///
/// `.id(entry.id)`: each entry gets its own page host. Without it, SwiftUI
/// keeps the host's @State when the entry in this slot changes, and the
/// screen would go on showing the page that was there before.
private struct PresentedRoute: View {
    let entry: NavEntry
    let pages: [DecodedPage]

    @State private var ctx = AppContext.shared

    var body: some View {
        let chain = presentedChain(ctx.stack, pages)
        let above = chain.firstIndex(where: { $0.id == entry.id })
            .flatMap { $0 + 1 < chain.count ? chain[$0 + 1] : nil }
        RouteView(entry: entry, pages: pages)
            .id(entry.id)
            .sheet(isPresented: dismissing(above)) {
                if let above {
                    PresentedRoute(entry: above, pages: pages)
                }
            }
    }
}

/// Single-destination wrapper: matches `path` against the declared
/// pages, applies the protected-page gate when needed, and mounts the
/// PageHost. Lives outside StackShell so each entry on the
/// NavigationStack gets its own scope: including its own `.task`
/// that bootstraps Auth.me when first appearing.
private struct RouteView: View {
    let entry: NavEntry
    let pages: [DecodedPage]

    private var path: String { entry.path }

    var body: some View {
        if let match = matchPath(path) {
            if match.page.isProtected {
                ProtectedRoute(match: match, entry: entry.runtimeEntry)
            } else {
                MarPageHost(runtime: PageRuntime(page: match.page,
                                                 user: nil,
                                                 params: match.page.isDynamic ? match.params : nil,
                                                 entry: entry.runtimeEntry))
            }
        } else {
            ContentUnavailableView(
                "Page not found",
                systemImage: "questionmark.folder",
                description: Text("No page is registered at \(path).")
            )
        }
    }

    /// Resolve `path` to a page + params. Static pages match first
    /// (so a literal "/notes/new" beats the pattern "/notes/{id:Int}"),
    /// then dynamic patterns are tried in declaration order. No
    /// fallback: a missing match surfaces a "Page not found" view
    /// instead of silently mounting the wrong page.
    private func matchPath(_ url: String) -> PageMatch? {
        for pg in pages where !pg.isDynamic {
            if pg.path == url {
                return PageMatch(page: pg, params: .record(fields: [:], order: []))
            }
        }
        for pg in pages where pg.isDynamic {
            if let params = pg.matchURL(url) {
                return PageMatch(page: pg, params: params)
            }
        }
        return nil
    }
}

/// Gate that fronts every Page.protected destination with an Auth.me
/// bootstrap. Encapsulates the three rendering states (checking,
/// authed, unauthed) so each protected route owns its own auth check
/// scoped to the SwiftUI lifecycle of that route.
private struct ProtectedRoute: View {
    let match: PageMatch
    let entry: Int?

    @State private var ctx = AppContext.shared

    var body: some View {
        Group {
            if ctx.authPending || ctx.currentUser == nil {
                // A first screen that waits on the session check is still the
                // first render: the stores' init Cmds do not wait for it. Their
                // subscriptions start with the first page, as on the web.
                ProgressView("Checking session…")
                    .onAppear { MarSharedRegistry.drainInitEffects() }
            } else if let user = unwrapJustUser(ctx.currentUser) {
                MarPageHost(runtime: PageRuntime(page: match.page,
                                                 user: user,
                                                 params: match.page.isDynamic ? match.params : nil,
                                                 entry: entry))
            } else if !ctx.signInPath.isEmpty {
                // No session: replace the stack with [signInPath].
                // navigate(replace:true) wipes the back-history so a
                // post-login navigate can return the user to wherever
                // they intended to land (via Auth.completeSignIn +
                // pendingReturnPath).
                Color.clear
                    .task {
                        if ctx.currentPath != ctx.signInPath {
                            ctx.pendingReturnPath = ctx.currentPath
                            ctx.navigate(path: ctx.signInPath, replace: true)
                        }
                    }
            } else {
                // Misconfiguration: Page.protected used without
                // Auth.config { signInPage = ... }. Surface visibly
                // instead of looping or going blank.
                ContentUnavailableView(
                    "Sign-in page not configured",
                    systemImage: "lock.slash",
                    description: Text("""
                        This app uses Page.protected but no `signInPage` is declared in \
                        Auth.config. Add `signInPage = Frontend.SignIn.page` (or similar) \
                        so unauthed users have somewhere to land.
                        """)
                )
            }
        }
        .task {
            // Bootstrap Auth.me on first appearance. nil = never
            // checked; reuse any prior Just/Nothing answer instead of
            // hammering the server on every protected-page visit.
            guard ctx.currentUser == nil else { return }
            ctx.authPending = true
            // An answer that arrives after a replace, a sign-out or a
            // switch to another server is about what came before, and
            // would overwrite the answer to the question asked now
            // (authEpoch moves with all three).
            let asked = ctx.authEpoch
            let result = await MarHTTP.fetchAuthMe()
            guard ctx.authEpoch == asked else { return }
            // Nor is a request this view cancelled, by going away (a
            // swipe back while the session is checked) an answer:
            // URLSession reports it as an error, which reads as "nobody",
            // and the next protected page would send a signed-in person to
            // sign-in. Leave the question open for the next gate.
            if Task.isCancelled {
                ctx.authPending = false
                return
            }
            ctx.currentUser = result ?? .ctor(tag: "Nothing", args: [])
            ctx.authPending = false
        }
    }

    private func unwrapJustUser(_ v: MarValue?) -> MarValue? {
        guard let v, case .ctor(let tag, let args) = v else { return nil }
        if tag == "Just", let first = args.first { return first }
        return nil
    }
}

/// Result of resolving the current URL: the matching page plus the
/// captured Params record (empty for static pages).
private struct PageMatch {
    let page: DecodedPage
    let params: MarValue
}

/// Shown when `main` doesn't expose any pages (backend-only mar
/// project). The iOS shell has nothing to render in that case.
private struct BackendOnlyPlaceholder: View {
    var body: some View {
        ContentUnavailableView(
            "No pages to render",
            systemImage: "rectangle.stack.badge.minus",
            description: Text("""
                This mar app exposes a backend but no `Page` values. Add at least one \
                `Page.create` to `App.fullstack { pages = ... }`.
                """)
        )
    }
}

/// What the phone shows when the app could not start: the same words as the
/// browser's boot failure screen (BootFailureHead in internal/jsserve), so a
/// person sees one message on either. The cause shows under it only in a
/// DEBUG build, where the person looking can fix it, like the detail
/// `mar dev` adds in the browser. The button is the platform's: Retry runs
/// the load again in place, where the browser reloads.
private struct FailedView: View {
    let message: String
    @Environment(AppViewModel.self) private var viewModel

    var body: some View {
        ContentUnavailableView {
            Label("This app couldn't load.", systemImage: "exclamationmark.triangle")
        } description: {
            Text(Self.description(message))
        } actions: {
            Button("Retry") {
                Task { await viewModel.loadAll() }
            }
            .buttonStyle(.borderedProminent)
        }
    }

    static func description(_ cause: String) -> String {
        #if DEBUG
        return "Check your connection and try again.\n\n" + cause
        #else
        return "Check your connection and try again."
        #endif
    }
}
