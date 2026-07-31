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
import QuartzCore

/// Drives a closure at a requested tick interval via CADisplayLink. Used for
/// game-rate `Time.every` ticks so the loop is vsync-aligned and never
/// backlogs (a Timer at 60Hz queues up ticks when a frame overruns, which
/// spirals a heavy game into a death loop). Mirrors the web runtime's rAF
/// tick source (ADR-0003) step for step:
///  - display period within ±25% of the interval (the healthy 60Hz case):
///    lock ONE tick per painted frame — glass-smooth, ~4% slower than
///    nominal, imperceptible.
///  - faster panels (120Hz ProMotion): the accumulator fires at the correct
///    average rate (the frame-rate range below is a hint, not a guarantee).
///  - SLOWER frames (Low Power Mode, a heavy scene): 2..catchUpMax
///    back-to-back ticks per painted frame keep the GAME clock at true
///    speed — only the paint rate drops, instead of dropped frames dilating
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
        // guarantee — the accumulator below is what actually gates ticks.
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
    // model, so its four functions cannot be captured once. `shared` holds the
    // builder and the four become computed: every read rebuilds the page ctor
    // against the store's CURRENT model. A plain page keeps the captured
    // values and pays nothing.
    //
    // The web hit this too and solved it the same way, by passing an accessor
    // instead of four values into buildPageEntry.
    @ObservationIgnored private let shared: DecodedPage.SharedBinding?
    @ObservationIgnored private let capturedInit: MarValue
    @ObservationIgnored private let capturedUpdate: MarValue
    @ObservationIgnored private let capturedView: MarValue
    @ObservationIgnored private let capturedSubs: MarValue

    /// The page ctor as it stands right now: rebuilt from the shared model
    /// when there is one, otherwise nil so the captured values are used.
    private var liveArgs: [MarValue]? {
        guard let shared,
              let store = MarSharedRegistry.lookup(shared.key),
              let page = try? Eval.apply(shared.builder, store.model),
              case .ctor(_, let args, _) = page, args.count >= 4
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
    }

    /// Records a runtime error raised somewhere that cannot propagate one.
    ///
    /// Every caller of this used to be a `try?`, which drops the error whole:
    /// no message, no log, nothing recorded. A failing `subscriptions` simply
    /// stopped registering and a failing tagger simply stopped delivering, so
    /// a page whose arithmetic broke looked like "the keyboard died on its
    /// own" — with no way to tell that from a bug in the framework.
    ///
    /// Swallowing is never the right default. A subscription that cannot be
    /// built is still a broken program, and the person running it deserves to
    /// know which part broke.
    private func report(_ site: String, _ error: Error) {
        // Subscriptions and taggers: the model is untouched, so the screen
        // behind the message is still a consistent one.
        record(site, .dispatch, error.localizedDescription)
    }

    /// Applies a subscription tagger, reporting instead of swallowing.
    /// Which store, if any, owns a delivered tagger. Rebuilt on every
    /// reconcile, because a page can gain or lose shared subscriptions as the
    /// shared model moves.
    @ObservationIgnored private var sharedTaggerOwners: [String: String] = [:]

    /// A stable identity for a tagger value. Taggers are closures, and Swift
    /// closures are not Equatable — the pointer is what the reconciler already
    /// keys survivors on, so it is the honest choice here too.
    private static func taggerIdentity(_ v: MarValue) -> String {
        if case .fn(let f) = v { return String(UInt(bitPattern: ObjectIdentifier(f).hashValue)) }
        return String(describing: v)
    }

    private func applyTagger(_ tagger: MarValue, _ value: MarValue, _ site: String) -> MarValue? {
        do {
            let msg = try Eval.apply(tagger, value)
            // Route by tagger, not by source: one key can have owners on both
            // sides, and the page's update would not understand a shared msg.
            if let key = sharedTaggerOwners[Self.taggerIdentity(tagger)] {
                MarSharedRegistry.lookup(key)?.dispatch(msg)
                return nil
            }
            return msg
        } catch {
            report(site, error)
            return nil
        }
    }

    @ObservationIgnored private var initEffectFired = false
    @ObservationIgnored private var pendingInitEffect: MarValue?
    @ObservationIgnored private var activeSubs: [String: LiveSub] = [:]

    /// A live subscription source. Different kinds (timer, keyboard, gamepad,
    /// device, sound) hold different native handles behind `stop`; all carry
    /// the current taggers, refreshed on reconcile without a restart (identity
    /// is the item content, never the tagger). Mirrors the JS `subSources`
    /// records + activeSubs map in internal/jsserve/runtime.js.
    final class LiveSub {
        var taggers: [MarValue]
        let stop: () -> Void
        /// Optional live-retarget hook, called with the item's payload when
        /// the SAME key survives a reconcile. Sound.voice / Sound.glide use it to slide
        /// the held bed to a new pitch/volume (the JS `update:` on the
        /// ambient subSource); sources without live state leave it nil.
        var update: ((MarValue) -> Void)?
        init(taggers: [MarValue], stop: @escaping () -> Void) { self.taggers = taggers; self.stop = stop }
    }

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

    /// Called once when the page first appears on screen. Fires the
    /// init Effect and wires the dispatcher so async effects can
    /// post Msgs back into this runtime.
    ///
    /// `currentOwner` is stamped with this instance's identity so
    /// `unmount` can tell whether the dispatcher slot is still
    /// ours when SwiftUI eventually tears us down — see the
    /// comment on MarDispatcher.currentOwner for why that matters.
    func mount() {
        // One line per screen that comes up, naming what it drew. "mounted
        // with no view and no error" is the silent failure this catches: the
        // page rendered EmptyView and nobody would ever know.
        #if DEBUG
        print("[mar] mount \(path) drew=\(currentView().map { $0.tag } ?? "nothing")")
        #endif
        MarDispatcher.shared.currentOwner = ObjectIdentifier(self)
        MarDispatcher.shared.current = { [weak self] msg in
            self?.dispatch(msg)
        }
        // A page built by Page.withShared IS a function of the shared model,
        // so a change there changes what this page is — not merely what it
        // shows. Re-reconcile and let @Observable redraw; the four functions
        // are computed, so the next read already sees the new model.
        //
        // Registered per MOUNTED page rather than in one slot on the store: a
        // pushed stack keeps the screens beneath it alive, and every one of
        // them is reading the same model.
        if shared != nil {
            MarSharedRegistry.addObserver(ObjectIdentifier(self)) { [weak self] in
                guard let self else { return }
                self.reconcileSubs()
                self.touch += 1
            }
            MarSharedRegistry.drainInitEffects()
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
        MarSharedRegistry.removeObserver(ObjectIdentifier(self))
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
            // A dispatch that completed is the app working again, which is the
            // only honest reason to take the message down. A `page` failure is
            // left alone: nothing here re-ran `view`.
            if lastErrorSite == .dispatch {
                lastError = nil
                lastErrorSite = nil
            }
            runEffect(eff)
            reconcileSubs()
        } catch MarRuntimeError.noMatch {
            // A stale message: an async effect (a Service.call, Cmd.perform,
            // or subscription) started by a PREVIOUS page resolved AFTER we
            // navigated away, so this page received a message it can't match.
            // Case exhaustiveness is checked at compile time, so a no-match
            // reaching dispatch can only be a message meant for a torn-down
            // page — drop it silently instead of surfacing it as an error.
            // Mirrors the guard in internal/jsserve/runtime.js.
        } catch {
            record("update", .dispatch, error.localizedDescription)
        }
    }

    /// Re-renders the view from the current model. Failure surfaces
    /// as `lastError`; the prior render stays visible until the
    /// model recovers.
    func currentView() -> MarView? {
        // Reading `touch` is what subscribes this view to shared-model changes.
        // @Observable tracks READS, so a counter nobody reads redraws nothing —
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
    //
    // Reconcile the live subscription sources against `subscriptions model`.
    // Run after init (mount) and after every dispatch — the same funnel as the
    // JS runtime's render(): start newly-returned sources, stop ones no longer
    // returned, refresh taggers on survivors. Identity is the item content,
    // never the tagger. Mirrors reconcileSubs + subSources in
    // internal/jsserve/runtime.js. Handles Time.every plus the web-first
    // sources: Keyboard / Gamepad / Device (input + capability) and Sound
    // (loop / ambient / once). Compile-checked, verified on a real iOS build.
    private func reconcileSubs() {
        var desired: [String: [MarValue]] = [:]      // key -> taggers
        var makers: [String: () -> LiveSub] = [:]    // key -> native source factory
        var payloads: [String: MarValue] = [:]       // key -> live-retarget payload

        func want(_ key: String, _ tagger: MarValue?, _ make: @escaping () -> LiveSub) {
            if desired[key] == nil { desired[key] = []; makers[key] = make }
            if let tagger { desired[key]!.append(tagger) }
        }

        var subVal: MarValue?
        do {
            let applied = try PageRuntime.applyExtras(subscriptionsFn, user: user, params: params)
            subVal = try Eval.apply(applied, model)
        } catch {
            report("subscriptions", error)
        }

        // A shared store subscribes too, and its messages belong to ITS update
        // — not to whichever page happened to mount the source. The reconciler
        // groups by key across owners, so the destination has to travel with
        // each TAGGER rather than sit on the registration. The web hit exactly
        // this and solved it the same way (deliverSub in runtime.js).
        var sharedTaggers: [String: String] = [:]   // tagger identity -> store key
        var allItems: [MarValue] = []
        if let subVal, case .ctor("__Sub", let items, _) = subVal {
            allItems.append(contentsOf: items)
        }
        if let shared, let store = MarSharedRegistry.lookup(shared.key),
           case .ctor("__Sub", let items, _) = store.subscriptions() {
            for item in items {
                if case .ctor(_, let a, _) = item, let tagger = a.last {
                    sharedTaggers[Self.taggerIdentity(tagger)] = shared.key
                }
            }
            allItems.append(contentsOf: items)
        }
        self.sharedTaggerOwners = sharedTaggers

        if !allItems.isEmpty {
            let items = allItems
            for item in items {
                guard case .ctor(let itag, let a, _) = item else { continue }
                switch itag {
                case "__SubEvery":
                    guard a.count == 2, case .duration(let seconds) = a[0] else { continue }
                    let key = "every:\(seconds)"
                    want(key, a[1]) { [weak self] in self?.makeTimer(key, seconds) ?? LiveSub(taggers: []) {} }
                case "__SubKeyboard":
                    want("keyboard", a.first) { [weak self] in self?.makeKeyboard() ?? LiveSub(taggers: []) {} }
                case "__SubGamepad":
                    want("gamepad", a.first) { [weak self] in self?.makeGamepad() ?? LiveSub(taggers: []) {} }
                case "__SubDevice":
                    want("device", a.first) { [weak self] in self?.makeDevice() ?? LiveSub(taggers: []) {} }
                case "__SubSound":
                    guard a.count == 2, case .string(let mode) = a[0] else { continue }
                    let snd = a[1]
                    // voice / glide are HELD sources (docs/proposals/sound.md):
                    // their identity is the structure without volume, and for
                    // glide without pitch either (MarSound.heldKey, the JS
                    // heldKey). Handing one back with only a live parameter
                    // changed must GLIDE the running node (the survivor's
                    // update hook below), never stop+restart it — restarting
                    // clicked AND, at 60 renders/sec, stalled the frame rate.
                    // loop / once keep the full content key so a genuine
                    // change swaps the track.
                    let held = mode == "voice" || mode == "glide"
                    let key = held
                        ? "sound:\(mode):\(MarSound.heldKey(snd, withFreq: mode == "voice"))"
                        : "sound:\(mode):\(MarSound.contentKey(snd))"
                    if held { payloads[key] = snd }
                    want(key, nil) { [weak self] in self?.makeSound(mode, snd) ?? LiveSub(taggers: []) {} }
                default:
                    continue
                }
            }
        }
        // Stop sources no longer desired (collect keys first — never mutate
        // while iterating).
        for key in activeSubs.keys.filter({ desired[$0] == nil }) {
            activeSubs[key]?.stop()
            activeSubs.removeValue(forKey: key)
        }
        // Start new sources; refresh taggers on survivors and let sources
        // with live state (the ambient bed) retarget to the new payload.
        for (key, taggers) in desired {
            if let live = activeSubs[key] {
                live.taggers = taggers
                if let p = payloads[key] { live.update?(p) }
            } else {
                let live = makers[key]!()
                live.taggers = taggers
                activeSubs[key] = live
            }
        }
    }

    // MARK: source factories

    private func makeTimer(_ key: String, _ seconds: Double) -> LiveSub {
        // Game-rate ticks (<= ~40fps, e.g. Time.every (millis 16)) ride a
        // CADisplayLink: a vsync-aligned game clock (ADR-0003) — locked 1:1
        // on a healthy display, capped catch-up when frames drop, never an
        // unbounded backlog. A 60Hz Timer both misaligns with vsync and
        // QUEUES ticks under load — a heavy game then spirals into
        // unplayability as the queue never drains. This mirrors the web
        // runtime's rAF tick source for Time.every ≤20ms.
        if seconds <= 0.025 {
            let proxy = DisplayLinkProxy(interval: seconds)
            proxy.onFrame = { [weak self] in self?.fireTime(key) }
            proxy.start()
            return LiveSub(taggers: []) { proxy.stop() }
        }
        // Slower clocks stay on a Timer (a display link can't do long
        // intervals and would freeze in a backgrounded tab). 1ms floor so
        // Time.millis can still drive sub-second ticks.
        let timer = Timer.scheduledTimer(withTimeInterval: max(seconds, 0.001), repeats: true) { [weak self] _ in
            Task { @MainActor in self?.fireTime(key) }
        }
        return LiveSub(taggers: []) { timer.invalidate() }
    }
    private func fireTime(_ key: String) {
        guard let live = activeSubs[key] else { return }
        let now = MarValue.time(Int(Date().timeIntervalSince1970 * 1000))
        for t in live.taggers { if let m = applyTagger(t, now, "a Time.every subscription") { dispatch(m) } }
    }

    // Keyboard.watch / Gamepad.watch — held-state mirrors. Each registers a
    // change listener on its hub (which seeds the current snapshot on subscribe,
    // deferred, like Device.watch) and delivers the whole record on every
    // change. Mirrors subSources.keyboardWatch / gamepadWatch in runtime.js.
    private func makeKeyboard() -> LiveSub {
        let tok = KeyboardHub.shared.add { [weak self] in
            guard let self, let live = self.activeSubs["keyboard"] else { return }
            let rec = KeyboardHub.shared.currentRecord()
            for t in live.taggers { if let m = self.applyTagger(t, rec, "a keyboard subscription") { self.dispatch(m) } }
        }
        return LiveSub(taggers: []) { KeyboardHub.shared.remove(tok) }
    }

    private func makeGamepad() -> LiveSub {
        let tok = GamepadHub.shared.add { [weak self] in
            guard let self, let live = self.activeSubs["gamepad"] else { return }
            let rec = GamepadHub.shared.currentRecord()
            for t in live.taggers { if let m = self.applyTagger(t, rec, "a gamepad subscription") { self.dispatch(m) } }
        }
        return LiveSub(taggers: []) { GamepadHub.shared.remove(tok) }
    }

    private func makeDevice() -> LiveSub {
        let tok = DeviceHub.shared.add { [weak self] in
            guard let self, let live = self.activeSubs["device"] else { return }
            let rec = DeviceHub.shared.currentRecord()
            for t in live.taggers { if let m = self.applyTagger(t, rec, "a device subscription") { self.dispatch(m) } }
        }
        return LiveSub(taggers: []) { DeviceHub.shared.remove(tok) }
    }

    private func makeSound(_ mode: String, _ snd: MarValue) -> LiveSub {
        switch mode {
        case "loop":
            let handle = MarSound.shared.startLoop(snd)
            return LiveSub(taggers: []) { MarSound.shared.stop(handle) }
        case "voice", "glide":
            let handle = MarSound.shared.startHeld(snd)
            let live = LiveSub(taggers: []) { MarSound.shared.stop(handle) }
            // Same held key surviving a reconcile = slide the running node to
            // the new levels, and for glide to the new pitch as well (the
            // racer's engine note). What the key covers decides which.
            live.update = { MarSound.shared.glideTo(handle, $0, promptLevel: mode == "voice") }
            return live
        default:
            let handle = MarSound.shared.startOnce(snd)
            return LiveSub(taggers: []) { MarSound.shared.stop(handle) }
        }
    }

    /// Invalidate every live source. Called from unmount() so leaving a page
    /// stops its subscriptions (mirrors the JS reconciler dropping the page's
    /// keys on navigation).
    private func teardownSubs() {
        for (_, live) in activeSubs { live.stop() }
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
            record("effect [\(eff.tag)]", .dispatch, error.localizedDescription)
        }
    }
}
