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
    @ObservationIgnored let subscriptionsFn: MarValue

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
    @ObservationIgnored private var activeSubs: [String: (timer: Timer, taggers: [MarValue])] = [:]

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
        self.subscriptionsFn = page.subscriptionsFn
        self.user = user
        self.params = params

        // Run init: applyExtras threads User then Params depending on
        // the page flavor, and the result IS the (Model, Effect)
        // tuple. Static public pages get neither extra, matching
        // Page.create's `init : (Model, Effect)` (init is a value;
        // there is no vestigial unit argument).
        do {
            let initial = try PageRuntime.applyExtras(initFn, user: user, params: params)
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
        reconcileSubs()
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
        teardownSubs()
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
            reconcileSubs()
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

    // MARK: - Subscriptions
    //
    // Reconcile the live subscription sources against `subscriptions model`.
    // Run after init (mount) and after every dispatch — the same funnel as the
    // JS runtime's render(): start newly-returned sources, stop ones no longer
    // returned, refresh taggers on survivors. Identity is the interval (the
    // data), never the tagger. Mirrors reconcileSubs in internal/jsserve/runtime.js.
    // NOTE: not compiled in CI (no xcode) — verify on a real iOS build.
    private func reconcileSubs() {
        var desired: [String: (seconds: Int, taggers: [MarValue])] = [:]
        if let applied = try? PageRuntime.applyExtras(subscriptionsFn, user: user, params: params),
           let subVal = try? Eval.apply(applied, model),
           case .ctor(let tag, let items, _) = subVal, tag == "__Sub" {
            for item in items {
                guard case .ctor(let itag, let a, _) = item, itag == "__SubEvery",
                      a.count == 2, case .duration(let seconds) = a[0] else { continue }
                let key = "timeEvery:\(seconds)"
                if desired[key] == nil { desired[key] = (seconds, []) }
                desired[key]!.taggers.append(a[1])
            }
        }
        // Stop sources no longer desired (collect keys first — never mutate
        // the dictionary while iterating it).
        for key in activeSubs.keys.filter({ desired[$0] == nil }) {
            activeSubs[key]?.timer.invalidate()
            activeSubs.removeValue(forKey: key)
        }
        // Start new sources; refresh taggers on survivors.
        for (key, g) in desired {
            if var existing = activeSubs[key] {
                existing.taggers = g.taggers
                activeSubs[key] = existing
            } else {
                let interval = TimeInterval(max(g.seconds, 1))
                let timer = Timer.scheduledTimer(withTimeInterval: interval, repeats: true) { [weak self] _ in
                    Task { @MainActor in self?.fireSub(key) }
                }
                activeSubs[key] = (timer, g.taggers)
            }
        }
    }

    /// Fire one subscription key: read the wall clock and deliver each
    /// tagger's Msg into the loop.
    private func fireSub(_ key: String) {
        guard let rec = activeSubs[key] else { return }
        let now = MarValue.time(Int(Date().timeIntervalSince1970 * 1000))
        for tagger in rec.taggers {
            if let msg = try? Eval.apply(tagger, now) {
                dispatch(msg)
            }
        }
    }

    /// Invalidate every live timer. Called from unmount() so leaving a page
    /// stops its subscriptions (mirrors the JS reconciler dropping the page's
    /// keys on navigation).
    private func teardownSubs() {
        for (_, rec) in activeSubs { rec.timer.invalidate() }
        activeSubs.removeAll()
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
