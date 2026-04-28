package runtime

import (
	"fmt"
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
			var out []Value
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
	}
}
