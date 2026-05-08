// Environment / scope chain — port of `envNew/envBind/envDefine/
// envLookup` in runtime.js. Reference type so a child Env can extend
// its parent without copying; lookups walk up the chain.
//
// `define` mutates the current frame (used by `loadModule` to set
// top-level bindings); `bind` returns a new child frame (used by
// `let` and `case` and lambda call to add scoped bindings).

import Foundation

final class Env {
    var bindings: [String: MarValue] = [:]
    let parent: Env?

    init(parent: Env? = nil) {
        self.parent = parent
    }

    func bind(_ name: String, _ value: MarValue) -> Env {
        let child = Env(parent: self)
        child.bindings[name] = value
        return child
    }

    func define(_ name: String, _ value: MarValue) {
        bindings[name] = value
    }

    func lookup(_ name: String) -> MarValue? {
        var cur: Env? = self
        while let e = cur {
            if let v = e.bindings[name] { return v }
            cur = e.parent
        }
        return nil
    }
}
