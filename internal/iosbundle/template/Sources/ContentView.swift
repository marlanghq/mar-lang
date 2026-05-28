// Top-level UI. Interprets the user's mar program via MarLoader +
// MarBuiltins, captures the page list, and mounts each Page natively
// as a SwiftUI screen via MarPageHost.
//
// States:
//
//   - loading  → spinner
//   - loaded   → user's Pages. Two render modes:
//
//     * Stack mode (any Page.protected present): SwiftUI
//       NavigationStack driven by AppContext.navPath. Native
//       swipe-back pops the top entry; Nav.push/Nav.replace
//       append or rewrite the stack. Page.protected destinations
//       gate on Auth.me.
//
//     * Tabs mode (all-public multi-page apps): TabView with one
//       tab per page.
//
//   - failed   → error banner with retry.
//
// Connectivity is handled invisibly: in DEBUG, Bonjour discovers
// `_mar._tcp` services on the LAN (auto-finds `mar dev` running on
// your laptop); in RELEASE, the baked-in `ios.serverUrl` from
// mar.json is the only target. No user-facing "settings" UI — the
// backend is configuration, not preference.

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
        .task {
            // Always fire loadAll on first appear. If state is .idle
            // (no embedded snapshot), this is the only fetch and it
            // shows the loading indicator. If state is already .loaded
            // (embedded paint succeeded in init), loadAll detects
            // pages.isEmpty == false and runs as a silent background
            // refresh — the user sees the embedded UI immediately and
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
        } else if pages.contains(where: { $0.isProtected || $0.isDynamic }) {
            // Any Page.protected or Page.dynamic → stack mode. Tabs
            // mode only makes sense for all-public, all-static apps
            // (each tab a fixed page).
            StackShell(pages: pages)
        } else {
            // All-public multi-page → tabs UX.
            TabView {
                ForEach(pages) { page in
                    NavigationStack {
                        MarPageHost(runtime: PageRuntime(page: page))
                    }
                    .tabItem {
                        Label(page.displayTitle, systemImage: tabIcon(for: page))
                    }
                }
            }
        }
    }

    private func tabIcon(for page: DecodedPage) -> String {
        let p = page.path.lowercased()
        if p.contains("setting") { return "gearshape" }
        if p.contains("home") || p == "/" { return "house" }
        if p.contains("profile") || p.contains("user") { return "person" }
        return "square"
    }
}

/// Stack-mode renderer backed by SwiftUI's native NavigationStack.
///
/// `AppContext.navPath` is the source of truth for the navigation
/// stack — it's a `[String]` where each entry is the path of a
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
/// pop, which is the correct behavior — there's no "blank screen"
/// the user could end up on.
private struct StackShell: View {
    let pages: [DecodedPage]

    @State private var ctx = AppContext.shared

    var body: some View {
        // Custom binding so SwiftUI's pop gestures update the shared
        // navPath. Get exposes everything after the root (the
        // "pushed" portion); set rewrites that suffix while
        // preserving the root entry. When the user swipes back the
        // only pushed entry, set is called with [], leaving navPath
        // with just the root — correct.
        let pushedBinding = Binding<[String]>(
            get: { Array(ctx.navPath.dropFirst()) },
            set: { newPushed in
                if let first = ctx.navPath.first {
                    ctx.navPath = [first] + newPushed
                } else {
                    ctx.navPath = newPushed
                }
            }
        )

        // Cross-fade between root swaps. SwiftUI's NavigationStack
        // animates push/pop internally with the standard slide-from-
        // right; that's the right cue for "going deeper / coming back".
        // But `Nav.replace` is destructive — the previous stack is
        // discarded — and the slide animation would visually lie
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
            RouteView(path: rootPath, pages: pages)
                .navigationDestination(for: String.self) { path in
                    RouteView(path: path, pages: pages)
                }
        }
        .id(ctx.rootGeneration)
        .transition(.opacity)
        .animation(.easeInOut(duration: 0.25), value: ctx.rootGeneration)
    }

    /// The path SwiftUI renders as the root view of NavigationStack.
    /// In normal operation this is `navPath.first`; the empty-path
    /// fallback covers the brief window during cold-start before
    /// `seedRoot` runs in AppViewModel — falling back to the first
    /// declared page keeps the renderer from blanking out.
    private var rootPath: String {
        ctx.navPath.first ?? pages.first?.path ?? "/"
    }
}

/// Single-destination wrapper: matches `path` against the declared
/// pages, applies the protected-page gate when needed, and mounts the
/// PageHost. Lives outside StackShell so each entry on the
/// NavigationStack gets its own scope — including its own `.task`
/// that bootstraps Auth.me when first appearing.
private struct RouteView: View {
    let path: String
    let pages: [DecodedPage]

    var body: some View {
        if let match = matchPath(path) {
            if match.page.isProtected {
                ProtectedRoute(match: match)
            } else {
                MarPageHost(runtime: PageRuntime(page: match.page,
                                                 user: nil,
                                                 params: match.page.isDynamic ? match.params : nil))
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
    /// fallback — a missing match surfaces a "Page not found" view
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

    @State private var ctx = AppContext.shared

    var body: some View {
        Group {
            if ctx.authPending || ctx.currentUser == nil {
                ProgressView("Checking session…")
            } else if let user = unwrapJustUser(ctx.currentUser) {
                MarPageHost(runtime: PageRuntime(page: match.page,
                                                 user: user,
                                                 params: match.page.isDynamic ? match.params : nil))
            } else if !ctx.signInPath.isEmpty {
                // No session — replace the stack with [signInPath].
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
                    description: Text("This app uses Page.protected but no `signInPage` is declared in Auth.config. Add `signInPage = Frontend.SignIn.page` (or similar) so unauthed users have somewhere to land.")
                )
            }
        }
        .task {
            // Bootstrap Auth.me on first appearance. nil = never
            // checked; reuse any prior Just/Nothing answer instead of
            // hammering the server on every protected-page visit.
            guard ctx.currentUser == nil else { return }
            ctx.authPending = true
            let result = await MarHTTP.fetchAuthMe()
            ctx.currentUser = result ?? .ctor(tag: "Nothing", args: [], origin: nil)
            ctx.authPending = false
        }
    }

    private func unwrapJustUser(_ v: MarValue?) -> MarValue? {
        guard let v, case .ctor(let tag, let args, _) = v else { return nil }
        if tag == "Just", let first = args.first { return first }
        return nil
    }
}

/// Result of resolving the current URL — the matching page plus the
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
            description: Text("This mar app exposes a backend but no `Page` values. Add at least one `Page.create` to `App.fullstack { pages = ... }`.")
        )
    }
}

private struct FailedView: View {
    let message: String
    @Environment(AppViewModel.self) private var viewModel

    var body: some View {
        ContentUnavailableView {
            Label("Couldn't load the mar program", systemImage: "exclamationmark.triangle")
        } description: {
            Text(message)
        } actions: {
            Button("Retry") {
                Task { await viewModel.loadAll() }
            }
            .buttonStyle(.borderedProminent)
        }
    }
}
