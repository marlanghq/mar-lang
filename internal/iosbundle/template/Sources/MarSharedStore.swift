import Foundation
import Observation

/// Client state that outlives navigation (ADR-0026), mirroring the JS runtime's
/// `sharedStores` in internal/jsserve/runtime.js.
///
/// The web keys a store by the IDENTITY OF THE DEF VALUE: `App.shared { ... }`
/// is a top-level binding, so it evaluates once and everyone holding it holds
/// the same object. `MarValue` is an enum, a value type, so object identity
/// does not exist here. Instead `App.shared` stamps a key onto the ctor it
/// returns, in evaluation order, exactly the way the web's `__originKey`
/// (`shared:0`, `shared:1`) does. Top-level evaluation is dependency-sorted and
/// therefore deterministic, so the same def gets the same key every run.
///
/// The three builtins are deliberately thin: all of the behaviour is here, so
/// there is one place to read when the two runtimes are compared.
@Observable
final class MarSharedStore {
    /// The store's own model, updated only through `dispatch`.
    private(set) var model: MarValue = .unit

    @ObservationIgnored let key: String
    @ObservationIgnored private let updateFn: MarValue
    @ObservationIgnored private let subscriptionsFn: MarValue

    init(key: String, model: MarValue, updateFn: MarValue, subscriptionsFn: MarValue) {
        self.key = key
        self.model = model
        self.updateFn = updateFn
        self.subscriptionsFn = subscriptionsFn
    }

    /// One message through the store's own update. Returns nothing: callers
    /// react through the registry's observers, so a page cannot accidentally
    /// read a model that a later message has already replaced.
    func dispatch(_ msg: MarValue) {
        let out: MarValue
        do {
            out = try Eval.apply(Eval.apply(updateFn, msg), model)
        } catch {
            // Unlike a page msg, a shared msg has no "arrived after navigation"
            // excuse: the store is page-independent and always live, so a
            // failure here is a real one and is reported rather than swallowed.
            // Same reasoning as sharedDispatch in runtime.js.
            print("[mar] shared update failed: \(error.localizedDescription)")
            return
        }
        // update : msg -> model -> (model, Cmd)
        var effect: MarValue? = nil
        if case .tuple(let parts) = out, parts.count >= 1 {
            model = parts[0]
            if parts.count >= 2 { effect = parts[1] }
        } else {
            model = out
        }
        MarSharedRegistry.preserve(key: key, model: model)
        if let effect { MarSharedRegistry.runEffect(effect) }
        MarSharedRegistry.notifyChanged()
    }

    /// The store's subscriptions, resolved against the CURRENT model. Callers
    /// tag each item so a delivered message comes back to `dispatch` rather
    /// than to whichever page happened to mount the subscription: the web hit
    /// exactly this and had to carry the destination per tagger.
    func subscriptions() -> MarValue {
        guard case .fn = subscriptionsFn else { return subscriptionsFn }
        return (try? Eval.apply(subscriptionsFn, model)) ?? .unit
    }
}

/// Every store in the app, by key. A global because the stores outlive every
/// page and the app has exactly one of each: the same reason the web keeps a
/// module-level Map.
enum MarSharedRegistry {
    private static var stores: [String: MarSharedStore] = [:]
    private static var nextIndex = 0

    /// Models kept across a program reload, keyed by DECLARATION ORDER rather
    /// than by store object. Mirrors `preservedSharedModels` on the web: a
    /// `mar dev` save should not empty your cart or sign you out, exactly as
    /// it does not reset a page model.
    private static var preservedModels: [String: MarValue] = [:]

    /// Cmds returned by a store's `init`, deferred until the first page mounts
    /// so the Service.call that fills the store dispatches into a live loop
    /// rather than into a dead one. The web defers these the same way
    /// (`pendingSharedCmds`).
    private static var pendingInitEffects: [MarValue] = []

    /// Mounted pages that want to hear about a shared change, keyed by the
    /// page runtime's identity. A SET rather than one slot, because a pushed
    /// stack keeps the screens beneath it alive and every one of them is a
    /// function of this model. Registered by `mount`, dropped by `unmount`:
    /// never by `init`, which SwiftUI runs on throwaway instances every time
    /// a parent view's body is re-evaluated.
    private static var observers: [ObjectIdentifier: () -> Void] = [:]

    /// Called before a program's top level is evaluated. The keys are minted
    /// in evaluation order, so the counter has to start over or the second
    /// run mints `shared:1` for the same def and every page ends up bound to
    /// a brand-new empty store. The MODELS survive: that is the whole point
    /// of keying them by declaration order. Mirrors `marReload` on the web.
    static func beginProgramLoad() {
        stores = [:]
        nextIndex = 0
        pendingInitEffects = []
    }

    /// Mints the key for one `App.shared` call, in evaluation order.
    static func nextKey() -> String {
        defer { nextIndex += 1 }
        return "shared:\(nextIndex)"
    }

    /// The store for a key, created once per program load. A reload finds the
    /// preserved model and keeps it, which is what makes the key positional
    /// rather than a fresh UUID.
    static func store(
        key: String, initFn: MarValue, updateFn: MarValue, subscriptionsFn: MarValue
    ) -> MarSharedStore {
        if let existing = stores[key] { return existing }
        // `init` is a VALUE of `(model, Cmd)`, not a function: the same shape
        // Page.create uses, so the unwrap is the same too.
        var initial: MarValue = initFn
        var initEffect: MarValue? = nil
        if case .tuple(let parts) = initFn, parts.count >= 1 {
            initial = parts[0]
            if parts.count >= 2 { initEffect = parts[1] }
        }
        let preserved = preservedModels[key]
        let made = MarSharedStore(
            key: key,
            model: preserved ?? initial,
            updateFn: updateFn,
            subscriptionsFn: subscriptionsFn)
        stores[key] = made
        if preserved == nil {
            preservedModels[key] = made.model
            if let initEffect { pendingInitEffects.append(initEffect) }
        }
        return made
    }

    static func lookup(_ key: String) -> MarSharedStore? { stores[key] }

    static func all() -> [MarSharedStore] { Array(stores.values) }

    static func preserve(key: String, model: MarValue) {
        preservedModels[key] = model
    }

    // MARK: - Observers

    static func addObserver(_ owner: ObjectIdentifier, _ handler: @escaping () -> Void) {
        observers[owner] = handler
    }

    static func removeObserver(_ owner: ObjectIdentifier) {
        observers.removeValue(forKey: owner)
    }

    /// A shared change changes what every mounted page IS, not merely what it
    /// shows: its builder is re-applied with the new value. So every observer
    /// hears about it, not just the top of the stack.
    static func notifyChanged() {
        for handler in observers.values { handler() }
    }

    // MARK: - Effects

    /// Runs an effect a shared update returned. App-level on purpose: the
    /// store has no navigator and no HTTP client of its own, and handing it a
    /// page's would make whichever page happened to be on screen the owner of
    /// app-wide work. The web runs shared effects through its global
    /// `runEffect` for the same reason.
    static func runEffect(_ v: MarValue) {
        guard case .effect(let eff) = v else { return }
        do {
            _ = try eff.run()
        } catch {
            print("[mar] shared effect [\(eff.tag)] failed: \(error.localizedDescription)")
        }
    }

    /// Fires the queued `init` Cmds. Called from the first page mount, and a
    /// no-op on every mount after that.
    static func drainInitEffects() {
        let queued = pendingInitEffects
        pendingInitEffects = []
        for eff in queued { runEffect(eff) }
    }
}
