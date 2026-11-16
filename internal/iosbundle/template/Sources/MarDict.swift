// Dict / Set runtime support: Swift port of Go's `internal/runtime/dict.go`
// and the matching JS code in `internal/jsserve/runtime.js`.
//
// Two pieces in this file:
//
//  1. The MarDict helpers (binary search + sorted insert/remove + the
//     two-pointer merge used by union/intersect/diff). These are
//     reused both by the builtin registrations below and by the JSON
//     codec when rebuilding `__dict` / `__set` payloads off the wire.
//
//  2. The `registerDictAndSetBuiltins(_ env:)` function that binds
//     every Dict.* / Set.* qualified name (plus the flat aliases the
//     dispatcher uses for the unqualified builtins) into the env.
//
// The invariant: `.dict(pairs)` keeps `pairs` sorted ascending by key
// per compareMar; `.set(items)` keeps `items` sorted ascending.
// "Comparable" means Int / Float / String at runtime: same constraint
// the Go and JS runtimes enforce. The typechecker doesn't model this
// constraint yet, so a misuse becomes a runtime error.

import Foundation

enum MarDict {

    // MARK: - Dict helpers (used by Builtins + JSONCodec)

    /// Binary search for `key`. Returns the insertion index plus a
    /// `found` flag. Sort key is compareMar.
    static func search(_ d: MarValue, _ key: MarValue) -> (idx: Int, found: Bool) {
        guard case .dict(let pairs) = d else { return (0, false) }
        var lo = 0, hi = pairs.count
        while lo < hi {
            let mid = (lo + hi) >> 1
            let c = pairs[mid].key.compareMar(key)
            if c < 0 { lo = mid + 1 } else { hi = mid }
        }
        let found = lo < pairs.count && pairs[lo].key.compareMar(key) == 0
        return (lo, found)
    }

    /// Insert (or replace) the (key, value) pair into d. Caller's `d`
    /// is unmodified; we return a fresh `.dict` with the new pair
    /// spliced in at the right sorted position.
    static func insert(_ d: MarValue, _ key: MarValue, _ value: MarValue) -> MarValue {
        guard case .dict(let pairs) = d else { return .dict([MarPair(key: key, value: value)]) }
        let (idx, found) = search(d, key)
        var out = pairs
        if found {
            out[idx] = MarPair(key: key, value: value)
        } else {
            out.insert(MarPair(key: key, value: value), at: idx)
        }
        return .dict(out)
    }

    /// Remove pair at index `idx`.
    static func removeAt(_ d: MarValue, _ idx: Int) -> MarValue {
        guard case .dict(var pairs) = d else { return d }
        pairs.remove(at: idx)
        return .dict(pairs)
    }

    // MARK: - Set helpers

    static func setSearch(_ s: MarValue, _ key: MarValue) -> (idx: Int, found: Bool) {
        guard case .set(let items) = s else { return (0, false) }
        var lo = 0, hi = items.count
        while lo < hi {
            let mid = (lo + hi) >> 1
            let c = items[mid].compareMar(key)
            if c < 0 { lo = mid + 1 } else { hi = mid }
        }
        let found = lo < items.count && items[lo].compareMar(key) == 0
        return (lo, found)
    }

    static func setInsert(_ s: MarValue, _ key: MarValue) -> MarValue {
        guard case .set(let items) = s else { return .set([key]) }
        let (idx, found) = setSearch(s, key)
        if found { return s }
        var out = items
        out.insert(key, at: idx)
        return .set(out)
    }

    /// A Set of `items`, built once: sorted by compareMar, keeping the first
    /// of two equal items, as setInsert does. For values that are known to
    /// share one comparable type (a checked program's, or a decoded value
    /// that passed its shape check). Set.fromList and MarWire.coerce build
    /// with it; runtime.js setFromItems and Go's setFromItems are the same
    /// rule, and the corpus's Set.fromList cases compare the three.
    static func setFromItems(_ items: [MarValue]) -> MarValue {
        let sorted = items.enumerated().sorted { a, b in
            let c = a.element.compareMar(b.element)
            return c != 0 ? c < 0 : a.offset < b.offset
        }.map(\.element)
        var out: [MarValue] = []
        out.reserveCapacity(sorted.count)
        for item in sorted where out.last.map({ $0.compareMar(item) != 0 }) ?? true {
            out.append(item)
        }
        return .set(out)
    }

    /// A Dict of `pairs`, built once: sorted by key, keeping the last pair for
    /// a key, as insert does. On the same terms as setFromItems.
    static func dictFromPairs(_ pairs: [MarPair]) -> MarValue {
        let sorted = pairs.enumerated().sorted { a, b in
            let c = a.element.key.compareMar(b.element.key)
            return c != 0 ? c < 0 : a.offset < b.offset
        }.map(\.element)
        var out: [MarPair] = []
        out.reserveCapacity(sorted.count)
        for pair in sorted {
            if let last = out.last, last.key.compareMar(pair.key) == 0 {
                out[out.count - 1] = pair
            } else {
                out.append(pair)
            }
        }
        return .dict(out)
    }

    // MARK: - Builtins

    // One entry per Dict and Set builtin: a table, so length and branch count
    // measure the API surface rather than a function that grew.
    // swiftlint:disable:next function_body_length cyclomatic_complexity
    static func register(_ env: Env) {
        // ---------- Dict ----------
        env.define("dictEmpty",  .dict([]))
        env.define("Dict.empty", .dict([]))

        let dictSingleton = MarFn.native(2) { args in
            .dict([MarPair(key: args[0], value: args[1])])
        }
        env.define("dictSingleton",  .fn(dictSingleton))
        env.define("Dict.singleton", .fn(dictSingleton))

        let dictInsert = MarFn.native(3) { args in
            guard case .dict = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[2]))
            }
            return MarDict.insert(args[2], args[0], args[1])
        }
        env.define("dictInsert",  .fn(dictInsert))
        env.define("Dict.insert", .fn(dictInsert))

        // Dict.update : k -> (Maybe v -> Maybe v) -> Dict k v -> Dict k v
        let dictUpdate = MarFn.native(3) { args in
            guard case .dict(let pairs) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[2]))
            }
            let (idx, found) = MarDict.search(args[2], args[0])
            let current: MarValue = found
                ? .ctor(tag: "Just", args: [pairs[idx].value])
                : .ctor(tag: "Nothing", args: [])
            let next = try Eval.apply(args[1], current)
            guard case .ctor(let tag, let nArgs) = next else {
                throw MarRuntimeError.message("Dict.update: function did not return a Maybe")
            }
            switch tag {
            case "Just":
                guard let nv = nArgs.first else {
                    throw MarRuntimeError.message("Dict.update: malformed Just")
                }
                return MarDict.insert(args[2], args[0], nv)
            case "Nothing":
                if !found { return args[2] }
                return MarDict.removeAt(args[2], idx)
            default:
                throw MarRuntimeError.message("Dict.update: function did not return a Maybe")
            }
        }
        env.define("dictUpdate",  .fn(dictUpdate))
        env.define("Dict.update", .fn(dictUpdate))

        let dictRemove = MarFn.native(2) { args in
            guard case .dict = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[1]))
            }
            let (idx, found) = MarDict.search(args[1], args[0])
            if !found { return args[1] }
            return MarDict.removeAt(args[1], idx)
        }
        env.define("dictRemove",  .fn(dictRemove))
        env.define("Dict.remove", .fn(dictRemove))

        let dictIsEmpty = MarFn.native(1) { args in
            guard case .dict(let pairs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[0]))
            }
            return .bool(pairs.isEmpty)
        }
        env.define("dictIsEmpty",  .fn(dictIsEmpty))
        env.define("Dict.isEmpty", .fn(dictIsEmpty))

        let dictMember = MarFn.native(2) { args in
            guard case .dict = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[1]))
            }
            return .bool(MarDict.search(args[1], args[0]).found)
        }
        env.define("dictMember",  .fn(dictMember))
        env.define("Dict.member", .fn(dictMember))

        let dictGet = MarFn.native(2) { args in
            guard case .dict(let pairs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[1]))
            }
            let (idx, found) = MarDict.search(args[1], args[0])
            if !found { return .ctor(tag: "Nothing", args: []) }
            return .ctor(tag: "Just", args: [pairs[idx].value])
        }
        env.define("dictGet",  .fn(dictGet))
        env.define("Dict.get", .fn(dictGet))

        let dictSize = MarFn.native(1) { args in
            guard case .dict(let pairs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[0]))
            }
            return .int(pairs.count)
        }
        env.define("dictSize",  .fn(dictSize))
        env.define("Dict.size", .fn(dictSize))

        let dictKeys = MarFn.native(1) { args in
            guard case .dict(let pairs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[0]))
            }
            return .list(pairs.map { $0.key })
        }
        env.define("dictKeys",  .fn(dictKeys))
        env.define("Dict.keys", .fn(dictKeys))

        let dictValues = MarFn.native(1) { args in
            guard case .dict(let pairs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[0]))
            }
            return .list(pairs.map { $0.value })
        }
        env.define("dictValues",  .fn(dictValues))
        env.define("Dict.values", .fn(dictValues))

        let dictToList = MarFn.native(1) { args in
            guard case .dict(let pairs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[0]))
            }
            return .list(pairs.map { .tuple([$0.key, $0.value]) })
        }
        env.define("dictToList",  .fn(dictToList))
        env.define("Dict.toList", .fn(dictToList))

        let dictFromList = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            var pairs: [MarPair] = []
            pairs.reserveCapacity(xs.count)
            for e in xs {
                guard case .tuple(let m) = e, m.count == 2 else {
                    throw MarRuntimeError.message("Dict.fromList: element not a 2-tuple")
                }
                pairs.append(MarPair(key: m[0], value: m[1]))
            }
            return MarDict.dictFromPairs(pairs)
        }
        env.define("dictFromList",  .fn(dictFromList))
        env.define("Dict.fromList", .fn(dictFromList))

        // Dict.map : (k -> v -> w) -> Dict k v -> Dict k w
        let dictMap = MarFn.native(2) { args in
            guard case .dict(let pairs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[1]))
            }
            var out: [MarPair] = []
            out.reserveCapacity(pairs.count)
            for p in pairs {
                let partial = try Eval.apply(args[0], p.key)
                let v = try Eval.apply(partial, p.value)
                out.append(MarPair(key: p.key, value: v))
            }
            return .dict(out)
        }
        env.define("dictMap",  .fn(dictMap))
        env.define("Dict.map", .fn(dictMap))

        let dictFoldl = MarFn.native(3) { args in
            guard case .dict(let pairs) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[2]))
            }
            var acc = args[1]
            for p in pairs {
                let p1 = try Eval.apply(args[0], p.key)
                let p2 = try Eval.apply(p1, p.value)
                acc = try Eval.apply(p2, acc)
            }
            return acc
        }
        env.define("dictFoldl",  .fn(dictFoldl))
        env.define("Dict.foldl", .fn(dictFoldl))

        let dictFoldr = MarFn.native(3) { args in
            guard case .dict(let pairs) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[2]))
            }
            var acc = args[1]
            for p in pairs.reversed() {
                let p1 = try Eval.apply(args[0], p.key)
                let p2 = try Eval.apply(p1, p.value)
                acc = try Eval.apply(p2, acc)
            }
            return acc
        }
        env.define("dictFoldr",  .fn(dictFoldr))
        env.define("Dict.foldr", .fn(dictFoldr))

        let dictFilter = MarFn.native(2) { args in
            guard case .dict(let pairs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[1]))
            }
            var out: [MarPair] = []
            for p in pairs {
                let p1 = try Eval.apply(args[0], p.key)
                let res = try Eval.apply(p1, p.value)
                guard case .bool(let keep) = res else {
                    throw MarRuntimeError.message("Dict.filter: predicate didn't return Bool")
                }
                if keep { out.append(p) }
            }
            return .dict(out)
        }
        env.define("dictFilter",  .fn(dictFilter))
        env.define("Dict.filter", .fn(dictFilter))

        let dictPartition = MarFn.native(2) { args in
            guard case .dict(let pairs) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict", got: Eval.typeOf(args[1]))
            }
            var yes: [MarPair] = []
            var no: [MarPair] = []
            for p in pairs {
                let p1 = try Eval.apply(args[0], p.key)
                let res = try Eval.apply(p1, p.value)
                guard case .bool(let keep) = res else {
                    throw MarRuntimeError.message("Dict.partition: predicate didn't return Bool")
                }
                if keep { yes.append(p) } else { no.append(p) }
            }
            return .tuple([.dict(yes), .dict(no)])
        }
        env.define("dictPartition",  .fn(dictPartition))
        env.define("Dict.partition", .fn(dictPartition))

        // Dict.union: left-biased on collision (matches Elm).
        let dictUnion = MarFn.native(2) { args in
            guard case .dict(let a) = args[0], case .dict(let b) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict, Dict",
                                                   got: "\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1]))")
            }
            var out: [MarPair] = []
            var i = 0, j = 0
            while i < a.count && j < b.count {
                let c = a[i].key.compareMar(b[j].key)
                if c < 0 { out.append(a[i]); i += 1 } else if c > 0 { out.append(b[j]); j += 1 } else { out.append(a[i]); i += 1; j += 1 }
            }
            while i < a.count { out.append(a[i]); i += 1 }
            while j < b.count { out.append(b[j]); j += 1 }
            return .dict(out)
        }
        env.define("dictUnion",  .fn(dictUnion))
        env.define("Dict.union", .fn(dictUnion))

        let dictIntersect = MarFn.native(2) { args in
            guard case .dict(let a) = args[0], case .dict(let b) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict, Dict",
                                                   got: "\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1]))")
            }
            var out: [MarPair] = []
            var i = 0, j = 0
            while i < a.count && j < b.count {
                let c = a[i].key.compareMar(b[j].key)
                if c < 0 { i += 1 } else if c > 0 { j += 1 } else { out.append(a[i]); i += 1; j += 1 }
            }
            return .dict(out)
        }
        env.define("dictIntersect",  .fn(dictIntersect))
        env.define("Dict.intersect", .fn(dictIntersect))

        let dictDiff = MarFn.native(2) { args in
            guard case .dict(let a) = args[0], case .dict(let b) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Dict, Dict",
                                                   got: "\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1]))")
            }
            var out: [MarPair] = []
            var i = 0, j = 0
            while i < a.count {
                if j >= b.count {
                    out.append(contentsOf: a[i...])
                    break
                }
                let c = a[i].key.compareMar(b[j].key)
                if c < 0 { out.append(a[i]); i += 1 } else if c > 0 { j += 1 } else { i += 1; j += 1 }
            }
            return .dict(out)
        }
        env.define("dictDiff",  .fn(dictDiff))
        env.define("Dict.diff", .fn(dictDiff))

        // ---------- Set ----------
        env.define("setEmpty",  .set([]))
        env.define("Set.empty", .set([]))

        let setSingleton = MarFn.native(1) { args in .set([args[0]]) }
        env.define("setSingleton",  .fn(setSingleton))
        env.define("Set.singleton", .fn(setSingleton))

        let setInsert = MarFn.native(2) { args in
            guard case .set = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Set", got: Eval.typeOf(args[1]))
            }
            return MarDict.setInsert(args[1], args[0])
        }
        env.define("setInsert",  .fn(setInsert))
        env.define("Set.insert", .fn(setInsert))

        let setRemove = MarFn.native(2) { args in
            guard case .set(var items) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Set", got: Eval.typeOf(args[1]))
            }
            let (idx, found) = MarDict.setSearch(args[1], args[0])
            if !found { return args[1] }
            items.remove(at: idx)
            return .set(items)
        }
        env.define("setRemove",  .fn(setRemove))
        env.define("Set.remove", .fn(setRemove))

        let setIsEmpty = MarFn.native(1) { args in
            guard case .set(let items) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Set", got: Eval.typeOf(args[0]))
            }
            return .bool(items.isEmpty)
        }
        env.define("setIsEmpty",  .fn(setIsEmpty))
        env.define("Set.isEmpty", .fn(setIsEmpty))

        let setMember = MarFn.native(2) { args in
            guard case .set = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Set", got: Eval.typeOf(args[1]))
            }
            return .bool(MarDict.setSearch(args[1], args[0]).found)
        }
        env.define("setMember",  .fn(setMember))
        env.define("Set.member", .fn(setMember))

        let setSize = MarFn.native(1) { args in
            guard case .set(let items) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Set", got: Eval.typeOf(args[0]))
            }
            return .int(items.count)
        }
        env.define("setSize",  .fn(setSize))
        env.define("Set.size", .fn(setSize))

        let setToList = MarFn.native(1) { args in
            guard case .set(let items) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "Set", got: Eval.typeOf(args[0]))
            }
            return .list(items)
        }
        env.define("setToList",  .fn(setToList))
        env.define("Set.toList", .fn(setToList))

        let setFromList = MarFn.native(1) { args in
            guard case .list(let xs) = args[0] else {
                throw MarRuntimeError.typeMismatch(expected: "List", got: Eval.typeOf(args[0]))
            }
            return MarDict.setFromItems(xs)
        }
        env.define("setFromList",  .fn(setFromList))
        env.define("Set.fromList", .fn(setFromList))

        // Set.map can change element type, so re-sort/dedupe via
        // setInsert rather than copying items in place.
        let setMap = MarFn.native(2) { args in
            guard case .set(let items) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Set", got: Eval.typeOf(args[1]))
            }
            var out: MarValue = .set([])
            for it in items {
                let v = try Eval.apply(args[0], it)
                out = MarDict.setInsert(out, v)
            }
            return out
        }
        env.define("setMap",  .fn(setMap))
        env.define("Set.map", .fn(setMap))

        let setFoldl = MarFn.native(3) { args in
            guard case .set(let items) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "Set", got: Eval.typeOf(args[2]))
            }
            var acc = args[1]
            for it in items {
                let p = try Eval.apply(args[0], it)
                acc = try Eval.apply(p, acc)
            }
            return acc
        }
        env.define("setFoldl",  .fn(setFoldl))
        env.define("Set.foldl", .fn(setFoldl))

        let setFoldr = MarFn.native(3) { args in
            guard case .set(let items) = args[2] else {
                throw MarRuntimeError.typeMismatch(expected: "Set", got: Eval.typeOf(args[2]))
            }
            var acc = args[1]
            for it in items.reversed() {
                let p = try Eval.apply(args[0], it)
                acc = try Eval.apply(p, acc)
            }
            return acc
        }
        env.define("setFoldr",  .fn(setFoldr))
        env.define("Set.foldr", .fn(setFoldr))

        let setFilter = MarFn.native(2) { args in
            guard case .set(let items) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Set", got: Eval.typeOf(args[1]))
            }
            var out: [MarValue] = []
            for it in items {
                let v = try Eval.apply(args[0], it)
                guard case .bool(let keep) = v else {
                    throw MarRuntimeError.message("Set.filter: predicate didn't return Bool")
                }
                if keep { out.append(it) }
            }
            return .set(out)
        }
        env.define("setFilter",  .fn(setFilter))
        env.define("Set.filter", .fn(setFilter))

        let setPartition = MarFn.native(2) { args in
            guard case .set(let items) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Set", got: Eval.typeOf(args[1]))
            }
            var yes: [MarValue] = []
            var no: [MarValue] = []
            for it in items {
                let v = try Eval.apply(args[0], it)
                guard case .bool(let keep) = v else {
                    throw MarRuntimeError.message("Set.partition: predicate didn't return Bool")
                }
                if keep { yes.append(it) } else { no.append(it) }
            }
            return .tuple([.set(yes), .set(no)])
        }
        env.define("setPartition",  .fn(setPartition))
        env.define("Set.partition", .fn(setPartition))

        let setUnion = MarFn.native(2) { args in
            guard case .set(let a) = args[0], case .set(let b) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Set, Set",
                                                   got: "\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1]))")
            }
            var out: [MarValue] = []
            var i = 0, j = 0
            while i < a.count && j < b.count {
                let c = a[i].compareMar(b[j])
                if c < 0 { out.append(a[i]); i += 1 } else if c > 0 { out.append(b[j]); j += 1 } else { out.append(a[i]); i += 1; j += 1 }
            }
            while i < a.count { out.append(a[i]); i += 1 }
            while j < b.count { out.append(b[j]); j += 1 }
            return .set(out)
        }
        env.define("setUnion",  .fn(setUnion))
        env.define("Set.union", .fn(setUnion))

        let setIntersect = MarFn.native(2) { args in
            guard case .set(let a) = args[0], case .set(let b) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Set, Set",
                                                   got: "\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1]))")
            }
            var out: [MarValue] = []
            var i = 0, j = 0
            while i < a.count && j < b.count {
                let c = a[i].compareMar(b[j])
                if c < 0 { i += 1 } else if c > 0 { j += 1 } else { out.append(a[i]); i += 1; j += 1 }
            }
            return .set(out)
        }
        env.define("setIntersect",  .fn(setIntersect))
        env.define("Set.intersect", .fn(setIntersect))

        let setDiff = MarFn.native(2) { args in
            guard case .set(let a) = args[0], case .set(let b) = args[1] else {
                throw MarRuntimeError.typeMismatch(expected: "Set, Set",
                                                   got: "\(Eval.typeOf(args[0])), \(Eval.typeOf(args[1]))")
            }
            var out: [MarValue] = []
            var i = 0, j = 0
            while i < a.count {
                if j >= b.count {
                    out.append(contentsOf: a[i...])
                    break
                }
                let c = a[i].compareMar(b[j])
                if c < 0 { out.append(a[i]); i += 1 } else if c > 0 { j += 1 } else { i += 1; j += 1 }
            }
            return .set(out)
        }
        env.define("setDiff",  .fn(setDiff))
        env.define("Set.diff", .fn(setDiff))
    }
}
