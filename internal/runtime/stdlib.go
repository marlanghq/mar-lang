package runtime

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// extendBaseEnv augments env with stdlib functions: List.*, String.*, Maybe.*, etc.
//
// Names are flat (no module prefix yet), so the user calls e.g. listMap, listLength.
// Once qualified names work, these become List.map, etc.
func extendBaseEnv(env *Env) *Env {
	for name, v := range stdlib() {
		env.Define(name, v)
	}
	return env
}

func stdlib() map[string]Value {
	return map[string]Value{
		// List ops
		"listLength": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("listLength: not a list")
			}
			return VInt{V: int64(len(l.Elements))}, nil
		}),
		"listMap": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listMap: second arg not a list")
			}
			out := make([]Value, len(l.Elements))
			for i, e := range l.Elements {
				v, err := apply(fn, e)
				if err != nil {
					return nil, err
				}
				out[i] = v
			}
			return VList{Elements: out}, nil
		}),
		"listFilter": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listFilter: second arg not a list")
			}
			out := make([]Value, 0, len(l.Elements))
			for _, e := range l.Elements {
				v, err := apply(fn, e)
				if err != nil {
					return nil, err
				}
				b, ok := v.(VBool)
				if !ok {
					return nil, fmt.Errorf("listFilter: predicate didn't return Bool")
				}
				if b.V {
					out = append(out, e)
				}
			}
			return VList{Elements: out}, nil
		}),
		"listFoldl": nativeFn(3, func(args []Value) (Value, error) {
			fn := args[0]
			acc := args[1]
			l, ok := args[2].(VList)
			if !ok {
				return nil, fmt.Errorf("listFoldl: third arg not a list")
			}
			for _, e := range l.Elements {
				partial, err := apply(fn, acc)
				if err != nil {
					return nil, err
				}
				next, err := apply(partial, e)
				if err != nil {
					return nil, err
				}
				acc = next
			}
			return acc, nil
		}),
		"listSum": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("listSum: not a list")
			}
			var sum int64 = 0
			for _, e := range l.Elements {
				iv, ok := e.(VInt)
				if !ok {
					return nil, fmt.Errorf("listSum: element not Int")
				}
				sum += iv.V
			}
			return VInt{V: sum}, nil
		}),
		"listRange": nativeFn(2, func(args []Value) (Value, error) {
			from, ok1 := args[0].(VInt)
			to, ok2 := args[1].(VInt)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("listRange: expected Int args")
			}
			var out []Value
			for i := from.V; i <= to.V; i++ {
				out = append(out, VInt{V: i})
			}
			return VList{Elements: out}, nil
		}),
		"listReverse": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("listReverse: not a list")
			}
			n := len(l.Elements)
			out := make([]Value, n)
			for i, e := range l.Elements {
				out[n-1-i] = e
			}
			return VList{Elements: out}, nil
		}),
		"listHead": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("listHead: not a list")
			}
			if len(l.Elements) == 0 {
				return VCtor{Tag: "Nothing"}, nil
			}
			return VCtor{Tag: "Just", Args: []Value{l.Elements[0]}}, nil
		}),
		"listTail": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("listTail: not a list")
			}
			if len(l.Elements) == 0 {
				return VCtor{Tag: "Nothing"}, nil
			}
			return VCtor{Tag: "Just", Args: []Value{VList{Elements: l.Elements[1:]}}}, nil
		}),
		"listIsEmpty": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("listIsEmpty: not a list")
			}
			return VBool{V: len(l.Elements) == 0}, nil
		}),
		"listConcat": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("listConcat: not a list")
			}
			var out []Value
			for _, e := range l.Elements {
				inner, ok := e.(VList)
				if !ok {
					return nil, fmt.Errorf("listConcat: element not a list")
				}
				out = append(out, inner.Elements...)
			}
			return VList{Elements: out}, nil
		}),
		// listTake : Int -> List a -> List a
		"listTake": nativeFn(2, func(args []Value) (Value, error) {
			n, ok1 := args[0].(VInt)
			l, ok2 := args[1].(VList)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("listTake: expected Int, List")
			}
			k := int(n.V)
			if k <= 0 {
				return VList{Elements: nil}, nil
			}
			if k >= len(l.Elements) {
				return l, nil
			}
			out := make([]Value, k)
			copy(out, l.Elements[:k])
			return VList{Elements: out}, nil
		}),
		// listDrop : Int -> List a -> List a
		"listDrop": nativeFn(2, func(args []Value) (Value, error) {
			n, ok1 := args[0].(VInt)
			l, ok2 := args[1].(VList)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("listDrop: expected Int, List")
			}
			k := int(n.V)
			if k <= 0 {
				return l, nil
			}
			if k >= len(l.Elements) {
				return VList{Elements: nil}, nil
			}
			out := make([]Value, len(l.Elements)-k)
			copy(out, l.Elements[k:])
			return VList{Elements: out}, nil
		}),
		// listMove : Int -> Int -> List a -> List a
		// Pure splice: removes element at `from`, inserts at `to`.
		// Defensive no-ops:
		//   - from == to               → input unchanged (common: user
		//                                released drag where they
		//                                started)
		//   - either index out of bounds → input unchanged (covers
		//                                stale Msgs from races
		//                                between client and server
		//                                where the model already
		//                                shrunk)
		"listMove": nativeFn(3, func(args []Value) (Value, error) {
			fromV, ok1 := args[0].(VInt)
			toV, ok2 := args[1].(VInt)
			l, ok3 := args[2].(VList)
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("listMove: expected Int, Int, List")
			}
			from := int(fromV.V)
			to := int(toV.V)
			n := len(l.Elements)
			if from == to || from < 0 || from >= n || to < 0 || to >= n {
				return l, nil
			}
			out := make([]Value, 0, n)
			out = append(out, l.Elements[:from]...)
			out = append(out, l.Elements[from+1:]...)
			// insert at `to` in the post-removal list
			elt := l.Elements[from]
			out = append(out[:to], append([]Value{elt}, out[to:]...)...)
			return VList{Elements: out}, nil
		}),
		// listMember : a -> List a -> Bool — structural equality.
		"listMember": nativeFn(2, func(args []Value) (Value, error) {
			needle := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listMember: second arg not a list")
			}
			for _, e := range l.Elements {
				if equalValues(needle, e) {
					return VBool{V: true}, nil
				}
			}
			return VBool{V: false}, nil
		}),
		// listAny : (a -> Bool) -> List a -> Bool — short-circuit.
		"listAny": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listAny: second arg not a list")
			}
			for _, e := range l.Elements {
				v, err := apply(fn, e)
				if err != nil {
					return nil, err
				}
				b, ok := v.(VBool)
				if !ok {
					return nil, fmt.Errorf("listAny: predicate didn't return Bool")
				}
				if b.V {
					return VBool{V: true}, nil
				}
			}
			return VBool{V: false}, nil
		}),
		// listAll : (a -> Bool) -> List a -> Bool — short-circuit.
		"listAll": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listAll: second arg not a list")
			}
			for _, e := range l.Elements {
				v, err := apply(fn, e)
				if err != nil {
					return nil, err
				}
				b, ok := v.(VBool)
				if !ok {
					return nil, fmt.Errorf("listAll: predicate didn't return Bool")
				}
				if !b.V {
					return VBool{V: false}, nil
				}
			}
			return VBool{V: true}, nil
		}),
		// listFoldr : (a -> b -> b) -> b -> List a -> b — fold from
		// the right. Same shape as foldl but the accumulator is the
		// SECOND argument to the combine fn (matches Elm).
		"listFoldr": nativeFn(3, func(args []Value) (Value, error) {
			fn := args[0]
			acc := args[1]
			l, ok := args[2].(VList)
			if !ok {
				return nil, fmt.Errorf("listFoldr: third arg not a list")
			}
			for i := len(l.Elements) - 1; i >= 0; i-- {
				partial, err := apply(fn, l.Elements[i])
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
		// listIndexedMap : (Int -> a -> b) -> List a -> List b
		"listIndexedMap": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listIndexedMap: second arg not a list")
			}
			out := make([]Value, len(l.Elements))
			for i, e := range l.Elements {
				partial, err := apply(fn, VInt{V: int64(i)})
				if err != nil {
					return nil, err
				}
				v, err := apply(partial, e)
				if err != nil {
					return nil, err
				}
				out[i] = v
			}
			return VList{Elements: out}, nil
		}),
		// listRepeat : Int -> a -> List a
		"listRepeat": nativeFn(2, func(args []Value) (Value, error) {
			n, ok := args[0].(VInt)
			if !ok {
				return nil, fmt.Errorf("listRepeat: expected Int")
			}
			k := int(n.V)
			if k <= 0 {
				return VList{Elements: nil}, nil
			}
			out := make([]Value, k)
			for i := 0; i < k; i++ {
				out[i] = args[1]
			}
			return VList{Elements: out}, nil
		}),
		// listIntersperse : a -> List a -> List a — insert separator
		// between each pair of elements. Empty/single-element lists
		// stay unchanged.
		"listIntersperse": nativeFn(2, func(args []Value) (Value, error) {
			sep := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listIntersperse: second arg not a list")
			}
			n := len(l.Elements)
			if n <= 1 {
				return l, nil
			}
			out := make([]Value, 0, 2*n-1)
			for i, e := range l.Elements {
				if i > 0 {
					out = append(out, sep)
				}
				out = append(out, e)
			}
			return VList{Elements: out}, nil
		}),
		// listPartition : (a -> Bool) -> List a -> (List a, List a)
		// First tuple element holds matches, second holds rejects —
		// matches Elm semantics.
		"listPartition": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listPartition: second arg not a list")
			}
			var yes, no []Value
			for _, e := range l.Elements {
				v, err := apply(fn, e)
				if err != nil {
					return nil, err
				}
				b, ok := v.(VBool)
				if !ok {
					return nil, fmt.Errorf("listPartition: predicate didn't return Bool")
				}
				if b.V {
					yes = append(yes, e)
				} else {
					no = append(no, e)
				}
			}
			return VTuple{Members: []Value{VList{Elements: yes}, VList{Elements: no}}}, nil
		}),
		// listConcatMap : (a -> List b) -> List a -> List b
		"listConcatMap": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listConcatMap: second arg not a list")
			}
			var out []Value
			for _, e := range l.Elements {
				v, err := apply(fn, e)
				if err != nil {
					return nil, err
				}
				inner, ok := v.(VList)
				if !ok {
					return nil, fmt.Errorf("listConcatMap: function didn't return a List")
				}
				out = append(out, inner.Elements...)
			}
			return VList{Elements: out}, nil
		}),
		// listFilterMap : (a -> Maybe b) -> List a -> List b
		// Keeps elements where the function returns Just; drops Nothings.
		"listFilterMap": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listFilterMap: second arg not a list")
			}
			var out []Value
			for _, e := range l.Elements {
				v, err := apply(fn, e)
				if err != nil {
					return nil, err
				}
				c, ok := v.(VCtor)
				if !ok {
					return nil, fmt.Errorf("listFilterMap: function didn't return a Maybe")
				}
				if c.Tag == "Just" && len(c.Args) == 1 {
					out = append(out, c.Args[0])
				}
			}
			return VList{Elements: out}, nil
		}),
		// listMaximum / listMinimum : List a -> Maybe a — uses the
		// shared compareValues, so works for List Int / List Float /
		// List String. Other element types yield Nothing rather
		// than a hard error.
		"listMaximum": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("listMaximum: not a list")
			}
			if len(l.Elements) == 0 {
				return VCtor{Tag: "Nothing"}, nil
			}
			best := l.Elements[0]
			for _, e := range l.Elements[1:] {
				c, err := compareValues(best, e)
				if err != nil {
					return VCtor{Tag: "Nothing"}, nil
				}
				if c < 0 {
					best = e
				}
			}
			return VCtor{Tag: "Just", Args: []Value{best}}, nil
		}),
		"listMinimum": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("listMinimum: not a list")
			}
			if len(l.Elements) == 0 {
				return VCtor{Tag: "Nothing"}, nil
			}
			best := l.Elements[0]
			for _, e := range l.Elements[1:] {
				c, err := compareValues(best, e)
				if err != nil {
					return VCtor{Tag: "Nothing"}, nil
				}
				if c > 0 {
					best = e
				}
			}
			return VCtor{Tag: "Just", Args: []Value{best}}, nil
		}),
		// listProduct : List Int -> Int — mirrors listSum's shape.
		"listProduct": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("listProduct: not a list")
			}
			var p int64 = 1
			for _, e := range l.Elements {
				iv, ok := e.(VInt)
				if !ok {
					return nil, fmt.Errorf("listProduct: element not Int")
				}
				p *= iv.V
			}
			return VInt{V: p}, nil
		}),
		// listSort : List a -> List a — comparable elements via the
		// shared compareValues. Mirrors Elm: stable sort.
		"listSort": nativeFn(1, func(args []Value) (Value, error) {
			l, ok := args[0].(VList)
			if !ok {
				return nil, fmt.Errorf("listSort: not a list")
			}
			out := append([]Value(nil), l.Elements...)
			var sortErr error
			sort.SliceStable(out, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				c, err := compareValues(out[i], out[j])
				if err != nil {
					sortErr = err
					return false
				}
				return c < 0
			})
			if sortErr != nil {
				return nil, sortErr
			}
			return VList{Elements: out}, nil
		}),
		// listSortBy : (a -> b) -> List a -> List a — sort by a
		// derived key. The key extractor runs once per element
		// (cached) so a 30-elem list runs the fn 30 times, not
		// O(n log n) times.
		"listSortBy": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listSortBy: second arg not a list")
			}
			keys := make([]Value, len(l.Elements))
			for i, e := range l.Elements {
				k, err := apply(fn, e)
				if err != nil {
					return nil, err
				}
				keys[i] = k
			}
			idx := make([]int, len(l.Elements))
			for i := range idx {
				idx[i] = i
			}
			var sortErr error
			sort.SliceStable(idx, func(a, b int) bool {
				if sortErr != nil {
					return false
				}
				c, err := compareValues(keys[idx[a]], keys[idx[b]])
				if err != nil {
					sortErr = err
					return false
				}
				return c < 0
			})
			if sortErr != nil {
				return nil, sortErr
			}
			out := make([]Value, len(l.Elements))
			for i, j := range idx {
				out[i] = l.Elements[j]
			}
			return VList{Elements: out}, nil
		}),
		// listSortWith : (a -> a -> Order) -> List a -> List a
		// Comparator returns LT / EQ / GT (a 3-way ADT, not -1/0/1).
		// We sort by "less-than", which is "comparator returned LT".
		"listSortWith": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			l, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("listSortWith: second arg not a list")
			}
			out := append([]Value(nil), l.Elements...)
			var sortErr error
			sort.SliceStable(out, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				partial, err := apply(fn, out[i])
				if err != nil {
					sortErr = err
					return false
				}
				v, err := apply(partial, out[j])
				if err != nil {
					sortErr = err
					return false
				}
				c, ok := v.(VCtor)
				if !ok {
					sortErr = fmt.Errorf("listSortWith: comparator didn't return Order")
					return false
				}
				switch c.Tag {
				case "LT":
					return true
				case "EQ", "GT":
					return false
				default:
					sortErr = fmt.Errorf("listSortWith: comparator returned %s, expected LT/EQ/GT", c.Tag)
					return false
				}
			})
			if sortErr != nil {
				return nil, sortErr
			}
			return VList{Elements: out}, nil
		}),

		// String ops
		"stringLength": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("stringLength: not a String")
			}
			return VInt{V: int64(len([]rune(s.V)))}, nil
		}),
		"stringContains": nativeFn(2, func(args []Value) (Value, error) {
			needle, ok1 := args[0].(VString)
			hay, ok2 := args[1].(VString)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("stringContains: expected String args")
			}
			return VBool{V: strings.Contains(hay.V, needle.V)}, nil
		}),
		"stringStartsWith": nativeFn(2, func(args []Value) (Value, error) {
			prefix, ok1 := args[0].(VString)
			s, ok2 := args[1].(VString)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("stringStartsWith: expected String args")
			}
			return VBool{V: strings.HasPrefix(s.V, prefix.V)}, nil
		}),
		"stringFromInt": nativeFn(1, func(args []Value) (Value, error) {
			i, ok := args[0].(VInt)
			if !ok {
				return nil, fmt.Errorf("stringFromInt: not an Int")
			}
			return VString{V: fmt.Sprintf("%d", i.V)}, nil
		}),
		"stringToUpper": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("stringToUpper: not a String")
			}
			return VString{V: strings.ToUpper(s.V)}, nil
		}),
		"stringToLower": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("stringToLower: not a String")
			}
			return VString{V: strings.ToLower(s.V)}, nil
		}),

		// Maybe helpers
		"maybeWithDefault": nativeFn(2, func(args []Value) (Value, error) {
			def := args[0]
			c, ok := args[1].(VCtor)
			if !ok {
				return nil, fmt.Errorf("maybeWithDefault: not a Maybe")
			}
			switch c.Tag {
			case "Just":
				if len(c.Args) == 1 {
					return c.Args[0], nil
				}
			case "Nothing":
				return def, nil
			}
			return def, nil
		}),
		"maybeMap": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			c, ok := args[1].(VCtor)
			if !ok {
				return nil, fmt.Errorf("maybeMap: not a Maybe")
			}
			switch c.Tag {
			case "Just":
				v, err := apply(fn, c.Args[0])
				if err != nil {
					return nil, err
				}
				return VCtor{Tag: "Just", Args: []Value{v}}, nil
			case "Nothing":
				return c, nil
			}
			return c, nil
		}),
		"maybeAndThen": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			c, ok := args[1].(VCtor)
			if !ok {
				return nil, fmt.Errorf("maybeAndThen: not a Maybe")
			}
			switch c.Tag {
			case "Just":
				return apply(fn, c.Args[0])
			case "Nothing":
				return c, nil
			}
			return c, nil
		}),

		// Result helpers
		"resultMap": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			c, ok := args[1].(VCtor)
			if !ok {
				return nil, fmt.Errorf("resultMap: not a Result")
			}
			switch c.Tag {
			case "Ok":
				v, err := apply(fn, c.Args[0])
				if err != nil {
					return nil, err
				}
				return VCtor{Tag: "Ok", Args: []Value{v}}, nil
			case "Err":
				return c, nil
			}
			return c, nil
		}),
		"resultAndThen": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			c, ok := args[1].(VCtor)
			if !ok {
				return nil, fmt.Errorf("resultAndThen: not a Result")
			}
			switch c.Tag {
			case "Ok":
				return apply(fn, c.Args[0])
			case "Err":
				return c, nil
			}
			return c, nil
		}),
		"resultMapError": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			c, ok := args[1].(VCtor)
			if !ok {
				return nil, fmt.Errorf("resultMapError: not a Result")
			}
			switch c.Tag {
			case "Err":
				v, err := apply(fn, c.Args[0])
				if err != nil {
					return nil, err
				}
				return VCtor{Tag: "Err", Args: []Value{v}}, nil
			case "Ok":
				return c, nil
			}
			return c, nil
		}),
		// resultWithDefault : a -> Result e a -> a
		"resultWithDefault": nativeFn(2, func(args []Value) (Value, error) {
			fallback := args[0]
			c, ok := args[1].(VCtor)
			if !ok {
				return nil, fmt.Errorf("resultWithDefault: not a Result")
			}
			if c.Tag == "Ok" && len(c.Args) == 1 {
				return c.Args[0], nil
			}
			return fallback, nil
		}),
		// resultFromMaybe : err -> Maybe a -> Result err a
		"resultFromMaybe": nativeFn(2, func(args []Value) (Value, error) {
			err := args[0]
			c, ok := args[1].(VCtor)
			if !ok {
				return nil, fmt.Errorf("resultFromMaybe: not a Maybe")
			}
			if c.Tag == "Just" && len(c.Args) == 1 {
				return VCtor{Tag: "Ok", Args: []Value{c.Args[0]}}, nil
			}
			return VCtor{Tag: "Err", Args: []Value{err}}, nil
		}),
		// resultToMaybe : Result err a -> Maybe a — Ok x → Just x;
		// Err _ → Nothing (matches Elm; the error info is discarded).
		"resultToMaybe": nativeFn(1, func(args []Value) (Value, error) {
			c, ok := args[0].(VCtor)
			if !ok {
				return nil, fmt.Errorf("resultToMaybe: not a Result")
			}
			if c.Tag == "Ok" && len(c.Args) == 1 {
				return VCtor{Tag: "Just", Args: []Value{c.Args[0]}}, nil
			}
			return VCtor{Tag: "Nothing"}, nil
		}),

		// Maybe extras
		// maybeMap2 : (a -> b -> c) -> Maybe a -> Maybe b -> Maybe c
		"maybeMap2": nativeFn(3, func(args []Value) (Value, error) {
			fn := args[0]
			a, ok1 := args[1].(VCtor)
			b, ok2 := args[2].(VCtor)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("maybeMap2: not a Maybe")
			}
			if a.Tag != "Just" || b.Tag != "Just" {
				return VCtor{Tag: "Nothing"}, nil
			}
			partial, err := apply(fn, a.Args[0])
			if err != nil {
				return nil, err
			}
			v, err := apply(partial, b.Args[0])
			if err != nil {
				return nil, err
			}
			return VCtor{Tag: "Just", Args: []Value{v}}, nil
		}),
		// maybeMap3 : (a -> b -> c -> d) -> Maybe a -> Maybe b -> Maybe c -> Maybe d
		"maybeMap3": nativeFn(4, func(args []Value) (Value, error) {
			fn := args[0]
			a, ok1 := args[1].(VCtor)
			b, ok2 := args[2].(VCtor)
			c, ok3 := args[3].(VCtor)
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("maybeMap3: not a Maybe")
			}
			if a.Tag != "Just" || b.Tag != "Just" || c.Tag != "Just" {
				return VCtor{Tag: "Nothing"}, nil
			}
			p1, err := apply(fn, a.Args[0])
			if err != nil {
				return nil, err
			}
			p2, err := apply(p1, b.Args[0])
			if err != nil {
				return nil, err
			}
			v, err := apply(p2, c.Args[0])
			if err != nil {
				return nil, err
			}
			return VCtor{Tag: "Just", Args: []Value{v}}, nil
		}),
		// maybeAndMap : Maybe a -> Maybe (a -> b) -> Maybe b
		// Applicative-style chain: lets you build mapN out of map +
		// andMap. `Just f |> andMap (Just x)` ≡ `Just (f x)`.
		"maybeAndMap": nativeFn(2, func(args []Value) (Value, error) {
			val, ok1 := args[0].(VCtor)
			fn, ok2 := args[1].(VCtor)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("maybeAndMap: not a Maybe")
			}
			if val.Tag != "Just" || fn.Tag != "Just" {
				return VCtor{Tag: "Nothing"}, nil
			}
			v, err := apply(fn.Args[0], val.Args[0])
			if err != nil {
				return nil, err
			}
			return VCtor{Tag: "Just", Args: []Value{v}}, nil
		}),
		// maybeFilter : (a -> Bool) -> Maybe a -> Maybe a — keeps
		// Just only when the predicate passes; otherwise Nothing.
		"maybeFilter": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			c, ok := args[1].(VCtor)
			if !ok {
				return nil, fmt.Errorf("maybeFilter: not a Maybe")
			}
			if c.Tag != "Just" || len(c.Args) != 1 {
				return VCtor{Tag: "Nothing"}, nil
			}
			v, err := apply(fn, c.Args[0])
			if err != nil {
				return nil, err
			}
			b, ok := v.(VBool)
			if !ok {
				return nil, fmt.Errorf("maybeFilter: predicate didn't return Bool")
			}
			if b.V {
				return c, nil
			}
			return VCtor{Tag: "Nothing"}, nil
		}),

		// Tuple — minimal ops on 2-element tuples. Mar tuples are
		// VTuple values; these helpers normalize the most common
		// access / construction patterns.
		"tupleFirst": nativeFn(1, func(args []Value) (Value, error) {
			t, ok := args[0].(VTuple)
			if !ok || len(t.Members) < 2 {
				return nil, fmt.Errorf("tupleFirst: not a 2-tuple")
			}
			return t.Members[0], nil
		}),
		"tupleSecond": nativeFn(1, func(args []Value) (Value, error) {
			t, ok := args[0].(VTuple)
			if !ok || len(t.Members) < 2 {
				return nil, fmt.Errorf("tupleSecond: not a 2-tuple")
			}
			return t.Members[1], nil
		}),
		// tuplePair : a -> b -> (a, b) — sugar for `(a, b)` literal.
		"tuplePair": nativeFn(2, func(args []Value) (Value, error) {
			return VTuple{Members: []Value{args[0], args[1]}}, nil
		}),
		// tupleMapFirst / tupleMapSecond : (a -> a') -> (a, b) -> (a', b)
		"tupleMapFirst": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			t, ok := args[1].(VTuple)
			if !ok || len(t.Members) < 2 {
				return nil, fmt.Errorf("tupleMapFirst: not a 2-tuple")
			}
			v, err := apply(fn, t.Members[0])
			if err != nil {
				return nil, err
			}
			return VTuple{Members: []Value{v, t.Members[1]}}, nil
		}),
		"tupleMapSecond": nativeFn(2, func(args []Value) (Value, error) {
			fn := args[0]
			t, ok := args[1].(VTuple)
			if !ok || len(t.Members) < 2 {
				return nil, fmt.Errorf("tupleMapSecond: not a 2-tuple")
			}
			v, err := apply(fn, t.Members[1])
			if err != nil {
				return nil, err
			}
			return VTuple{Members: []Value{t.Members[0], v}}, nil
		}),
		// tupleMapBoth : (a -> a') -> (b -> b') -> (a, b) -> (a', b')
		"tupleMapBoth": nativeFn(3, func(args []Value) (Value, error) {
			fnA := args[0]
			fnB := args[1]
			t, ok := args[2].(VTuple)
			if !ok || len(t.Members) < 2 {
				return nil, fmt.Errorf("tupleMapBoth: not a 2-tuple")
			}
			a, err := apply(fnA, t.Members[0])
			if err != nil {
				return nil, err
			}
			b, err := apply(fnB, t.Members[1])
			if err != nil {
				return nil, err
			}
			return VTuple{Members: []Value{a, b}}, nil
		}),

		// String extras
		"stringSplit": nativeFn(2, func(args []Value) (Value, error) {
			sep, ok1 := args[0].(VString)
			s, ok2 := args[1].(VString)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("stringSplit: expected String, String")
			}
			parts := strings.Split(s.V, sep.V)
			out := make([]Value, len(parts))
			for i, p := range parts {
				out[i] = VString{V: p}
			}
			return VList{Elements: out}, nil
		}),
		"stringJoin": nativeFn(2, func(args []Value) (Value, error) {
			sep, ok1 := args[0].(VString)
			list, ok2 := args[1].(VList)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("stringJoin: expected String, List String")
			}
			parts := make([]string, len(list.Elements))
			for i, e := range list.Elements {
				s, ok := e.(VString)
				if !ok {
					return nil, fmt.Errorf("stringJoin: list element not String")
				}
				parts[i] = s.V
			}
			return VString{V: strings.Join(parts, sep.V)}, nil
		}),
		"stringTrim": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("stringTrim: expected String")
			}
			return VString{V: strings.TrimSpace(s.V)}, nil
		}),
		// stringEndsWith : String suffix -> String s -> Bool
		"stringEndsWith": nativeFn(2, func(args []Value) (Value, error) {
			suf, ok1 := args[0].(VString)
			s, ok2 := args[1].(VString)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("stringEndsWith: expected String args")
			}
			return VBool{V: strings.HasSuffix(s.V, suf.V)}, nil
		}),
		// stringToInt : String -> Maybe Int — Nothing on parse failure.
		// strconv.ParseInt(_, 10, 64) accepts a leading sign and rejects
		// empty / non-digit / overflow.
		"stringToInt": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("stringToInt: expected String")
			}
			n, err := strconv.ParseInt(strings.TrimSpace(s.V), 10, 64)
			if err != nil {
				return VCtor{Tag: "Nothing"}, nil
			}
			return VCtor{Tag: "Just", Args: []Value{VInt{V: n}}}, nil
		}),
		// stringToFloat : String -> Maybe Float
		"stringToFloat": nativeFn(1, func(args []Value) (Value, error) {
			s, ok := args[0].(VString)
			if !ok {
				return nil, fmt.Errorf("stringToFloat: expected String")
			}
			f, err := strconv.ParseFloat(strings.TrimSpace(s.V), 64)
			if err != nil {
				return VCtor{Tag: "Nothing"}, nil
			}
			return VCtor{Tag: "Just", Args: []Value{VFloat{V: f}}}, nil
		}),
		// stringFromFloat : Float -> String — uses Go's shortest
		// round-trip representation; preserves enough precision to
		// recover the same Float on parse.
		"stringFromFloat": nativeFn(1, func(args []Value) (Value, error) {
			f, ok := args[0].(VFloat)
			if !ok {
				return nil, fmt.Errorf("stringFromFloat: expected Float")
			}
			return VString{V: strconv.FormatFloat(f.V, 'g', -1, 64)}, nil
		}),
		// stringReplace : String needle -> String replacement -> String s -> String
		"stringReplace": nativeFn(3, func(args []Value) (Value, error) {
			needle, ok1 := args[0].(VString)
			rep, ok2 := args[1].(VString)
			s, ok3 := args[2].(VString)
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("stringReplace: expected String args")
			}
			return VString{V: strings.ReplaceAll(s.V, needle.V, rep.V)}, nil
		}),
		// stringRepeat : Int -> String -> String
		"stringRepeat": nativeFn(2, func(args []Value) (Value, error) {
			n, ok1 := args[0].(VInt)
			s, ok2 := args[1].(VString)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("stringRepeat: expected Int, String")
			}
			if n.V <= 0 {
				return VString{V: ""}, nil
			}
			return VString{V: strings.Repeat(s.V, int(n.V))}, nil
		}),
		// stringPadLeft / stringPadRight : Int -> Char -> String -> String
		// Pad with a single Char (matches Elm exactly). Forcing Char
		// here rather than String prevents the "I meant one space but
		// passed two by accident" footgun.
		"stringPadLeft": nativeFn(3, func(args []Value) (Value, error) {
			w, ok1 := args[0].(VInt)
			pad, ok2 := args[1].(VChar)
			s, ok3 := args[2].(VString)
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("stringPadLeft: expected Int, Char, String")
			}
			return VString{V: padString(s.V, int(w.V), string(pad.V), true)}, nil
		}),
		"stringPadRight": nativeFn(3, func(args []Value) (Value, error) {
			w, ok1 := args[0].(VInt)
			pad, ok2 := args[1].(VChar)
			s, ok3 := args[2].(VString)
			if !ok1 || !ok2 || !ok3 {
				return nil, fmt.Errorf("stringPadRight: expected Int, Char, String")
			}
			return VString{V: padString(s.V, int(w.V), string(pad.V), false)}, nil
		}),
		// stringIndexes : String needle -> String s -> List Int
		// Byte offsets of every occurrence (overlap NOT respected —
		// Elm's behavior too; KMP scan advances past each hit).
		"stringIndexes": nativeFn(2, func(args []Value) (Value, error) {
			needle, ok1 := args[0].(VString)
			s, ok2 := args[1].(VString)
			if !ok1 || !ok2 {
				return nil, fmt.Errorf("stringIndexes: expected String args")
			}
			if needle.V == "" {
				return VList{}, nil
			}
			var out []Value
			start := 0
			for {
				i := strings.Index(s.V[start:], needle.V)
				if i < 0 {
					break
				}
				out = append(out, VInt{V: int64(start + i)})
				start += i + len(needle.V)
			}
			return VList{Elements: out}, nil
		}),
	}
}

// padString left- or right-pads s with copies of pad until the result
// is at least width characters. When s is already >= width, returns
// s unchanged. When pad is empty (corner case), returns s unchanged
// so a typo doesn't silently produce an infinite loop.
func padString(s string, width int, pad string, left bool) string {
	if len(s) >= width || pad == "" {
		return s
	}
	need := width - len(s)
	filler := strings.Repeat(pad, (need+len(pad)-1)/len(pad))
	if len(filler) > need {
		filler = filler[:need]
	}
	if left {
		return filler + s
	}
	return s + filler
}
