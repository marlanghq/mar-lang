// Package runtime provides a tree-walking interpreter for Mar programs.
//
// The interpreter assumes the program has been type-checked. It does not
// perform any runtime type checks; if a program reaches the interpreter
// without going through typecheck, behavior is undefined for ill-typed
// programs.
//
// This is the MVP: enough to run pure functional programs end-to-end. No
// effects, no I/O, no concurrency. Those layers come on top.
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

func (VInt) isValue() {}
func (v VInt) Display() string { return fmt.Sprintf("%d", v.V) }

// VFloat is a floating-point value.
type VFloat struct{ V float64 }

func (VFloat) isValue() {}
func (v VFloat) Display() string { return fmt.Sprintf("%g", v.V) }

// VString is a string value.
type VString struct{ V string }

func (VString) isValue() {}
func (v VString) Display() string { return fmt.Sprintf("%q", v.V) }

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

func (VUnit) isValue()         {}
func (VUnit) Display() string  { return "()" }

// VFn is a function value: either a closure (user lambda) or a built-in.
type VFn struct {
	// For closures:
	Params  []string
	Body    any // *ast.Expr — typed as any to avoid import cycle
	Env     *Env
	// For built-ins:
	Native  func([]Value) (Value, error)
	// For partial application: arguments collected so far.
	Applied []Value
	// Total arity (params for closure, fixed for native).
	Arity   int
	// CtorTag is set when this VFn was produced by makeCtorValue for a
	// custom-type constructor with arity ≥ 1. Empty string for plain
	// functions. Used by View.form rendering to extract the constructor
	// name without applying the function.
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
