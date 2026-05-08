// Package typecheck implements Hindley-Milner type inference for the Mar AST.
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
	TInt      = TCon{Name: "Int"}
	TFloat    = TCon{Name: "Float"}
	TString   = TCon{Name: "String"}
	TBool     = TCon{Name: "Bool"}
	TChar     = TCon{Name: "Char"}
	TDuration = TCon{Name: "Duration"}
	TTime     = TCon{Name: "Time"}
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

// TEntity returns the parameterized "Entity a" type — an entity describing
// a SQL table whose row shape is `a`. The row type drives Repo decode and
// the type of values returned by query operations.
func TEntity(row Type) TCon {
	return TCon{Name: "Entity", Args: []Type{row}}
}

// TService returns the parameterized "Service req resp" type — a typed RPC
// contract that the frontend can call (Service.call) and the backend
// implements (the function inside the constructor). Req/Resp drive
// JSON encode/decode at the wire boundary and type-check the call site.
func TService(req, resp Type) TCon {
	return TCon{Name: "Service", Args: []Type{req, resp}}
}

// TExposedService is the type-erased form of a Service — opaque, no
// parameters, so a List of services with different Req/Resp can be
// homogeneous in mar's HM. Produced by Service.expose, consumed by
// App.fullstack / App.backend's `services` field.
func TExposedService() TCon {
	return TCon{Name: "ExposedService"}
}

// TAuth returns the parameterized "Auth user" type — the opaque value
// returned by Auth.config that captures the framework's auth wiring.
// Carrying the user row type lets Auth.protected handlers receive a
// typed User without the user code restating it.
func TAuth(user Type) TCon {
	return TCon{Name: "Auth", Args: []Type{user}}
}

// TColumn returns the "Column t" type — a single column declaration
// produced by Entity.serial / .int / .text / .bool / .dateTime. The
// parameter is the value type stored in the column.
func TColumn(t Type) TCon {
	return TCon{Name: "Column", Args: []Type{t}}
}

// TConstraint returns the opaque "Constraint" type — values like
// Entity.notNull / Entity.optional that modify a Column declaration.
func TConstraint() TCon {
	return TCon{Name: "Constraint"}
}

// TView returns "View msg" — the type of MVU views parameterized by the
// type of messages they can produce when interacted with. Plain leaves
// like View.text "..." inhabit `forall msg. View msg`; buttons and forms
// pin msg to the user's Msg type.
func TView(msg Type) TCon {
	return TCon{Name: "View", Args: []Type{msg}}
}

// TAttr returns the opaque "Attr" type — values produced by layout
// modifiers (View.padding, View.spacing, View.fill, ...) and consumed
// by view constructors as their first argument list. elm-ui-style
// attributes; runtime translates each to its platform equivalent.
func TAttr() TCon {
	return TCon{Name: "Attr"}
}

// TPage returns the opaque "Page" type — a single MVU screen bound to a
// URL path. Both single-screen and multi-screen apps are expressed as a
// list of pages; single = list of one with path "/".
func TPage() TCon {
	return TCon{Name: "Page"}
}

// TEndpoint returns the opaque "Endpoint" type.
func TEndpoint() TCon {
	return TCon{Name: "Endpoint"}
}

// TPath returns the parameterized "Path r" type — a URL pattern with
// typed `:param` segments. Each Path value carries the row of params
// it captures from the URL: `Path { id : Int }` corresponds to the
// pattern `"/notes/{id:Int}"`. Constructed by coercion from a String
// literal (the typechecker parses the pattern at elaboration time
// and synthesizes the row); destructured at runtime by the matcher
// (URL → params record) and at call sites by `linkTo` / `Nav.pushTo`
// (params record → URL).
//
// Empty params (`Path {}`) means a static path like "/" or "/about" —
// not common in practice (usually you'd use Page.create for those),
// but the type stays uniform so utility functions can be polymorphic
// over `Path r`.
func TPath(row Type) TCon {
	return TCon{Name: "Path", Args: []Type{row}}
}
