// Runtime context shared between the interpreter and async effects.
//
// Where an async result goes: to whoever RAN the effect. A page runtime or a
// shared store opens `issuing` around `eff.run()`; every async builtin calls
// `replyTo()` synchronously inside its run and keeps that sink until the
// result arrives. Mirrors the JS effectIssuer, runEffectAs and deliverFrom in
// internal/jsserve/runtime.js. There is no "current page" slot any more: a slot answers
// "who is on screen when the response lands", which is the wrong question
// (a push, a sheet, a SwiftUI re-mount of a covered page all moved it).
//
// baseURL lets builtins build absolute URLs from the relative paths the
// user writes (`/services/Foo.bar`).

import Foundation

/// The destination of one async result. @Sendable because URLSession's
/// completion handlers carry it; @MainActor because it runs `update`.
typealias MarReply = @MainActor @Sendable (MarValue) -> Void

@MainActor
final class MarDispatcher {
    static let shared = MarDispatcher()

    /// The owner of the effects running right now, or nil outside any
    /// page or store (running `main`). Stack discipline: Cmd.toShared runs
    /// a store's update, and so the store's effects, INSIDE a page's.
    private var issuer: MarReply?
    /// True while the effects running belong to a page that is not on screen
    /// (another is pushed or presented over it). Its model is kept up to date,
    /// but it does not move the person off the page they are on or play sound
    /// behind it. Mirrors the JS refusedFromCovered in runtime.js.
    private var issuerCovered = false

    var baseURL: URL = URL(string: "http://localhost:3000")!

    private init() {}

    func issuing<T>(_ owner: @escaping MarReply, covered: Bool = false, _ body: () throws -> T) rethrows -> T {
        let outer = issuer
        let outerCovered = issuerCovered
        issuer = owner
        issuerCovered = covered
        defer {
            issuer = outer
            issuerCovered = outerCovered
        }
        return try body()
    }

    /// For an effect that acts on the screen (navigation, a sound): true, and
    /// the effect does nothing, when the page running it is covered.
    func refusedFromCovered(_ what: String) -> Bool {
        guard issuerCovered else { return false }
        #if DEBUG
        print("[mar] ignored \(what) from a page that is not on screen")
        #endif
        return true
    }

    /// Captured by an async effect when it starts, called when it ends.
    func replyTo() -> MarReply {
        if let issuer { return issuer }
        return { _ in print("[mar] a result for an effect no page or store ran was dropped") }
    }

    func resolve(path: String) -> URL? {
        URL(string: path, relativeTo: baseURL)
    }
}
