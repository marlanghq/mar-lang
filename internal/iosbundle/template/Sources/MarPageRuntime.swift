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
// Http.get) start tasks whose result comes back to the runtime that ran
// them (MarDispatcher.issuing / replyTo), covered or not, and is dropped
// once its stack entry is gone.

import Foundation
import Observation
import SwiftUI
import QuartzCore

/// Drives a closure at a requested tick interval via CADisplayLink. Used for
/// game-rate `Time.every` ticks so the loop is vsync-aligned and never
/// backlogs (a Timer at 60Hz queues up ticks when a frame overruns, which
/// spirals a heavy game into a death loop). Mirrors the web runtime's rAF
/// tick source (ADR-0003) step for step:
///  - display period within ±25% of the interval (the healthy 60Hz case):
///    lock ONE tick per painted frame: glass-smooth, ~4% slower than
///    nominal, imperceptible.
///  - faster panels (120Hz ProMotion): the accumulator fires at the correct
///    average rate (the frame-rate range below is a hint, not a guarantee).
///  - SLOWER frames (Low Power Mode, a heavy scene): 2..catchUpMax
///    back-to-back ticks per painted frame keep the GAME clock at true
///    speed: only the paint rate drops, instead of dropped frames dilating
///    game time into slow motion. Debt beyond the cap is dropped, so an
///    impossible load degrades to slow motion, never a catch-up spiral.
///    SwiftUI coalesces the burst's model writes into a single body
///    re-evaluation, so a burst costs N updates + ONE render.
@MainActor
final class DisplayLinkProxy {
    /// Max catch-up ticks per painted frame: holds true game speed down to
    /// 15 display-fps at a 16ms interval; past that the debt clamp below
    /// drops the excess.
    static let catchUpMax = 4
    var onFrame: (() -> Void)?
    private var link: CADisplayLink?
    private let interval: TimeInterval
    private var acc: TimeInterval = 0
    private var ema: TimeInterval = 0
    private var lastTs: CFTimeInterval = 0

    init(interval: TimeInterval) {
        self.interval = interval
    }

    func start() {
        let l = CADisplayLink(target: self, selector: #selector(step(_:)))
        // Ask the panel for the requested rate (16ms -> ~60Hz) so ProMotion
        // doesn't wake us at 120Hz for nothing. This is a hint, not a
        // guarantee: the accumulator below is what actually gates ticks.
        let hz = Float(min(120, max(30, Int((1.0 / max(interval, 1.0 / 120.0)).rounded()))))
        l.preferredFrameRateRange = CAFrameRateRange(minimum: 30, maximum: hz, preferred: hz)
        l.add(to: .main, forMode: .common)
        link = l
    }

    @objc private func step(_ l: CADisplayLink) {
        if lastTs == 0 { lastTs = l.timestamp; return }
        // Clamp a suspend/resume gap so returning to the foreground catches
        // up at most one frame's worth of ticks (the web clamps at 100ms).
        let d = min(l.timestamp - lastTs, 0.1)
        lastTs = l.timestamp
        ema = ema > 0 ? ema * 0.9 + d * 0.1 : d
        if abs(ema - interval) <= interval / 4 {
            onFrame?()
        } else {
            acc += d
            var n = 0
            while acc >= interval && n < Self.catchUpMax {
                acc -= interval
                n += 1
                onFrame?()
            }
            // At most one interval of residual carries to the next frame
            // (the slow-motion floor).
            if acc > interval { acc = interval }
        }
    }

    func stop() { link?.invalidate(); link = nil }
}

@MainActor
@Observable
final class PageRuntime {
    let path: String
    let title: String
    // A page from `Page.withShared` (ADR-0026) is a FUNCTION of the shared
    // models, so its four functions cannot be captured once. `shared` holds
    // the wrapper and the four become computed: every read rebuilds the page
    // ctor against the stores' CURRENT models. A plain page keeps the captured
    // values and pays nothing.
    //
    // The web hit this too and solved it the same way, by passing an accessor
    // instead of four values into buildPageEntry.
    @ObservationIgnored private let shared: DecodedPage.SharedBinding?
    @ObservationIgnored private let capturedInit: MarValue
    @ObservationIgnored private let capturedUpdate: MarValue
    @ObservationIgnored private let capturedView: MarValue
    @ObservationIgnored private let capturedSubs: MarValue

    /// The page ctor as it stands right now: rebuilt from the shared models
    /// when there are any, otherwise nil so the captured values are used.
    private var liveArgs: [MarValue]? {
        guard let shared,
              let page = try? MarSharedRegistry.resolve(shared.wrapper).page,
              case .ctor(_, let args) = page, args.count >= 4
        else { return nil }
        return args
    }

    @ObservationIgnored private var initFn: MarValue { liveArgs?[1] ?? capturedInit }
    @ObservationIgnored private var updateFn: MarValue { liveArgs?[2] ?? capturedUpdate }
    @ObservationIgnored private var viewFn: MarValue { liveArgs?[3] ?? capturedView }
    @ObservationIgnored private var subscriptionsFn: MarValue {
        guard let a = liveArgs, a.count >= 6 else { return capturedSubs }
        return a[5]
    }

    /// For Page.protected, the User to thread into init/update/view.
    /// nil for public pages.
    @ObservationIgnored let user: MarValue?

    /// For Page.dynamic / Page.dynamicProtected, the Params record
    /// captured from the URL pattern. nil for static pages.
    @ObservationIgnored let params: MarValue?

    /// The navigation entry this page belongs to (AppContext.stack), or nil
    /// for the one-page shell, which has no stack. A result from an effect
    /// this page ran is dropped once its entry has left the stack; until then
    /// it reaches this page's kept model, covered or not (ADR 0009).
    @ObservationIgnored let entry: Int?

    /// Between mount() and unmount(). A page a push covers may be unmounted
    /// by SwiftUI while its runtime, and its model, live on: it still takes
    /// its own results, but starts no subscription until it is shown again.
    @ObservationIgnored private var mounted = false

    private(set) var model: MarValue = .unit
    private(set) var lastError: String?

    /// Bumped when the shared model moves. The page's own model is untouched,
    /// so nothing else here tells SwiftUI that the view is now different.
    private(set) var touch: Int = 0

    /// Which of ADR 0020's cases the recorded failure belongs to. `dispatch`
    /// means the app is still standing (`update` never returned a new model,
    /// so what is on screen is consistent); `page` means what failed was
    /// drawing, and there is no screen to preserve.
    enum FailureSite { case dispatch, page }
    private(set) var lastErrorSite: FailureSite?

    /// A `page` failure stopped this runtime (ADR 0028). `view` is a pure
    /// function of the model, so the same model fails the same way forever:
    /// the program cannot reach a working state on its own, and every tick it
    /// keeps taking is spent behind a screen nobody will see again.
    private(set) var halted = false

    private func record(_ site: String, _ where_: FailureSite, _ message: String) {
        lastError = "\(site) failed: \(message)"
        lastErrorSite = where_
        // Also on the console. The screen is the primary channel (ADR 0020),
        // but a failure that only exists as pixels cannot be grepped, and
        // checking every example on both platforms means reading logs rather
        // than eyeballing screenshots.
        #if DEBUG
        print("[mar] FAILURE on \(path): \(lastError ?? "")")
        #endif
        // Stop, and only for the case that cannot recover. A `dispatch`
        // failure leaves a consistent screen behind it and the next message
        // may well succeed; a `page` failure will not. Cutting the
        // subscriptions here, after the message is recorded, never before:
        // is what keeps a broken screen from running a display link at sixty
        // frames a second for as long as the phone stays awake.
        if where_ == .page {
            halted = true
            MarSubscriptions.shared.refresh()
        }
    }

    /// Records a runtime error raised somewhere that cannot propagate one.
    ///
    /// Every caller of this used to be a `try?`, which drops the error whole:
    /// no message, no log, nothing recorded. A failing `subscriptions` simply
    /// stopped registering and a failing tagger simply stopped delivering, so
    /// a page whose arithmetic broke looked like "the keyboard died on its
    /// own": with no way to tell that from a bug in the framework.
    ///
    /// Swallowing is never the right default. A subscription that cannot be
    /// built is still a broken program, and the person running it deserves to
    /// know which part broke.
    private func report(_ site: String, _ error: Error) {
        // Subscriptions and taggers: the model is untouched, so the screen
        // behind the message is still a consistent one.
        record(site, .dispatch, error.localizedDescription)
    }

    /// A subscription of this page failed: building it, or a tagger. The
    /// model is untouched, so the screen behind the message is still a
    /// consistent one.
    func reportSubscriptionFailure(_ site: String, _ error: Error) {
        report(site, error)
    }

    @ObservationIgnored private var initEffectFired = false
    @ObservationIgnored private var pendingInitEffect: MarValue?

    /// Public-page constructor (no User threading, no params).
    convenience init(page: DecodedPage) {
        self.init(page: page, user: nil, params: nil, entry: nil)
    }

    /// Full constructor: covers all four page flavors. When `user`
    /// is non-nil, init/update/view are partially applied with it as
    /// the first argument; when `params` is non-nil, it's applied
    /// next. Order matches the type sigs in env.go: User first, then
    /// Params.
    init(page: DecodedPage, user: MarValue?, params: MarValue?, entry: Int? = nil) {
        self.entry = entry
        self.path = page.path
        self.title = page.title
        self.shared = page.shared
        self.capturedInit = page.initFn
        self.capturedUpdate = page.updateFn
        self.capturedView = page.viewFn
        self.capturedSubs = page.subscriptionsFn
        self.user = user
        self.params = params

        // NOTHING that touches the shared store belongs here. SwiftUI
        // evaluates `PageRuntime(page:)` on every pass of the parent's body
        // and keeps only the first instance (MarPageHost holds it in @State),
        // so an `init` with side effects runs on throwaway objects: a store
        // callback installed here would be stolen by an instance that
        // deallocates a moment later, and the queued init Cmd would be
        // consumed by a page that never appears. The wiring lives in
        // `mount`/`unmount`, which SwiftUI calls on the surviving instance.

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
            self.record("init", .page, error.localizedDescription)
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

    /// Called when the page appears on screen. Fires the init Effect the
    /// first time; the page's subscriptions run while it is the page on top
    /// (MarSubscriptions). Nothing here decides where async results go: each
    /// effect carries its issuer (runEffect), so a SwiftUI re-mount of a
    /// covered page cannot take another page's result.
    func mount() {
        mounted = true
        // One line per screen that comes up, naming what it drew. "mounted
        // with no view and no error" is the silent failure this catches: the
        // page rendered EmptyView and nobody would ever know.
        #if DEBUG
        print("[mar] mount \(path) drew=\(currentView().map { $0.tag } ?? "nothing")")
        MarLiveRuntimes.mounted(self)
        #endif
        // A page built by Page.withShared IS a function of the shared models,
        // so a change there changes what this page is, not merely what it
        // shows. Let @Observable redraw; the four functions are computed, so
        // the next read already sees the new model.
        //
        // Registered per MOUNTED page rather than in one slot on the store:
        // every mounted page that reads a store is a function of its model.
        if shared != nil {
            MarSharedRegistry.addObserver(ObjectIdentifier(self)) { [weak self] in
                self?.touch += 1
            }
        }
        if !initEffectFired, let eff = pendingInitEffect {
            initEffectFired = true
            runEffect(eff)
        }
        // The stores' init Cmds run after the first page's own init (a no-op
        // once they ran).
        MarSharedRegistry.drainInitEffects()
        MarSubscriptions.shared.pageMounted(self)
    }

    /// Called when the page leaves the screen. The model stays, and so does
    /// delivery of this page's own results while its entry is on the stack.
    func unmount() {
        mounted = false
        #if DEBUG
        MarLiveRuntimes.unmounted(self)
        #endif
        MarSharedRegistry.removeObserver(ObjectIdentifier(self))
        MarSubscriptions.shared.pageUnmounted(self)
    }

    /// A result of an effect this page ran. Dropped when the page's entry
    /// left the stack (Back past it, a replace); otherwise it is this page's
    /// message, on screen or covered.
    private func deliver(_ msg: MarValue) {
        if let entry, !AppContext.shared.holds(entry) { return }
        dispatch(msg)
    }

    func dispatch(_ msg: MarValue) {
        // A halted runtime accepts no more messages. The subscriptions are
        // already gone; this catches what was in flight when they were cut.
        if halted { return }
        do {
            let updateFnApplied = try PageRuntime.applyExtras(updateFn, user: user, params: params)
            let partial = try Eval.apply(updateFnApplied, msg)
            let result = try Eval.apply(partial, model)
            let (newModel, eff) = unwrapModelEffect(result)
            model = newModel
            // A dispatch that completed is the app working again, which is the
            // only honest reason to take the message down. A `page` failure is
            // left alone: nothing here re-ran `view`.
            if lastErrorSite == .dispatch {
                lastError = nil
                lastErrorSite = nil
            }
            runEffect(eff)
            // Only the page on top subscribes: a covered page's result must
            // not start a timer or a sound loop behind the screen the person
            // is looking at. It catches up when it is on top again.
            if mounted && isOnTop { MarSubscriptions.shared.refresh() }
        } catch {
            record("update", .dispatch, error.localizedDescription)
        }
    }

    /// Re-renders the view from the current model. Failure surfaces
    /// as `lastError`; the prior render stays visible until the
    /// model recovers.
    func currentView() -> MarView? {
        // Reading `touch` is what subscribes this view to shared-model changes.
        // @Observable tracks READS, so a counter nobody reads redraws nothing:
        // and `viewFn` being computed is not enough on its own, because the
        // page's own model never moved.
        _ = touch
        do {
            let viewFnApplied = try PageRuntime.applyExtras(viewFn, user: user, params: params)
            let v = try Eval.apply(viewFnApplied, model)
            guard case .view(let mv) = v else {
                // Name what came back. A bare "not a View" leaves the two
                // likely causes indistinguishable: a `fn` means the view was
                // applied to too few arguments, anything else means the page
                // returned the wrong thing entirely.
                record("view", .page, "returned \(Eval.typeOf(v)), not a View")
                return nil
            }
            return mv
        } catch {
            record("view", .page, error.localizedDescription)
            return nil
        }
    }

    // MARK: - Subscriptions

    /// What this page subscribes to now: `subscriptions` applied to the model.
    /// MarSubscriptions runs these while the page is on top. A failure is
    /// reported on this page and subscribes to nothing.
    func subscriptionItems() -> [MarValue] {
        do {
            let applied = try PageRuntime.applyExtras(subscriptionsFn, user: user, params: params)
            if case .ctor("__Sub", let items) = try Eval.apply(applied, model) { return items }
            return []
        } catch {
            report("subscriptions", error)
            return []
        }
    }

    /// What the page was last handed by each state source, by source key,
    /// while it was on top. A page shown again with the model it had is handed
    /// the state only if it changed while the page was covered, as if it had
    /// kept listening. Mirrors pageStateSeen in runtime.js.
    @ObservationIgnored private var pageStateSeen: [String: MarValue] = [:]

    func stateSeen(_ key: String) -> MarValue? { pageStateSeen[key] }
    func noteStateSeen(_ key: String, _ value: MarValue) { pageStateSeen[key] = value }

    // MARK: - Helpers

    /// Splits a `(model, effect)` tuple from init/update; if the user
    /// returned a bare model (no effect), wrap as `(v, Effect.none)`.
    private func unwrapModelEffect(_ v: MarValue) -> (MarValue, MarValue) {
        if case .tuple(let xs) = v, xs.count == 2 {
            return (xs[0], xs[1])
        }
        return (v, .effect(MarEffect(tag: "none") { .unit }))
    }

    /// Whether this page's entry is the top of the stack: on screen, not
    /// covered by a pushed or presented page.
    var isOnTop: Bool {
        guard let entry else { return true }
        return AppContext.shared.stack.last?.id == entry
    }

    /// Run an effect, ignoring its synchronous return value (we use
    /// effects only for side-effect dispatch). This page is the issuer for
    /// the whole run: an async builtin captures it (MarDispatcher.replyTo)
    /// and its result comes back here through `deliver`. Whether it is
    /// covered is decided once, here, for the whole run.
    private func runEffect(_ v: MarValue) {
        guard case .effect(let eff) = v else { return }
        do {
            _ = try MarDispatcher.shared.issuing({ [weak self] msg in self?.deliver(msg) }, covered: !isOnTop) {
                try eff.run()
            }
        } catch {
            record("effect [\(eff.tag)]", .dispatch, error.localizedDescription)
        }
    }
}

#if DEBUG
/// The pages that are on screen right now, bottom of the stack first.
///
/// On iOS the navigation lifecycle is not bookkeeping the runtime does: it is
/// SwiftUI's. Each entry on the NavigationStack builds its own MarPageHost,
/// whose `@State` gives it its own PageRuntime; popping destroys the pushed
/// host while the one underneath was never torn down. That is exactly why
/// "a push re-inits and Back restores" holds here, and also why it cannot be
/// checked without SwiftUI actually running.
///
/// So this observes rather than reimplements: mount() appends, unmount()
/// removes, and the resulting array IS the live stack, in stack order. It
/// exists for the lifecycle harness (AppViewModel.navLifecycle) and is
/// compiled out of Release builds.
///
/// The references are strong on purpose. A runtime whose host SwiftUI
/// destroyed but whose unmount never ran would linger here and be read as the
/// top of the stack, which is the failure showing up as a wrong answer
/// instead of as nothing at all.
@MainActor
enum MarLiveRuntimes {
    private(set) static var stack: [PageRuntime] = []

    static func mounted(_ runtime: PageRuntime) {
        stack.append(runtime)
    }

    static func unmounted(_ runtime: PageRuntime) {
        stack.removeAll { $0 === runtime }
    }

    /// Reset between program loads, so a hot reload does not leave the
    /// previous program's pages in the list.
    static func reset() {
        stack.removeAll()
    }
}

/// The route the shell is PRESENTING over the stack, or nil when the top of
/// the stack is simply pushed. Written by StackShell as it renders.
///
/// Separate from "is this path a sheet route?", which the route table can
/// answer on its own. What matters to the harness is what the shell did with
/// that answer, and those two can disagree: a shell that pushed a sheet route
/// would still look right to anything that re-derived the answer itself.
@MainActor
enum MarPresentation {
    static var current: String?
}
#endif
