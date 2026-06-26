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

// Kind classifies type variables by what kinds of types they're
// allowed to unify with. Mirrors Elm's small built-in set of
// constrained type variables (`comparable`, `appendable`, `number`).
// We currently model only `Comparable`, which is what Dict/Set keys
// need.
//
// This is NOT type classes — there's no dictionary passing, no
// dispatching, no user-defined constraints. Just a closed enum the
// unifier consults when binding a TVar to a concrete type.
type Kind int8

const (
	// KindAny — unconstrained type variable, unifies with anything.
	// The zero value, so `TVar{ID: x}` literals without an explicit
	// Constraint default to the unconstrained case.
	KindAny Kind = 0

	// KindComparable — restricted to Int / Float / String / Char.
	// Used by Dict.* and Set.* schemes so a user attempting to key
	// a Dict on a Record or custom type gets a type error at the
	// call site, not a runtime "comparison: unsupported types".
	KindComparable Kind = 1

	// KindAppendable — restricted to String / List. Used by `++` so
	// `1 ++ 2` (or `True ++ False`) is a type error at the call site,
	// matching Elm's `appendable` constrained variable. Elm folds
	// comparable+appendable into `compappend`; we keep the two kinds
	// disjoint and reject the overlap (see mergeKinds).
	KindAppendable Kind = 2
)

func (k Kind) String() string {
	switch k {
	case KindComparable:
		return "comparable"
	case KindAppendable:
		return "appendable"
	default:
		return ""
	}
}

// TVar is a type variable, identified by an integer ID and (optionally)
// a Kind that restricts what concrete types it can unify with.
//
// Variables are immutable values; the binding (if any) lives in a Subst.
// Use Subst.Resolve / Subst.Apply to chase bindings.
//
// Constraint defaults to KindAny — that's the zero value — so a
// `TVar{ID: 7}` literal without an explicit Constraint is unconstrained.
// The `Constraint` field sits on top of HM unification: when bindVar is
// asked to bind a Comparable var to a concrete type, it rejects
// non-comparable types up front; when binding two TVars, the
// constraint propagates to the bound var so future unifications stay
// honest.
type TVar struct {
	ID         int
	Constraint Kind
}

func (TVar) isType() {}
func (v TVar) String() string {
	// Constrained vars render with their kind name ("comparable3",
	// "appendable7") so raw debug output shows the restriction; the
	// pretty-printer (pretty.go) renames unconstrained vars to letters.
	switch v.Constraint {
	case KindComparable:
		return fmt.Sprintf("comparable%d", v.ID)
	case KindAppendable:
		return fmt.Sprintf("appendable%d", v.ID)
	}
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

func (TUnit) isType()        {}
func (TUnit) String() string { return "()" }

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

// FreshVar returns a fresh, globally-unique unconstrained type variable.
func FreshVar() TVar {
	id := atomic.AddInt64(&nextVarID, 1)
	return TVar{ID: int(id)}
}

// atomicNextVarID is a low-level helper for code that needs to mint
// fresh IDs but build the TVar itself (e.g. Instantiate constructing
// a constrained replacement). Most call sites should use FreshVar
// instead.
func atomicNextVarID() int64 {
	return atomic.AddInt64(&nextVarID, 1)
}

// resetVarIDsForTesting resets the variable counter. Test-only helper.
func resetVarIDsForTesting() {
	atomic.StoreInt64(&nextVarID, 0)
}

// IsComparableType reports whether t is a built-in comparable type.
// Used by unify when binding a Comparable-kind TVar to a concrete
// type. Mirrors the runtime's compareValues / cmpValues / compareMar.
// Currently: Int / Float / String / Char.
//
// A free TVar is NOT comparable on its own — the caller (bindVar)
// handles that case separately by promoting the constraint onto the
// var rather than calling this helper.
func IsComparableType(t Type) bool {
	c, ok := t.(TCon)
	if !ok {
		return false
	}
	switch c.Name {
	case "Int", "Float", "String", "Char":
		return len(c.Args) == 0
	}
	return false
}

// IsAppendableType reports whether t is a built-in appendable type —
// the types `++` accepts. Mirrors Elm's `appendable`: String and List.
// The runtime's append handles exactly these two (string concat and
// list concat), so anything else was a latent runtime error before the
// constraint turned it into a compile error.
//
// A free TVar is NOT appendable on its own — bindVar propagates the
// constraint onto the var instead of calling this helper, same as
// Comparable.
func IsAppendableType(t Type) bool {
	c, ok := t.(TCon)
	if !ok {
		return false
	}
	switch c.Name {
	case "String":
		return len(c.Args) == 0
	case "List":
		return len(c.Args) == 1
	}
	return false
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
	// TOrder — three-way comparison result. Inhabited by LT, EQ, GT
	// (constructors registered in typecheck.builtinCustomTypes and
	// the value envs of all three runtimes). Used by List.sortWith
	// and any user-defined comparator: "-1 means less" was a lie
	// we refused to keep telling ourselves — the comparator returns
	// an Order, period.
	TOrder = TCon{Name: "Order"}
	// TMethod — the HTTP verb a Service is declared with. Inhabited by
	// GET, POST, PUT, PATCH, DELETE (constructors registered in
	// builtinCustomTypes and the value envs of all three runtimes). The
	// first argument to Service.declare: it fixes the verb a service
	// answers on, and the compiler holds GET handlers to read-only.
	TMethod = TCon{Name: "Method"}
	// TServiceError — the failure a Service.call delivers to the frontend,
	// in the Err of its Result. A union (Offline / Unauthorized /
	// ServerError String) so transport failure is a value you case on, the
	// Elm way, instead of a stringly-typed match. Constructors registered
	// in builtinCustomTypes and the value envs of all three runtimes.
	TServiceError = TCon{Name: "Service.Error"}
	// TAuthRequestOutcome — the domain outcome of Auth.requestCode. Each
	// endpoint gets its OWN outcome union (never a shared auth catch-all):
	// the email screen branches on these three and nothing else.
	TAuthRequestOutcome = TCon{Name: "Auth.RequestOutcome"}
)

// TAuthVerifyOutcome returns "Auth.VerifyOutcome user" — the domain outcome
// of Auth.verifyCode, parameterized on the app's own user record (the
// framework cannot name it; Auth.config's entity decides it).
func TAuthVerifyOutcome(user Type) TCon {
	return TCon{Name: "Auth.VerifyOutcome", Args: []Type{user}}
}

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

// TDict returns "Dict k v". The typechecker enforces at call sites
// that `k` resolves to a comparable type (Int / Float / String /
// Char) — see isComparableType in env.go.
func TDict(k, v Type) TCon {
	return TCon{Name: "Dict", Args: []Type{k, v}}
}

// TSet returns "Set k". Same key-comparable constraint as Dict.
func TSet(k Type) TCon {
	return TCon{Name: "Set", Args: []Type{k}}
}

// TTask returns "Task a" — the backend value-monad ("await"). A service
// handler runs a Task and the produced `a` becomes the response; Task.andThen
// threads the produced value (do A, then with A's result do B). Backend-only.
// Failure is a value (a String via Task.fail, surfaced to the client as a
// Service.Error), never a type index — so one type parameter, no error index.
func TTask(a Type) TCon {
	return TCon{Name: "Task", Args: []Type{a}}
}

// TCmd returns "Cmd msg" — the frontend message-monoid (Mar's Cmd). What
// `init` / `update` return: the runtime performs it and delivers a `msg` back
// into the MVU loop. Frontend-only. Composed with Cmd.batch / Cmd.none; it has
// no andThen (dependent client async chains through messages, not a value).
func TCmd(a Type) TCon {
	return TCon{Name: "Cmd", Args: []Type{a}}
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

// TKeyedView returns "KeyedView msg" — a View tagged with a stable
// identity (a String key). Constructed via `UI.keyed key view`, accepted
// only as a child of `UI.keyedList`. The dedicated wrapper type makes it
// impossible to feed a regular `View` into `keyedList` (where the
// reconciler needs identity to match rows across reorders / deletes) —
// the misuse becomes a compile error instead of a silent runtime bug
// that swaps the wrong row's content.
//
// Phantom-typed wrapper at runtime: the actual VView carries an internal
// `key` slot. The type distinction exists only at compile time.
func TKeyedView(msg Type) TCon {
	return TCon{Name: "KeyedView", Args: []Type{msg}}
}

// TAttr returns the parameterized "Attr h" type — attributes carry a
// phantom "host" type indicating which primitive they apply to.
//
//   - Attr Input     — textField / textArea / picker
//   - Attr Section   — section (header/footer)
//   - Attr KeyedList — keyedList (header/footer/onMove/onDelete)
//   - Attr NavStack  — navigationStack (title/trailing/leading)
//   - Attr Button    — button
//   - Attr Link      — navigationLink
//   - Attr Toggle    — toggle
//   - Attr Stack     — hstack / vstack
//   - Attr List      — list (container of sections/keyedLists)
//
// Universal attrs (e.g. `disabled`) declare `forall a. Attr a`, so they
// unify with whatever host the surrounding list expects. Specific
// attrs declare a concrete host, so passing `width (chars 6)` (which
// returns `Attr Input`) to a `section` (which wants `Attr Section`)
// is a type error caught at compile time.
//
// Categories are opaque marker types — they exist only at the type
// level and are never inhabited; the runtime ignores them.
func TAttr(host Type) Type {
	return TCon{Name: "Attr", Args: []Type{host}}
}

// TAttrInputHost and the sibling host markers below are nullary TCons
// used only as the phantom parameter to TAttr — one per Attr category.
func TAttrInputHost() Type     { return TCon{Name: "Input"} }
func TAttrSectionHost() Type   { return TCon{Name: "Section"} }
func TAttrNavStackHost() Type  { return TCon{Name: "NavStack"} }
func TAttrButtonHost() Type    { return TCon{Name: "Button"} }
func TAttrLinkHost() Type      { return TCon{Name: "Link"} }
func TAttrToggleHost() Type    { return TCon{Name: "Toggle"} }
func TAttrStackHost() Type     { return TCon{Name: "Stack"} }
func TAttrListHost() Type      { return TCon{Name: "List"} }
func TAttrKeyedListHost() Type { return TCon{Name: "KeyedList"} }
func TAttrImageHost() Type     { return TCon{Name: "Image"} }

// TAttrInlineHost is the host marker for inline-text attrs (bold,
// italic, strikethrough, code, link). Used by `span [attrs] "..."`
// inside `paragraph` to style or link individual text runs. Inline
// attrs DON'T unify with other categories — `bold` in a block-level
// `text [...]` list is rejected at compile time.
func TAttrInlineHost() Type { return TCon{Name: "Inline"} }

// TAttrTextHost is the host marker for the block-level text leaf,
// `text [attrs] "..."`. No text-specific attrs exist yet — the list
// exists for the universal layout attrs (width / height), which are
// polymorphic in their host and so fit here like anywhere else.
func TAttrTextHost() Type { return TCon{Name: "Text"} }

// TInline returns the `Inline msg` type — a run of text inside a
// paragraph. Distinct from `View msg` so `paragraph` can refuse
// block-level content (sections, buttons, lists), and the rest of
// the UI vocabulary can refuse loose `Inline` atoms outside a
// paragraph wrapper. msg flows through so future inline atoms with
// onClick handlers (analytics-tracked links, etc.) work without
// breaking the type.
func TInline(msg Type) TCon {
	return TCon{Name: "Inline", Args: []Type{msg}}
}

// TSize — the opaque sizing-value type, phantom-parameterized by axis:
// `chars : Int -> Size Width`, `lines : Int -> Size Height`, and
// `fill : Size axis` (polymorphic — fits either). The phantom keeps
// `width (lines 5)` / `height (chars 5)` compile errors while letting
// the single `fill` constant serve both attrs. TWidth / THeight are
// the two concrete instantiations, kept as named helpers because
// that's how every scheme reads ("width takes a TWidth()").
func TSize(axis Type) TCon { return TCon{Name: "Size", Args: []Type{axis}} }

// TWidthAxis / THeightAxis — the phantom axis markers. Never inhabited;
// they exist so Size Width and Size Height are distinct types.
func TWidthAxis() TCon  { return TCon{Name: "Width"} }
func THeightAxis() TCon { return TCon{Name: "Height"} }

func TWidth() TCon  { return TSize(TWidthAxis()) }
func THeight() TCon { return TSize(THeightAxis()) }

// TAlignment — cross-axis alignment for stack children. Values:
// leading / center / trailing (vstack's horizontal cross axis) and
// top / center / bottom (hstack's vertical cross axis). One type for
// both axes — `center` is shared, and renderers ignore a wrong-axis
// value (top in a vstack) rather than the type system splitting the
// Stack host in two for it. Alignment is pure *position*: it places
// hugging children in the leftover cross-axis space. "Fill" is not an
// alignment — that's sizing, spelled `width fill` / `height fill` on
// the child.
func TAlignment() TCon { return TCon{Name: "Alignment"} }

// TPixels — opaque pixel sizing unit for media. `px : Int -> Pixels`.
// Deliberately distinct from Width / Height (the char/line units for
// inputs) so pixel sizing stays scoped to images: `width (px 8)` on a
// text field and `size (chars 8) ...` on an image are both rejected at
// compile time. Keeps the "no arbitrary pixel layout" rule intact while
// giving images the one place raw dimensions genuinely matter.
func TPixels() TCon { return TCon{Name: "Pixels"} }

// TPage returns the opaque "Page" type — a single MVU screen bound to a
// URL path. Both single-screen and multi-screen apps are expressed as a
// list of pages; single = list of one with path "/".
func TPage() TCon {
	return TCon{Name: "Page"}
}

// TAdminSession returns the opaque "AdminSession" type — the capability
// token the framework threads into a Page.adminProtected page's
// init/update/view. It has no user-facing constructor: only the admin
// page provides it. Because the Mar.Admin.* functions require an
// AdminSession as their first argument, they can only be called from
// inside an admin page — a normal page has no AdminSession in scope, so
// referencing Mar.Admin.* there is a compile error.
func TAdminSession() TCon {
	return TCon{Name: "AdminSession"}
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
