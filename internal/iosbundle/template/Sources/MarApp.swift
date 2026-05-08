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
    // PageRuntime is @Observable, so just holding a let-binding here
    // is enough for SwiftUI to track property reads. @State would
    // freeze the initial value (ignoring later inits with a fresh
    // runtime), which is the wrong semantics when the parent re-
    // creates MarPageHost on page navigation.
    let runtime: PageRuntime

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
