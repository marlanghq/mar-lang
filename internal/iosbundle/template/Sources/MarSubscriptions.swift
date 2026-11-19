// The app's subscriptions: one reconciler for every source the app listens
// to, as on the web (reconcileSubs and subSources in
// internal/jsserve/runtime.js). Two kinds of subscriber share it:
//
//  - the page on top of the stack. Only that page listens: a page a push or
//    a sheet covers stops listening, and when it is on top again it listens
//    again, handed the state of a state source only if it changed meanwhile
//    (PageRuntime.stateSeen).
//  - every store (App.shared), from the first screen for as long as the app
//    runs, whatever page is on screen.
//
// Sources are merged by key, so a page and a store asking for the same one
// share it, and each message goes to the subscriber whose tagger made it.

import Foundation

@MainActor
final class MarSubscriptions {
    static let shared = MarSubscriptions()

    /// A live subscription source. Different kinds (timer, keyboard, gamepad,
    /// device, sound) hold different native handles behind `stop`; all carry
    /// the current taggers, refreshed on reconcile without a restart (identity
    /// is the item content, never the tagger). Mirrors the JS `subSources`
    /// records + activeSubs map in internal/jsserve/runtime.js.
    final class LiveSub {
        var taggers: [MarValue]
        /// Who each tagger belongs to, index for index: a showing of the page
        /// on top ("page:" and its number) or a store ("shared:" and its key).
        /// Taggers are fresh closures on every reconcile, so only the owner
        /// says that a subscriber is new rather than the same one again.
        var owners: [String] = []
        let stop: () -> Void
        /// Optional live-retarget hook, called with the item's payload when
        /// the SAME key survives a reconcile. Sound.voice / Sound.glide use it to slide
        /// the held bed to a new pitch/volume (the JS `update:` on the
        /// ambient subSource); sources without live state leave it nil.
        var update: ((MarValue) -> Void)?
        /// A source that reports a state (Device, Keyboard, Gamepad): each
        /// owner that starts listening, when the source starts or by joining
        /// it, is handed the current state (see seedOwners).
        var reportsState = false
        /// Owners waiting for their seed, delivered as one dispatch on the
        /// main actor. Nil when nothing is pending.
        var seedFor: Set<String>?
        /// The record each owner was last handed, for a source that can
        /// report one it already has (the device).
        var handed: [String: MarValue] = [:]
        init(taggers: [MarValue], stop: @escaping () -> Void) { self.taggers = taggers; self.stop = stop }
    }

    private var activeSubs: [String: LiveSub] = [:]

    /// The pages SwiftUI has mounted, oldest first. Weak: a page whose host
    /// went away without an unmount must not be taken for the page on top.
    private struct Mounted { weak var page: PageRuntime? }
    private var mounted: [Mounted] = []

    /// The page whose subscriptions run now. A page is a new subscriber each
    /// time it is shown, so each showing has an owner of its own: the web's
    /// pageSubOwner. Compared by the object itself: a page's identifier can
    /// be handed to a new page once it is freed.
    private weak var subPageRuntime: PageRuntime?
    private var subPageShowing = 0

    /// Nothing subscribes before the first page mounts: on the web the first
    /// render that reaches a page starts the subscriptions, the stores' too,
    /// and a first screen waiting on the session check does not.
    private var started = false

    /// What the page on top last asked for, with its owner, and that page.
    /// Kept while the next page on top has not mounted yet (a push, a sheet,
    /// Back) and that page is still mounted: the sources both ask for keep
    /// running across the change, as they do on the web, where the next page
    /// renders in the same turn. Its messages meanwhile go nowhere (deliver),
    /// since that page is no longer on top. Once it unmounts with nothing on
    /// top (a route with no page), they stop.
    private var pageItems: [(item: MarValue, owner: String)] = []
    private weak var pageItemsOwner: PageRuntime?

    private static let pageOwnerPrefix = "page:"
    static let storeOwnerPrefix = "shared:"
    private static func isPageOwner(_ owner: String) -> Bool { owner.hasPrefix(pageOwnerPrefix) }

    func pageMounted(_ page: PageRuntime) {
        mounted.append(Mounted(page: page))
        started = true
        refresh()
    }

    func pageUnmounted(_ page: PageRuntime) {
        mounted.removeAll { $0.page == nil || $0.page === page }
        refresh()
    }

    /// The page on top: the mounted page built for the entry on top of the
    /// stack, or, with no such page, one built for no entry (the one-page
    /// shell). Nil while the page on top has not mounted yet (see pageItems).
    private func pageOnTop() -> PageRuntime? {
        mounted.removeAll { $0.page == nil }
        if let top = AppContext.shared.stack.last?.id,
           let page = mounted.last(where: { $0.page?.entry == top })?.page {
            return page
        }
        return mounted.last(where: { $0.page?.entry == nil })?.page
    }

    /// Brings the sources up to what the page on top and every store ask for
    /// right now. Called whenever one of those can have changed: a page
    /// mounts or unmounts, the top of the stack moves, the page on top or a
    /// store takes a message, a program loads.
    func refresh() {
        guard started else { return }
        var items: [(item: MarValue, owner: String)] = []
        let onTop = pageOnTop()
        // ADR 0028: a page that failed to draw will fail the same way for the
        // same model, so while it is on screen the program is quiet, the
        // stores included, as on the web (presentPageFailure tears every
        // subscription down). A working page on top starts them again.
        if let onTop, onTop.halted {
            pageItems = []
            reconcileSubs([])
            return
        }
        if let page = onTop {
            if subPageRuntime !== page {
                subPageRuntime = page
                subPageShowing += 1
            }
            let owner = Self.pageOwnerPrefix + String(subPageShowing)
            pageItems = page.subscriptionItems().map { ($0, owner) }
            pageItemsOwner = page
        } else {
            // The page that was on top is not any more, so when it is again
            // it is a new showing, handed what changed meanwhile.
            subPageRuntime = nil
            if !mounted.contains(where: { $0.page != nil && $0.page === pageItemsOwner }) {
                pageItems = []
            }
        }
        items += pageItems
        for key in MarSharedRegistry.keys {
            guard let store = MarSharedRegistry.lookup(key),
                  case .ctor("__Sub", let storeItems) = store.subscriptions() else { continue }
            items += storeItems.map { ($0, Self.storeOwnerPrefix + key) }
        }
        reconcileSubs(items)
    }

    // Start newly asked-for sources, stop ones no longer asked for, refresh
    // taggers on survivors. Identity is the item content, never the tagger.
    // Mirrors reconcileSubs + subSources in internal/jsserve/runtime.js.
    // Handles Time.every plus Keyboard / Gamepad / Device (input and
    // capability) and Sound (loop / ambient / once).
    private func reconcileSubs(_ items: [(item: MarValue, owner: String)]) {
        var desired: [String: [MarValue]] = [:]      // key -> taggers
        var desiredOwners: [String: [String]] = [:]  // key -> owner of each tagger
        var makers: [String: () -> LiveSub] = [:]    // key -> native source factory
        var payloads: [String: MarValue] = [:]       // key -> live-retarget payload

        func want(_ key: String, _ tagger: MarValue?, _ owner: String, _ make: @escaping () -> LiveSub) {
            if desired[key] == nil { desired[key] = []; desiredOwners[key] = []; makers[key] = make }
            if let tagger { desired[key]!.append(tagger); desiredOwners[key]!.append(owner) }
        }

        let allItems = items.map(\.item)
        let itemOwners = items.map(\.owner)
        if !allItems.isEmpty {
            for (item, owner) in zip(allItems, itemOwners) {
                guard case .ctor(let itag, let a) = item else { continue }
                switch itag {
                case "__SubEvery":
                    guard a.count == 2, case .duration(let ms) = a[0] else { continue }
                    let key = "every:\(ms)"
                    let seconds = Double(ms) / 1000
                    want(key, a[1], owner) { [weak self] in self?.makeTimer(key, seconds) ?? LiveSub(taggers: []) {} }
                case "__SubKeyboard":
                    want("keyboard", a.first, owner) { [weak self] in self?.makeKeyboard() ?? LiveSub(taggers: []) {} }
                case "__SubGamepad":
                    want("gamepad", a.first, owner) { [weak self] in self?.makeGamepad() ?? LiveSub(taggers: []) {} }
                case "__SubDevice":
                    want("device", a.first, owner) { [weak self] in self?.makeDevice() ?? LiveSub(taggers: []) {} }
                case "__SubSound":
                    guard a.count == 2, case .string(let mode) = a[0] else { continue }
                    let snd = a[1]
                    // voice / glide are HELD sources (docs/proposals/sound.md):
                    // their identity is the structure without volume, and for
                    // glide without pitch either (MarSound.heldKey, the JS
                    // heldKey). Handing one back with only a live parameter
                    // changed must GLIDE the running node (the survivor's
                    // update hook below), never stop+restart it: restarting
                    // clicked AND, at 60 renders/sec, stalled the frame rate.
                    // loop / once keep the full content key so a genuine
                    // change swaps the track.
                    let held = mode == "voice" || mode == "glide"
                    let key = held
                        ? "sound:\(mode):\(MarSound.heldKey(snd, withFreq: mode == "voice"))"
                        : "sound:\(mode):\(MarSound.contentKey(snd))"
                    if held { payloads[key] = snd }
                    want(key, nil, owner) { [weak self] in self?.makeSound(mode, snd) ?? LiveSub(taggers: []) {} }
                default:
                    continue
                }
            }
        }
        // Stop sources no longer desired (collect keys first, never mutate
        // while iterating).
        for key in activeSubs.keys.filter({ desired[$0] == nil }) {
            activeSubs[key]?.stop()
            activeSubs.removeValue(forKey: key)
        }
        // Start new sources; refresh taggers on survivors and let sources
        // with live state (the ambient bed) retarget to the new payload.
        for (key, taggers) in desired {
            let owners = desiredOwners[key] ?? []
            if let live = activeSubs[key] {
                let joined = live.reportsState ? owners.filter { !live.owners.contains($0) } : []
                live.taggers = taggers
                live.owners = owners
                live.handed = live.handed.filter { owners.contains($0.key) }
                if let p = payloads[key] { live.update?(p) }
                seedOwners(key, live, joined)
            } else {
                let live = makers[key]!()
                live.taggers = taggers
                live.owners = owners
                activeSubs[key] = live
                seedOwners(key, live, owners)
            }
        }
    }

    /// A message from a source, for the subscriber whose tagger made it: the
    /// page on top, or a store. A page's message waits for nothing: when its
    /// page is no longer on top it is dropped, as the page has stopped
    /// listening. A tagger that fails is reported on the page it belongs to.
    private func deliver(_ tagger: MarValue, owner: String, _ value: MarValue, _ site: String) {
        if owner.hasPrefix(Self.storeOwnerPrefix) {
            let key = String(owner.dropFirst(Self.storeOwnerPrefix.count))
            do {
                let msg = try Eval.apply(tagger, value)
                MarSharedRegistry.lookup(key)?.dispatch(msg)
            } catch {
                print("[mar] \(site) of a shared store failed: \(error.localizedDescription)")
            }
            return
        }
        guard let page = subPageRuntime, page.isOnTop else { return }
        do {
            let msg = try Eval.apply(tagger, value)
            page.dispatch(msg)
        } catch {
            page.reportSubscriptionFailure(site, error)
        }
    }

    private func readState(_ key: String) -> MarValue? {
        switch key {
        case "device": return DeviceHub.shared.currentRecord()
        case "keyboard": return KeyboardHub.shared.currentRecord()
        case "gamepad": return GamepadHub.shared.currentRecord()
        default: return nil
        }
    }

    /// Every delivery of a state source goes through here: a change to every
    /// subscriber, or a seed to the ones that started listening. What the page
    /// on top is handed is remembered on that page (PageRuntime.stateSeen).
    private func deliverState(_ key: String, _ taggers: [MarValue], _ owners: [String], _ value: MarValue) {
        var taggers = taggers, owners = owners
        // The Device record is reported on events that do not always change
        // it (coming back to the foreground, launch among them); the web has
        // no such event and reports only a change. So each subscriber is
        // handed a record once, never the one it already has.
        if key == "device", let live = activeSubs[key] {
            let fresh = Set(owners.filter { !(live.handed[$0]?.equalsMar(value) ?? false) })
            let keep = owners.indices.filter { fresh.contains(owners[$0]) }
            taggers = keep.map { taggers[$0] }
            owners = keep.map { owners[$0] }
            for o in fresh { live.handed[o] = value }
        }
        // Seen only by a page that gets it: a page no longer on top is handed
        // nothing (deliver), so it is handed this when it is shown again.
        if owners.contains(where: Self.isPageOwner), let page = subPageRuntime, page.isOnTop {
            page.noteStateSeen(key, value)
        }
        let site = "a \(key) subscription"
        for (t, o) in zip(taggers, owners) { deliver(t, owner: o, value, site) }
    }

    /// Each owner that starts listening to a state source, when the source
    /// starts or by joining one already running (a page that starts watching
    /// the device while its shared store already does), is handed the current
    /// state once, as one dispatch on the main actor. Who receives it is
    /// decided at delivery: an owner that left in between gets nothing, and an
    /// owner that started listening twice before it landed gets it once.
    /// Mirrors seedSub in runtime.js.
    private func seedOwners(_ key: String, _ live: LiveSub, _ owners: [String]) {
        guard live.reportsState, !owners.isEmpty else { return }
        if live.seedFor == nil {
            live.seedFor = []
            Task { @MainActor [weak self, weak live] in
                guard let self, let live else { return }
                let pending = live.seedFor ?? []
                live.seedFor = nil
                guard self.activeSubs[key] === live, let value = self.readState(key) else { return }
                let unchanged = self.subPageRuntime?.stateSeen(key).map { $0.equalsMar(value) } ?? false
                var taggers: [MarValue] = []
                var who: [String] = []
                for (t, o) in zip(live.taggers, live.owners) where pending.contains(o) {
                    // A page shown again already has this state: it is not
                    // handed it, and for the device it counts as handed.
                    if Self.isPageOwner(o) && unchanged {
                        if key == "device" { live.handed[o] = value }
                        continue
                    }
                    taggers.append(t)
                    who.append(o)
                }
                if !taggers.isEmpty { self.deliverState(key, taggers, who, value) }
            }
        }
        live.seedFor?.formUnion(owners)
    }

    // MARK: source factories

    private func makeTimer(_ key: String, _ seconds: Double) -> LiveSub {
        // Game-rate ticks (<= ~40fps, e.g. Time.every (millis 16)) ride a
        // CADisplayLink: a vsync-aligned game clock (ADR-0003), locked 1:1
        // on a healthy display, capped catch-up when frames drop, never an
        // unbounded backlog. A 60Hz Timer both misaligns with vsync and
        // QUEUES ticks under load: a heavy game then spirals into
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
        guard let now = MarCivil.everyTick() else { return }
        for (t, o) in zip(live.taggers, live.owners) { deliver(t, owner: o, now, "a Time.every subscription") }
    }

    // Keyboard.watch / Gamepad.watch / Device.watch: state mirrors. Each
    // registers a change listener on its hub and delivers the whole record on
    // every change; a subscriber is handed the current record when it starts
    // listening (seedOwners). Mirrors subSources.keyboardWatch / gamepadWatch /
    // deviceWatch in runtime.js.
    private func makeKeyboard() -> LiveSub { makeStateSource("keyboard", KeyboardHub.shared.add, KeyboardHub.shared.remove) }
    private func makeGamepad() -> LiveSub { makeStateSource("gamepad", GamepadHub.shared.add, GamepadHub.shared.remove) }
    private func makeDevice() -> LiveSub { makeStateSource("device", DeviceHub.shared.add, DeviceHub.shared.remove) }

    /// A state source: its hub calls back on every change, which goes to every
    /// subscriber; the state a subscriber is handed when it starts listening
    /// comes from seedOwners, not from the hub.
    private func makeStateSource(_ key: String,
                                 _ add: (@escaping () -> Void) -> Int,
                                 _ remove: @escaping (Int) -> Void) -> LiveSub {
        let tok = add { [weak self] in
            guard let self, let live = self.activeSubs[key], let value = self.readState(key) else { return }
            self.deliverState(key, live.taggers, live.owners, value)
        }
        let live = LiveSub(taggers: []) { remove(tok) }
        live.reportsState = true
        return live
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
}
