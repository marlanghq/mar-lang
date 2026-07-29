// How a runtime failure reaches the person using the app (ADR 0020).
//
// Every runtime error a checked Mar program can still raise is a bug:
// runaway recursion, a `case` over literals with no catch-all, or an integer
// leaving the 53-bit range. All three are deterministic, so the message never
// offers a retry the way `Service.errorToString` does — there the network
// really does come back, and here nothing changes until someone fixes it.
//
// The strings must stay identical to the JS runtime's; see
// internal/jsserve/runtime.js and the test that compares the two.

import SwiftUI

enum MarFailure {
    static let title = "This app has a critical bug."

    /// Which of the three cases applies, which is only a question of whether
    /// anything is left to stand on.
    enum Kind {
        /// update, a tagger, or an effect. The model was never replaced, so
        /// the screen behind the message is a consistent one and stays up.
        case dispatch
        /// view or init, with an entry to go back to.
        case page
        /// view or init on the first screen. Nothing to return to, and
        /// reloading would re-run the init that just failed.
        case stuck
    }

    static func body(_ kind: Kind) -> [String] {
        switch kind {
        case .dispatch:
            return ["Something unexpected happened and your request could not be completed.",
                    "Nothing was changed. The app is back at its last consistent state."]
        case .page:
            return ["Something unexpected happened and this screen could not be shown.",
                    "Go back to return the app to its last consistent state."]
        case .stuck:
            return ["Something unexpected happened and this screen could not be shown.",
                    "The app cannot continue until a developer fixes it."]
        }
    }
}

/// The message itself. Deliberately plain: no animation, nothing that moves.
/// A `view` that throws throws again every frame, so any motion here would
/// become a strobe.
struct MarFailureView: View {
    let kind: MarFailure.Kind
    /// The raw "site failed: message" text, shown only in development.
    let detail: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            #if DEBUG
            // Development gets the failure itself instead of the copy: the
            // person reading it is the one who can fix it.
            Text(detail)
                .font(.system(.footnote, design: .monospaced))
                .foregroundStyle(.red)
            #else
            Text(MarFailure.title)
                .font(.headline)
            ForEach(MarFailure.body(kind), id: \.self) { line in
                Text(line).font(.callout)
            }
            if kind == .page {
                // Its own button rather than the navigation bar's: the bar's
                // title and toolbar come from the same `view` that threw.
                MarFailureBackButton()
            }
            #endif
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(16)
        .background(Color.red.opacity(0.08))
        .clipShape(RoundedRectangle(cornerRadius: 12))
    }
}

private struct MarFailureBackButton: View {
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        Button("Go back") { dismiss() }
            .buttonStyle(.bordered)
            .padding(.top, 4)
    }
}
