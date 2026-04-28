// Package typecheck implements Hindley-Milner type inference for the Mar
// (Elm-style) AST.
//
// This is a minimal self-contained implementation. It does not reuse the
// older `internal/types` package because that one is tightly coupled to the
// Lisp AST (`internal/expr`). When the Lisp version is retired, the two can
// be unified.
//
// Public surface:
//
//   - Type, TVar, TCon, TArrow, TRecord, TForall — the type AST.
//   - Subst — substitution (variable -> Type bindings).
//   - Unify — unification with occurs check.
//   - TypeEnv — environment of bound names.
//   - Infer — type inference for an expression.
//   - Check — top-level driver: type-checks a whole module.
//
// See infer.go for the algorithm (Damas-Hindley-Milner / Algorithm W).
package typecheck

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
)

// --- Type AST ---

// Type is the interface implemented by every type node.
type Type interface {
	isType()
	String() string
}

// TVar is a type variable, identified by an integer ID.
//
// Variables are immutable values; the binding (if any) lives in a Subst.
// Use Subst.Resolve / Subst.Apply to chase bindings.
type TVar struct {
	ID int
}

func (TVar) isType() {}
func (v TVar) String() string {
	return fmt.Sprintf("t%d", v.ID)
}

// TCon is a nullary or n-ary type constructor.
//
// Conventions:
//   - Primitives are nullary: TCon{Name: "Int"}, TCon{Name: "String"}, ...
//   - Generic containers carry their args: TCon{Name: "List", Args: [Int]}.
//   - Function types are NOT TCon; we use TArrow for clarity.
type TCon struct {
	Name string
	Args []Type
}

func (TCon) isType() {}
func (c TCon) String() string {
	if len(c.Args) == 0 {
		return c.Name
	}
	parts := make([]string, len(c.Args))
	for i, a := range c.Args {
		parts[i] = parenIfArrow(a)
	}
	return c.Name + " " + strings.Join(parts, " ")
}

// TArrow is a function type: From -> To.
//
// Multi-argument functions are curried: a -> b -> c is TArrow{a, TArrow{b, c}}.
type TArrow struct {
	From Type
	To   Type
}

func (TArrow) isType() {}
func (a TArrow) String() string {
	return parenIfArrow(a.From) + " -> " + a.To.String()
}

// TRecord is a record type with optional row variable for extension.
//
//   - Tail == nil: closed record. Two close records unify only if their
//     fields are exactly equal.
//   - Tail != nil (typically a TVar): open row. Means "{f1 : T1, ... | tail}".
//     Used by row polymorphism for functions that work on any record having a
//     given set of fields.
//
// Order preserves declaration order for stable pretty-printing; field equality
// is by name, not by position.
type TRecord struct {
	Fields map[string]Type
	Order  []string
	Tail   Type // nil = closed; non-nil (usually TVar) = open
}

func (TRecord) isType() {}
func (r TRecord) String() string {
	if len(r.Fields) == 0 && r.Tail == nil {
		return "{}"
	}
	var sb strings.Builder
	sb.WriteString("{ ")
	if r.Tail != nil {
		sb.WriteString(r.Tail.String())
		sb.WriteString(" | ")
	}
	for i, name := range r.Order {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString(name)
		sb.WriteString(" : ")
		sb.WriteString(r.Fields[name].String())
	}
	sb.WriteString(" }")
	return sb.String()
}

// TUnit is the unit type ().
type TUnit struct{}

func (TUnit) isType()         {}
func (TUnit) String() string  { return "()" }

// TTuple is a tuple type (a, b, ...).
type TTuple struct {
	Members []Type
}

func (TTuple) isType() {}
func (t TTuple) String() string {
	parts := make([]string, len(t.Members))
	for i, m := range t.Members {
		parts[i] = m.String()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// TForall is a type scheme with universally quantified variables. Appears
// only at the top of an entry in TypeEnv (rank-1 polymorphism). Never nested.
type TForall struct {
	Vars []int
	Body Type
}

func (TForall) isType() {}
func (f TForall) String() string {
	if len(f.Vars) == 0 {
		return f.Body.String()
	}
	parts := make([]string, len(f.Vars))
	for i, v := range f.Vars {
		parts[i] = fmt.Sprintf("t%d", v)
	}
	return "forall " + strings.Join(parts, " ") + ". " + f.Body.String()
}

// --- Pretty-print helpers ---

func parenIfArrow(t Type) string {
	if _, ok := t.(TArrow); ok {
		return "(" + t.String() + ")"
	}
	return t.String()
}

// SortedFields returns a record's fields sorted by name (for deterministic
// equality checks where order doesn't matter).
func SortedFields(r TRecord) []string {
	names := make([]string, 0, len(r.Fields))
	for n := range r.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// --- Type variable supply ---

var nextVarID int64

// FreshVar returns a fresh, globally-unique type variable.
func FreshVar() TVar {
	id := atomic.AddInt64(&nextVarID, 1)
	return TVar{ID: int(id)}
}

// resetVarIDsForTesting resets the variable counter. Test-only helper.
func resetVarIDsForTesting() {
	atomic.StoreInt64(&nextVarID, 0)
}

// --- Built-in nullary types ---

var (
	TInt    = TCon{Name: "Int"}
	TFloat  = TCon{Name: "Float"}
	TString = TCon{Name: "String"}
	TBool   = TCon{Name: "Bool"}
	TChar   = TCon{Name: "Char"}
)

// TList returns the type "List elem".
func TList(elem Type) TCon {
	return TCon{Name: "List", Args: []Type{elem}}
}

// TMaybe returns "Maybe a".
func TMaybe(a Type) TCon {
	return TCon{Name: "Maybe", Args: []Type{a}}
}

// TResult returns "Result e a".
func TResult(e, a Type) TCon {
	return TCon{Name: "Result", Args: []Type{e, a}}
}

// TEffect returns "Effect e a".
func TEffect(e, a Type) TCon {
	return TCon{Name: "Effect", Args: []Type{e, a}}
}

// TDb returns the opaque "Db" type for a database connection.
func TDb() TCon {
	return TCon{Name: "Db"}
}

// TEntity returns the opaque "Entity" type.
func TEntity() TCon {
	return TCon{Name: "Entity"}
}
