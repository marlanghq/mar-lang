package runtime

import (
	"fmt"
	"slices"
	"sort"
)

// dictBuiltins returns runtime functions for the Dict module: an
// ordered, polymorphic key-value map. Keys must be "comparable" per
// compareValues (Int / Float / String); the typechecker doesn't yet
// enforce that constraint, so the runtime returns an error if a
// non-comparable key sneaks through.
//
// The underlying VDict value keeps its Pairs slice sorted ascending
// by key, which gives:
//   - O(log n) get / member via binary search
//   - O(n) insert / remove (slice rebuild: fine for the sizes we
//     expect in user code; a balanced tree is the upgrade path)
//   - O(n) union / intersect / diff via two-pointer merge
//   - Deterministic toList / keys / values iteration
//
// See VDict in value.go for the in-memory shape and json.go for the
// "__dict" wire format.
func dictBuiltins() map[string]Value {
	return map[string]Value{
		"dictEmpty": VDict{},
		"dictSingleton": nativeFn(2, func(args []Value) (Value, error) {
			return VDict{Pairs: []VDictPair{{Key: args[0], Value: args[1]}}}, nil
		}),
		"dictInsert": nativeFn(3, func(args []Value) (Value, error) {
			d, ok := args[2].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.insert: not a Dict")
			}
			return dictInsert(d, args[0], args[1])
		}),
		"dictUpdate": nativeFn(3, func(args []Value) (Value, error) {
			key := args[0]
			fn := args[1]
			d, ok := args[2].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.update: not a Dict")
			}
			idx, found, err := dictSearch(d, key)
			if err != nil {
				return nil, err
			}
			var current Value
			if found {
				current = VCtor{Tag: "Just", Args: []Value{d.Pairs[idx].Value}}
			} else {
				current = VCtor{Tag: "Nothing"}
			}
			next, err := apply(fn, current)
			if err != nil {
				return nil, err
			}
			c, ok := next.(VCtor)
			if !ok {
				return nil, fmt.Errorf("Dict.update: function didn't return a Maybe")
			}
			switch c.Tag {
			case "Just":
				if len(c.Args) != 1 {
					return nil, fmt.Errorf("Dict.update: malformed Just")
				}
				return dictInsertAt(d, idx, found, key, c.Args[0]), nil
			case "Nothing":
				if !found {
					return d, nil
				}
				return dictRemoveAt(d, idx), nil
			}
			return nil, fmt.Errorf("Dict.update: function didn't return a Maybe")
		}),
		"dictRemove": nativeFn(2, func(args []Value) (Value, error) {
			d, ok := args[1].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.remove: not a Dict")
			}
			idx, found, err := dictSearch(d, args[0])
			if err != nil {
				return nil, err
			}
			if !found {
				return d, nil
			}
			return dictRemoveAt(d, idx), nil
		}),
		"dictIsEmpty": nativeFn(1, func(args []Value) (Value, error) {
			d, ok := args[0].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.isEmpty: not a Dict")
			}
			return VBool{V: len(d.Pairs) == 0}, nil
		}),
		"dictMember": nativeFn(2, func(args []Value) (Value, error) {
			d, ok := args[1].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.member: not a Dict")
			}
			_, found, err := dictSearch(d, args[0])
			if err != nil {
				return nil, err
			}
			return VBool{V: found}, nil
		}),
		"dictGet": nativeFn(2, func(args []Value) (Value, error) {
			d, ok := args[1].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.get: not a Dict")
			}
			idx, found, err := dictSearch(d, args[0])
			if err != nil {
				return nil, err
			}
			if !found {
				return VCtor{Tag: "Nothing"}, nil
			}
			return VCtor{Tag: "Just", Args: []Value{d.Pairs[idx].Value}}, nil
		}),
		"dictSize": nativeFn(1, func(args []Value) (Value, error) {
			d, ok := args[0].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.size: not a Dict")
			}
			return VInt{V: int64(len(d.Pairs))}, nil
		}),
		"dictKeys": nativeFn(1, func(args []Value) (Value, error) {
			d, ok := args[0].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.keys: not a Dict")
			}
			out := make([]Value, len(d.Pairs))
			for i, p := range d.Pairs {
				out[i] = p.Key
			}
			return VList{Elements: out}, nil
		}),
		"dictValues": nativeFn(1, func(args []Value) (Value, error) {
			d, ok := args[0].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.values: not a Dict")
			}
			out := make([]Value, len(d.Pairs))
			for i, p := range d.Pairs {
				out[i] = p.Value
			}
			return VList{Elements: out}, nil
		}),
		"dictToList": nativeFn(1, func(args []Value) (Value, error) {
			d, ok := args[0].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.toList: not a Dict")
			}
			out := make([]Value, len(d.Pairs))
			for i, p := range d.Pairs {
				out[i] = VTuple{Members: []Value{p.Key, p.Value}}
			}
			return VList{Elements: out}, nil
		}),
		"dictFromList": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("Dict.fromList: not a List")
			}
			pairs := make([]VDictPair, len(l.Elements))
			for i, e := range l.Elements {
				t, ok := e.(VTuple)
				if !ok || len(t.Members) != 2 {
					return nil, fmt.Errorf("Dict.fromList: element not a 2-tuple")
				}
				pairs[i] = VDictPair{Key: t.Members[0], Value: t.Members[1]}
			}
			return dictFromPairs(pairs)
		}),
		// dictMap : (k -> v -> w) -> Dict k v -> Dict k w
		// Keys are NOT touched, so sort order is preserved without
		// reinserting.
		"dictMap": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			d, ok := args[1].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.map: not a Dict")
			}
			out := make([]VDictPair, len(d.Pairs))
			for i, p := range d.Pairs {
				partial, err := apply(fn, p.Key)
				if err != nil {
					return nil, err
				}
				v, err := apply(partial, p.Value)
				if err != nil {
					return nil, err
				}
				out[i] = VDictPair{Key: p.Key, Value: v}
			}
			return VDict{Pairs: out}, nil
		}),
		// dictFoldl / dictFoldr : (k -> v -> b -> b) -> b -> Dict k v -> b
		// Iteration is in key order; foldr walks back to front.
		"dictFoldl": nativeFn(3, func(args []Value) (Value, error) {
			fn := args[0]
			acc := args[1]
			d, ok := args[2].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.foldl: not a Dict")
			}
			for _, p := range d.Pairs {
				partial, err := apply(fn, p.Key)
				if err != nil {
					return nil, err
				}
				partial2, err := apply(partial, p.Value)
				if err != nil {
					return nil, err
				}
				next, err := apply(partial2, acc)
				if err != nil {
					return nil, err
				}
				acc = next
			}
			return acc, nil
		}),
		"dictFoldr": nativeFn(3, func(args []Value) (Value, error) {
			fn := args[0]
			acc := args[1]
			d, ok := args[2].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.foldr: not a Dict")
			}
			for i := len(d.Pairs) - 1; i >= 0; i-- {
				p := d.Pairs[i]
				partial, err := apply(fn, p.Key)
				if err != nil {
					return nil, err
				}
				partial2, err := apply(partial, p.Value)
				if err != nil {
					return nil, err
				}
				next, err := apply(partial2, acc)
				if err != nil {
					return nil, err
				}
				acc = next
			}
			return acc, nil
		}),
		"dictFilter": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			d, ok := args[1].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.filter: not a Dict")
			}
			out := make([]VDictPair, 0, len(d.Pairs))
			for _, p := range d.Pairs {
				keep, err := dictPredicate(fn, p)
				if err != nil {
					return nil, err
				}
				if keep {
					out = append(out, p)
				}
			}
			return VDict{Pairs: out}, nil
		}),
		"dictPartition": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			d, ok := args[1].(VDict)
			if !ok {
				return nil, fmt.Errorf("Dict.partition: not a Dict")
			}
			var yes, no []VDictPair
			for _, p := range d.Pairs {
				keep, err := dictPredicate(fn, p)
				if err != nil {
					return nil, err
				}
				if keep {
					yes = append(yes, p)
				} else {
					no = append(no, p)
				}
			}
			return VTuple{Members: []Value{VDict{Pairs: yes}, VDict{Pairs: no}}}, nil
		}),
		// dictUnion : Dict k v -> Dict k v -> Dict k v
		// Left-biased: keys present in `a` keep their `a` value even if
		// `b` has them too (matches Elm).
		"dictUnion": nativeFn(2, func(args []Value) (Value, error) {
			a, ok1 := args[0].(VDict)
			b, ok2 := args[1].(VDict)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Dict.union: expected two Dicts")
			}
			out, err := dictMerge(a, b, func(_ Value, av, _ Value, inA, _ bool) (Value, bool, error) {
				if inA {
					return av, true, nil
				}
				// Only in b: keep b's value.
				return nil, true, nil
			})
			if err != nil {
				return nil, err
			}
			return out, nil
		}),
		// dictIntersect : Dict k v -> Dict k v -> Dict k v
		// Keeps pairs whose key is in both dicts; value comes from the
		// LEFT dict (matches Elm).
		"dictIntersect": nativeFn(2, func(args []Value) (Value, error) {
			a, ok1 := args[0].(VDict)
			b, ok2 := args[1].(VDict)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Dict.intersect: expected two Dicts")
			}
			out := []VDictPair{}
			i, j := 0, 0
			for i < len(a.Pairs) && j < len(b.Pairs) {
				c, err := compareValues(a.Pairs[i].Key, b.Pairs[j].Key)
				if err != nil {
					return nil, err
				}
				switch {
				case c < 0:
					i++
				case c > 0:
					j++
				default:
					out = append(out, a.Pairs[i])
					i++
					j++
				}
			}
			return VDict{Pairs: out}, nil
		}),
		// dictDiff : Dict k v -> Dict k v -> Dict k v
		// Removes from `a` any key present in `b`.
		"dictDiff": nativeFn(2, func(args []Value) (Value, error) {
			a, ok1 := args[0].(VDict)
			b, ok2 := args[1].(VDict)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Dict.diff: expected two Dicts")
			}
			out := []VDictPair{}
			i, j := 0, 0
			for i < len(a.Pairs) {
				if j >= len(b.Pairs) {
					out = append(out, a.Pairs[i:]...)
					break
				}
				c, err := compareValues(a.Pairs[i].Key, b.Pairs[j].Key)
				if err != nil {
					return nil, err
				}
				switch {
				case c < 0:
					out = append(out, a.Pairs[i])
					i++
				case c > 0:
					j++
				default:
					i++
					j++
				}
			}
			return VDict{Pairs: out}, nil
		}),
	}
}

// setBuiltins returns runtime functions for the Set module. Sets are
// internally `VSet { Items []Value }` with `Items` kept sorted by
// compareValues: same comparable-key constraint as Dict.
//
// The signatures mirror Elm's Set API; under the hood many ops
// reuse the same two-pointer merge logic that powers Dict.
func setBuiltins() map[string]Value {
	return map[string]Value{
		"setEmpty": VSet{},
		"setSingleton": nativeFn(1, func(args []Value) (Value, error) {
			return VSet{Items: []Value{args[0]}}, nil
		}),
		"setInsert": nativeFn(2, func(args []Value) (Value, error) {
			s, ok := args[1].(VSet)
			if !ok {
				return nil, fmt.Errorf("Set.insert: not a Set")
			}
			return setInsert(s, args[0])
		}),
		"setRemove": nativeFn(2, func(args []Value) (Value, error) {
			s, ok := args[1].(VSet)
			if !ok {
				return nil, fmt.Errorf("Set.remove: not a Set")
			}
			idx, found, err := setSearch(s, args[0])
			if err != nil {
				return nil, err
			}
			if !found {
				return s, nil
			}
			out := make([]Value, 0, len(s.Items)-1)
			out = append(out, s.Items[:idx]...)
			out = append(out, s.Items[idx+1:]...)
			return VSet{Items: out}, nil
		}),
		"setIsEmpty": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VSet)
			if !ok {
				return nil, fmt.Errorf("Set.isEmpty: not a Set")
			}
			return VBool{V: len(s.Items) == 0}, nil
		}),
		"setMember": nativeFn(2, func(args []Value) (Value, error) {
			s, ok := args[1].(VSet)
			if !ok {
				return nil, fmt.Errorf("Set.member: not a Set")
			}
			_, found, err := setSearch(s, args[0])
			if err != nil {
				return nil, err
			}
			return VBool{V: found}, nil
		}),
		"setSize": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VSet)
			if !ok {
				return nil, fmt.Errorf("Set.size: not a Set")
			}
			return VInt{V: int64(len(s.Items))}, nil
		}),
		"setToList": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VSet)
			if !ok {
				return nil, fmt.Errorf("Set.toList: not a Set")
			}
			out := make([]Value, len(s.Items))
			copy(out, s.Items)
			return VList{Elements: out}, nil
		}),
		"setFromList": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("Set.fromList: not a List")
			}
			return setFromItems(l.Elements)
		}),
		// setMap : (k -> j) -> Set k -> Set j
		// Output element type can change, so we re-sort/dedupe by
		// inserting one-by-one rather than copy-in-place.
		"setMap": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			s, ok := args[1].(VSet)
			if !ok {
				return nil, fmt.Errorf("Set.map: not a Set")
			}
			mapped := make([]Value, len(s.Items))
			for i, it := range s.Items {
				v, err := apply(fn, it)
				if err != nil {
					return nil, err
				}
				mapped[i] = v
			}
			return setFromItems(mapped)
		}),
		"setFoldl": nativeFn(3, func(args []Value) (Value, error) {
			fn := args[0]
			acc := args[1]
			s, ok := args[2].(VSet)
			if !ok {
				return nil, fmt.Errorf("Set.foldl: not a Set")
			}
			for _, it := range s.Items {
				partial, err := apply(fn, it)
				if err != nil {
					return nil, err
				}
				next, err := apply(partial, acc)
				if err != nil {
					return nil, err
				}
				acc = next
			}
			return acc, nil
		}),
		"setFoldr": nativeFn(3, func(args []Value) (Value, error) {
			fn := args[0]
			acc := args[1]
			s, ok := args[2].(VSet)
			if !ok {
				return nil, fmt.Errorf("Set.foldr: not a Set")
			}
			for i := len(s.Items) - 1; i >= 0; i-- {
				partial, err := apply(fn, s.Items[i])
				if err != nil {
					return nil, err
				}
				next, err := apply(partial, acc)
				if err != nil {
					return nil, err
				}
				acc = next
			}
			return acc, nil
		}),
		"setFilter": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			s, ok := args[1].(VSet)
			if !ok {
				return nil, fmt.Errorf("Set.filter: not a Set")
			}
			out := make([]Value, 0, len(s.Items))
			for _, it := range s.Items {
				keep, err := setPredicate(fn, it)
				if err != nil {
					return nil, err
				}
				if keep {
					out = append(out, it)
				}
			}
			return VSet{Items: out}, nil
		}),
		"setPartition": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			s, ok := args[1].(VSet)
			if !ok {
				return nil, fmt.Errorf("Set.partition: not a Set")
			}
			var yes, no []Value
			for _, it := range s.Items {
				keep, err := setPredicate(fn, it)
				if err != nil {
					return nil, err
				}
				if keep {
					yes = append(yes, it)
				} else {
					no = append(no, it)
				}
			}
			return VTuple{Members: []Value{VSet{Items: yes}, VSet{Items: no}}}, nil
		}),
		"setUnion": nativeFn(2, func(args []Value) (Value, error) {
			a, ok1 := args[0].(VSet)
			b, ok2 := args[1].(VSet)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Set.union: expected two Sets")
			}
			out := []Value{}
			i, j := 0, 0
			for i < len(a.Items) && j < len(b.Items) {
				c, err := compareValues(a.Items[i], b.Items[j])
				if err != nil {
					return nil, err
				}
				switch {
				case c < 0:
					out = append(out, a.Items[i])
					i++
				case c > 0:
					out = append(out, b.Items[j])
					j++
				default:
					out = append(out, a.Items[i])
					i++
					j++
				}
			}
			out = append(out, a.Items[i:]...)
			out = append(out, b.Items[j:]...)
			return VSet{Items: out}, nil
		}),
		"setIntersect": nativeFn(2, func(args []Value) (Value, error) {
			a, ok1 := args[0].(VSet)
			b, ok2 := args[1].(VSet)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Set.intersect: expected two Sets")
			}
			out := []Value{}
			i, j := 0, 0
			for i < len(a.Items) && j < len(b.Items) {
				c, err := compareValues(a.Items[i], b.Items[j])
				if err != nil {
					return nil, err
				}
				switch {
				case c < 0:
					i++
				case c > 0:
					j++
				default:
					out = append(out, a.Items[i])
					i++
					j++
				}
			}
			return VSet{Items: out}, nil
		}),
		"setDiff": nativeFn(2, func(args []Value) (Value, error) {
			a, ok1 := args[0].(VSet)
			b, ok2 := args[1].(VSet)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("Set.diff: expected two Sets")
			}
			out := []Value{}
			i, j := 0, 0
			for i < len(a.Items) {
				if j >= len(b.Items) {
					out = append(out, a.Items[i:]...)
					break
				}
				c, err := compareValues(a.Items[i], b.Items[j])
				if err != nil {
					return nil, err
				}
				switch {
				case c < 0:
					out = append(out, a.Items[i])
					i++
				case c > 0:
					j++
				default:
					i++
					j++
				}
			}
			return VSet{Items: out}, nil
		}),
	}
}

// --- VDict helpers ---

// dictSearch binary-searches Pairs for `key`. Returns the index where
// `key` IS or WOULD BE inserted, plus a `found` flag. Errors only when
// the key type doesn't support comparison.
func dictSearch(d VDict, key Value) (int, bool, error) {
	var searchErr error
	idx := sort.Search(len(d.Pairs), func(i int) bool {
		if searchErr != nil {
			return false
		}
		c, err := compareValues(d.Pairs[i].Key, key)
		if err != nil {
			searchErr = err
			return false
		}
		return c >= 0
	})
	if searchErr != nil {
		return 0, false, searchErr
	}
	if idx < len(d.Pairs) {
		c, err := compareValues(d.Pairs[idx].Key, key)
		if err != nil {
			return 0, false, err
		}
		if c == 0 {
			return idx, true, nil
		}
	}
	return idx, false, nil
}

// dictInsert inserts (or replaces) the given pair, preserving the
// sorted-by-key invariant.
func dictInsert(d VDict, key, value Value) (Value, error) {
	idx, found, err := dictSearch(d, key)
	if err != nil {
		return nil, err
	}
	return dictInsertAt(d, idx, found, key, value), nil
}

// dictInsertAt rebuilds the pair slice for an insert at a known
// position. When `found` is true, the existing pair at idx is
// replaced; otherwise the new pair is spliced in at idx.
func dictInsertAt(d VDict, idx int, found bool, key, value Value) VDict {
	if found {
		out := make([]VDictPair, len(d.Pairs))
		copy(out, d.Pairs)
		out[idx] = VDictPair{Key: key, Value: value}
		return VDict{Pairs: out}
	}
	out := make([]VDictPair, 0, len(d.Pairs)+1)
	out = append(out, d.Pairs[:idx]...)
	out = append(out, VDictPair{Key: key, Value: value})
	out = append(out, d.Pairs[idx:]...)
	return VDict{Pairs: out}
}

// dictRemoveAt rebuilds the pair slice without the pair at idx.
func dictRemoveAt(d VDict, idx int) VDict {
	out := make([]VDictPair, 0, len(d.Pairs)-1)
	out = append(out, d.Pairs[:idx]...)
	out = append(out, d.Pairs[idx+1:]...)
	return VDict{Pairs: out}
}

// dictPredicate applies a (k -> v -> Bool) function to a pair.
func dictPredicate(fn Value, p VDictPair) (bool, error) {
	partial, err := apply(fn, p.Key)
	if err != nil {
		return false, err
	}
	v, err := apply(partial, p.Value)
	if err != nil {
		return false, err
	}
	b, ok := v.(VBool)
	if !ok {
		return false, fmt.Errorf("Dict predicate didn't return Bool")
	}
	return b.V, nil
}

// dictMerge runs a two-pointer merge over the two dicts, calling `pick`
// at each step to decide what (if anything) goes into the output. The
// callback returns:
//   - the chosen value
//   - a `keep` flag (false drops the key from the output)
//   - any comparison error
//
// When `pick` returns nil for `val`, the value from whichever side
// IS present is used (caller doesn't have to recopy it). When both
// sides are present, the callback must pick a side explicitly.
func dictMerge(
	a, b VDict,
	pick func(key Value, aVal, bVal Value, inA, inB bool) (Value, bool, error),
) (VDict, error) {
	out := []VDictPair{}
	i, j := 0, 0
	for i < len(a.Pairs) && j < len(b.Pairs) {
		c, err := compareValues(a.Pairs[i].Key, b.Pairs[j].Key)
		if err != nil {
			return VDict{}, err
		}
		switch {
		case c < 0:
			v, keep, err := pick(a.Pairs[i].Key, a.Pairs[i].Value, nil, true, false)
			if err != nil {
				return VDict{}, err
			}
			if keep {
				if v == nil {
					v = a.Pairs[i].Value
				}
				out = append(out, VDictPair{Key: a.Pairs[i].Key, Value: v})
			}
			i++
		case c > 0:
			v, keep, err := pick(b.Pairs[j].Key, nil, b.Pairs[j].Value, false, true)
			if err != nil {
				return VDict{}, err
			}
			if keep {
				if v == nil {
					v = b.Pairs[j].Value
				}
				out = append(out, VDictPair{Key: b.Pairs[j].Key, Value: v})
			}
			j++
		default:
			v, keep, err := pick(a.Pairs[i].Key, a.Pairs[i].Value, b.Pairs[j].Value, true, true)
			if err != nil {
				return VDict{}, err
			}
			if keep {
				if v == nil {
					v = a.Pairs[i].Value
				}
				out = append(out, VDictPair{Key: a.Pairs[i].Key, Value: v})
			}
			i++
			j++
		}
	}
	for ; i < len(a.Pairs); i++ {
		v, keep, err := pick(a.Pairs[i].Key, a.Pairs[i].Value, nil, true, false)
		if err != nil {
			return VDict{}, err
		}
		if keep {
			if v == nil {
				v = a.Pairs[i].Value
			}
			out = append(out, VDictPair{Key: a.Pairs[i].Key, Value: v})
		}
	}
	for ; j < len(b.Pairs); j++ {
		v, keep, err := pick(b.Pairs[j].Key, nil, b.Pairs[j].Value, false, true)
		if err != nil {
			return VDict{}, err
		}
		if keep {
			if v == nil {
				v = b.Pairs[j].Value
			}
			out = append(out, VDictPair{Key: b.Pairs[j].Key, Value: v})
		}
	}
	return VDict{Pairs: out}, nil
}

// --- VSet helpers ---

func setSearch(s VSet, key Value) (int, bool, error) {
	var searchErr error
	idx := sort.Search(len(s.Items), func(i int) bool {
		if searchErr != nil {
			return false
		}
		c, err := compareValues(s.Items[i], key)
		if err != nil {
			searchErr = err
			return false
		}
		return c >= 0
	})
	if searchErr != nil {
		return 0, false, searchErr
	}
	if idx < len(s.Items) {
		c, err := compareValues(s.Items[idx], key)
		if err != nil {
			return 0, false, err
		}
		if c == 0 {
			return idx, true, nil
		}
	}
	return idx, false, nil
}

func setInsert(s VSet, item Value) (Value, error) {
	idx, found, err := setSearch(s, item)
	if err != nil {
		return nil, err
	}
	if found {
		return s, nil
	}
	out := make([]Value, 0, len(s.Items)+1)
	out = append(out, s.Items[:idx]...)
	out = append(out, item)
	out = append(out, s.Items[idx:]...)
	return VSet{Items: out}, nil
}

func setPredicate(fn Value, item Value) (bool, error) {
	v, err := apply(fn, item)
	if err != nil {
		return false, err
	}
	b, ok := v.(VBool)
	if !ok {
		return false, fmt.Errorf("Set predicate didn't return Bool")
	}
	return b.V, nil
}

// --- building a Dict or a Set from many items at once ---
//
// Inserting n items one at a time rebuilds the whole sorted slice n times:
// n*n/2 copies. That is invisible at twenty items and ruinous at a hundred
// thousand, and one of the callers is the JSON decoder, which builds whatever
// a request body asks for. Measured before these existed: an 869 KB body, under
// the server's 1 MiB limit, holding one `{"__set":[...]}` of 140,000 integers
// took 38 s to decode and allocated 157 GB doing it; built this way it takes
// 0.12 s and 24 MB.
//
// Sorting once and dropping duplicates gives the same value:
//
//   - a Set keeps the FIRST of two equal items, as setInsert does (it returns
//     the set untouched when the item is already there);
//   - a Dict keeps the LAST pair for a key, key and value both, as dictInsert
//     does (it overwrites the pair it finds).
//
// "Equal" is compareValues answering 0, which is wider than identical: 1.5 and
// 1.50 are one key. That is why which one survives is spelled out.

// setFromItems is Set.fromList: the items of a checked program all have one
// comparable type, so the comparison cannot fail and sorting is always safe.
func setFromItems(items []Value) (Value, error) {
	return buildSetSorted(items)
}

// dictFromPairs is Dict.fromList, on the same terms as setFromItems.
func dictFromPairs(pairs []VDictPair) (Value, error) {
	return buildDictSorted(pairs)
}

func buildSetSorted(items []Value) (VSet, error) {
	if len(items) == 0 {
		return VSet{}, nil
	}
	out := make([]Value, len(items))
	copy(out, items)
	var cmpErr error
	sort.SliceStable(out, func(i, j int) bool {
		if cmpErr != nil {
			return false
		}
		c, err := compareValues(out[i], out[j])
		if err != nil {
			cmpErr = err
			return false
		}
		return c < 0
	})
	if cmpErr != nil {
		return VSet{}, cmpErr
	}
	n := 1
	for i := 1; i < len(out); i++ {
		c, err := compareValues(out[n-1], out[i])
		if err != nil {
			return VSet{}, err
		}
		if c != 0 {
			out[n] = out[i]
			n++
		}
	}
	return VSet{Items: out[:n:n]}, nil
}

func buildDictSorted(pairs []VDictPair) (VDict, error) {
	if len(pairs) == 0 {
		return VDict{}, nil
	}
	out := make([]VDictPair, len(pairs))
	copy(out, pairs)
	var cmpErr error
	sort.SliceStable(out, func(i, j int) bool {
		if cmpErr != nil {
			return false
		}
		c, err := compareValues(out[i].Key, out[j].Key)
		if err != nil {
			cmpErr = err
			return false
		}
		return c < 0
	})
	if cmpErr != nil {
		return VDict{}, cmpErr
	}
	n := 1
	for i := 1; i < len(out); i++ {
		c, err := compareValues(out[n-1].Key, out[i].Key)
		if err != nil {
			return VDict{}, err
		}
		if c == 0 {
			out[n-1] = out[i]
			continue
		}
		out[n] = out[i]
		n++
	}
	return VDict{Pairs: out[:n:n]}, nil
}

// isCompoundKey reports a key whose comparison looks inside it. Two such keys
// can compare without error even when they do not have one type ([1,"a"]
// against [2,3] is decided by the first position), so WHICH pairs get compared
// decides whether a malformed payload is refused. For those the wire builders
// below keep the comparisons insertion makes.
func isCompoundKey(v Value) bool {
	switch v.(type) {
	case VList, VTuple:
		return true
	}
	return false
}

// setFromWire builds the Set a `{"__set":[...]}` payload describes, refusing
// exactly the payloads one-at-a-time insertion refused.
//
// Flat items (numbers, strings, characters, and everything that cannot be
// ordered at all) fail a comparison precisely when two of them are not one
// orderable type, whichever two are compared, so sorting refuses the same
// payloads insertion did. Compound items are inserted one at a time, in place:
// the same binary search, without rebuilding the slice for each.
func setFromWire(items []Value) (VSet, error) {
	if !slices.ContainsFunc(items, isCompoundKey) {
		return buildSetSorted(items)
	}
	out := make([]Value, 0, len(items))
	for _, it := range items {
		idx, found, err := setSearch(VSet{Items: out}, it)
		if err != nil {
			return VSet{}, err
		}
		if !found {
			out = slices.Insert(out, idx, it)
		}
	}
	return VSet{Items: slices.Clip(out)}, nil
}

// dictFromWire is setFromWire for `{"__dict":[[k,v],...]}`.
func dictFromWire(pairs []VDictPair) (VDict, error) {
	if !slices.ContainsFunc(pairs, func(p VDictPair) bool { return isCompoundKey(p.Key) }) {
		return buildDictSorted(pairs)
	}
	out := make([]VDictPair, 0, len(pairs))
	for _, p := range pairs {
		idx, found, err := dictSearch(VDict{Pairs: out}, p.Key)
		if err != nil {
			return VDict{}, err
		}
		if found {
			out[idx] = p
		} else {
			out = slices.Insert(out, idx, p)
		}
	}
	return VDict{Pairs: slices.Clip(out)}, nil
}
