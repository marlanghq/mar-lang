// Environment / scope chain: port of `envNew/envBind/envDefine/
// envLookup` in runtime.js. Reference type so a child Env can extend
// its parent without copying; lookups walk up the chain.
//
// Storage is HYBRID, sized for how the interpreter actually uses frames:
//
//  - Call / let / case frames hold 1-8 names. They live in a flat array
//    of (name, value) pairs scanned in REVERSE (so later bindings shadow
//    earlier ones). Appending is one amortized buffer write; lookup is a
//    couple of small-string comparisons (two word-compares each for
//    identifiers ≤ 15 UTF-8 bytes). A Dictionary here paid a SipHash of
//    the name on EVERY insert and EVERY probe of every frame the chain
//    walk crossed: at millions of lookups per canvas frame that hashing
//    was one of the interpreter's biggest line items.
//
//  - The module root (hundreds of builtins + the program's top-level
//    bindings) SPILLS to a Dictionary once it outgrows the threshold,
//    keeping global lookup O(1). The spill is one-way and per-frame.
//
// `define` mutates the current frame (used by `loadModule` to set
// top-level bindings, replacing on redefinition); `appendBinding` is the
// no-replace-scan path for frames whose names are known distinct; and
// `init(parent:adopting:)` builds a call / let / case frame from bindings
// gathered beforehand, taking the array as its storage.

import Foundation

/// One name in a frame. A struct, not a `(name, value)` tuple: an Array of
/// tuples is copied and destroyed through the Swift runtime's GENERIC tuple
/// witnesses, which walk the tuple's metadata per element, and every call
/// frame is destroyed when its call returns.
struct EnvBinding {
    let name: String
    var value: MarValue
}

final class Env {
    private var slots: [EnvBinding] = []
    private var dict: [String: MarValue]?
    let parent: Env?

    /// Past this many bindings a frame stops being "small" and moves to
    /// Dictionary storage. Only the module root ever crosses it.
    private static let spillThreshold = 16

    init(parent: Env? = nil) {
        self.parent = parent
    }

    /// A frame whose bindings are already known: a call's parameters, or the
    /// variables one pattern bound. The array BECOMES the frame's storage, so
    /// a frame costs one buffer sized once, instead of a buffer regrown by
    /// each append (a 3-parameter call used to allocate three times) and a
    /// copy of every binding out of the pattern's bag. The names are distinct
    /// by construction; were they not, the reverse lookup would still let the
    /// later one win, exactly as appending them one by one did.
    init(parent: Env?, adopting bindings: [EnvBinding]) {
        self.parent = parent
        if bindings.count > Env.spillThreshold {
            var d = [String: MarValue](minimumCapacity: bindings.count * 2)
            for b in bindings { d[b.name] = b.value }
            dict = d
        } else {
            slots = bindings
        }
    }

    /// Define with replace-on-same-name semantics (module load, builtin
    /// registration). Same-frame redefinition overwrites, matching the
    /// Dictionary behavior the runtime always had.
    func define(_ name: String, _ value: MarValue) {
        if dict != nil {
            dict![name] = value
            return
        }
        var i = slots.count - 1
        while i >= 0 {
            if slots[i].name == name {
                slots[i].value = value
                return
            }
            i -= 1
        }
        appendBinding(name, value)
    }

    /// Append WITHOUT scanning for an existing entry. Callers guarantee
    /// the name isn't already in this frame (`define` has just looked).
    /// Reverse lookup keeps shadowing correct even if that guarantee ever
    /// slips. Call and pattern frames do not come through here: they are
    /// built whole by `init(parent:adopting:)`.
    func appendBinding(_ name: String, _ value: MarValue) {
        if dict != nil {
            dict![name] = value
            return
        }
        slots.append(EnvBinding(name: name, value: value))
        if slots.count > Env.spillThreshold {
            var d = [String: MarValue](minimumCapacity: slots.count * 2)
            for b in slots { d[b.name] = b.value }
            dict = d
            slots.removeAll(keepingCapacity: false)
        }
    }

    /// Register a native builtin under BOTH its bare desugared name
    /// (`soundTone`) and its dotted user-facing name (`Sound.tone`):
    /// the standard two-key convention. Convenience used by the
    /// web-first subsystems (Sound / Canvas / Gamepad / Keyboard / Device).
    func defineFn(_ bare: String, _ dotted: String, _ arity: Int,
                  _ fn: @escaping @MainActor ([MarValue]) throws -> MarValue) {
        let v = MarValue.fn(MarFn.native(arity, fn))
        define(bare, v)
        define(dotted, v)
    }

    func lookup(_ name: String) -> MarValue? {
        var cur: Env? = self
        while let e = cur {
            if let d = e.dict {
                if let v = d[name] { return v }
            } else {
                var i = e.slots.count - 1
                while i >= 0 {
                    if e.slots[i].name == name { return e.slots[i].value }
                    i -= 1
                }
            }
            cur = e.parent
        }
        return nil
    }

    /// Every binding that belongs to module `modName`: keys of the
    /// form `modName.suffix` where suffix has no further dot (so
    /// `Mar.Admin.x` exports from `Mar.Admin`, not from `Mar`).
    /// Powers `import M exposing (..)`, mirroring the Go runtime's
    /// Env.ExportsOf. Frames are applied outermost-first so inner
    /// bindings win, matching lookup's shadowing order. (Cold path:
    /// runs once per import at load, never per frame.)
    func exportsOf(_ modName: String) -> [String: MarValue] {
        let prefix = modName + "."
        var frames: [Env] = []
        var cur: Env? = self
        while let e = cur {
            frames.append(e)
            cur = e.parent
        }
        var out: [String: MarValue] = [:]
        for e in frames.reversed() {
            if let d = e.dict {
                for (name, v) in d {
                    guard name.hasPrefix(prefix) else { continue }
                    let suffix = String(name.dropFirst(prefix.count))
                    if suffix.isEmpty || suffix.contains(".") { continue }
                    out[suffix] = v
                }
            }
            // Within one array frame, later slots must win: iterate in
            // insertion order so the last write lands in `out`.
            for b in e.slots {
                guard b.name.hasPrefix(prefix) else { continue }
                let suffix = String(b.name.dropFirst(prefix.count))
                if suffix.isEmpty || suffix.contains(".") { continue }
                out[suffix] = b.value
            }
        }
        return out
    }
}
