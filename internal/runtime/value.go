// Package runtime provides a tree-walking interpreter for Mar programs.
//
// The interpreter assumes the program has been type-checked. It does not
// perform any runtime type checks; if a program reaches the interpreter
// without going through typecheck, behavior is undefined for ill-typed
// programs.
//
// Pure functional evaluation lives here; effects, I/O, and concurrency
// are layered on top via VEffect (effect.go) and the per-domain
// builtins (io.go, repo.go, server.go, …).
package runtime

import (
	"fmt"
	"strings"
)

// Value is a runtime value.
type Value interface {
	isValue()
	Display() string
}

// VInt is an integer value.
type VInt struct{ V int64 }

func (VInt) isValue()          {}
func (v VInt) Display() string { return fmt.Sprintf("%d", v.V) }

// VFloat is a floating-point value.
type VFloat struct{ V float64 }

func (VFloat) isValue()          {}
func (v VFloat) Display() string { return fmt.Sprintf("%g", v.V) }

// VString is a string value.
type VString struct{ V string }

func (VString) isValue()          {}
func (v VString) Display() string { return fmt.Sprintf("%q", v.V) }

// VChar is a single Unicode code point (rune in Go terms). Distinct
// from VString so that `'a' == "a"` is a type error rather than a
// silently-true equality, and so List Char / String round-trip
// (via String.toList / fromList) stays meaningful.
//
// Wire format on the JSON boundary is `{"__char": "x"}` — see json.go.
type VChar struct{ V rune }

func (VChar) isValue()          {}
func (v VChar) Display() string { return fmt.Sprintf("'%c'", v.V) }

// VBool is a boolean value.
type VBool struct{ V bool }

func (VBool) isValue() {}
func (v VBool) Display() string {
	if v.V {
		return "True"
	}
	return "False"
}

// VUnit is the unit value ().
type VUnit struct{}

func (VUnit) isValue()        {}
func (VUnit) Display() string { return "()" }

// VDuration is a time interval, normalized to seconds. Built only via
// Time.seconds / Time.minutes / Time.hours / Time.days / Time.weeks
// — there's no public Int → Duration coercion, so unit confusion is
// impossible at the call site.
type VDuration struct{ Seconds int64 }

func (VDuration) isValue() {}
func (v VDuration) Display() string {
	return fmt.Sprintf("%ds", v.Seconds)
}

// VTime is an absolute moment in time, as Unix milliseconds. Built
// via Time.now (an effect that reads the wall clock) or
// Time.fromIso. Use Time.add / Time.sub to shift by a Duration;
// Time.diff for the difference between two moments.
type VTime struct{ Millis int64 }

func (VTime) isValue() {}
func (v VTime) Display() string {
	return fmt.Sprintf("<time:%dms>", v.Millis)
}

// VFn is a function value: either a closure (user lambda) or a built-in.
type VFn struct {
	// For closures:
	Params []string
	Body   any // *ast.Expr — typed as any to avoid import cycle
	Env    *Env
	// For built-ins:
	Native func([]Value) (Value, error)
	// For partial application: arguments collected so far.
	Applied []Value
	// Total arity (params for closure, fixed for native).
	Arity int
	// CtorTag is set when this VFn was produced by makeCtorValue for a
	// custom-type constructor with arity ≥ 1. Empty string for plain
	// functions. Renderers read this to recognize a constructor
	// without having to apply the function (e.g. capturing it as a
	// form's submit msg constructor).
	CtorTag string
}

func (VFn) isValue() {}
func (f VFn) Display() string {
	return fmt.Sprintf("<function/%d>", f.Arity-len(f.Applied))
}

// VCtor is a constructed value: user-defined custom type tag plus payload.
type VCtor struct {
	Tag  string
	Args []Value
}

func (VCtor) isValue() {}
func (c VCtor) Display() string {
	if len(c.Args) == 0 {
		return c.Tag
	}
	parts := make([]string, len(c.Args))
	for i, a := range c.Args {
		parts[i] = atomicDisplay(a)
	}
	return c.Tag + " " + strings.Join(parts, " ")
}

// VRecord is a record value.
type VRecord struct {
	Fields map[string]Value
	Order  []string
}

func (VRecord) isValue() {}
func (r VRecord) Display() string {
	if len(r.Fields) == 0 {
		return "{}"
	}
	var sb strings.Builder
	sb.WriteString("{ ")
	for i, n := range r.Order {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(n)
		sb.WriteString(" = ")
		sb.WriteString(r.Fields[n].Display())
	}
	sb.WriteString(" }")
	return sb.String()
}

// VList is a list value.
type VList struct {
	Elements []Value
}

func (VList) isValue() {}
func (l VList) Display() string {
	parts := make([]string, len(l.Elements))
	for i, e := range l.Elements {
		parts[i] = e.Display()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// VTuple is a tuple value.
type VTuple struct {
	Members []Value
}

func (VTuple) isValue() {}
func (t VTuple) Display() string {
	parts := make([]string, len(t.Members))
	for i, m := range t.Members {
		parts[i] = m.Display()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// VDict is an ordered dictionary value (the Dict module's underlying
// representation). Keys are kept sorted by compareValues to give
// O(n) ops on insert/get/remove for now — a simple, easy-to-audit
// implementation. Upgrade to a balanced tree if perfilation shows a
// hot path.
//
// The pairs slice IS the canonical representation; keeping it sorted
// lets us:
//   - Diff two dicts (union/intersect/diff) in linear time
//   - Iterate in deterministic key order (so toList is stable)
//   - Equality-compare via memberwise pair equality (no rebalance noise)
//
// Keys must be "comparable" per compareValues — Int / Float / String /
// Char. The typechecker enforces this at call sites; the runtime
// returns an error if a non-comparable key sneaks through.
type VDict struct {
	// Pairs is sorted ascending by Key (per compareValues). MUST
	// stay sorted across mutations — every helper that touches
	// pairs rebuilds them in order.
	Pairs []VDictPair
}

// VDictPair is one key/value entry. Stored as a struct (not a tuple)
// so the Go compiler keeps it tight; the user-facing API exposes
// these as `(k, v)` tuples via Dict.toList.
type VDictPair struct {
	Key   Value
	Value Value
}

func (VDict) isValue() {}
func (d VDict) Display() string {
	if len(d.Pairs) == 0 {
		return "Dict.empty"
	}
	parts := make([]string, len(d.Pairs))
	for i, p := range d.Pairs {
		parts[i] = p.Key.Display() + " => " + p.Value.Display()
	}
	return "Dict{" + strings.Join(parts, ", ") + "}"
}

// VSet is an ordered set value (the Set module's underlying
// representation). Items are kept sorted by compareValues. Same
// "comparable-only" key constraint as VDict — the typechecker
// enforces it at call sites, the runtime guards against accidents.
//
// Internally we could have reused VDict-with-Unit-values (which is how
// Elm implements Set), but a dedicated VSet keeps Display() readable
// ("Set{1, 2, 3}" vs "Dict{1 => (), 2 => (), 3 => ()}") and lets the
// JSON codec emit a cleaner wire ("__set":[1,2,3]) without a synthetic
// Unit on every entry.
type VSet struct {
	// Items is sorted ascending per compareValues. MUST stay sorted
	// across mutations — every helper rebuilds them in order.
	Items []Value
}

func (VSet) isValue() {}
func (s VSet) Display() string {
	if len(s.Items) == 0 {
		return "Set.empty"
	}
	parts := make([]string, len(s.Items))
	for i, it := range s.Items {
		parts[i] = it.Display()
	}
	return "Set{" + strings.Join(parts, ", ") + "}"
}

func atomicDisplay(v Value) string {
	d := v.Display()
	if _, ok := v.(VCtor); ok {
		c := v.(VCtor)
		if len(c.Args) > 0 {
			return "(" + d + ")"
		}
	}
	return d
}
