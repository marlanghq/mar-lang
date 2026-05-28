// Top-level shell that wires DecodedPages to native navigation.
//
// Strategy mirrors what the JS runtime does for App.frontend with
// multiple pages: each page has a path, and the user moves between
// them via in-page links. iOS doesn't have a URL bar, so:
//
//  - 1 page  → mount it directly.
//  - 2+ pages → put each in a TabView. The displayTitle becomes the
//               tab label.
//
// MarPageHost is the stable wrapper around PageRuntime that
// re-renders on Observable property reads and bridges click events
// to the runtime's dispatch().

import SwiftUI

struct MarPageHost: View {
    // Owned via @State so SwiftUI preserves the same PageRuntime
    // instance across parent re-renders. Critical correctness fix:
    // a background `program.json` refresh causes AppContext.pages
    // to be reassigned mid-flight, which makes the route view
    // rebuild with a freshly-constructed PageRuntime. As a plain
    // `let`, that fresh instance would replace the live one, but
    // .onAppear/.mount() wouldn't fire again (SwiftUI considers
    // the route's identity stable). MarDispatcher.shared.current
    // would still reference the now-orphaned old PageRuntime via
    // `[weak self]` and silently no-op once it deallocates — any
    // in-flight HTTP response would land on a dead runtime and the
    // UI would freeze (e.g. "Working…" forever after Auth.verifyCode).
    //
    // With @State, the runtime survives re-renders. Navigation
    // between distinct paths still works because each entry in
    // NavigationStack's path is a fresh navigationDestination,
    // which produces a new MarPageHost — @State is initialized
    // anew per path, so the new page gets its own PageRuntime.
    @State private var runtime: PageRuntime

    init(runtime: PageRuntime) {
        _runtime = State(initialValue: runtime)
    }

    var body: some View {
        Group {
            if let view = runtime.currentView() {
                if Self.isNativeContainer(view) {
                    // The root view is a `navigationStack`, `form`,
                    // `list`, or `centered` — each renders to a
                    // SwiftUI primitive that already provides its own
                    // scrolling and layout. Wrapping them in
                    // ScrollView+VStack would collapse Form/List to
                    // zero height (they measure against the
                    // container, and ScrollView's content height is
                    // intrinsic).
                    MarRenderer(view: view) { msg in
                        runtime.dispatch(msg)
                    }
                } else {
                    // Bare leaf or stack at the root — wrap in a
                    // ScrollView so long content is reachable.
                    ScrollView {
                        VStack(alignment: .leading, spacing: 0) {
                            MarRenderer(view: view) { msg in
                                runtime.dispatch(msg)
                            }
                        }
                        .padding(16)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
            } else if let err = runtime.lastError {
                Text(err)
                    .font(.callout)
                    .foregroundStyle(.red)
                    .padding()
            } else {
                EmptyView()
            }
        }
        .navigationTitle(runtime.title.isEmpty ? runtime.path : runtime.title)
        .navigationBarTitleDisplayMode(.inline)
        .onAppear { runtime.mount() }
        .onDisappear { runtime.unmount() }
    }

    /// Whether the top-level view tag renders to a SwiftUI primitive
    /// that already provides scrolling + layout. Updated when adding
    /// new container tags to the UI vocabulary.
    private static func isNativeContainer(_ view: MarView) -> Bool {
        switch view.tag {
        case "navigationStack", "form", "uiList", "centered":
            return true
        default:
            return false
        }
    }
}

/// One-page shell — a NavigationStack so the title bar shows up
/// consistently with the multi-page case.
struct MarSinglePageView: View {
    let page: DecodedPage

    var body: some View {
        NavigationStack {
            MarPageHost(runtime: PageRuntime(page: page))
        }
    }
}
