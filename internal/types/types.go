// Package types implements the Hindley-Milner type system for mar-lang.
//
// See DESIGN.md for the full plan. This file defines the Type AST and the
// pretty-printer. Other files in the package implement substitution,
// unification, generalization/instantiation, the initial environment of
// builtins, and the inference algorithm itself.
package types

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// Type is the interface implemented by every type AST node.
type Type interface {
	isType()
	String() string
}

// TVar is a type variable. Identity is the int ID; equality is by ID.
//
// Variables are immutable values. Unification records bindings in a Subst,
// not on the variable itself. To find the current binding of a variable, use
// Subst.Resolve or Subst.Apply.
type TVar struct {
	ID int
}

// TCon is a type constructor (nullary or n-ary).
//
// Conventions:
//   - Primitive types are nullary cons: TCon{"bool", nil}, TCon{"int", nil}, ...
//   - Function types are TCon{"->", [arg1, arg2, ..., argN, ret]}. The last
//     element of Args is the return type; the rest are the parameters in
//     declaration order.
//   - Generic containers are n-ary: TCon{"list", [elem]}, TCon{"maybe", [a]},
//     TCon{"result", [err, ok]}.
type TCon struct {
	Name string
	Args []Type
}

// TRecord represents a record type, either closed nominal or open structural.
//
//   - Name != "" and Tail == nil:  closed nominal record (e.g. entity Post).
//     Two such records unify only if their Names match.
//   - Tail != nil (typically a TVar):  open structural record. Means
//     "{f1: T1, ..., fn: Tn | ρ}" — at least these fields, plus whatever is
//     in ρ. Used by row polymorphism for functions that work on any record
//     having a given set of fields.
//   - Name == "" and Tail == nil:  closed anonymous record. Two of these
//     unify only if their fields and types match exactly.
//
// Order preserves declaration order for stable pretty-printing.
type TRecord struct {
	Name   string
	Fields map[string]Type
	Order  []string
	Tail   Type // nil = closed; non-nil (usually TVar) = open row
}

// TUnion is a nominal tagged union (sum type). Variants maps each tag to its
// payload types in order; VariantOrder lists tags in declaration order;
// FieldNames optionally records the named fields of each variant.
type TUnion struct {
	Name         string
	Variants     map[string][]Type
	VariantOrder []string
	FieldNames   map[string][]string
}

// TForall is a type scheme with universally quantified variables. Only appears
// at the top of a scheme stored in the type environment — never nested inside
// another type (rank-1 polymorphism).
type TForall struct {
	Vars []int
	Body Type
}

func (TVar) isType()     {}
func (TCon) isType()     {}
func (TRecord) isType()  {}
func (TUnion) isType()   {}
func (TForall) isType()  {}

// Built-in nullary type constructors. Provided as constructors rather than
// vars so callers always get a fresh value (no accidental sharing).
func TBool() Type     { return TCon{Name: "bool"} }
func TInt() Type      { return TCon{Name: "int"} }
func TDecimal() Type  { return TCon{Name: "decimal"} }
func TString() Type   { return TCon{Name: "string"} }
func TDate() Type     { return TCon{Name: "date"} }
func TDateTime() Type { return TCon{Name: "datetime"} }
func TCursor() Type   { return TCon{Name: "cursor"} }
func TUnit() Type     { return TCon{Name: "unit"} }

// TList builds a list type with the given element type.
func TList(elem Type) Type { return TCon{Name: "list", Args: []Type{elem}} }

// TMaybe builds a maybe type with the given inner type.
func TMaybe(inner Type) Type { return TCon{Name: "maybe", Args: []Type{inner}} }

// TResult builds a result type with the given error and ok types.
func TResult(err, ok Type) Type { return TCon{Name: "result", Args: []Type{err, ok}} }

// TArrow builds a function type with the given parameter types and return
// type. The wire format is TCon{"->", params... + ret}, so a zero-arg function
// is TCon{"->", [ret]}.
func TArrow(params []Type, ret Type) Type {
	args := make([]Type, 0, len(params)+1)
	args = append(args, params...)
	args = append(args, ret)
	return TCon{Name: "->", Args: args}
}

// TCmd builds a Cmd type parameterized by the message type a screen handles.
// `(command (query args...) ok-msg fail-msg)` evaluates to Cmd Msg in F3.
func TCmd(msg Type) Type { return TCon{Name: "cmd", Args: []Type{msg}} }

// TView builds a View type parameterized by the message type a screen emits
// from interactive items. Reserved for F5.
func TView(msg Type) Type { return TCon{Name: "view", Args: []Type{msg}} }

// IsArrow reports whether t is a function type and returns its parameter
// types and return type.
func IsArrow(t Type) (params []Type, ret Type, ok bool) {
	c, isCon := t.(TCon)
	if !isCon || c.Name != "->" || len(c.Args) == 0 {
		return nil, nil, false
	}
	return c.Args[:len(c.Args)-1], c.Args[len(c.Args)-1], true
}

var nextVarID atomic.Int64

// FreshVar returns a new unbound type variable with a unique ID.
func FreshVar() TVar {
	id := nextVarID.Add(1)
	return TVar{ID: int(id)}
}

// FreeVars returns the IDs of all free type variables in t. A variable is
// "free" if it is unbound (Ref == nil) and not quantified by an enclosing
// TForall.
func FreeVars(t Type) []int {
	seen := map[int]struct{}{}
	collectFreeVars(t, nil, seen)
	out := make([]int, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Ints(out)
	return out
}

func collectFreeVars(t Type, bound map[int]struct{}, out map[int]struct{}) {
	switch tt := t.(type) {
	case TVar:
		if _, isBound := bound[tt.ID]; isBound {
			return
		}
		out[tt.ID] = struct{}{}
	case TCon:
		for _, a := range tt.Args {
			collectFreeVars(a, bound, out)
		}
	case TRecord:
		for _, t := range tt.Fields {
			collectFreeVars(t, bound, out)
		}
		if tt.Tail != nil {
			collectFreeVars(tt.Tail, bound, out)
		}
	case TUnion:
		for _, tag := range tt.VariantOrder {
			for _, payload := range tt.Variants[tag] {
				collectFreeVars(payload, bound, out)
			}
		}
	case TForall:
		nested := map[int]struct{}{}
		for k := range bound {
			nested[k] = struct{}{}
		}
		for _, v := range tt.Vars {
			nested[v] = struct{}{}
		}
		collectFreeVars(tt.Body, nested, out)
	}
}

// String implementations.

func (v TVar) String() string {
	return varName(v.ID)
}

// varName turns an internal var ID into a readable Greek-letter name:
// 1 → α, 2 → β, ..., 24 → ω, 25 → α₁, ...
func varName(id int) string {
	const greek = "αβγδεζηθικλμνξοπρστυφχψω"
	letters := []rune(greek)
	idx := (id - 1) % len(letters)
	cycle := (id - 1) / len(letters)
	if cycle == 0 {
		return string(letters[idx])
	}
	return fmt.Sprintf("%s%d", string(letters[idx]), cycle)
}

func (c TCon) String() string {
	if c.Name == "->" {
		params, ret, _ := IsArrow(c)
		parts := make([]string, 0, len(params))
		for _, p := range params {
			parts = append(parts, atomString(p))
		}
		if len(parts) == 0 {
			return fmt.Sprintf("(-> %s)", ret.String())
		}
		return fmt.Sprintf("(%s -> %s)", strings.Join(parts, " "), ret.String())
	}
	if len(c.Args) == 0 {
		return c.Name
	}
	parts := make([]string, 0, len(c.Args))
	for _, a := range c.Args {
		parts = append(parts, atomString(a))
	}
	return fmt.Sprintf("(%s %s)", c.Name, strings.Join(parts, " "))
}

// atomString wraps t in parentheses if needed for unambiguous nesting. For
// nullary cons and TVar the bare form is fine; everything else gets parens
// when used as an argument inside another type.
func atomString(t Type) string {
	switch tt := t.(type) {
	case TVar:
		return tt.String()
	case TCon:
		if len(tt.Args) == 0 {
			return tt.String()
		}
		return tt.String() // already parenthesized
	case TRecord:
		return tt.String()
	case TUnion:
		return tt.String()
	case TForall:
		return tt.String()
	default:
		return t.String()
	}
}

func (r TRecord) String() string {
	if r.Name != "" && r.Tail == nil {
		return r.Name
	}
	order := r.Order
	if len(order) == 0 {
		// Fallback: derive order from map for stable string.
		order = make([]string, 0, len(r.Fields))
		for k := range r.Fields {
			order = append(order, k)
		}
		sort.Strings(order)
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		t, ok := r.Fields[name]
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", name, t.String()))
	}
	body := strings.Join(parts, ", ")
	if r.Tail != nil {
		return fmt.Sprintf("{%s | %s}", body, r.Tail.String())
	}
	return fmt.Sprintf("{%s}", body)
}

func (u TUnion) String() string {
	if u.Name != "" {
		return u.Name
	}
	parts := make([]string, 0, len(u.VariantOrder))
	for _, tag := range u.VariantOrder {
		payload := u.Variants[tag]
		if len(payload) == 0 {
			parts = append(parts, fmt.Sprintf("(%s)", tag))
			continue
		}
		args := make([]string, 0, len(payload))
		for _, p := range payload {
			args = append(args, atomString(p))
		}
		parts = append(parts, fmt.Sprintf("(%s %s)", tag, strings.Join(args, " ")))
	}
	return strings.Join(parts, " | ")
}

func (f TForall) String() string {
	if len(f.Vars) == 0 {
		return f.Body.String()
	}
	names := make([]string, 0, len(f.Vars))
	for _, id := range f.Vars {
		names = append(names, varName(id))
	}
	return fmt.Sprintf("∀%s. %s", strings.Join(names, " "), f.Body.String())
}

// resetVarIDsForTesting resets the global counter. Use only in tests.
func resetVarIDsForTesting() {
	nextVarID.Store(0)
}
