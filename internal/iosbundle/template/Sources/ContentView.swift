// Top-level UI. Interprets the user's mar program via MarLoader +
// MarBuiltins, captures the page list, and mounts each Page natively
// as a SwiftUI screen via MarPageHost.
//
// States:
//
//   - loading  → spinner
//   - loaded   → user's Pages. Two render modes:
//
//     * Stack mode (any Page.protected present): a single active
//       page driven by AppContext.currentPath, gated by Auth.me.
//       Mirrors the web router model. Backwards-compatible Settings
//       reachable via toolbar button.
//
//     * Tabs mode (legacy, all-public multi-page apps): TabView with
//       one tab per page plus Settings — preserves the existing
//       multi-screen.mar UX.
//
//   - failed   → error banner with retry + open settings.
//
// Settings is always reachable, so the user can switch baseURL when
// the server isn't reachable.

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
            // All-public multi-page → keep legacy tabs UX.
            TabView {
                ForEach(pages) { page in
                    NavigationStack {
                        MarPageHost(runtime: PageRuntime(page: page))
                    }
                    .tabItem {
                        Label(page.displayTitle, systemImage: tabIcon(for: page))
                    }
                }
                NavigationStack {
                    SettingsTab()
                }
                .tabItem {
                    Label("Settings", systemImage: "gearshape")
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

/// Stack-mode renderer: one active page at a time, driven by
/// `AppContext.currentPath`. Protected pages bootstrap Auth.me on
/// first entry and either route to their `redirect` target (when
/// no user) or mount with the User threaded through.
private struct StackShell: View {
    let pages: [DecodedPage]

    @State private var ctx = AppContext.shared
    @State private var showSettings = false

    var body: some View {
        NavigationStack {
            content
                .toolbar {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button {
                            showSettings = true
                        } label: {
                            Image(systemName: "gearshape")
                        }
                    }
                }
        }
        .sheet(isPresented: $showSettings) {
            NavigationStack {
                SettingsTab()
            }
        }
        .task(id: ctx.currentPath) {
            // When entering a protected page, ensure we know the
            // user. nil means we haven't checked yet; fetch now.
            // Existing Just/Nothing answers are reused.
            guard let match = currentMatch(), match.page.isProtected else { return }
            if ctx.currentUser != nil { return }
            ctx.authPending = true
            let result = await MarHTTP.fetchAuthMe()
            ctx.currentUser = result ?? .ctor(tag: "Nothing", args: [], origin: nil)
            ctx.authPending = false
        }
    }

    @ViewBuilder
    private var content: some View {
        if let match = currentMatch() {
            if match.page.isProtected {
                protectedView(match)
            } else {
                MarPageHost(runtime: PageRuntime(page: match.page,
                                                 user: nil,
                                                 params: match.page.isDynamic ? match.params : nil))
                    // .id keys on the URL so two visits to the same
                    // pattern (e.g. /notes/abc → /notes/xyz) rebuild
                    // a fresh PageRuntime instead of reusing the
                    // previous note's model.
                    .id(ctx.currentPath)
            }
        } else {
            ContentUnavailableView(
                "Page not found",
                systemImage: "questionmark.folder",
                description: Text("No page is registered at \(ctx.currentPath).")
            )
        }
    }

    @ViewBuilder
    private func protectedView(_ match: PageMatch) -> some View {
        let pg = match.page
        if ctx.authPending || ctx.currentUser == nil {
            ProgressView("Checking session…")
        } else if let user = unwrapJustUser(ctx.currentUser) {
            MarPageHost(runtime: PageRuntime(page: pg,
                                             user: user,
                                             params: pg.isDynamic ? match.params : nil))
                .id(ctx.currentPath)
        } else if !ctx.signInPath.isEmpty {
            // No session — bounce to the sign-in path declared in
            // Auth.config.signInPage. Wrap in a Color so the .task
            // triggers; the redirect mutates currentPath which
            // re-runs the parent .task(id:) and re-renders content.
            Color.clear
                .task {
                    if ctx.signInPath != ctx.currentPath {
                        ctx.currentPath = ctx.signInPath
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

    /// Resolve the current path to a page + params. Static pages match
    /// first (so a literal "/notes/new" beats the pattern
    /// "/notes/{id:Int}"), then dynamic patterns are tried in
    /// declaration order. Falls back to the first registered page when
    /// nothing matches — keeps deep links to a stale URL from blanking
    /// the screen.
    private func currentMatch() -> PageMatch? {
        let url = ctx.currentPath
        // 1) literal match
        for pg in pages where !pg.isDynamic {
            if pg.path == url {
                return PageMatch(page: pg, params: .record(fields: [:], order: []))
            }
        }
        // 2) dynamic-pattern match
        for pg in pages where pg.isDynamic {
            if let params = pg.matchURL(url) {
                return PageMatch(page: pg, params: params)
            }
        }
        // 3) fallback (preserves prior behavior)
        if let first = pages.first {
            return PageMatch(page: first, params: .record(fields: [:], order: []))
        }
        return nil
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
        TabView {
            ContentUnavailableView(
                "No pages to render",
                systemImage: "rectangle.stack.badge.minus",
                description: Text("This mar app exposes a backend but no `Page` values. Use the Settings tab to switch backends.")
            )
            .tabItem {
                Label("Home", systemImage: "house")
            }
            NavigationStack {
                SettingsTab()
            }
            .tabItem {
                Label("Settings", systemImage: "gearshape")
            }
        }
    }
}

private struct FailedView: View {
    let message: String
    @Environment(AppViewModel.self) private var viewModel
    @State private var showSettings = false

    var body: some View {
        ContentUnavailableView {
            Label("Couldn't load the mar program", systemImage: "exclamationmark.triangle")
        } description: {
            Text(message)
        } actions: {
            HStack {
                Button("Retry") {
                    Task { await viewModel.loadAll() }
                }
                .buttonStyle(.borderedProminent)

                Button("Settings") {
                    showSettings = true
                }
                .buttonStyle(.bordered)
            }
        }
        .sheet(isPresented: $showSettings) {
            NavigationStack {
                SettingsTab()
            }
        }
    }
}
