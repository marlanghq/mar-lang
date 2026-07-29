// Gamepad + Keyboard + Device — the native mirror of the web-first input and
// capability subsystems (internal/jsserve/runtime.js: subSources.keyboardWatch/
// gamepadWatch/deviceWatch, plus the Device.* builtins). All three are STATE
// mirrors: a subscribe delivers the whole current snapshot as a record, then a
// fresh record on every change — the runtime owns the bookkeeping.
//
// On the web these are window-level global listeners. On iOS the clean,
// view-free analogs are:
//   - Gamepad  -> GameController's GCController.extendedGamepad
//   - Keyboard -> GameController's GCKeyboard (hardware keyboard, iOS 14+)
//   - Device   -> UITraitCollection + scene bounds + GCMouse + UIAccessibility
//
// Each is a @MainActor hub singleton: native events fan out to listeners the
// page runtime registers (reconcileSubs), and the builtins produce the same
// `__Sub` items the runtime consumes. Constructors (Gamepad.<Button>,
// Keyboard.<Key>, Coarse/Fine) and the Device record match the other runtimes.
//
// Compile-checked, not run, in the build environment — behavior is verified on
// device.

import Foundation
import SwiftUI
import GameController
#if canImport(UIKit)
import UIKit
#endif

// MARK: - Gamepad

@MainActor
final class GamepadHub {
    static let shared = GamepadHub()

    // One listener per active Gamepad.watch sub. On any state change every
    // listener is notified and reads currentRecord() — the whole-pad snapshot.
    private var listeners: [Int: () -> Void] = [:]
    private var token = 0
    private var wired = false
    private var connected = false
    private var pressed: [String] = []        // held buttons, canonical order
    private var lx = 0, ly = 0, rx = 0, ry = 0

    func add(_ cb: @escaping () -> Void) -> Int {
        wire(); token += 1; listeners[token] = cb
        DispatchQueue.main.async { cb() }      // seed on subscribe
        return token
    }
    func remove(_ t: Int) { listeners[t] = nil }

    private func fire() { for cb in listeners.values { cb() } }

    private func wire() {
        guard !wired else { return }
        wired = true
        NotificationCenter.default.addObserver(forName: .GCControllerDidConnect, object: nil, queue: .main) { [weak self] note in
            MainActor.assumeIsolated { self?.attach(note.object as? GCController); self?.refreshConnected() }
        }
        NotificationCenter.default.addObserver(forName: .GCControllerDidDisconnect, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.refreshConnected() }
        }
        for c in GCController.controllers() { attach(c) }
        refreshConnected()
    }

    private func attach(_ controller: GCController?) {
        guard let gp = controller?.extendedGamepad else { return }
        controller?.handlerQueue = .main
        gp.valueChangedHandler = { [weak self] pad, _ in
            MainActor.assumeIsolated { self?.poll(pad) }
        }
    }

    /// A connect / disconnect flips `connected`; a loss also clears stale
    /// buttons + sticks so they self-heal (mirror principle 4).
    private func refreshConnected() {
        let now = !GCController.controllers().compactMap { $0.extendedGamepad }.isEmpty
        guard now != connected else { return }
        connected = now
        if !now { pressed = []; lx = 0; ly = 0; rx = 0; ry = 0 }
        fire()
    }

    /// Sample the pad; fire once if anything changed (buttons, either stick, or
    /// connection). Buttons are collected in a fixed canonical order so the
    /// `down` list is deterministic across runtimes.
    private func poll(_ pad: GCExtendedGamepad) {
        var now: [String] = []
        func mark(_ b: GCControllerButtonInput?, _ name: String) { if b?.isPressed == true { now.append(name) } }
        mark(pad.buttonA, "A"); mark(pad.buttonB, "B"); mark(pad.buttonX, "X"); mark(pad.buttonY, "Y")
        mark(pad.leftShoulder, "L1"); mark(pad.rightShoulder, "R1")
        mark(pad.leftTrigger, "L2"); mark(pad.rightTrigger, "R2")
        mark(pad.buttonOptions, "Select"); mark(pad.buttonMenu, "Start")
        mark(pad.leftThumbstickButton, "L3"); mark(pad.rightThumbstickButton, "R3")
        mark(pad.dpad.up, "Up"); mark(pad.dpad.down, "Down"); mark(pad.dpad.left, "Left"); mark(pad.dpad.right, "Right")

        var changed = false
        if !connected { connected = true; changed = true }
        if now != pressed { pressed = now; changed = true }

        let dz: Float = 0.12
        func ax(_ v: Float) -> Int { abs(v) < dz ? 0 : Int((v * 100).rounded()) }
        let nlx = ax(pad.leftThumbstick.xAxis.value), nly = ax(pad.leftThumbstick.yAxis.value)
        let nrx = ax(pad.rightThumbstick.xAxis.value), nry = ax(pad.rightThumbstick.yAxis.value)
        if nlx != lx || nly != ly || nrx != rx || nry != ry { lx = nlx; ly = nly; rx = nrx; ry = nry; changed = true }
        if changed { fire() }
    }

    /// The whole-pad record: { connected, leftX, leftY, rightX, rightY, down }.
    func currentRecord() -> MarValue {
        let downList = pressed.map { MarValue.ctor(tag: $0, args: [], origin: nil) }  // matches Gamepad.<name>
        return .record(fields: [
            "connected": .bool(connected),
            "leftX": .int(lx), "leftY": .int(ly),
            "rightX": .int(rx), "rightY": .int(ry),
            "down": .list(downList),
        ], order: ["connected", "leftX", "leftY", "rightX", "rightY", "down"])
    }
}

// MARK: - Keyboard (hardware, via GCKeyboard)

@MainActor
final class KeyboardHub {
    static let shared = KeyboardHub()

    // One listener per active Keyboard.watch sub. Any change to the held set
    // notifies all; each reads currentRecord() ({ down : List Key }).
    private var listeners: [Int: () -> Void] = [:]
    private var token = 0
    private var wired = false
    private var held: [String] = []     // held codes, in press order

    func add(_ cb: @escaping () -> Void) -> Int {
        wire(); token += 1; listeners[token] = cb
        DispatchQueue.main.async { cb() }   // seed on subscribe
        return token
    }
    func remove(_ t: Int) { listeners[t] = nil }

    private func fire() { for cb in listeners.values { cb() } }

    private func wire() {
        guard !wired else { return }
        wired = true
        NotificationCenter.default.addObserver(forName: .GCKeyboardDidConnect, object: nil, queue: .main) { [weak self] note in
            MainActor.assumeIsolated { self?.attach(note.object as? GCKeyboard) }
        }
        attach(GCKeyboard.coalesced)
        #if canImport(UIKit)
        // App backgrounded / resigned: the OS stops delivering key-up, so clear
        // the held set (the iOS analog of the web's window-blur clear).
        NotificationCenter.default.addObserver(forName: UIApplication.willResignActiveNotification, object: nil, queue: .main) { [weak self] _ in
            MainActor.assumeIsolated { self?.clearHeld() }
        }
        #endif
    }

    private func clearHeld() { if !held.isEmpty { held = []; fire() } }

    private func attach(_ kbd: GCKeyboard?) {
        guard let input = kbd?.keyboardInput else { return }
        input.keyChangedHandler = { [weak self] _, _, keyCode, pressed in
            MainActor.assumeIsolated {
                guard let self, let code = KeyboardHub.codeName(keyCode) else { return }
                // OS auto-repeat re-fires pressed for a held key; guard on
                // membership so only a real add/remove notifies watchers.
                if pressed {
                    if !self.held.contains(code) { self.held.append(code); self.fire() }
                } else if let i = self.held.firstIndex(of: code) {
                    self.held.remove(at: i); self.fire()
                }
            }
        }
    }

    /// The held-key record: { down : List Key }.
    func currentRecord() -> MarValue {
        let downList = held.map { MarValue.ctor(tag: $0, args: [], origin: nil) }  // matches Keyboard.<code>
        return .record(fields: ["down": .list(downList)], order: ["down"])
    }

    /// Map a GCKeyCode to the DOM `event.code` string the Keyboard.Key union
    /// mirrors (KeyA, Digit0, ArrowUp, Space, ...). A code with no mapping
    /// simply produces no ctor, so it falls to the user's `_` branch.
    static func codeName(_ k: GCKeyCode) -> String? {
        // letters
        let letters: [GCKeyCode: String] = [
            .keyA: "KeyA", .keyB: "KeyB", .keyC: "KeyC", .keyD: "KeyD", .keyE: "KeyE",
            .keyF: "KeyF", .keyG: "KeyG", .keyH: "KeyH", .keyI: "KeyI", .keyJ: "KeyJ",
            .keyK: "KeyK", .keyL: "KeyL", .keyM: "KeyM", .keyN: "KeyN", .keyO: "KeyO",
            .keyP: "KeyP", .keyQ: "KeyQ", .keyR: "KeyR", .keyS: "KeyS", .keyT: "KeyT",
            .keyU: "KeyU", .keyV: "KeyV", .keyW: "KeyW", .keyX: "KeyX", .keyY: "KeyY", .keyZ: "KeyZ",
        ]
        if let s = letters[k] { return s }
        let others: [GCKeyCode: String] = [
            .one: "Digit1", .two: "Digit2", .three: "Digit3", .four: "Digit4", .five: "Digit5",
            .six: "Digit6", .seven: "Digit7", .eight: "Digit8", .nine: "Digit9", .zero: "Digit0",
            .upArrow: "ArrowUp", .downArrow: "ArrowDown", .leftArrow: "ArrowLeft", .rightArrow: "ArrowRight",
            .spacebar: "Space", .returnOrEnter: "Enter", .tab: "Tab", .deleteOrBackspace: "Backspace",
            .escape: "Escape", .home: "Home", .end: "End", .pageUp: "PageUp", .pageDown: "PageDown",
            .leftShift: "ShiftLeft", .rightShift: "ShiftRight", .leftControl: "ControlLeft", .rightControl: "ControlRight",
            .leftAlt: "AltLeft", .rightAlt: "AltRight", .leftGUI: "MetaLeft", .rightGUI: "MetaRight",
            .hyphen: "Minus", .equalSign: "Equal", .comma: "Comma", .period: "Period", .slash: "Slash",
            .semicolon: "Semicolon", .quote: "Quote", .openBracket: "BracketLeft", .closeBracket: "BracketRight",
            .backslash: "Backslash", .graveAccentAndTilde: "Backquote",
        ]
        return others[k]
    }
}

// MARK: - Device

@MainActor
final class DeviceHub {
    static let shared = DeviceHub()

    private var listeners: [Int: () -> Void] = [:]
    private var token = 0
    private var wired = false

    func add(_ cb: @escaping () -> Void) -> Int {
        wire(); token += 1; listeners[token] = cb
        // Fire once immediately (Device.watch delivers the current value on
        // subscribe), deferred so it lands as its own dispatch.
        DispatchQueue.main.async { cb() }
        return token
    }
    func remove(_ t: Int) { listeners[t] = nil }

    private func wire() {
        guard !wired else { return }
        wired = true
        let names: [Notification.Name] = [
            UIAccessibility.reduceMotionStatusDidChangeNotification,
            UIDevice.orientationDidChangeNotification,
            UIApplication.didBecomeActiveNotification,
            .GCMouseDidConnect, .GCMouseDidDisconnect,
        ]
        for n in names {
            NotificationCenter.default.addObserver(forName: n, object: nil, queue: .main) { [weak self] _ in
                MainActor.assumeIsolated { self?.fire() }
            }
        }
    }

    private func fire() { for cb in listeners.values { cb() } }

    /// The current Device record. iOS primary input is a finger (Coarse) with
    /// no hover; a mouse attached (GCMouse) flips anyFine. Size is the key
    /// window's bounds; dark / reduced-motion from the trait environment.
    func currentRecord() -> MarValue {
        var w = 0, h = 0
        var dark = false
        #if canImport(UIKit)
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        if let win = scene?.windows.first(where: { $0.isKeyWindow }) ?? scene?.windows.first {
            w = Int(win.bounds.width); h = Int(win.bounds.height)
        }
        dark = (scene?.traitCollection.userInterfaceStyle ?? .light) == .dark
        #endif
        let anyFine = !GCMouse.mice().isEmpty
        let reduce = UIAccessibility.isReduceMotionEnabled
        return .record(fields: [
            "pointer": .ctor(tag: "Coarse", args: [], origin: nil),
            "anyFine": .bool(anyFine),
            "supportsHover": .bool(false),
            "width": .int(w),
            "height": .int(h),
            "prefersDark": .bool(dark),
            "prefersReducedMotion": .bool(reduce),
        ], order: ["pointer", "anyFine", "supportsHover", "width", "height", "prefersDark", "prefersReducedMotion"])
    }
}

// MARK: - builtin registration

enum MarInput {
    static func register(_ env: Env) {
        // The Keyboard.Key and Gamepad.Button constructors come from the
        // generated registry (MarBuiltinCtors.swift), which is derived from
        // the same typecheck lists the exhaustiveness checker uses — this
        // file used to hold its own copy of the ~100 key codes, and the two
        // copies could drift with nothing failing.
        // Pointer constructors — global, like Order / Method.
        env.define("Coarse", .ctor(tag: "Coarse", args: [], origin: nil))
        env.define("Fine",   .ctor(tag: "Fine", args: [], origin: nil))

        func sub(_ tag: String, _ tagger: MarValue) -> MarValue {
            .ctor(tag: "__Sub", args: [.ctor(tag: tag, args: [tagger], origin: nil)], origin: nil)
        }
        env.defineFn("keyboardWatch", "Keyboard.watch", 1) { a in sub("__SubKeyboard", a[0]) }
        env.defineFn("gamepadWatch",  "Gamepad.watch",  1) { a in sub("__SubGamepad", a[0]) }
        env.defineFn("deviceWatch",   "Device.watch",   1) { a in sub("__SubDevice", a[0]) }

        // Device.touchOnly / canHover — pure readings off a Device record.
        func boolField(_ v: MarValue, _ k: String) -> Bool { if case .record(let f, _) = v, case .bool(let b)? = f[k] { return b }; return false }
        env.defineFn("deviceTouchOnly", "Device.touchOnly", 1) { a in
            let coarse: Bool = { if case .record(let f, _) = a[0], case .ctor(let t, _, _)? = f["pointer"] { return t == "Coarse" }; return false }()
            return .bool(coarse && !boolField(a[0], "anyFine") && !boolField(a[0], "supportsHover"))
        }
        env.defineFn("deviceCanHover", "Device.canHover", 1) { a in .bool(boolField(a[0], "supportsHover")) }
    }

}
