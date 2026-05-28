// MVU loop per page. Mirrors the JS `mountPages` per-page record
// (model + init + update + view + initEffect) but exposes it as an
// @Observable type so SwiftUI re-renders on each model change.
//
// Lifecycle:
//
//  - init(): apply initFn(), unwrap (model, effect) tuple, store
//    model + remember the init effect to fire on the first render.
//
//  - dispatch(msg): apply update msg model, unwrap tuple, swap
//    model, fire any post-update effect.
//
//  - currentView(): apply viewFn(model), unwrap to MarView for
//    the renderer.
//
// Effects are run synchronously; async ones (Service.call,
// Http.get) start tasks that come back via MarDispatcher.

import Foundation
import Observation
import SwiftUI

@MainActor
@Observable
final class PageRuntime {
    let path: String
    let title: String
    @ObservationIgnored let initFn: MarValue
    @ObservationIgnored let updateFn: MarValue
    @ObservationIgnored let viewFn: MarValue

    /// For Page.protected, the User to thread into init/update/view.
    /// nil for public pages.
    @ObservationIgnored let user: MarValue?

    /// For Page.dynamic / Page.dynamicProtected, the Params record
    /// captured from the URL pattern. nil for static pages.
    @ObservationIgnored let params: MarValue?

    private(set) var model: MarValue = .unit
    private(set) var lastError: String?

    @ObservationIgnored private var initEffectFired = false
    @ObservationIgnored private var pendingInitEffect: MarValue?

    /// Public-page constructor (no User threading, no params).
    convenience init(page: DecodedPage) {
        self.init(page: page, user: nil, params: nil)
    }

    /// Full constructor — covers all four page flavors. When `user`
    /// is non-nil, init/update/view are partially applied with it as
    /// the first argument; when `params` is non-nil, it's applied
    /// next. Order matches the type sigs in env.go: User first, then
    /// Params.
    init(page: DecodedPage, user: MarValue?, params: MarValue?) {
        self.path = page.path
        self.title = page.title
        self.initFn = page.initFn
        self.updateFn = page.updateFn
        self.viewFn = page.viewFn
        self.user = user
        self.params = params

        // Run init: applyExtras(initFn)(unit) → tuple(Model, Effect).
        // applyExtras threads User then Params depending on the page
        // flavor; static public pages get neither and init takes only
        // unit, matching Page.create's `init : () -> (Model, Effect)`.
        do {
            let initFnApplied = try PageRuntime.applyExtras(initFn, user: user, params: params)
            let initial = try Eval.apply(initFnApplied, .unit)
            let (m, eff) = unwrapModelEffect(initial)
            self.model = m
            self.pendingInitEffect = eff
        } catch {
            self.lastError = "init failed: \(error.localizedDescription)"
        }
    }

    /// Apply User then Params to `fn` (skipping each when the
    /// corresponding value is nil). Mirrors the JS runtime's
    /// applyExtras helper so the same handler code typechecks and
    /// runs identically across the two runtimes.
    private static func applyExtras(_ fn: MarValue, user: MarValue?, params: MarValue?) throws -> MarValue {
        var f = fn
        if let user { f = try Eval.apply(f, user) }
        if let params { f = try Eval.apply(f, params) }
        return f
    }

    /// Called once when the page first appears on screen. Fires the
    /// init Effect and wires the dispatcher so async effects can
    /// post Msgs back into this runtime.
    ///
    /// `currentOwner` is stamped with this instance's identity so
    /// `unmount` can tell whether the dispatcher slot is still
    /// ours when SwiftUI eventually tears us down — see the
    /// comment on MarDispatcher.currentOwner for why that matters.
    func mount() {
        MarDispatcher.shared.currentOwner = ObjectIdentifier(self)
        MarDispatcher.shared.current = { [weak self] msg in
            self?.dispatch(msg)
        }
        if !initEffectFired, let eff = pendingInitEffect {
            initEffectFired = true
            runEffect(eff)
        }
    }

    /// Called when the page leaves the screen. Detaches the
    /// dispatcher so a stale closure can't dispatch into a torn-down
    /// page — but ONLY when the slot is still ours. After a
    /// navigation, the incoming page's `mount` may have already
    /// fired before our `unmount` runs (SwiftUI's lifecycle order
    /// for `.id`-swap views is "onAppear new, then onDisappear
    /// old"). Without this guard we'd wipe the incoming page's
    /// freshly-set dispatcher, breaking every async msg that
    /// page's init effect posted.
    func unmount() {
        if MarDispatcher.shared.currentOwner == ObjectIdentifier(self) {
            MarDispatcher.shared.currentOwner = nil
            MarDispatcher.shared.current = nil
        }
    }

    func dispatch(_ msg: MarValue) {
        do {
            let updateFnApplied = try PageRuntime.applyExtras(updateFn, user: user, params: params)
            let partial = try Eval.apply(updateFnApplied, msg)
            let result = try Eval.apply(partial, model)
            let (newModel, eff) = unwrapModelEffect(result)
            model = newModel
            runEffect(eff)
        } catch {
            lastError = "update failed: \(error.localizedDescription)"
        }
    }

    /// Re-renders the view from the current model. Failure surfaces
    /// as `lastError`; the prior render stays visible until the
    /// model recovers.
    func currentView() -> MarView? {
        do {
            let viewFnApplied = try PageRuntime.applyExtras(viewFn, user: user, params: params)
            let v = try Eval.apply(viewFnApplied, model)
            guard case .view(let mv) = v else {
                lastError = "view returned non-View value"
                return nil
            }
            return mv
        } catch {
            lastError = "view failed: \(error.localizedDescription)"
            return nil
        }
    }

    // MARK: - Helpers

    /// Splits a `(model, effect)` tuple from init/update; if the user
    /// returned a bare model (no effect), wrap as `(v, Effect.none)`.
    private func unwrapModelEffect(_ v: MarValue) -> (MarValue, MarValue) {
        if case .tuple(let xs) = v, xs.count == 2 {
            return (xs[0], xs[1])
        }
        return (v, .effect(MarEffect(tag: "none") { .unit }))
    }

    /// Run an effect, ignoring its synchronous return value (we use
    /// effects only for side-effect dispatch). Async effects
    /// (Service.call, Http.get) post Msgs back via MarDispatcher.
    private func runEffect(_ v: MarValue) {
        guard case .effect(let eff) = v else { return }
        do {
            _ = try eff.run()
        } catch {
            lastError = "effect [\(eff.tag)] failed: \(error.localizedDescription)"
        }
    }
}
