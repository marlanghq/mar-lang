package typecheck

import (
	"sort"
	"strings"
)

// TypeEnv maps names to types (or schemes). Implemented as an immutable
// linked list of frames so that scoping works naturally.
//
// The root frame also carries a customs map — the registered custom-type
// declarations indexed by name. Used by exhaustiveness checking on case
// expressions to know "what are all the constructors of Msg?" without
// reverse-engineering it from the constructor schemes.
type TypeEnv struct {
	bindings map[string]Type
	parent   *TypeEnv
	customs  map[string]CustomType // populated only on the root frame
}

// Names returns every binding visible from this frame, sorted and deduped.
// Exported for the cross-runtime conformance gate, which needs the list of
// things whose meaning the Go, JS and Swift runtimes must agree on in order to
// tell "not tested yet" apart from "tested".
func (e *TypeEnv) Names() []string {
	seen := map[string]bool{}
	for f := e; f != nil; f = f.parent {
		for n := range f.bindings {
			seen[n] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// NewEnv returns an empty top-level environment.
func NewEnv() *TypeEnv {
	return &TypeEnv{bindings: map[string]Type{}, customs: map[string]CustomType{}}
}

// RegisterCustom adds (or overwrites) a custom-type entry on the root
// environment. Walks up the parent chain to find the root frame and
// registers there so all child scopes see the same entry.
func (e *TypeEnv) RegisterCustom(name string, ct CustomType) {
	root := e
	for root.parent != nil {
		root = root.parent
	}
	if root.customs == nil {
		root.customs = map[string]CustomType{}
	}
	root.customs[name] = ct
}

// LookupCustom finds a custom-type registration by name. Walks parents
// to the root.
func (e *TypeEnv) LookupCustom(name string) (CustomType, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if cur.customs != nil {
			if ct, ok := cur.customs[name]; ok {
				return ct, true
			}
		}
	}
	return CustomType{}, false
}

// Lookup searches this env and all parents for `name`. Returns the bound type
// (or scheme) and ok=true if found.
func (e *TypeEnv) Lookup(name string) (Type, bool) {
	for cur := e; cur != nil; cur = cur.parent {
		if t, ok := cur.bindings[name]; ok {
			return t, true
		}
	}
	return nil, false
}

// Bind returns a new env extending this one with name -> t. Original is
// not mutated.
func (e *TypeEnv) Bind(name string, t Type) *TypeEnv {
	frame := map[string]Type{name: t}
	return &TypeEnv{bindings: frame, parent: e}
}

// ExportsOf collects every binding that belongs to module `modName`:
// keys of the form `modName.suffix` where suffix itself contains no
// further dot (so `Mar.Admin.x` is an export of `Mar.Admin`, not of
// `Mar`). Powers `import M exposing (..)` — the returned map is the
// full set of bare names the wildcard brings into scope. Walks frames
// outermost-first so an inner (re)binding of the same qualified name
// wins, matching Lookup's shadowing order.
func (e *TypeEnv) ExportsOf(modName string) map[string]Type {
	prefix := modName + "."
	// Stack the frames so we can apply them root → leaf; later
	// (inner) frames overwrite earlier ones, mirroring Lookup.
	var frames []*TypeEnv
	for cur := e; cur != nil; cur = cur.parent {
		frames = append(frames, cur)
	}
	out := map[string]Type{}
	for i := len(frames) - 1; i >= 0; i-- {
		for name, t := range frames[i].bindings {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			suffix := name[len(prefix):]
			if suffix == "" || strings.Contains(suffix, ".") {
				continue
			}
			out[suffix] = t
		}
	}
	return out
}

// Define mutates the env's top frame with the given binding. Use only in
// contexts (REPL, module setup) where state must persist across calls.
func (e *TypeEnv) Define(name string, t Type) {
	e.bindings[name] = t
}

// BaseEnv returns the initial environment populated with built-in functions
// and operators.
//
// Built-ins are encoded as TForall when polymorphic (e.g. == : forall a. a -> a -> Bool).
func BaseEnv() *TypeEnv {
	env := NewEnv()
	flat := baseBindings()
	for name, t := range flat {
		env = env.Bind(name, t)
	}
	for name, t := range qualifiedAliases(flat) {
		env = env.Bind(name, t)
	}
	// Register built-in custom types so the exhaustiveness check on case
	// expressions can see their variants. Without these, `case (x : Maybe a)`
	// branches that omit one of `Just`/`Nothing` would compile silently.
	for name, ct := range builtinCustomTypes() {
		env.RegisterCustom(name, ct)
	}
	return env
}

// BaseQualifiedSymbols returns the qualified stdlib bindings
// (Module.name → Type) as a flat map. Consumed by the LSP to power
// completion / hover / workspace-symbol over the framework's
// built-ins. Bare-name aliases (e.g. `listMap` for `List.map`) are
// excluded — only the user-facing qualified form is reported, since
// the bare names are an internal-runtime convention.
func BaseQualifiedSymbols() map[string]Type {
	return qualifiedAliases(baseBindings())
}

// BareGlobals returns the user-facing builtins that live in no module —
// the handful spelled without a qualifier, like Elm's Basics. It is the
// bare-name counterpart to ExportsOf, and exists so tooling that walks
// the stdlib module by module (the /reference generator) can still see
// these; without it they are invisible to any module-shaped traversal.
//
// The set is derived, not listed: a name is a bare global exactly when
// the user-facing alias table binds it without a dot. Add one there and
// it shows up here, which is what makes the reference's coverage test
// able to fail on an undocumented addition.
func BareGlobals() map[string]Type {
	out := map[string]Type{}
	for name, t := range qualifiedAliases(baseBindings()) {
		if !strings.Contains(name, ".") {
			out[name] = t
		}
	}
	return out
}

// BaseCustomTypes returns the stdlib custom-type registrations
// (Maybe, Result, Bool) so the LSP can advertise the variants for
// completion in case expressions and surface a hover summary.
func BaseCustomTypes() map[string]CustomType {
	return builtinCustomTypes()
}

// builtinCustomTypes returns the CustomType registrations for stdlib types
// that participate in pattern matching (Maybe, Result, Bool). These mirror
// the value-env entries for Just/Nothing/Ok/Err in baseBindings, just at
// the custom-type level so exhaustiveness checking has them on hand.
func builtinCustomTypes() map[string]CustomType {
	tva := TVar{ID: -101}
	tvb := TVar{ID: -102}
	return map[string]CustomType{
		"Maybe": {
			Name:   "Maybe",
			Params: []string{"a"},
			Constructors: map[string]CustomCtor{
				"Just":    {Args: []Type{tva}, Result: TMaybe(tva)},
				"Nothing": {Args: nil, Result: TMaybe(tva)},
			},
			CtorOrder: []string{"Just", "Nothing"},
		},
		"Result": {
			Name:   "Result",
			Params: []string{"e", "a"},
			Constructors: map[string]CustomCtor{
				"Ok":  {Args: []Type{tvb}, Result: TResult(tva, tvb)},
				"Err": {Args: []Type{tva}, Result: TResult(tva, tvb)},
			},
			CtorOrder: []string{"Ok", "Err"},
		},
		"Bool": {
			Name:         "Bool",
			Params:       nil,
			Constructors: map[string]CustomCtor{"True": {Result: TBool}, "False": {Result: TBool}},
			CtorOrder:    []string{"True", "False"},
		},
		// Order — three-way comparison result. Mirrors Elm exactly so
		// user code that came from Elm (or that the user wrote
		// expecting Elm-style semantics for sortWith) just works.
		// Registered as a built-in custom type so `case ord of LT -> ...`
		// pattern matches exhaustively.
		"Order": {
			Name:   "Order",
			Params: nil,
			Constructors: map[string]CustomCtor{
				"LT": {Result: TOrder},
				"EQ": {Result: TOrder},
				"GT": {Result: TOrder},
			},
			CtorOrder: []string{"LT", "EQ", "GT"},
		},
		// Method — the HTTP verb a service answers on. The first argument
		// to Service.declare. Registered as a built-in custom type so a
		// `case method of GET -> ...` matches exhaustively.
		"Method": {
			Name:   "Method",
			Params: nil,
			Constructors: map[string]CustomCtor{
				"GET":    {Result: TMethod},
				"POST":   {Result: TMethod},
				"PUT":    {Result: TMethod},
				"PATCH":  {Result: TMethod},
				"DELETE": {Result: TMethod},
			},
			CtorOrder: []string{"GET", "POST", "PUT", "PATCH", "DELETE"},
		},
		// Service.Error — the failure a Service.call delivers in its Err.
		// A union so the frontend cases on it (Offline shows a retry,
		// Unauthorized redirects to sign-in, RateLimited asks the user to
		// slow down, ServerError shows the message) instead of matching
		// strings.
		"Service.Error": {
			Name:   "Service.Error",
			Module: "Service",
			Params: nil,
			Constructors: map[string]CustomCtor{
				"Offline":      {Result: TServiceError},
				"Unauthorized": {Result: TServiceError},
				"RateLimited":  {Result: TServiceError},
				"ServerError":  {Args: []Type{TString}, Result: TServiceError},
			},
			CtorOrder: []string{"Offline", "Unauthorized", "RateLimited", "ServerError"},
		},
		// Auth.RequestOutcome / Auth.VerifyOutcome — per-endpoint domain
		// outcomes for the auth flow. Each endpoint declares only the
		// branches it can produce (the email screen never sees WrongCode;
		// the code screen never sees InvalidEmail), and the privacy rule
		// holds: CodeSent never reveals whether the email has an account.
		"Auth.RequestOutcome": {
			Name:   "Auth.RequestOutcome",
			Module: "Auth",
			Params: nil,
			Constructors: map[string]CustomCtor{
				"CodeSent":     {Result: TAuthRequestOutcome},
				"InvalidEmail": {Result: TAuthRequestOutcome},
				"RateLimited":  {Result: TAuthRequestOutcome},
			},
			CtorOrder: []string{"CodeSent", "InvalidEmail", "RateLimited"},
		},
		"Auth.VerifyOutcome": {
			Name:   "Auth.VerifyOutcome",
			Module: "Auth",
			Params: []string{"user"},
			Constructors: map[string]CustomCtor{
				"SignedIn":        {Args: []Type{tva}, Result: TAuthVerifyOutcome(tva)},
				"WrongCode":       {Result: TAuthVerifyOutcome(tva)},
				"TooManyAttempts": {Result: TAuthVerifyOutcome(tva)},
			},
			CtorOrder: []string{"SignedIn", "WrongCode", "TooManyAttempts"},
		},
		// Canvas.Transform — what a group does to its children: the matrix ops
		// (translate / scale / rotate) applied in declaration order, plus the
		// two compositing modifiers (Alpha, Blend) that are not matrix ops at
		// all — the renderers pull those out before walking the rest. Reachable
		// only as `Canvas.Translate` etc.: `import Canvas exposing
		// (Transform(..))` parses but does NOT bind the bare names, since
		// open-exposing of a BUILTIN union is still unimplemented (verified
		// 2026-07-15). Registered here so `case t of Translate ...` is
		// exhaustive and so that gap can be closed from one place.
		"Transform": {
			Name:   "Transform",
			Module: "Canvas",
			Params: nil,
			Constructors: map[string]CustomCtor{
				"Translate": {Args: []Type{TInt, TInt}, Result: TTransform},
				"Scale":     {Args: []Type{TInt, TInt}, Result: TTransform},
				"Rotate":    {Args: []Type{TInt}, Result: TTransform},
				"Alpha":     {Args: []Type{TInt}, Result: TTransform},
				"Blend":     {Args: []Type{TBlend}, Result: TTransform},
			},
			CtorOrder: []string{"Translate", "Scale", "Rotate", "Alpha", "Blend"},
		},
		// Canvas.Blend — the compositing mode of a group (see TBlend). Same
		// exposing rule as Align and for a sharper reason: a game writing
		// `type Op = Add | Sub` must not collide with a rendering mode.
		"Rounding": {
			Name:   "Rounding",
			Module: "Decimal",
			Params: nil,
			Constructors: map[string]CustomCtor{
				"HalfEven": {Result: TRounding},
				"HalfUp":   {Result: TRounding},
				"Down":     {Result: TRounding},
				"Up":       {Result: TRounding},
				"Floor":    {Result: TRounding},
				"Ceiling":  {Result: TRounding},
			},
			CtorOrder: []string{"HalfEven", "HalfUp", "Down", "Up", "Floor", "Ceiling"},
		},
		"Blend": {
			Name:   "Blend",
			Module: "Canvas",
			Params: nil,
			Constructors: map[string]CustomCtor{
				"Normal":   {Result: TBlend},
				"Add":      {Result: TBlend},
				"Multiply": {Result: TBlend},
				"Screen":   {Result: TBlend},
				"Erase":    {Result: TBlend},
			},
			CtorOrder: []string{"Normal", "Add", "Multiply", "Screen", "Erase"},
		},
		// Canvas.Align — horizontal text anchor. Same exposing rule as
		// Transform; the constructors are NOT global (unlike Order/Method)
		// precisely so a game can still write `type Direction = Left | Right`
		// without importing Canvas.
		"Align": {
			Name:   "Align",
			Module: "Canvas",
			Params: nil,
			Constructors: map[string]CustomCtor{
				"Left":   {Result: TAlign},
				"Center": {Result: TAlign},
				"Right":  {Result: TAlign},
			},
			CtorOrder: []string{"Left", "Center", "Right"},
		},
		// Pointer — the precision of the PRIMARY input, inside a Device record
		// (Device.watch). Coarse = finger / TV remote, Fine = mouse / trackpad /
		// stylus. Constructors ARE global (like Order / Method, unlike Align):
		// `d.pointer == Coarse` reads naturally and the two names are distinctive
		// enough not to collide with a game's own union.
		"Pointer": {
			Name:   "Pointer",
			Params: nil,
			Constructors: map[string]CustomCtor{
				"Coarse": {Result: TPointer},
				"Fine":   {Result: TPointer},
			},
			CtorOrder: []string{"Coarse", "Fine"},
		},
		// CanvasMode — the mandatory first argument of `canvas`. Pixelated =
		// 1× buffer, nearest-neighbour (60 fps action games); Crisp = retina
		// buffer, sharp text (turn-based / text). Global constructors (bare
		// `Pixelated` / `Crisp`), like Pointer — distinctive, no collision.
		"CanvasMode": {
			Name:   "CanvasMode",
			Params: nil,
			Constructors: map[string]CustomCtor{
				"Pixelated": {Result: TCanvasMode},
				"Crisp":     {Result: TCanvasMode},
			},
			CtorOrder: []string{"Pixelated", "Crisp"},
		},
		// Keyboard.Key — a physical key (Keyboard.watch delivers the held set).
		// ~100 constructors mirroring the DOM event.code set of a US keyboard,
		// built from the shared list in keyboard.go. Qualified-only (like
		// Service.Error) so the ~100 names don't pollute the global scope.
		"Keyboard.Key":   keyboardKeyCustomType(),
		"Gamepad.Button": gamepadButtonCustomType(),
		// Sound.Wave — chip-audio waveforms (Square/Triangle/Sawtooth/Noise) from
		// sound.go. Qualified-only, like the other web-first input/output unions.
		"Sound.Wave": soundWaveCustomType(),
	}
}

func baseBindings() map[string]Type {
	a := TVar{ID: -1}
	b := TVar{ID: -2}

	// `cmp` is the Comparable-constrained quantified var used by the
	// ordering operators below. Same mechanism as Dict/Set keys: when
	// the user writes `record1 < record2`, unification tries to bind
	// this comparable TVar to a TRecord, the unifier rejects it, and
	// inferBinop surfaces the kind-mismatch reason. Strict semantics —
	// only Int / Float / String / Char satisfy Comparable. Tuples /
	// lists / records / custom types don't (the runtime's
	// compareValues doesn't recurse).
	cmp := TVar{ID: -22, Constraint: KindComparable}

	out := map[string]Type{}

	// Arithmetic: forall n:number. n -> n -> n, where number is
	// Int | Decimal (docs/proposals/decimal.md). Both sides unify to
	// ONE member — mixing Int and Decimal is a type error whose fix
	// (Decimal.fromInt, or writing the literal as 3.0) stays visible.
	// These three are exact for Decimal: add/sub align scales, mul
	// adds them; no rounding exists in any of them.
	num := TVar{ID: -30, Constraint: KindNumber}
	out["+"] = TForall{Vars: []int{num.ID}, Body: TArrow{From: num, To: TArrow{From: num, To: num}}}
	out["-"] = TForall{Vars: []int{num.ID}, Body: TArrow{From: num, To: TArrow{From: num, To: num}}}
	out["*"] = TForall{Vars: []int{num.ID}, Body: TArrow{From: num, To: TArrow{From: num, To: num}}}

	// Integer division wears its truncation in its spelling — Elm's
	// `//`, total (x // 0 == 0).
	out["//"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TInt}}

	// Decimal division produces a QUESTION, not a number: the inert
	// exact quotient. Only Decimal.rounded / withRemainder
	// turn it into a value, and each one writes the precision at the
	// use site. No implicit rounding anywhere in the language.
	out["/"] = TArrow{From: TDecimal, To: TArrow{From: TDecimal, To: TDivision}}

	// always : a -> b -> a — Elm's Basics.always. Ignores its second
	// argument; `always x` is `\_ -> x`. The everyday use is a constant
	// function, e.g. `subscriptions = always Sub.none`.
	out["always"] = TForall{
		Vars: []int{a.ID, b.ID},
		Body: TArrow{From: a, To: TArrow{From: b, To: a}},
	}

	// Equality: forall a. a -> a -> Bool. Stays polymorphic because
	// equalValues is fully structural — records, tuples, lists, ctors
	// all compare element-wise. Equality is universal; ordering is not.
	out["=="] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{From: a, To: TArrow{From: a, To: TBool}},
	}
	out["/="] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{From: a, To: TArrow{From: a, To: TBool}},
	}
	// Ordering: forall k:Comparable. k -> k -> Bool. Comparable is
	// Int / Float / String / Char only — see the `cmp` declaration
	// above for the rationale.
	out["<"] = TForall{
		Vars: []int{cmp.ID},
		Body: TArrow{From: cmp, To: TArrow{From: cmp, To: TBool}},
	}
	out[">"] = TForall{
		Vars: []int{cmp.ID},
		Body: TArrow{From: cmp, To: TArrow{From: cmp, To: TBool}},
	}
	out["<="] = TForall{
		Vars: []int{cmp.ID},
		Body: TArrow{From: cmp, To: TArrow{From: cmp, To: TBool}},
	}
	out[">="] = TForall{
		Vars: []int{cmp.ID},
		Body: TArrow{From: cmp, To: TArrow{From: cmp, To: TBool}},
	}

	// Logical
	out["&&"] = TArrow{From: TBool, To: TArrow{From: TBool, To: TBool}}
	out["||"] = TArrow{From: TBool, To: TArrow{From: TBool, To: TBool}}

	// not : Bool -> Bool — Elm's Basics.not, bare like `always`. Mar
	// has no prefix operator, so negation is a function: `not model.busy`
	// rather than `!model.busy`. It completes the boolean algebra —
	// without it the complement of a predicate has to be spelled
	// `if p x then False else True`.
	out["not"] = TArrow{From: TBool, To: TBool}

	// The numeric kit, bare and Elm-named. These are NOT here because
	// they are short — `imax a b = if a > b then a else b` is one line.
	// They are here because eleven shipped apps each wrote their own
	// (871 call sites), which means the language was asking every author
	// to re-derive the same answer, and because Int-only hand-rolls
	// cannot serve Decimal at all.
	//
	// max/min/clamp ride Comparable, so they order Char and String too,
	// not just numbers. `clamp` takes the bounds first, like Elm:
	// `clamp low high x`.
	out["max"] = TForall{Vars: []int{cmp.ID}, Body: TArrow{From: cmp, To: TArrow{From: cmp, To: cmp}}}
	out["min"] = TForall{Vars: []int{cmp.ID}, Body: TArrow{From: cmp, To: TArrow{From: cmp, To: cmp}}}
	out["clamp"] = TForall{Vars: []int{cmp.ID}, Body: TArrow{From: cmp, To: TArrow{From: cmp, To: TArrow{From: cmp, To: cmp}}}}

	// abs rides number, so it covers Int and Decimal with one name.
	// Decimal.abs stays as the explicit spelling in Decimal-only code.
	out["abs"] = TForall{Vars: []int{num.ID}, Body: TArrow{From: num, To: num}}

	// Two remainders, because they disagree on negatives and both uses
	// are real. `modBy d n` follows the sign of the DIVISOR (floor
	// modulo) — what wrapping a coordinate or an index needs, and what
	// every game hand-rolled as `modI`. `remainderBy d n` follows the
	// sign of the DIVIDEND, matching `//`'s truncation so that
	// `(n // d) * d + remainderBy d n == n`. Divisor first in both,
	// like Elm, so they partially apply as `modBy 8`.
	out["modBy"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TInt}}
	out["remainderBy"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TInt}}

	// String/list append. `app` is Appendable-constrained — only
	// String and List satisfy it — so `1 ++ 2` (or `True ++ False`)
	// is rejected at the call site, matching Elm's `appendable`.
	// Without the constraint this was `forall a. a -> a -> a`, which
	// typechecked nonsense the runtime append could never honor.
	app := TVar{ID: -24, Constraint: KindAppendable}
	out["++"] = TForall{
		Vars: []int{app.ID},
		Body: TArrow{From: app, To: TArrow{From: app, To: app}},
	}

	// Cons: a -> List a -> List a
	out["::"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{From: a, To: TArrow{From: TList(a), To: TList(a)}},
	}

	// Pipe operators: forall a b. (a -> b) -> a -> b  /  a -> (a -> b) -> b
	out["|>"] = TForall{
		Vars: []int{a.ID, b.ID},
		Body: TArrow{
			From: a,
			To: TArrow{
				From: TArrow{From: a, To: b},
				To:   b,
			},
		},
	}
	out["<|"] = TForall{
		Vars: []int{a.ID, b.ID},
		Body: TArrow{
			From: TArrow{From: a, To: b},
			To:   TArrow{From: a, To: b},
		},
	}

	// Bool literals
	out["True"] = TBool
	out["False"] = TBool

	// Maybe constructors
	out["Nothing"] = TForall{Vars: []int{a.ID}, Body: TMaybe(a)}
	out["Just"] = TForall{Vars: []int{a.ID}, Body: TArrow{From: a, To: TMaybe(a)}}

	// Result constructors
	out["Ok"] = TForall{Vars: []int{a.ID, b.ID}, Body: TArrow{From: b, To: TResult(a, b)}}
	out["Err"] = TForall{Vars: []int{a.ID, b.ID}, Body: TArrow{From: a, To: TResult(a, b)}}

	// Order constructors — nullary, monomorphic.
	out["LT"] = TOrder
	out["EQ"] = TOrder
	out["GT"] = TOrder

	// Method constructors — the HTTP verbs, nullary and monomorphic.
	// Bare-exposed (like LT/EQ/GT) so a service reads `Service.declare
	// GET "/path"`.
	out["GET"] = TMethod
	out["POST"] = TMethod
	out["PUT"] = TMethod
	out["PATCH"] = TMethod
	out["DELETE"] = TMethod

	// Service.Error constructors — the transport failure a Service.call
	// delivers in its Err. Offline / Unauthorized / RateLimited are nullary;
	// ServerError carries the server's message.
	out["Service.Offline"] = TServiceError
	out["Service.Unauthorized"] = TServiceError
	out["Service.RateLimited"] = TServiceError
	out["Service.ServerError"] = TArrow{From: TString, To: TServiceError}

	// Auth outcome constructors — qualified-only, like Service.Error.
	// SignedIn is polymorphic in the app's user record.
	out["Auth.CodeSent"] = TAuthRequestOutcome
	out["Auth.InvalidEmail"] = TAuthRequestOutcome
	out["Auth.RateLimited"] = TAuthRequestOutcome
	out["Auth.SignedIn"] = TForall{Vars: []int{a.ID}, Body: TArrow{From: a, To: TAuthVerifyOutcome(a)}}
	out["Auth.WrongCode"] = TForall{Vars: []int{a.ID}, Body: TAuthVerifyOutcome(a)}
	out["Auth.TooManyAttempts"] = TForall{Vars: []int{a.ID}, Body: TAuthVerifyOutcome(a)}

	// Keyboard.Key constructors (Keyboard.KeyW, Keyboard.ArrowUp, Keyboard.Space,
	// ...) — the ~100 physical keys from keyboard.go, qualified-only like
	// Service.Error so the names don't leak into the global scope.
	for name, ty := range keyboardKeyBindings() {
		out[name] = ty
	}
	// Keyboard.watch : ({ down : List Keyboard.Key } -> msg) -> Sub msg — the
	// held-key MIRROR. Fires the whole current set on subscribe and on every
	// change; OS auto-repeat emits nothing (the set is unchanged) and window
	// blur clears it. "A key just went down" is derived by the app diffing the
	// new set against the previous one. There is no per-event key subscription:
	// held keys are state, so they arrive as state.
	out["keyboardWatch"] = TForall{Vars: []int{b.ID}, Body: TArrow{From: TArrow{From: TKeyboardStateRecord(), To: b}, To: TSub(b)}}

	// Gamepad.Button constructors (Gamepad.A, Gamepad.Up, ...) — the standard
	// controller buttons from gamepad.go, qualified-only like Keyboard.Key.
	for name, ty := range gamepadButtonBindings() {
		out[name] = ty
	}
	// Gamepad.watch : (pad -> msg) -> Sub msg — the full-pad MIRROR: connection,
	// both analog sticks (x/y in -100..100 with a deadzone), and the held
	// buttons, as one snapshot that re-fires on change. Web-first (JS polls
	// getGamepads()). Like the keyboard, the pad is state, so it arrives as state
	// — no per-button or per-stick event subscription.
	out["gamepadWatch"] = TForall{Vars: []int{b.ID}, Body: TArrow{From: TArrow{From: TGamepadStateRecord(), To: b}, To: TSub(b)}}

	// === Sound — chip-audio SFX + looping music (docs/proposals/sound.md) ===
	//
	// Sound.Wave constructors (Sound.Square, ...) come from soundWaveBindings
	// (used as VALUES, the first arg to tone). The builders are monomorphic
	// (Sound in, Sound out); play yields a Cmd and music a Sub, both parametric
	// in the page msg. The JS runtime does the WebAudio synthesis; Go is inert.
	for name, ty := range soundWaveBindings() {
		out[name] = ty
	}
	// Sound.tone : Sound.Wave -> Int -> Int -> Sound  (wave, freq Hz, ms)
	out["soundTone"] = TArrow{From: TSoundWave, To: TArrow{From: TInt, To: TArrow{From: TInt, To: TSound}}}
	// Sound.volume / Sound.sweep : Int -> Sound -> Sound  (0..100 / end freq Hz)
	out["soundVolume"] = TArrow{From: TInt, To: TArrow{From: TSound, To: TSound}}
	out["soundSweep"] = TArrow{From: TInt, To: TArrow{From: TSound, To: TSound}}
	// Tone shaping: cut everything below / above a frequency. Two separate
	// combinators rather than one band-pass because you usually want only one
	// end (wind wants a low cut, a muffled sound wants a high cut), and a single
	// two-argument form would force a sentinel value for "don't cut this side".
	out["soundLowCut"] = TArrow{From: TInt, To: TArrow{From: TSound, To: TSound}}
	out["soundHighCut"] = TArrow{From: TInt, To: TArrow{From: TSound, To: TSound}}
	// Sound.holdPitch : Int -> Sound -> Sound  (hold the pitch flat for ms, then sweep)
	out["soundHoldPitch"] = TArrow{From: TInt, To: TArrow{From: TSound, To: TSound}}
	// The ENVELOPE, in ms. It belongs to the voice, not to the Sub that plays it:
	// before this, `once`/`loop` attacked in 8ms while `ambient` faded in over
	// 400, so swapping one Sub for another silently changed how a sound speaks —
	// a value could not say "arrive now" or "arrive slowly". Both paths read
	// these, so the note decides and the lifetime is all the Sub decides.
	// (docs/proposals/sound-envelope.md)
	out["soundAttack"] = TArrow{From: TInt, To: TArrow{From: TSound, To: TSound}}
	out["soundRelease"] = TArrow{From: TInt, To: TArrow{From: TSound, To: TSound}}
	// Expressiveness pack — the chip-character modifiers.
	// Sound.duty : Int -> Sound -> Sound  (pulse width % for Square: 12/25/50/75).
	out["soundDuty"] = TArrow{From: TInt, To: TArrow{From: TSound, To: TSound}}
	// Sound.vibrato : Int -> Int -> Sound -> Sound  (depth in cents, rate in Hz).
	out["soundVibrato"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TArrow{From: TSound, To: TSound}}}
	// Sound.arp : List Int -> Sound -> Sound  (cycle the pitch fast through these Hz
	//   plus the base — the classic chiptune arpeggio "chord" on one voice).
	out["soundArp"] = TArrow{From: TList(TInt), To: TArrow{From: TSound, To: TSound}}
	// Sound.chord / Sound.sequence : List Sound -> Sound  (layer / string together)
	out["soundChord"] = TArrow{From: TList(TSound), To: TSound}
	out["soundSequence"] = TArrow{From: TList(TSound), To: TSound}
	// Sound.rest : Int -> Sound  (silence of `ms`; occupies time inside a sequence)
	out["soundRest"] = TArrow{From: TInt, To: TSound}
	// Sound.play : Sound -> Cmd msg (fire once).
	// Sound.loop : Sound -> Sub msg (replay the sound seamlessly while subscribed —
	//   melodies / background music).
	// Sound.once : Sound -> Sub msg (play the sound ONE time while subscribed —
	//   a death dirge, a stinger — and cut it off if the sub goes away first;
	//   the cancellable middle ground between play and loop).
	// Sound.voice : Sound -> Sub msg (one held voice, sounding for as long as it
	//   is returned. Its PITCH is part of its identity, so two pitches are two
	//   voices: that is polyphony, one voice per held key).
	// Sound.glide : Sound -> Sub msg (the same held source with its pitch as a
	//   LIVE parameter instead of its identity, so handing it a new pitch slides
	//   the running oscillator there — an engine note, a siren. Monophonic by
	//   construction, exactly like glide on a real synth: there is one source,
	//   and it is always on its way somewhere).
	out["soundPlay"] = TForall{Vars: []int{b.ID}, Body: TArrow{From: TSound, To: TCmd(b)}}
	out["soundLoop"] = TForall{Vars: []int{b.ID}, Body: TArrow{From: TSound, To: TSub(b)}}
	out["soundVoice"] = TForall{Vars: []int{b.ID}, Body: TArrow{From: TSound, To: TSub(b)}}
	out["soundGlide"] = TForall{Vars: []int{b.ID}, Body: TArrow{From: TSound, To: TSub(b)}}
	out["soundOnce"] = TForall{Vars: []int{b.ID}, Body: TArrow{From: TSound, To: TSub(b)}}
	// Note helpers : Int -> Int  (octave -> Hz, equal temperament, C4 = middle C,
	// A4 = 440). Kills the magic-Hz table: Sound.tone Sound.Square (Sound.e 5) dur.
	for _, n := range soundNoteNames {
		out["soundPitch_"+n] = TArrow{From: TInt, To: TInt}
	}
	// App-owned audio controls (the game gets a real mute button / volume slider,
	// instead of hand-gating every play/sub on its own model.muted).
	// Sound.setMuted : Bool -> Cmd msg ; Sound.master : Int -> Cmd msg (0..100).
	out["soundSetMuted"] = TForall{Vars: []int{b.ID}, Body: TArrow{From: TBool, To: TCmd(b)}}
	out["soundMaster"] = TForall{Vars: []int{b.ID}, Body: TArrow{From: TInt, To: TCmd(b)}}

	// === Canvas (v0.0.7) — the 2D draw-list vocabulary ===
	//
	// `canvas` is a View that reports its own box size (onResize) and taps
	// (onTap); shapes are positional and re-issued every frame, so a game
	// lays them out from the live width/height (reflow) and nothing has to
	// distort. The shape builders are monomorphic — they carry no msg; the
	// msg rides only on the canvas attrs, exactly like uiOnMove. Color is
	// opaque (built only by `rgb`); Shape is opaque (rect / circle /
	// canvasText / group). Transform / Align are the two user-facing unions
	// (constructors registered in builtinCustomTypes + qualified below).

	// rgb : Int -> Int -> Int -> Color
	out["rgb"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TArrow{From: TInt, To: TColor}}}

	// rgba : Int -> Int -> Int -> Int -> Color   (r g b alpha, alpha 0-100)
	// Alpha is a percent int like Canvas.Scale, since Mar has no floats.
	out["rgba"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TArrow{From: TInt,
		To: TArrow{From: TInt, To: TColor}}}}

	// rect : Int -> Int -> Int -> Int -> Color -> Shape   (x y w h color)
	out["rect"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TArrow{From: TInt,
		To: TArrow{From: TInt, To: TArrow{From: TColor, To: TShape}}}}}

	// circle : Int -> Int -> Int -> Color -> Shape   (cx cy r color)
	out["circle"] = TArrow{From: TInt, To: TArrow{From: TInt,
		To: TArrow{From: TInt, To: TArrow{From: TColor, To: TShape}}}}

	// triangle : Int -> Int -> Int -> Int -> Int -> Int -> Color -> Shape
	//   (x1 y1 x2 y2 x3 y3 color) — a filled triangle from three points
	out["triangle"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TArrow{From: TInt,
		To: TArrow{From: TInt, To: TArrow{From: TInt, To: TArrow{From: TInt,
			To: TArrow{From: TColor, To: TShape}}}}}}}

	// Canvas.text : Int -> Int -> Int -> Align -> Color -> String -> Shape
	// (x y size align color str). Bare key `canvasText` leaves the bare name
	// `text` free for whichever module a file actually exposes it from.
	out["canvasText"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TArrow{From: TInt,
		To: TArrow{From: TAlign, To: TArrow{From: TColor, To: TArrow{From: TString, To: TShape}}}}}}

	// group : List Transform -> List Shape -> Shape
	out["group"] = TArrow{From: TList(TTransform), To: TArrow{From: TList(TShape), To: TShape}}

	// canvas : CanvasMode -> List (Attr Canvas) -> List Shape -> View a
	// The mode is mandatory (no silent default): Pixelated for 60 fps action
	// games, Crisp for sharp text on turn-based / text-heavy canvases.
	out["canvas"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{
			From: TCanvasMode,
			To: TArrow{
				From: TList(TAttr(TAttrCanvasHost())),
				To:   TArrow{From: TList(TShape), To: TView(a)},
			},
		},
	}

	// onTap : (Int -> Int -> msg) -> Attr Canvas
	// Same msg-threading shape as uiOnMove: the handler's msg generalizes
	// per use and the enclosing canvas unifies it with the page's Msg.
	out["onTap"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{
			From: TArrow{From: TInt, To: TArrow{From: TInt, To: a}},
			To:   TAttr(TAttrCanvasHost()),
		},
	}
	// watchSize : (Int -> Int -> msg) -> Attr Canvas — the element-box MIRROR
	// (w, h in CSS px, the model's coordinate space). Seeds the current size on
	// mount, re-fires on resize. It is a state mirror (the size persists), hence
	// `watch`, not `on`; it stays a canvas attr, not a Sub, because the box is
	// the element's, not the viewport's (Device.watch carries the viewport).
	out["watchSize"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{
			From: TArrow{From: TInt, To: TArrow{From: TInt, To: a}},
			To:   TAttr(TAttrCanvasHost()),
		},
	}
	// watchPointers : (List { id : Int, x : Int, y : Int } -> msg) -> Attr Canvas
	// — the pointer MIRROR: every finger / pressed mouse on this canvas, in
	// canvas CSS-pixel coordinates, as one snapshot that re-fires on change
	// (coalesced per frame; self-heals on pointercancel / blur). `id` is a small
	// stable integer per pointer. This is the multi-touch state channel; the
	// discrete onTap/onRelease/onDrag remain for genuine pointer occurrences.
	out["watchPointers"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{
			From: TArrow{From: TList(TCanvasPointerRecord()), To: a},
			To:   TAttr(TAttrCanvasHost()),
		},
	}
	// onRelease : (Int -> Int -> msg) -> Attr Canvas — the pointer-up companion
	// to onTap (which is pointer-down). Together they enable hold-to-move
	// controls: onTap starts, onRelease stops. Same msg-threading shape.
	out["onRelease"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{
			From: TArrow{From: TInt, To: TArrow{From: TInt, To: a}},
			To:   TAttr(TAttrCanvasHost()),
		},
	}
	// onDrag : (Int -> Int -> msg) -> Attr Canvas — pointer MOVE while pressed.
	// Fires (x, y) as the finger/mouse drags across the canvas between an onTap
	// (down) and onRelease (up). Enables an on-screen joystick you can steer.
	out["onDrag"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{
			From: TArrow{From: TInt, To: TArrow{From: TInt, To: a}},
			To:   TAttr(TAttrCanvasHost()),
		},
	}
	// Desktop-input trio (opt-in, null on touch). onHover : (Int -> Int -> msg)
	// -> Attr Canvas — pointer MOVE with NO button (edge-scroll, placement
	// ghost, hover tooltips). onAltTap : (Int -> Int -> msg) -> Attr Canvas —
	// right-click / two-finger tap, contextmenu preventDefault'd (SC2-style
	// orders). Both share onDrag's msg-threading shape.
	out["onHover"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{
			From: TArrow{From: TInt, To: TArrow{From: TInt, To: a}},
			To:   TAttr(TAttrCanvasHost()),
		},
	}
	out["onAltTap"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{
			From: TArrow{From: TInt, To: TArrow{From: TInt, To: a}},
			To:   TAttr(TAttrCanvasHost()),
		},
	}
	// onWheel : (Int -> Int -> msg) -> Attr Canvas — scroll delta as (dx, dy);
	// signs are stable across devices. Horizontal (dx) enables lateral scroll on
	// trackpads; keyboard callers can ignore whichever axis they don't use.
	out["onWheel"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{
			From: TArrow{From: TInt, To: TArrow{From: TInt, To: a}},
			To:   TAttr(TAttrCanvasHost()),
		},
	}

	// Transform / Align / Blend constructors — qualified-only bindings. These
	// dotted names are the ONLY way to reach them: open-exposing a builtin
	// union is unimplemented, so `exposing (Transform(..))` parses and then
	// leaves `Translate` unbound.
	out["Canvas.Translate"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TTransform}}
	// Scale takes percent ints (100 = 1×, 150 = 1.5×); Rotate takes whole
	// degrees. Mar has no Float, so continuous transforms are integer-encoded
	// and the renderer divides Scale by 100.
	out["Canvas.Scale"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TTransform}}
	out["Canvas.Rotate"] = TArrow{From: TInt, To: TTransform}
	// Alpha takes a percent int too (100 = opaque). It fades the group as ONE
	// composited image, not shape by shape: a sprite built from overlapping
	// parts fades as a whole instead of turning into glass, and a cloud drawn
	// from overlapping puffs comes out evenly translucent instead of showing
	// its seams. Reach for `rgba` when a single shape is translucent; reach
	// for Alpha when a group of them has to fade together.
	out["Canvas.Alpha"] = TArrow{From: TInt, To: TTransform}
	// Canvas.Blend : Blend -> Transform. Governs how the group's pixels land,
	// on each other AND on what is below. Absent = Normal, which is exactly
	// what every group already did, so this is inert for existing code. Add
	// sums light (stacked explosion discs climb orange → gold → white on their
	// own); Multiply darkens by the backdrop (one grey reads as a shadow over
	// ANY background, instead of a hand-picked "background × 0.7" constant
	// that breaks the moment it crosses a line); Screen lightens without
	// clipping; Erase punches holes — and Erase alone always composites the
	// group as one stamp, so overlapping erasers cut a single clean silhouette
	// instead of eating the seam twice.
	out["Canvas.Blend"] = TArrow{From: TBlend, To: TTransform}
	// === Decimal (docs/proposals/decimal.md) — exact base-10 numbers ===
	//
	// Rounding-mode constructors are qualified-only (Decimal.HalfEven):
	// Up / Down / Floor / Ceiling are far too ordinary to reserve as
	// bare global names — half the app unions ever written use them.
	out["Decimal.HalfEven"] = TRounding
	out["Decimal.HalfUp"] = TRounding
	out["Decimal.Down"] = TRounding
	out["Decimal.Up"] = TRounding
	out["Decimal.Floor"] = TRounding
	out["Decimal.Ceiling"] = TRounding

	// Conversions and arithmetic helpers. round uses banker's
	// (HalfEven) — Mar's round exists mostly for money; toIntWith
	// covers the other modes.
	out["Decimal.fromInt"] = TArrow{From: TInt, To: TDecimal}
	out["Decimal.fromCents"] = TArrow{From: TInt, To: TDecimal}
	out["Decimal.toCents"] = TArrow{From: TDecimal, To: TInt}
	out["Decimal.truncate"] = TArrow{From: TDecimal, To: TInt}
	out["Decimal.round"] = TArrow{From: TDecimal, To: TInt}
	out["Decimal.floor"] = TArrow{From: TDecimal, To: TInt}
	out["Decimal.ceiling"] = TArrow{From: TDecimal, To: TInt}
	out["Decimal.toIntWith"] = TArrow{From: TRounding, To: TArrow{From: TDecimal, To: TInt}}
	out["Decimal.toScale"] = TArrow{From: TRounding, To: TArrow{From: TInt, To: TArrow{From: TDecimal, To: TDecimal}}}
	out["Decimal.abs"] = TArrow{From: TDecimal, To: TDecimal}
	out["Decimal.negate"] = TArrow{From: TDecimal, To: TDecimal}
	out["Decimal.compare"] = TArrow{From: TDecimal, To: TArrow{From: TDecimal, To: TOrder}}
	out["Decimal.zero"] = TDecimal
	out["Decimal.fromString"] = TArrow{From: TString, To: TMaybe(TDecimal)}
	out["Decimal.toString"] = TArrow{From: TDecimal, To: TString}

	// Division resolvers — the ONLY exits from Decimal.Division, and
	// the place the rounding decision is written. withRemainder is the
	// lossless one: quotient truncated to the scale plus the exact
	// remainder (q * b + r == a), for the split-the-bill class of
	// problem where no cent may be invented or lost.
	out["Decimal.rounded"] = TArrow{From: TRounding, To: TArrow{From: TInt, To: TArrow{From: TDivision, To: TDecimal}}}
	out["Decimal.withRemainder"] = TArrow{From: TInt, To: TArrow{From: TDivision, To: TRecord{
		Fields: map[string]Type{"quotient": TDecimal, "remainder": TDecimal},
		Order:  []string{"quotient", "remainder"},
	}}}

	out["Canvas.Normal"] = TBlend
	out["Canvas.Add"] = TBlend
	out["Canvas.Multiply"] = TBlend
	out["Canvas.Screen"] = TBlend
	out["Canvas.Erase"] = TBlend
	out["Canvas.Left"] = TAlign
	out["Canvas.Center"] = TAlign
	out["Canvas.Right"] = TAlign

	// === Device (docs/proposals/device.md) — capabilities, not identities ===
	//
	// Device.watch : (Device -> msg) -> Sub msg. Fires immediately with the
	// current device record on subscribe (Canvas.onResize's precedent), then a
	// fresh record whenever ANY axis changes — window resize, tablet rotation,
	// split-view, a mouse getting plugged into an iPad, dark mode flipping at
	// sunset. Everything is read from CSS media queries (pointer / any-pointer /
	// hover / prefers-color-scheme / prefers-reduced-motion) + the viewport
	// size, NEVER a user-agent string (iPadOS lies and reports macOS). The
	// `Device` record shape is a builtin alias (builtinTypeAliases) so an app
	// can store it as `dev : Device`. Web-first; iOS deferred (iosDeferred).
	out["deviceWatch"] = TForall{Vars: []int{b.ID}, Body: TArrow{From: TArrow{From: TDeviceRecord(), To: b}, To: TSub(b)}}
	// Device.touchOnly : Device -> Bool — finger-only hardware (coarse pointer,
	//   nothing fine attached, no hover). iPhone yes, iPad yes; iPad+trackpad NO,
	//   touch-laptop NO. The blessed replacement for seasons-gp's usedTouch guess.
	// Device.canHover : Device -> Bool — a real hover story exists (tooltips and
	//   hover-reveal buttons are usable).
	out["deviceTouchOnly"] = TArrow{From: TDeviceRecord(), To: TBool}
	out["deviceCanHover"] = TArrow{From: TDeviceRecord(), To: TBool}
	// Pointer constructors — global (like Order's LT / Method's GET), so a game
	// writes `d.pointer == Coarse` without an import.
	out["Coarse"] = TPointer
	out["Fine"] = TPointer
	out["Pixelated"] = TCanvasMode
	out["Crisp"] = TCanvasMode

	// --- stdlib (List, String, Maybe) ---
	for k, v := range stdlibBindings() {
		out[k] = v
	}

	return out
}

func stdlibBindings() map[string]Type {
	a := TVar{ID: -3}
	b := TVar{ID: -4}

	// Comparable-constrained vars for Dict / Set keys. IDs -20 and -21
	// sit outside the existing range used by other stdlib schemes
	// (-3..-10 and -101..-102) so there's no aliasing risk. The
	// Constraint field makes the unifier reject non-comparable types
	// (Records / tuples / arbitrary custom types) at the call site.
	dictK := TVar{ID: -20, Constraint: KindComparable}
	setJ := TVar{ID: -21, Constraint: KindComparable}

	// Number-constrained var for List.sum / List.product, same reasoning
	// and the same untaken ID range.
	num := TVar{ID: -22, Constraint: KindNumber}

	return map[string]Type{
		// List
		"listLength": TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(a), To: TInt}},
		"listMap": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: a, To: b},
				To:   TArrow{From: TList(a), To: TList(b)},
			},
		},
		"listFilter": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TArrow{From: a, To: TBool},
				To:   TArrow{From: TList(a), To: TList(a)},
			},
		},
		"listFoldl": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: a, To: TArrow{From: b, To: b}},
				To: TArrow{
					From: b,
					To:   TArrow{From: TList(a), To: b},
				},
			},
		},
		// listSum : List number -> number. Two native implementations sit
		// behind this one name; the elaboration pass tells each call site
		// which. See ast.EQualified.Impl.
		"listSum":     TForall{Vars: []int{num.ID}, Body: TArrow{From: TList(num), To: num}},
		"listRange":   TArrow{From: TInt, To: TArrow{From: TInt, To: TList(TInt)}},
		"listReverse": TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(a), To: TList(a)}},
		"listHead":    TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(a), To: TMaybe(a)}},
		"listTail":    TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(a), To: TMaybe(TList(a))}},
		"listIsEmpty": TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(a), To: TBool}},
		"listConcat":  TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(TList(a)), To: TList(a)}},

		// listTake / listDrop : Int -> List a -> List a
		"listTake": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TInt, To: TArrow{From: TList(a), To: TList(a)}},
		},
		"listDrop": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TInt, To: TArrow{From: TList(a), To: TList(a)}},
		},
		// List.move : Int -> Int -> List a -> List a
		// Pure list-splice helper. Removes the element at `from` and
		// inserts it at `to`. Returns the input unchanged when
		// from == to or either index is out of range — defensive so
		// stale Msgs (race conditions) don't corrupt the list.
		"listMove": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TInt, To: TArrow{From: TInt, To: TArrow{From: TList(a), To: TList(a)}}},
		},
		// listMember : a -> List a -> Bool
		"listMember": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: a, To: TArrow{From: TList(a), To: TBool}},
		},
		// listAny / listAll : (a -> Bool) -> List a -> Bool
		"listAny": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TArrow{From: a, To: TBool},
				To:   TArrow{From: TList(a), To: TBool},
			},
		},
		"listAll": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TArrow{From: a, To: TBool},
				To:   TArrow{From: TList(a), To: TBool},
			},
		},
		// listFoldr : (a -> b -> b) -> b -> List a -> b
		"listFoldr": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: a, To: TArrow{From: b, To: b}},
				To: TArrow{
					From: b,
					To:   TArrow{From: TList(a), To: b},
				},
			},
		},
		// listIndexedMap : (Int -> a -> b) -> List a -> List b
		"listIndexedMap": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: TInt, To: TArrow{From: a, To: b}},
				To:   TArrow{From: TList(a), To: TList(b)},
			},
		},
		// listRepeat : Int -> a -> List a
		"listRepeat": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TInt, To: TArrow{From: a, To: TList(a)}},
		},
		// listIntersperse : a -> List a -> List a
		"listIntersperse": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: a, To: TArrow{From: TList(a), To: TList(a)}},
		},
		// listPartition : (a -> Bool) -> List a -> (List a, List a)
		"listPartition": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TArrow{From: a, To: TBool},
				To: TArrow{
					From: TList(a),
					To:   TTuple{Members: []Type{TList(a), TList(a)}},
				},
			},
		},
		// listConcatMap : (a -> List b) -> List a -> List b
		"listConcatMap": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: a, To: TList(b)},
				To:   TArrow{From: TList(a), To: TList(b)},
			},
		},
		// listFilterMap : (a -> Maybe b) -> List a -> List b
		"listFilterMap": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: a, To: TMaybe(b)},
				To:   TArrow{From: TList(a), To: TList(b)},
			},
		},
		// listMaximum / listMinimum : List a -> Maybe a
		"listMaximum": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TList(a), To: TMaybe(a)},
		},
		"listMinimum": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TList(a), To: TMaybe(a)},
		},
		// listProduct : List number -> number (same shape as listSum)
		"listProduct": TForall{Vars: []int{num.ID}, Body: TArrow{From: TList(num), To: num}},
		// listSort : List a -> List a
		"listSort": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TList(a), To: TList(a)},
		},
		// listSortBy : (a -> b) -> List a -> List a
		"listSortBy": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: a, To: b},
				To:   TArrow{From: TList(a), To: TList(a)},
			},
		},
		// listSortWith : (a -> a -> Order) -> List a -> List a
		// Comparator returns LT / EQ / GT — same convention as Elm.
		// (Earlier drafts used Int -1/0/1; using a named sum type
		// makes the result self-documenting and prevents the "I
		// returned 1 but meant LT" foot-gun.)
		"listSortWith": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TArrow{From: a, To: TArrow{From: a, To: TOrder}},
				To:   TArrow{From: TList(a), To: TList(a)},
			},
		},

		// String
		"stringLength":     TArrow{From: TString, To: TInt},
		"stringContains":   TArrow{From: TString, To: TArrow{From: TString, To: TBool}},
		"stringStartsWith": TArrow{From: TString, To: TArrow{From: TString, To: TBool}},
		"stringFromInt":    TArrow{From: TInt, To: TString},
		"stringToUpper":    TArrow{From: TString, To: TString},
		"stringToLower":    TArrow{From: TString, To: TString},

		// Maybe
		"maybeWithDefault": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: a, To: TArrow{From: TMaybe(a), To: a}},
		},
		"maybeMap": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: a, To: b},
				To:   TArrow{From: TMaybe(a), To: TMaybe(b)},
			},
		},
		"maybeAndThen": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: a, To: TMaybe(b)},
				To:   TArrow{From: TMaybe(a), To: TMaybe(b)},
			},
		},
		// Result helpers
		"resultMap": TForall{
			Vars: []int{a.ID, b.ID, -7},
			Body: TArrow{
				From: TArrow{From: b, To: TVar{ID: -7}},
				To:   TArrow{From: TResult(a, b), To: TResult(a, TVar{ID: -7})},
			},
		},
		"resultAndThen": TForall{
			Vars: []int{a.ID, b.ID, -7},
			Body: TArrow{
				From: TArrow{From: b, To: TResult(a, TVar{ID: -7})},
				To:   TArrow{From: TResult(a, b), To: TResult(a, TVar{ID: -7})},
			},
		},
		"resultMapError": TForall{
			Vars: []int{a.ID, b.ID, -7},
			Body: TArrow{
				From: TArrow{From: a, To: TVar{ID: -7}},
				To:   TArrow{From: TResult(a, b), To: TResult(TVar{ID: -7}, b)},
			},
		},
		// Result extras
		"resultWithDefault": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: b, To: TArrow{From: TResult(a, b), To: b}},
		},
		"resultFromMaybe": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: a, To: TArrow{From: TMaybe(b), To: TResult(a, b)}},
		},
		"resultToMaybe": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: TResult(a, b), To: TMaybe(b)},
		},
		// Maybe extras
		"maybeMap2": TForall{
			Vars: []int{a.ID, b.ID, -8},
			Body: TArrow{
				From: TArrow{From: a, To: TArrow{From: b, To: TVar{ID: -8}}},
				To: TArrow{
					From: TMaybe(a),
					To: TArrow{
						From: TMaybe(b),
						To:   TMaybe(TVar{ID: -8}),
					},
				},
			},
		},
		"maybeMap3": TForall{
			Vars: []int{a.ID, b.ID, -8, -9},
			Body: TArrow{
				From: TArrow{From: a, To: TArrow{From: b, To: TArrow{From: TVar{ID: -8}, To: TVar{ID: -9}}}},
				To: TArrow{
					From: TMaybe(a),
					To: TArrow{
						From: TMaybe(b),
						To: TArrow{
							From: TMaybe(TVar{ID: -8}),
							To:   TMaybe(TVar{ID: -9}),
						},
					},
				},
			},
		},
		// maybeAndMap : Maybe a -> Maybe (a -> b) -> Maybe b
		"maybeAndMap": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TMaybe(a),
				To:   TArrow{From: TMaybe(TArrow{From: a, To: b}), To: TMaybe(b)},
			},
		},
		"maybeFilter": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TArrow{From: a, To: TBool},
				To:   TArrow{From: TMaybe(a), To: TMaybe(a)},
			},
		},
		// Tuple — 2-tuple helpers. The tvars a, b are the two element
		// positions; ' (prime) suffix on output names tracks the
		// mapBoth/mapFirst/mapSecond renames cleanly.
		"tupleFirst": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: TTuple{Members: []Type{a, b}}, To: a},
		},
		"tupleSecond": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: TTuple{Members: []Type{a, b}}, To: b},
		},
		"tuplePair": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: a,
				To:   TArrow{From: b, To: TTuple{Members: []Type{a, b}}},
			},
		},
		"tupleMapFirst": TForall{
			Vars: []int{a.ID, b.ID, -8},
			Body: TArrow{
				From: TArrow{From: a, To: TVar{ID: -8}},
				To: TArrow{
					From: TTuple{Members: []Type{a, b}},
					To:   TTuple{Members: []Type{TVar{ID: -8}, b}},
				},
			},
		},
		"tupleMapSecond": TForall{
			Vars: []int{a.ID, b.ID, -8},
			Body: TArrow{
				From: TArrow{From: b, To: TVar{ID: -8}},
				To: TArrow{
					From: TTuple{Members: []Type{a, b}},
					To:   TTuple{Members: []Type{a, TVar{ID: -8}}},
				},
			},
		},
		"tupleMapBoth": TForall{
			Vars: []int{a.ID, b.ID, -8, -9},
			Body: TArrow{
				From: TArrow{From: a, To: TVar{ID: -8}},
				To: TArrow{
					From: TArrow{From: b, To: TVar{ID: -9}},
					To: TArrow{
						From: TTuple{Members: []Type{a, b}},
						To:   TTuple{Members: []Type{TVar{ID: -8}, TVar{ID: -9}}},
					},
				},
			},
		},
		// String extras
		"stringSplit":    TArrow{From: TString, To: TArrow{From: TString, To: TList(TString)}},
		"stringJoin":     TArrow{From: TString, To: TArrow{From: TList(TString), To: TString}},
		"stringTrim":     TArrow{From: TString, To: TString},
		"stringEndsWith": TArrow{From: TString, To: TArrow{From: TString, To: TBool}},
		"stringToInt":    TArrow{From: TString, To: TMaybe(TInt)},
		"stringReplace": TArrow{
			From: TString,
			To:   TArrow{From: TString, To: TArrow{From: TString, To: TString}},
		},
		"stringRepeat": TArrow{From: TInt, To: TArrow{From: TString, To: TString}},
		// padLeft / padRight take a Char (Elm-style) — see stringPadLeft
		// in internal/runtime/stdlib.go for the rationale.
		"stringPadLeft": TArrow{
			From: TInt,
			To:   TArrow{From: TChar, To: TArrow{From: TString, To: TString}},
		},
		"stringPadRight": TArrow{
			From: TInt,
			To:   TArrow{From: TChar, To: TArrow{From: TString, To: TString}},
		},
		"stringIndexes": TArrow{From: TString, To: TArrow{From: TString, To: TList(TInt)}},
		// String <-> [Char] bridges.
		"stringToList":   TArrow{From: TString, To: TList(TChar)},
		"stringFromList": TArrow{From: TList(TChar), To: TString},
		"stringCons":     TArrow{From: TChar, To: TArrow{From: TString, To: TString}},
		// String higher-order ops over Char. The accumulator type
		// `b` is reused from the outer scope, polymorphic per call.
		"stringMap": TArrow{
			From: TArrow{From: TChar, To: TChar},
			To:   TArrow{From: TString, To: TString},
		},
		"stringFilter": TArrow{
			From: TArrow{From: TChar, To: TBool},
			To:   TArrow{From: TString, To: TString},
		},
		// stringFoldl : (Char -> b -> b) -> b -> String -> b
		"stringFoldl": TForall{
			Vars: []int{b.ID},
			Body: TArrow{
				From: TArrow{From: TChar, To: TArrow{From: b, To: b}},
				To:   TArrow{From: b, To: TArrow{From: TString, To: b}},
			},
		},
		"stringAny": TArrow{
			From: TArrow{From: TChar, To: TBool},
			To:   TArrow{From: TString, To: TBool},
		},

		// Char module — monomorphic. Unicode code point semantics.
		"charToCode":   TArrow{From: TChar, To: TInt},
		"charFromCode": TArrow{From: TInt, To: TChar},
		"charIsDigit":  TArrow{From: TChar, To: TBool},
		"charIsAlpha":  TArrow{From: TChar, To: TBool},
		"charIsUpper":  TArrow{From: TChar, To: TBool},
		"charIsLower":  TArrow{From: TChar, To: TBool},
		"charToUpper":  TArrow{From: TChar, To: TChar},
		"charToLower":  TArrow{From: TChar, To: TChar},

		// Task — the value-monad ("await"): the backend's currency and any
		// value-producing effect. Task.andThen threads the produced value
		// (do A, then with A's result do B); a service handler runs a Task
		// and the value becomes the response. One type parameter — failure
		// is a value (Task.fail's String, surfaced as Service.Error), never a
		// type index. Lives on both sides; on the frontend a Task reaches the
		// MVU loop through Cmd.perform.
		"effectSucceed": TForall{
			Vars: []int{b.ID},
			Body: TArrow{From: b, To: TTask(b)},
		},
		"effectFail": TForall{
			Vars: []int{b.ID},
			Body: TArrow{From: TString, To: TTask(b)},
		},
		"effectMap": TForall{
			Vars: []int{b.ID, -5},
			Body: TArrow{
				From: TArrow{From: b, To: TVar{ID: -5}},
				To: TArrow{
					From: TTask(b),
					To:   TTask(TVar{ID: -5}),
				},
			},
		},
		"effectAndThen": TForall{
			Vars: []int{b.ID, -5},
			Body: TArrow{
				From: TArrow{From: b, To: TTask(TVar{ID: -5})},
				To: TArrow{
					From: TTask(b),
					To:   TTask(TVar{ID: -5}),
				},
			},
		},
		"effectForEach": TForall{
			Vars: []int{b.ID},
			Body: TArrow{
				From: TArrow{From: b, To: TTask(TUnit{})},
				To:   TArrow{From: TList(b), To: TTask(TUnit{})},
			},
		},
		"effectSequence": TForall{
			Vars: []int{b.ID},
			Body: TArrow{
				From: TList(TTask(b)),
				To:   TTask(TList(b)),
			},
		},
		// Cmd — the frontend message-monoid: what init/update return, which
		// the runtime performs to deliver a msg back into the MVU loop.
		//
		// Cmd.batch : List (Cmd msg) -> Cmd msg — fire-and-forget fan-out:
		//   launch several independent Service.calls from one update, each
		//   delivering through its own toMsg. Produces no aggregate value —
		//   the children's messages ARE the output.
		// Cmd.none : Cmd msg — the identity (do nothing).
		// Cmd.perform : (a -> msg) -> Task a -> Cmd msg — the Task→Cmd bridge:
		//   run a Task and deliver its produced value as a msg (Elm's
		//   Task.perform). The only way a Task reaches the frontend loop.
		"effectBatch": TForall{
			Vars: []int{b.ID},
			Body: TArrow{
				From: TList(TCmd(b)),
				To:   TCmd(b),
			},
		},
		"effectNone": TForall{
			Vars: []int{b.ID},
			Body: TCmd(b),
		},
		"cmdPerform": TForall{
			Vars: []int{b.ID, -5},
			Body: TArrow{
				From: TArrow{From: b, To: TVar{ID: -5}},
				To: TArrow{
					From: TTask(b),
					To:   TCmd(TVar{ID: -5}),
				},
			},
		},

		"subBatch": TForall{
			Vars: []int{b.ID},
			Body: TArrow{From: TList(TSub(b)), To: TSub(b)},
		},
		"subNone": TForall{
			Vars: []int{b.ID},
			Body: TSub(b),
		},

		// Random — Elm-style generators. `Generator a` is a recipe for a random
		// value; Random.generate runs it via the runtime RNG and delivers the value
		// as a Msg (a Cmd, like Service.call). Combinators build recipes purely.
		// (No float/weighted — Mar has no Float; Decimal analogs can come later. No
		// Seed/step API yet.)
		"randomGenerate": TForall{
			Vars: []int{a.ID, -70},
			Body: TArrow{From: TArrow{From: a, To: TVar{ID: -70}}, To: TArrow{From: TGenerator(a), To: TCmd(TVar{ID: -70})}},
		},
		"randomInt":      TArrow{From: TInt, To: TArrow{From: TInt, To: TGenerator(TInt)}},
		"randomConstant": TForall{Vars: []int{a.ID}, Body: TArrow{From: a, To: TGenerator(a)}},
		"randomUniform":  TForall{Vars: []int{a.ID}, Body: TArrow{From: a, To: TArrow{From: TList(a), To: TGenerator(a)}}},
		"randomList":     TForall{Vars: []int{a.ID}, Body: TArrow{From: TInt, To: TArrow{From: TGenerator(a), To: TGenerator(TList(a))}}},
		"randomPair":     TForall{Vars: []int{a.ID, b.ID}, Body: TArrow{From: TGenerator(a), To: TArrow{From: TGenerator(b), To: TGenerator(TTuple{Members: []Type{a, b}})}}},
		"randomMap":      TForall{Vars: []int{a.ID, b.ID}, Body: TArrow{From: TArrow{From: a, To: b}, To: TArrow{From: TGenerator(a), To: TGenerator(b)}}},
		"randomMap2": TForall{
			Vars: []int{a.ID, b.ID, -71},
			Body: TArrow{From: TArrow{From: a, To: TArrow{From: b, To: TVar{ID: -71}}}, To: TArrow{From: TGenerator(a), To: TArrow{From: TGenerator(b), To: TGenerator(TVar{ID: -71})}}},
		},
		"randomMap3": TForall{
			Vars: []int{a.ID, b.ID, -72, -73},
			Body: TArrow{From: TArrow{From: a, To: TArrow{From: b, To: TArrow{From: TVar{ID: -72}, To: TVar{ID: -73}}}}, To: TArrow{From: TGenerator(a), To: TArrow{From: TGenerator(b), To: TArrow{From: TGenerator(TVar{ID: -72}), To: TGenerator(TVar{ID: -73})}}}},
		},
		"randomAndThen": TForall{Vars: []int{a.ID, b.ID}, Body: TArrow{From: TArrow{From: a, To: TGenerator(b)}, To: TArrow{From: TGenerator(a), To: TGenerator(b)}}},

		// Pure stepping (Elm's Seed API). A Seed runs a Generator with no effect,
		// so randomness works on ANY side and replays deterministically — which
		// is why Random is usable on the server, not just the frontend.
		//   Random.initialSeed : Int -> Seed
		//   Random.step        : Generator a -> Seed -> (a, Seed)
		"randomInitialSeed": TArrow{From: TInt, To: TSeed()},
		"randomStep":        TForall{Vars: []int{a.ID}, Body: TArrow{From: TGenerator(a), To: TArrow{From: TSeed(), To: TTuple{Members: []Type{a, TSeed()}}}}},
		// Real OS entropy → a Seed, as a Task (runs on client and server).
		//   Random.seed : Task Seed
		"randomSeed": TTask(TSeed()),
		// Time — a small Duration type with unit-named smart constructors.
		//
		//   Time.seconds : Int -> Duration
		//   Time.minutes : Int -> Duration
		//   Time.hours   : Int -> Duration
		//   Time.days    : Int -> Duration
		//   Time.weeks   : Int -> Duration
		//   Time.add     : Duration -> Duration -> Duration
		//   Time.toSeconds : Duration -> Int
		//
		// There is intentionally no public Int → Duration coercion;
		// every Duration is constructed via one of the unit-named
		// builders so the call site documents the unit and unit
		// confusion is impossible (no "I thought 30 was days, it
		// was seconds" bugs). Used by `Auth.config.sessionDuration`
		// and anywhere else the framework or user code wants a
		// time interval.
		"timeMillis":    TArrow{From: TInt, To: TDuration},
		"timeSeconds":   TArrow{From: TInt, To: TDuration},
		"timeMinutes":   TArrow{From: TInt, To: TDuration},
		"timeHours":     TArrow{From: TInt, To: TDuration},
		"timeDays":      TArrow{From: TInt, To: TDuration},
		"timeWeeks":     TArrow{From: TInt, To: TDuration},
		"timeToSeconds": TArrow{From: TDuration, To: TInt},

		// Time — absolute moments. Stored as Unix milliseconds.
		// Time.now is a Task because it reads the wall clock;
		// .add / .sub shift a moment by a Duration; .diff gives
		// the Duration between two moments.
		//
		//   Time.now      : Effect e Time
		//   Time.add      : Time -> Duration -> Time
		//   Time.sub      : Time -> Duration -> Time
		//   Time.diff     : Time -> Time -> Duration
		//   Time.before   : Time -> Time -> Bool
		//   Time.after    : Time -> Time -> Bool
		//   Time.toIso    : Time -> String              -- ISO 8601 ("2026-05-05T13:45:30Z")
		//   Time.fromIso  : String -> Maybe Time        -- parse; Nothing on bad format
		//   Time.toMillis : Time -> Int                 -- escape hatch; Unix ms since 1970
		// Time.now : Task Time — the current time as a value-task. On the
		// backend you thread it with Task.andThen; on the frontend you reach
		// the MVU loop with Cmd.perform GotNow Time.now. (Elm's Time.now.)
		"timeNow": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TTask(TTime),
		},
		"timeEvery": TForall{
			Vars: []int{b.ID},
			Body: TArrow{From: TDuration, To: TArrow{From: TArrow{From: TTime, To: b}, To: TSub(b)}},
		},
		"timeAdd":      TArrow{From: TTime, To: TArrow{From: TDuration, To: TTime}},
		"timeSub":      TArrow{From: TTime, To: TArrow{From: TDuration, To: TTime}},
		"timeDiff":     TArrow{From: TTime, To: TArrow{From: TTime, To: TDuration}},
		"timeBefore":   TArrow{From: TTime, To: TArrow{From: TTime, To: TBool}},
		"timeAfter":    TArrow{From: TTime, To: TArrow{From: TTime, To: TBool}},
		"timeToIso":    TArrow{From: TTime, To: TString},
		"timeFromIso":  TArrow{From: TString, To: TMaybe(TTime)},
		"timeToMillis": TArrow{From: TTime, To: TInt},

		// Calendar-aware constructors and arithmetic — different from
		// Duration-based shifts because months and years aren't
		// fixed-length. `Time.add t (Time.days 30)` jumps exactly 30
		// days; `Time.addMonths t 1` honors variable month length and
		// year-end rollover.
		//
		//   Time.fromYMD   : Int -> Int -> Int -> Time   (year, month, day → midnight UTC)
		//   Time.addDays   : Time -> Int -> Time
		//   Time.addMonths : Time -> Int -> Time
		//   Time.addYears  : Time -> Int -> Time
		"timeFromYMD":   TArrow{From: TInt, To: TArrow{From: TInt, To: TArrow{From: TInt, To: TTime}}},
		"timeAddDays":   TArrow{From: TTime, To: TArrow{From: TInt, To: TTime}},
		"timeAddMonths": TArrow{From: TTime, To: TArrow{From: TInt, To: TTime}},
		"timeAddYears":  TArrow{From: TTime, To: TArrow{From: TInt, To: TTime}},

		// Component getters — extract calendar fields from a Time
		// (interpreted in UTC). Useful for rendering ("Posted on May
		// 5, 2026") and conditional logic ("if hour >= 18 then…").
		// Month is 1-indexed (1 = January, 12 = December) — matching
		// human convention rather than JavaScript's 0-indexed quirk.
		//
		//   Time.year   : Time -> Int
		//   Time.month  : Time -> Int    -- 1..12
		//   Time.day    : Time -> Int    -- 1..31
		//   Time.hour   : Time -> Int    -- 0..23
		//   Time.minute : Time -> Int    -- 0..59
		//   Time.second : Time -> Int    -- 0..59
		"timeYear":   TArrow{From: TTime, To: TInt},
		"timeMonth":  TArrow{From: TTime, To: TInt},
		"timeDay":    TArrow{From: TTime, To: TInt},
		"timeHour":   TArrow{From: TTime, To: TInt},
		"timeMinute": TArrow{From: TTime, To: TInt},
		"timeSecond": TArrow{From: TTime, To: TInt},

		// Dict k v / Set k — Elm-style polymorphic containers with a
		// Comparable constraint on the key. The constraint lives on
		// the TVar itself (KindComparable); the unifier rejects any
		// attempt to bind it to a Record / custom type / tuple /
		// function at the call site. This catches `Dict.fromList
		// [({name: "bob"}, 1)]` at compile time with a message like
		// "a record is not comparable; allowed key types are Int,
		// Float, String, Char" — no more waiting for a runtime
		// "comparison: unsupported types" surprise.
		//
		// k / j are the Comparable-marked vars (IDs -20 / -21).
		// v / acc / w continue to use the unconstrained `a` / `b` /
		// -10 already in scope.
		"dictEmpty": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TDict(dictK, b),
		},
		"dictSingleton": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: dictK, To: TArrow{From: b, To: TDict(dictK, b)}},
		},
		"dictInsert": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{
				From: dictK,
				To:   TArrow{From: b, To: TArrow{From: TDict(dictK, b), To: TDict(dictK, b)}},
			},
		},
		// dictUpdate : k -> (Maybe v -> Maybe v) -> Dict k v -> Dict k v
		"dictUpdate": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{
				From: dictK,
				To: TArrow{
					From: TArrow{From: TMaybe(b), To: TMaybe(b)},
					To:   TArrow{From: TDict(dictK, b), To: TDict(dictK, b)},
				},
			},
		},
		"dictRemove": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: dictK, To: TArrow{From: TDict(dictK, b), To: TDict(dictK, b)}},
		},
		"dictIsEmpty": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: TDict(dictK, b), To: TBool},
		},
		"dictMember": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: dictK, To: TArrow{From: TDict(dictK, b), To: TBool}},
		},
		"dictGet": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: dictK, To: TArrow{From: TDict(dictK, b), To: TMaybe(b)}},
		},
		"dictSize": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: TDict(dictK, b), To: TInt},
		},
		"dictKeys": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: TDict(dictK, b), To: TList(dictK)},
		},
		"dictValues": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: TDict(dictK, b), To: TList(b)},
		},
		"dictToList": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: TDict(dictK, b), To: TList(TTuple{Members: []Type{dictK, b}})},
		},
		"dictFromList": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: TList(TTuple{Members: []Type{dictK, b}}), To: TDict(dictK, b)},
		},
		// dictMap : (k -> v -> w) -> Dict k v -> Dict k w
		"dictMap": TForall{
			Vars: []int{dictK.ID, b.ID, -10},
			Body: TArrow{
				From: TArrow{From: dictK, To: TArrow{From: b, To: TVar{ID: -10}}},
				To:   TArrow{From: TDict(dictK, b), To: TDict(dictK, TVar{ID: -10})},
			},
		},
		// dictFoldl / dictFoldr : (k -> v -> acc -> acc) -> acc -> Dict k v -> acc
		"dictFoldl": TForall{
			Vars: []int{dictK.ID, b.ID, -10},
			Body: TArrow{
				From: TArrow{From: dictK, To: TArrow{From: b, To: TArrow{From: TVar{ID: -10}, To: TVar{ID: -10}}}},
				To:   TArrow{From: TVar{ID: -10}, To: TArrow{From: TDict(dictK, b), To: TVar{ID: -10}}},
			},
		},
		"dictFoldr": TForall{
			Vars: []int{dictK.ID, b.ID, -10},
			Body: TArrow{
				From: TArrow{From: dictK, To: TArrow{From: b, To: TArrow{From: TVar{ID: -10}, To: TVar{ID: -10}}}},
				To:   TArrow{From: TVar{ID: -10}, To: TArrow{From: TDict(dictK, b), To: TVar{ID: -10}}},
			},
		},
		"dictFilter": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: dictK, To: TArrow{From: b, To: TBool}},
				To:   TArrow{From: TDict(dictK, b), To: TDict(dictK, b)},
			},
		},
		"dictPartition": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: dictK, To: TArrow{From: b, To: TBool}},
				To: TArrow{
					From: TDict(dictK, b),
					To:   TTuple{Members: []Type{TDict(dictK, b), TDict(dictK, b)}},
				},
			},
		},
		"dictUnion": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: TDict(dictK, b), To: TArrow{From: TDict(dictK, b), To: TDict(dictK, b)}},
		},
		"dictIntersect": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: TDict(dictK, b), To: TArrow{From: TDict(dictK, b), To: TDict(dictK, b)}},
		},
		"dictDiff": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{From: TDict(dictK, b), To: TArrow{From: TDict(dictK, b), To: TDict(dictK, b)}},
		},

		// Set k — same Comparable constraint as Dict's key.
		"setEmpty":     TForall{Vars: []int{dictK.ID}, Body: TSet(dictK)},
		"setSingleton": TForall{Vars: []int{dictK.ID}, Body: TArrow{From: dictK, To: TSet(dictK)}},
		"setInsert": TForall{
			Vars: []int{dictK.ID},
			Body: TArrow{From: dictK, To: TArrow{From: TSet(dictK), To: TSet(dictK)}},
		},
		"setRemove": TForall{
			Vars: []int{dictK.ID},
			Body: TArrow{From: dictK, To: TArrow{From: TSet(dictK), To: TSet(dictK)}},
		},
		"setIsEmpty": TForall{Vars: []int{dictK.ID}, Body: TArrow{From: TSet(dictK), To: TBool}},
		"setMember": TForall{
			Vars: []int{dictK.ID},
			Body: TArrow{From: dictK, To: TArrow{From: TSet(dictK), To: TBool}},
		},
		"setSize":   TForall{Vars: []int{dictK.ID}, Body: TArrow{From: TSet(dictK), To: TInt}},
		"setToList": TForall{Vars: []int{dictK.ID}, Body: TArrow{From: TSet(dictK), To: TList(dictK)}},
		"setFromList": TForall{
			Vars: []int{dictK.ID},
			Body: TArrow{From: TList(dictK), To: TSet(dictK)},
		},
		// setMap : (k -> j) -> Set k -> Set j — BOTH sides comparable
		// (the output set re-sorts and needs comparable keys too).
		"setMap": TForall{
			Vars: []int{dictK.ID, setJ.ID},
			Body: TArrow{From: TArrow{From: dictK, To: setJ}, To: TArrow{From: TSet(dictK), To: TSet(setJ)}},
		},
		"setFoldl": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: dictK, To: TArrow{From: b, To: b}},
				To:   TArrow{From: b, To: TArrow{From: TSet(dictK), To: b}},
			},
		},
		"setFoldr": TForall{
			Vars: []int{dictK.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: dictK, To: TArrow{From: b, To: b}},
				To:   TArrow{From: b, To: TArrow{From: TSet(dictK), To: b}},
			},
		},
		"setFilter": TForall{
			Vars: []int{dictK.ID},
			Body: TArrow{
				From: TArrow{From: dictK, To: TBool},
				To:   TArrow{From: TSet(dictK), To: TSet(dictK)},
			},
		},
		"setPartition": TForall{
			Vars: []int{dictK.ID},
			Body: TArrow{
				From: TArrow{From: dictK, To: TBool},
				To: TArrow{
					From: TSet(dictK),
					To:   TTuple{Members: []Type{TSet(dictK), TSet(dictK)}},
				},
			},
		},
		"setUnion": TForall{
			Vars: []int{dictK.ID},
			Body: TArrow{From: TSet(dictK), To: TArrow{From: TSet(dictK), To: TSet(dictK)}},
		},
		"setIntersect": TForall{
			Vars: []int{dictK.ID},
			Body: TArrow{From: TSet(dictK), To: TArrow{From: TSet(dictK), To: TSet(dictK)}},
		},
		"setDiff": TForall{
			Vars: []int{dictK.ID},
			Body: TArrow{From: TSet(dictK), To: TArrow{From: TSet(dictK), To: TSet(dictK)}},
		},

		// Entity.timestamp : Constraint -> Column Time
		// Stored as INTEGER (Unix milliseconds). Round-trips to/from
		// Time values via the repo encode/decode path so handlers
		// only ever see Time, never raw integers.
		"entityTimestamp": TArrow{From: TConstraint(), To: TColumn(TTime)},

		// JSON (untyped — encode any value, decode produces "any" record/list/etc)
		"jsonEncode": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: a, To: TString},
		},
		"jsonDecode": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TString, To: TResult(TString, a)},
		},

		// HTTP client (browser-side). On the server these are stubs that just
		// fail; only the JS runtime actually performs the fetch and feeds the
		// response back as a Msg.
		"httpGet": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TString,
				To: TArrow{
					From: TArrow{From: TResult(TString, TString), To: b},
					To:   TCmd(b),
				},
			},
		},
		"httpPost": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TString,
				To: TArrow{
					From: TString,
					To: TArrow{
						From: TArrow{From: TResult(TString, TString), To: b},
						To:   TCmd(b),
					},
				},
			},
		},

		// Entity declaration (single-record form)
		//
		//   notes : Entity Note
		//   notes =
		//       Entity.define
		//           { name    = "notes"
		//           , columns =
		//               { id   = Entity.serial
		//               , body = Entity.text Entity.notNull
		//               }
		//           , uniques = []
		//           }
		//
		// Entity.define takes a single record carrying every piece of
		// the entity declaration: its table name, its column schema,
		// and any composite unique constraints. The `columns` sub-
		// record is fully polymorphic; the runtime cross-checks at
		// first Repo call that the schema's keys/types are compatible
		// with the row record. Trade-off documented in mar.md.
		//
		// `uniques` is required even when empty (`[]`) — Mar has no
		// default-argument story, so explicit "none here" is the rule.
		"entityDefine": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{
						"name":    TString,
						"columns": b,
						"uniques": TList(TList(TString)),
					},
					Order: []string{"name", "columns", "uniques"},
				},
				To: TEntity(a),
			},
		},
		// Column constructors. Each carries the value type stored in that
		// column; `Entity.serial` is special-cased as the auto-incrementing
		// integer primary key.
		"entitySerial": TColumn(TInt),
		"entityInt":    TArrow{From: TConstraint(), To: TColumn(TInt)},
		"entityText":   TArrow{From: TConstraint(), To: TColumn(TString)},
		"entityBool":   TArrow{From: TConstraint(), To: TColumn(TBool)},
		// Entity.decimal : Int -> Constraint -> Column Decimal
		//
		// Fixed-scale exact decimal. Stored as INTEGER in SQLite — the
		// coefficient at the column's scale (scale 2 → cents), which
		// makes the storage institutionalized cents rather than a
		// lossy REAL. Writes with a finer scale than the column abort
		// instead of silently rounding; coarser scales rescale
		// exactly on the way in.
		"entityDecimal": TArrow{From: TInt, To: TArrow{From: TConstraint(), To: TColumn(TDecimal)}},
		// Entity.enum : List a -> Constraint -> Column a
		//
		// Stored as TEXT in SQLite (the ctor's tag) plus a CHECK
		// constraint listing the accepted tags. The list literal —
		// e.g. `[Member, Admin]` — pins the type variable to the
		// enum's custom type, so misspelling a variant fails at
		// compile time.
		"entityEnum": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(a),
				To:   TArrow{From: TConstraint(), To: TColumn(a)},
			},
		},
		// Constraints. Only `notNull` is exposed today; optional /
		// foreign-key constraints would land once the type checker can
		// express the row-type ⇄ schema correspondence for nullable columns.
		"entityNotNull": TConstraint(),

		// Repo operations. Inputs (filter, patch, create-payload) are fully
		// polymorphic at the type-checker level; the runtime cross-checks at
		// call time that record fields are a subset of the entity's columns
		// with matching types. (Stricter compile-time check would need
		// row-poly subtyping mar's HM doesn't support today.)
		"repoAll": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TEntity(a), To: TTask(TList(a))},
		},
		"repoFindByID": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TEntity(a),
				To:   TArrow{From: TInt, To: TTask(TMaybe(a))},
			},
		},
		"repoFindBy": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TEntity(a),
				To:   TArrow{From: b, To: TTask(TList(a))},
			},
		},
		"repoCreate": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TEntity(a),
				To:   TArrow{From: b, To: TTask(a)},
			},
		},
		"repoUpdate": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TEntity(a),
				To: TArrow{
					From: TInt,
					To:   TArrow{From: b, To: TTask(TMaybe(a))},
				},
			},
		},
		"repoDeleteByID": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TEntity(a),
				To:   TArrow{From: TInt, To: TTask(TUnit{})},
			},
		},

		// Input-kind attrs. Used via the UI namespace (UI.email,
		// UI.password, UI.numeric, UI.oneTimeCode, UI.submit) on
		// `textField` to coordinate with mobile keyboards, browser
		// autofill, and password managers. The underlying builtin
		// names start with `view` for historical reasons; the UI
		// qualified aliases are what user code actually reaches for.
		//
		//   UI.email       — type=email, autocomplete=email, inputmode=email
		//   UI.password    — type=password, autocomplete=current-password
		//   UI.newPassword — type=password, autocomplete=new-password (signup/change)
		//   UI.numeric     — inputmode=numeric (10-key pad on mobile)
		//   UI.oneTimeCode — autocomplete=one-time-code (iOS Code-from-Mail)
		//   UI.numericCode — bundle of `numeric + oneTimeCode` for OTP/2FA
		//   UI.submit      — declarative submit-on-Enter / Return / Done / Go.
		//
		// Without an input-kind, browsers/keychains guess from page
		// context — usually wrong on auth screens, where Safari treats
		// the first un-typed input as a password field.
		// submit : forall msg. msg -> Attr Input
		// Polymorphic in the message (so it composes with any page's Msg);
		// host pinned to Input — only applies to text fields / text
		// areas / pickers.
		"viewSubmit": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: a, To: TAttr(TAttrInputHost())},
		},
		"viewEmail":       TAttr(TAttrInputHost()),
		"viewPassword":    TAttr(TAttrInputHost()),
		"viewNewPassword": TAttr(TAttrInputHost()),
		"viewNumeric":     TAttr(TAttrInputHost()),
		"viewOneTimeCode": TAttr(TAttrInputHost()),

		// chars / lines / fill — sizing values. `chars 6` returns a
		// `Size Width` (≈ 6 character columns at the current font);
		// `lines 5` a `Size Height` (≈ 5 lines at the current
		// line-height); `fill` is polymorphic in the axis ("take the
		// available space on whichever axis the attr names"). The
		// phantom axis keeps `width (lines 5)` / `height (chars 5)`
		// compile errors while one `fill` serves both attrs.
		"uiChars": TArrow{From: TInt, To: TWidth()},
		"uiLines": TArrow{From: TInt, To: THeight()},
		"uiFill": TForall{
			Vars: []int{a.ID},
			Body: TSize(a),
		},

		// width / height : Size axis -> Attr a — the universal sizing
		// attrs. Polymorphic in the host (like `disabled`), so any
		// view's attr list accepts them. What each value means:
		//   - chars n / lines n: content-box sizing (inputs keep their
		//     historical max-width / rows behavior).
		//   - fill: claim the free space on that axis. In an hstack,
		//     `width fill` is the equal-columns workhorse; in a vstack,
		//     `height fill` creates the slack that spacer / centered
		//     distribute. Sizing is "how big"; where a non-filling
		//     child SITS in the cross axis is `align` — a separate,
		//     position-only attr.
		"uiWidth": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TWidth(), To: TAttr(a)},
		},
		"uiHeight": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: THeight(), To: TAttr(a)},
		},

		// align : Alignment -> Attr Stack — cross-axis alignment for a
		// stack's hugging children. vstack: leading / center / trailing
		// (horizontal placement); hstack: top / center / bottom
		// (vertical placement). Wrong-axis values are ignored by the
		// renderer rather than split into two host types. A child with
		// `width fill` has no cross-axis slack, so align doesn't move
		// it — alignment is position, fill is size.
		"uiAlign":    TArrow{From: TAlignment(), To: TAttr(TAttrStackHost())},
		"uiLeading":  TAlignment(),
		"uiCenter":   TAlignment(),
		"uiTrailing": TAlignment(),
		"uiTop":      TAlignment(),
		"uiBottom":   TAlignment(),

		// px : Int -> Pixels — pixel sizing unit for images. Separate
		// from chars/lines so `px` only flows into image attrs and
		// chars/lines only into input attrs (enforced by the type).
		"uiPx": TArrow{From: TInt, To: TPixels()},

		// size : Pixels -> Pixels -> Attr Image — fixed width + height
		// for an image (e.g. a 64x64 avatar). Without it the image fills
		// its container width and keeps its aspect ratio.
		"uiSize": TArrow{From: TPixels(), To: TArrow{From: TPixels(), To: TAttr(TAttrImageHost())}},

		// fit / cover : Attr Image — how the image fills its frame.
		// fit (default) shows the whole image (letterboxed); cover
		// crops to fill the frame. Named after CSS object-fit
		// contain/cover — NOT "fill", which is the universal sizing
		// value (`width fill`); reusing the word for a crop mode
		// would overload it with a second, unrelated meaning.
		"uiFit":   TAttr(TAttrImageHost()),
		"uiCover": TAttr(TAttrImageHost()),

		// ---------- UI module: SwiftUI-style declarative vocabulary ----------
		//
		// Mirrors SwiftUI's container model so iOS gets `NavigationStack
		// { Form { Section { ... } } }` natively (with safe areas, swipe,
		// pull-to-refresh, dark mode, autofill — all of it free), and
		// web gets HTML5 semantic elements + Form-card-style CSS that
		// reads as a "card sections" layout familiar from iOS.
		//
		// The user describes intent ("this is a navigation stack with
		// a form of two sections"); the renderer picks the platform
		// idiom. No pixel decisions in user code.

		// UI.navigationStack : List Attr -> List (View msg) -> View msg
		// Top-level container. iOS: NavigationStack with title bar,
		// safe-area insets, swipe-back. Web: <main> with header bar
		// rendered from `navigationTitle` + `trailing`/`leading` attrs.
		"navigationStack": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrNavStackHost())),
				To:   TArrow{From: TList(TView(a)), To: TView(a)},
			},
		},

		// UI.form : List (View msg) -> View msg
		// Group of sections rendered in form style. iOS: SwiftUI Form
		// (auto-styles children as table-row inputs). Web: <form> with
		// CSS that mimics iOS card-list look. Children are typically
		// `section`s.
		"form": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TList(TView(a)), To: TView(a)},
		},

		// UI.list : List (Attr List) -> List (View msg) -> View msg
		// Vertical list of rows or sections. iOS: SwiftUI List (with
		// dividers, hover, swipe-actions hooks). Web: <ul> with
		// list-style CSS. Use for content (notes, items); use form
		// for input groupings.
		//
		// Reorder + delete semantics live on `keyedList` (children
		// have stable identity), not on `list` itself. `list` is the
		// page-level wrapper that hosts a mix of sections /
		// keyedLists.
		"list": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrListHost())),
				To:   TArrow{From: TList(TView(a)), To: TView(a)},
			},
		},

		// UI.section : List Attr -> List (View msg) -> View msg
		// A logical group inside form/list. Optional `header` /
		// `footer` attrs label the group. iOS: Section { } with
		// header/footer text. Web: <section> with <h2>/<p>.
		"uiSection": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrSectionHost())),
				To:   TArrow{From: TList(TView(a)), To: TView(a)},
			},
		},

		// UI.keyedList : List (Attr KeyedList) -> List (KeyedView msg) -> View msg
		// Section-shaped container for HOMOGENEOUS items with
		// stable identity. Mirrors `section` visually (rounded card,
		// optional header/footer), but its children must be
		// `KeyedView msg` (produced by `UI.keyed`) — not regular
		// Views. This dedicated children type is what makes
		// `onMove` and `onDelete` safe: the reconciler always has a
		// key to match each row across mutations, so deleting row
		// 0 actually removes row 0\'s DOM (not, say, row N\'s DOM
		// with row 1\'s text patched into row 0).
		//
		// Composes with `list` like `section` does — you can mix
		// keyedList and section as siblings inside a `list` for
		// pages that have both editable collections AND static
		// grouped content.
		"uiKeyedList": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrKeyedListHost())),
				To:   TArrow{From: TList(TKeyedView(a)), To: TView(a)},
			},
		},

		// UI.hstack / UI.vstack : List Attr -> List (View msg) -> View msg
		// Free composition. Use when section/form don't fit (e.g. a
		// row of input + button inside a section).
		"hstack": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrStackHost())),
				To:   TArrow{From: TList(TView(a)), To: TView(a)},
			},
		},
		"vstack": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrStackHost())),
				To:   TArrow{From: TList(TView(a)), To: TView(a)},
			},
		},

		// UI.textField : List Attr -> String -> String -> (String -> msg) -> View msg
		// Labeled text input. Args: attrs, placeholder, current value,
		// onChange. iOS: TextField with platform keyboard + autofill.
		// Web: <input> with semantic type. Composes with email /
		// numericCode / submitBy attrs.
		"textField": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrInputHost())),
				To: TArrow{
					From: TString,
					To: TArrow{
						From: TString,
						To: TArrow{
							From: TArrow{From: TString, To: a},
							To:   TView(a),
						},
					},
				},
			},
		},

		// UI.textArea : List Attr -> String -> String -> (String -> msg) -> View msg
		// Multi-line variant of textField for prose-shaped fields
		// (issue description, note body, biography). Same arg
		// shape; the renderer emits a <textarea> instead of an
		// <input>. iOS gets TextEditor. Use textField when the
		// answer fits on one line, textArea when it doesn't.
		"textArea": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrInputHost())),
				To: TArrow{
					From: TString,
					To: TArrow{
						From: TString,
						To: TArrow{
							From: TArrow{From: TString, To: a},
							To:   TView(a),
						},
					},
				},
			},
		},

		// UI.datePicker : List Attr -> Time -> (Time -> msg) -> View msg
		// Date-only field, and PURE: it shows exactly the Time you pass and
		// fires `(Time -> msg)` with the chosen day at local midnight. The
		// program owns the value — there is no renderer-invented default.
		// Seed "today" on the frontend with `Cmd.perform GotToday Time.now`
		// (hold the field as `Maybe Time`, render the picker once seeded).
		// iOS: SwiftUI DatePicker(.date). Web: <input type="date">. Use
		// textField for free text, picker for an enum, datePicker for a date.
		"datePicker": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrInputHost())),
				To: TArrow{
					From: TTime,
					To: TArrow{
						From: TArrow{From: TTime, To: a},
						To:   TView(a),
					},
				},
			},
		},

		// UI.picker : List Attr -> a -> List a -> (a -> String) -> (a -> msg) -> View msg
		// Single-selection field. `a` is the option's value type
		// (typically a custom enum like `IssuePriority`), `m`
		// (the second tvar) is the Msg ctor type. The picker
		// renders the currently-selected option, dispatches the
		// `(a -> msg)` callback when the user picks a different
		// option. Mirrors SwiftUI's Picker(selection: $value):
		// natural fit when the candidate set has more than ~2
		// variants and a column of toggles would dominate the
		// form's vertical space (priority, milestone, assignee,
		// status). Use toggle for boolean / two-state fields.
		"picker": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrInputHost())),
				To: TArrow{
					From: a,
					To: TArrow{
						From: TList(a),
						To: TArrow{
							From: TArrow{From: a, To: TString},
							To: TArrow{
								From: TArrow{From: a, To: b},
								To:   TView(b),
							},
						},
					},
				},
			},
		},

		// UI attrs.

		// navigationTitle : String -> Attr NavStack
		// Sets the navigation bar title (iOS) / page heading (web).
		"navigationTitle": TArrow{From: TString, To: TAttr(TAttrNavStackHost())},

		// topBarTrailing / topBarLeading : forall msg. View msg -> Attr NavStack
		// Add a toolbar item to the top bar of the navigation stack.
		// Names match SwiftUI's `.topBarTrailing` / `.topBarLeading`
		// placement (iOS 17+) — positional, not coupled to the
		// "navigation" semantics, so future top-bar uses (chat
		// headers, custom dashboards) can reuse the same vocabulary.
		"uiTopBarTrailing": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TView(a), To: TAttr(TAttrNavStackHost())},
		},
		"uiTopBarLeading": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TView(a), To: TAttr(TAttrNavStackHost())},
		},

		// header / footer : forall h. String -> Attr h
		// Text label for the top / bottom of a section-shaped container.
		// Honored by `section` and `keyedList` (both render the rounded
		// card with optional header eyebrow + footer caption). Other
		// hosts silently ignore — declared universal so the same attr
		// name works in both contexts without requiring a typeclass.
		// iOS: Section's header/footer slots. Web: <h2>/<small> in the
		// section card chrome.
		"header": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TString, To: TAttr(a)},
		},
		"footer": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TString, To: TAttr(a)},
		},

		// UI.text : List Attr -> String -> View msg
		// Plain text leaf. The attrs list follows the same first-arg
		// convention as every other view (button, textField, hstack);
		// today only the universal layout attrs (width / height) are
		// meaningful here — `text [ width fill ] "..."` claims the
		// row's free space, the workhorse of the equal-columns idiom.
		// No text-specific style attrs exist (per-leaf styling lives
		// on the section / form parent).
		"uiText": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrTextHost())),
				To:   TArrow{From: TString, To: TView(a)},
			},
		},

		// UI.button : List Attr -> msg -> String -> View msg
		// A button that dispatches `msg` on tap. The attrs list lets
		// modifier attrs (like `disabled`) tune the button's behavior
		// without bloating the positional signature.
		"uiButton": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrButtonHost())),
				To: TArrow{
					From: a,
					To:   TArrow{From: TString, To: TView(a)},
				},
			},
		},

		// UI.disabled : forall h. Bool -> Attr h
		// Universal attr — works on any host. Greys out the view and
		// suppresses interaction (dispatch / submit). Inputs, buttons,
		// links, toggles all honor it. Containers ignore it (no
		// interaction to suppress) but still typecheck because the
		// host is polymorphic.
		"uiDisabled": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TBool, To: TAttr(a)},
		},

		// UI.keyed : String -> View msg -> KeyedView msg
		// Wraps a regular View in a stable identity (the String key)
		// so it can be a child of UI.keyedList. The reconciler uses
		// the key to match this row to its previous DOM / SwiftUI
		// node across reorders / deletes / inserts — preserving
		// animation, input focus, and scroll position.
		//
		// The key MUST be a stable identifier of the row's content
		// (record id, unique label, etc.) — NOT the row's position
		// in the list. Index-based keys shift when the list mutates
		// and the reconciler ends up patching content into the wrong
		// DOM nodes (e.g. delete row 0 → row 0\'s DOM stays, gets
		// row 1\'s text; row N\'s DOM gets removed → looks like both
		// row 0 AND row N were deleted).
		//
		// Returns the dedicated KeyedView type — keyedList only
		// accepts these, and `keyed` is the only way to produce one.
		// This makes it impossible to pass a plain `View` into a
		// `keyedList` (compile error) or to forget the key entirely.
		"uiKeyed": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TString,
				To:   TArrow{From: TView(a), To: TKeyedView(a)},
			},
		},

		// UI.onMove : Bool -> (Int -> Int -> msg) -> Attr KeyedList
		// Makes a `keyedList` reorderable. The Bool is "is edit mode
		// currently active" — when False, no drag affordance shows
		// and the callback never fires; when True, rows render a
		// drag handle (web) / become `.onMove`-enabled (iOS).
		//
		// The callback receives `(fromIdx, toIdx)` once the user
		// completes a drag (or keyboard reorder via Space+arrows).
		// The app is responsible for applying the move to its model
		// (typically via `List.move`) and, if persistence is
		// desired, calling whatever Service updates the backend.
		// The framework does NOT touch the model — view is purely a
		// function of the children order.
		//
		// Bundling Bool + callback into a single attr (instead of
		// two separate attrs like `editing` and `onMove`) makes it
		// impossible to declare one without the other — eliminates
		// a class of "edit mode toggled but no handler wired"
		// silent bugs.
		//
		// Host = KeyedList because reorder requires identity. The
		// regular `section` doesn\'t carry keys, so applying onMove
		// to a section is a type error (caught at compile time).
		"uiOnMove": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TBool,
				To: TArrow{
					From: TArrow{From: TInt, To: TArrow{From: TInt, To: a}},
					To:   TAttr(TAttrKeyedListHost()),
				},
			},
		},

		// UI.onDelete : Bool -> (Int -> msg) -> Attr KeyedList
		// Makes a `keyedList`'s rows deletable. The Bool is the
		// "editing mode" flag — when True, every row shows a
		// permanent delete affordance (web: red `−` on the left,
		// iOS: native edit-mode minus button); when False, web
		// reveals the affordance on hover and iOS surfaces swipe-
		// to-delete. The callback receives the row's index and
		// returns the Msg to dispatch.
		//
		// Bundling Bool + callback into one attr (same shape as
		// onMove) ensures both are always declared together —
		// catches "deletion enabled but no handler" at compile
		// time.
		//
		// Host = KeyedList (same as onMove): per-row deletion needs
		// identity to animate the disappearance of the right row.
		"uiOnDelete": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TBool,
				To: TArrow{
					From: TArrow{From: TInt, To: a},
					To:   TAttr(TAttrKeyedListHost()),
				},
			},
		},

		// numericCode : Attr Input
		// Convenience attr combining `numeric` (10-key pad) +
		// `oneTimeCode` (Code-from-Mail / SMS autofill). The common
		// case for OTP / 2FA inputs. iOS: keyboardType .numberPad +
		// textContentType .oneTimeCode. Web: inputmode="numeric" +
		// autocomplete="one-time-code".
		"numericCode": TAttr(TAttrInputHost()),

		// UI.title : String -> View msg
		// Heading text. iOS: Text with .font(.title2.weight(.bold)).
		// Web: <h1> with display-size weight.
		"uiTitle": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TString, To: TView(a)},
		},

		// UI.subtitle : String -> View msg
		// Secondary heading / muted text. iOS: Text with
		// .font(.headline) + .foregroundStyle(.secondary). Web: <h2>
		// in muted gray.
		"uiSubtitle": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TString, To: TView(a)},
		},

		// UI.paragraph : List (Inline msg) -> View msg
		// Flowing block of inline text. Children are inline `text`
		// runs, each carrying its own attrs (bold, italic, code,
		// link, ...). Renders as <p> on web; AttributedString in a
		// Text on iOS. The first primitive that gives Mar a way to
		// mix multiple inline styles (a bold word, an inline code
		// span, a clickable link) inside a single wrapping
		// paragraph of body text.
		"uiParagraph": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TInline(a)),
				To:   TView(a),
			},
		},

		// UI.span : List (Attr Inline) -> String -> Inline msg
		//
		// Inline text run, used ONLY inside `paragraph`. Distinct
		// name from `UI.text` (which is the existing block-level
		// leaf `String -> View msg`) to avoid overloading — Mar
		// binds one name to one type. Mental model: <span> in
		// HTML, AttributedString.Run on iOS.
		//
		// Attrs (bold/italic/strikethrough/code/link) compose
		// freely: `span [bold, link "url"] "label"` gives a bold
		// link, `span [code, italic] "deprecated()"` gives italic
		// code, etc.
		"uiSpan": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrInlineHost())),
				To:   TArrow{From: TString, To: TInline(a)},
			},
		},

		// Inline attrs. bold/italic/strikethrough/code are bare
		// markers; link carries a URL string.
		"inlineBold":          TAttr(TAttrInlineHost()),
		"inlineItalic":        TAttr(TAttrInlineHost()),
		"inlineStrikethrough": TAttr(TAttrInlineHost()),
		"inlineCode":          TAttr(TAttrInlineHost()),
		"inlineLink": TArrow{
			From: TString,
			To:   TAttr(TAttrInlineHost()),
		},

		// UI.errorText : String -> View msg
		// Error message — semantically distinct from `text` so the
		// renderer can style it with destructive intent (red foreground
		// + semi-bold weight). Use for "couldn't reach the server",
		// "invalid code", form-validation feedback, etc — anywhere the
		// user needs to see what went wrong at a glance. iOS: Text with
		// .foregroundStyle(.red).fontWeight(.semibold). Web: <p> with
		// the `.mar-error-text` class.
		"uiErrorText": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TString, To: TView(a)},
		},

		// UI.image : List (Attr Image) -> { src : String, alt : String } -> View msg
		// Displays an image from a URL (remote) or app path (served from
		// dist/ or the project's public/ folder). `alt` is REQUIRED (a
		// record field, not an optional attr) so every image carries a
		// text description — for screen readers and a future text/CLI
		// renderer where alt IS the rendering. An empty alt ("") is the
		// deliberate "decorative, ignore me" escape.
		// Attrs (Image-hosted): `size (px w) (px h)`, `fit`, `fill`.
		//
		// RASTER ONLY: PNG / JPEG / WebP / GIF — the formats all three
		// runtimes decode natively (web <img>, iOS UIImage/AsyncImage,
		// Android). SVG is NOT supported: iOS and Android can't decode it
		// without a third-party library, and "works on every runtime" is
		// a hard rule. Vector art (icons/logos) belongs in a future
		// `icon` primitive mapped to native symbol sets, not in `image`.
		"uiImage": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrImageHost())),
				To: TArrow{
					From: TRecord{
						Fields: map[string]Type{"src": TString, "alt": TString},
						Order:  []string{"src", "alt"},
					},
					To: TView(a),
				},
			},
		},

		// UI.navigationLink : List Attr -> Path r -> r -> View msg -> View msg
		// Tappable navigation to another mar page. Mirror of
		// SwiftUI's `NavigationLink(value:){content}`: the typed
		// Path + record build the destination URL via the same
		// machinery as `linkTo`, and the content View becomes the
		// label — single-line `text` for a plain text link, or a
		// multi-line `vstack` for a list-style row with the
		// chevron auto-centered. The leading attrs list carries
		// `disabled` (and future modifiers) so a link can be
		// greyed-out / inert without removing it from the tree.
		//
		// Refactor-safe: renaming a route's URL pattern is a
		// compile-time error at every `navigationLink` site.
		//
		// Platform mapping:
		//   - iOS: NavigationLink wrapping the child view.
		//   - Web: <a class="mar-navigation-link"> wrapping the
		//     child DOM, with a `›` chevron after the content via
		//     CSS to match the iOS row look.
		//
		// Deliberately not called `link`: "link" connotes a web
		// anchor (open URL, possibly external), whereas
		// `navigationLink` says exactly what it does — push a new
		// page onto the navigation stack. External URLs are not
		// this builtin's concern (they'd use a separate primitive).
		"uiNavigationLink": TForall{
			Vars: []int{-40, a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrLinkHost())),
				To: TArrow{
					From: TPath(TVar{ID: -40}),
					To: TArrow{
						From: TVar{ID: -40},
						To:   TArrow{From: TView(a), To: TView(a)},
					},
				},
			},
		},

		// UI.spacer : View msg
		// Pure SwiftUI primitive — `Spacer()` — that expands to fill
		// the available space along a stack's main axis. The classic
		// "label on the left, action on the right" pattern is
		// `hstack [ text "..." , spacer , button [] ... ]`. On web,
		// renders as a `<div class="mar-spacer">` with `flex: 1`.
		"uiSpacer": TForall{
			Vars: []int{a.ID},
			Body: TView(a),
		},

		// UI.toggle : List Attr -> String -> Bool -> (Bool -> msg) -> View msg
		// Mirror of SwiftUI's `Toggle("Label", isOn: $value)`.
		// Leading attrs list carries `disabled` (and future
		// modifiers); then label, current state, message ctor
		// (same `oldValue -> msg` shape as `textField`). iOS
		// renders the native iOS switch; web uses a CSS-styled
		// checkbox that visually matches.
		"uiToggle": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr(TAttrToggleHost())),
				To: TArrow{
					From: TString,
					To: TArrow{
						From: TBool,
						To: TArrow{
							From: TArrow{From: TBool, To: TVar{ID: a.ID}},
							To:   TView(a),
						},
					},
				},
			},
		},

		// UI.empty : View msg
		// No-op placeholder. Useful in `case` branches that have
		// nothing to render — avoids an `if/else` ladder.
		"uiEmpty": TForall{
			Vars: []int{a.ID},
			Body: TView(a),
		},

		// UI.sheet : { open, onDismiss, outlet } -> List (View msg) -> View msg
		//
		// Modal sheet that slides up from the bottom (iOS-style page sheet).
		// Lives as a view modifier on the parent page — the parent owns
		// open/closed state in its own Model. Mirrors SwiftUI's
		// `.sheet(isPresented:)` modifier API.
		//
		//   open      : Bool         — whether the sheet is currently visible
		//   onDismiss : msg          — dispatched when the user dismisses
		//                              (swipe down, Escape, backdrop click,
		//                              browser back button)
		//   outlet    : String       — identifier for this sheet in the
		//                              navigation state. Required so the
		//                              browser history can capture
		//                              open/close transitions; iOS uses
		//                              it as a routing tag.
		"uiSheet": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{
						"open":      TBool,
						"onDismiss": a,
						"outlet":    TString,
					},
					Order: []string{"open", "onDismiss", "outlet"},
				},
				To: TArrow{
					From: TList(TView(a)),
					To:   TView(a),
				},
			},
		},

		// UI.centered : View msg -> View msg
		// Pure two-axis alignment: fills whatever space its PARENT
		// provides (it never invents a size of its own) and centers
		// the child in it. Sugar for the full-screen states (Loading,
		// Empty, Error) — the decomposed spelling would be a vstack
		// with `height fill` + `align center` + spacers. iOS:
		// frame(maxWidth: .infinity, maxHeight: .infinity, alignment:
		// .center). Web: flex: 1 in the page's height-propagating
		// column. In a parent that hugs (a section card), it simply
		// centers horizontally at content height.
		"uiCentered": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TView(a), To: TView(a)},
		},

		// UI.confirm : { title, confirmLabel, destructive,
		//                onConfirm, onCancel } -> View msg
		//
		// Modal destructive-action confirmation dialog. Renders as
		// a floating overlay with a backdrop — iOS maps to
		// `.confirmationDialog` (the system sheet that pops from
		// the bottom on iPhone, anchored centered on iPad), web
		// renders a centered alert-style dialog with backdrop blur.
		//
		//   title        : String  — primary question, e.g.
		//                            "Delete \"Buy milk\"?"
		//   confirmLabel : String  — label on the destructive button,
		//                            e.g. "Delete"
		//   destructive  : Bool    — True → confirm button is red
		//                            (iOS .destructive role; web red
		//                            tint). False → system accent
		//                            (blue) for benign confirms.
		//   onConfirm    : msg     — dispatched when user taps confirm
		//   onCancel     : msg     — dispatched when user taps cancel,
		//                            OR taps backdrop / swipes down /
		//                            presses Escape (web).
		//
		// Pattern: parent owns a `Maybe Something` in its Model. View
		// returns `UI.confirm {...}` when Just, `UI.empty` when
		// Nothing. The dialog is conceptually a sibling of the
		// underlying page content; both render simultaneously, with
		// the dialog floating on top.
		"uiConfirm": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{
						"title":        TString,
						"confirmLabel": TString,
						"destructive":  TBool,
						"onConfirm":    a,
						"onCancel":     a,
					},
					Order: []string{"title", "confirmLabel", "destructive", "onConfirm", "onCancel"},
				},
				To: TView(a),
			},
		},

		// Page — a single MVU screen bound to a URL path.
		//
		// Page.create takes a record describing the page:
		//
		//   { path   : String                              -- URL pattern (use "/" for single-page)
		//   , title  : String                              -- (optional) browser tab / nav title
		//   , init   : (Model, Effect e Msg)
		//   , update : Msg -> Model -> (Model, Effect e Msg)
		//   , view   : Model -> View Msg
		//   }
		//
		// Row-polymorphic in the trailing fields so optional config like
		// `title` can be omitted without ceremony.
		"pageCreate": TForall{
			Vars: []int{a.ID, b.ID, -8, -10},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{
						"path":          TString,
						"init":          TTuple{Members: []Type{a, TCmd(b)}},
						"update":        TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TCmd(b)}}}},
						"view":          TArrow{From: a, To: TView(b)},
						"subscriptions": TArrow{From: a, To: TSub(b)},
					},
					Order: []string{"path", "init", "update", "view", "subscriptions"},
					Tail:  TVar{ID: -10},
				},
				To: TPage(),
			},
		},

		// Page.protected — like Page.create, but the framework runs
		// Auth.me before mounting. If no session, navigates to the
		// `signInPage` declared in Auth.config. Otherwise threads
		// the User into init/update/view as the first argument, so
		// user code never juggles auth state.
		//
		//   { path     : String
		//   , title    : String                              -- (optional)
		//   , init     : User -> (Model, Effect e Msg)
		//   , update   : User -> Msg -> Model -> (Model, Effect e Msg)
		//   , view     : User -> Model -> View Msg
		//   }
		//
		// `User` is the same row carried by Auth.config, so the page
		// gets typed access to the logged-in user without redeclaring
		// the shape. The redirect destination is *not* per-page —
		// it's centralized in Auth.config so renaming the sign-in
		// page only changes one line.
		"pageProtected": TForall{
			Vars: []int{a.ID, b.ID, -16, -17, -18},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{
						"path":          TString,
						"init":          TArrow{From: TVar{ID: -16}, To: TTuple{Members: []Type{a, TCmd(b)}}},
						"update":        TArrow{From: TVar{ID: -16}, To: TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TCmd(b)}}}}},
						"view":          TArrow{From: TVar{ID: -16}, To: TArrow{From: a, To: TView(b)}},
						"subscriptions": TArrow{From: TVar{ID: -16}, To: TArrow{From: a, To: TSub(b)}},
					},
					Order: []string{"path", "init", "update", "view", "subscriptions"},
					Tail:  TVar{ID: -18},
				},
				To: TPage(),
			},
		},

		// Page.adminProtected — like Page.protected, but gated by the
		// framework's *admin* session (the operators in mar.json["admins"]),
		// not the app's user auth. Threads an opaque AdminSession into
		// init/update/view as the first argument. Because the Mar.Admin.*
		// functions require an AdminSession, they're reachable only from an
		// admin page — a normal page can't call them, caught at compile time.
		//
		//   { path  : String
		//   , title : String                                       -- (optional)
		//   , init  : AdminSession -> (Model, Effect e Msg)
		//   , update: AdminSession -> Msg -> Model -> (Model, Effect e Msg)
		//   , view  : AdminSession -> Model -> View Msg
		//   }
		"pageAdminProtected": TForall{
			Vars: []int{a.ID, b.ID, -26, -27},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{
						"path":          TString,
						"init":          TArrow{From: TAdminSession(), To: TTuple{Members: []Type{a, TCmd(b)}}},
						"update":        TArrow{From: TAdminSession(), To: TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TCmd(b)}}}}},
						"view":          TArrow{From: TAdminSession(), To: TArrow{From: a, To: TView(b)}},
						"subscriptions": TArrow{From: TAdminSession(), To: TArrow{From: a, To: TSub(b)}},
					},
					Order: []string{"path", "init", "update", "view", "subscriptions"},
					Tail:  TVar{ID: -27},
				},
				To: TPage(),
			},
		},

		// The Mar.Admin.* services are shaped EXACTLY like Service.call:
		//
		//   AdminSession -> (Result String resp -> msg) -> Effect String msg
		//
		// The AdminSession argument is the capability gate (see
		// Page.adminProtected) — only an admin page can supply one, so a
		// normal page can't call these (compile error). The trailing toMsg is
		// because the panel performs them as frontend Cmds: a frontend effect
		// delivers its result by dispatching a Msg (it can't return one
		// synchronously), so the result type is threaded through toMsg, never
		// returned directly. msg is the only free variable (the error channel
		// is always String).

		// Mar.Admin.serverInfo : AdminSession -> (Result String ServerInfo -> msg) -> Effect String msg
		"marAdminServerInfo": TForall{
			Vars: []int{-30},
			Body: TArrow{
				From: TAdminSession(),
				To: TArrow{
					From: TArrow{From: TResult(TString, TRecord{
						Fields: map[string]Type{
							"marVersion":       TString,
							"goVersion":        TString,
							"buildTarget":      TString,
							"bootedAtMs":       TInt,
							"requestsTotal":    TInt,
							"requestsInFlight": TInt,
						},
						Order: []string{"marVersion", "goVersion", "buildTarget", "bootedAtMs", "requestsTotal", "requestsInFlight"},
					}), To: TVar{ID: -30}},
					To: TCmd(TVar{ID: -30}),
				},
			},
		},

		// Mar.Admin.dbStats : AdminSession -> (Result String DbStats -> msg) -> Effect String msg
		"marAdminDbStats": TForall{
			Vars: []int{-31},
			Body: TArrow{
				From: TAdminSession(),
				To: TArrow{
					From: TArrow{From: TResult(TString, TRecord{
						Fields: map[string]Type{
							"dbSizeBytes":     TInt,
							"walSizeBytes":    TInt,
							"entities":        TList(TRecord{Fields: map[string]Type{"name": TString, "rowCount": TInt}, Order: []string{"name", "rowCount"}}),
							"frameworkTables": TList(TRecord{Fields: map[string]Type{"name": TString, "rowCount": TInt}, Order: []string{"name", "rowCount"}}),
						},
						Order: []string{"dbSizeBytes", "walSizeBytes", "entities", "frameworkTables"},
					}), To: TVar{ID: -31}},
					To: TCmd(TVar{ID: -31}),
				},
			},
		},

		// Mar.Admin.recentRequests : AdminSession -> (Result String (List Request) -> msg) -> Effect String msg
		"marAdminRecentRequests": TForall{
			Vars: []int{-32},
			Body: TArrow{
				From: TAdminSession(),
				To: TArrow{
					From: TArrow{From: TResult(TString, TList(TRecord{
						Fields: map[string]Type{
							"atMs":       TInt,
							"method":     TString,
							"path":       TString,
							"status":     TInt,
							"durationMs": TInt,
							"userEmail":  TString,
						},
						Order: []string{"atMs", "method", "path", "status", "durationMs", "userEmail"},
					})), To: TVar{ID: -32}},
					To: TCmd(TVar{ID: -32}),
				},
			},
		},

		// Mar.Admin.listEntities : AdminSession -> (Result String (List Entity) -> msg) -> Effect String msg
		// Schema introspection — every table name + its columns.
		"marAdminListEntities": TForall{
			Vars: []int{-33},
			Body: TArrow{
				From: TAdminSession(),
				To: TArrow{
					From: TArrow{From: TResult(TString, TList(TRecord{
						Fields: map[string]Type{"name": TString, "columns": TList(TString)},
						Order:  []string{"name", "columns"},
					})), To: TVar{ID: -33}},
					To: TCmd(TVar{ID: -33}),
				},
			},
		},

		// Mar.Admin.listBackups : AdminSession -> (Result String (List Backup) -> msg) -> Effect String msg
		// Lists the database backup catalog. The panel renders each entry with
		// a plain <a href> to /_mar/admin/api/database-backups/<id> — the
		// download itself is a normal browser download, not a Mar.Admin.* call.
		"marAdminListBackups": TForall{
			Vars: []int{-35},
			Body: TArrow{
				From: TAdminSession(),
				To: TArrow{
					From: TArrow{From: TResult(TString, TList(TRecord{
						Fields: map[string]Type{"id": TString, "sizeBytes": TInt, "createdAtMs": TInt},
						Order:  []string{"id", "sizeBytes", "createdAtMs"},
					})), To: TVar{ID: -35}},
					To: TCmd(TVar{ID: -35}),
				},
			},
		},

		// Mar.Admin.listEntityRows : AdminSession -> String -> (Result String (List (Dict String String)) -> msg) -> Effect String msg
		// Generic row browser for ANY table. Cells are stringified
		// server-side (v1) so a single Dict shape covers every column.
		"marAdminListEntityRows": TForall{
			Vars: []int{-34},
			Body: TArrow{
				From: TAdminSession(),
				To: TArrow{
					From: TString,
					To: TArrow{
						From: TArrow{From: TResult(TString, TList(TDict(TString, TString))), To: TVar{ID: -34}},
						To:   TCmd(TVar{ID: -34}),
					},
				},
			},
		},

		// Page.dynamic — pattern path with typed `{name:Type}` params.
		// The runtime matches the URL against the pattern and threads
		// a Params record through init/update/view. The pattern's
		// param names + types become the record's fields exactly:
		// pattern → record is one-to-one, no row variable.
		//
		//   path = "/notes/{id:Int}"           →  params : { id : Int }
		//   path = "/teams/{t:Int}/users/{u:String}" →  params : { t : Int, u : String }
		//
		// `path` is a `Path r` value — produced from a String literal
		// at compile time (the typechecker parses the pattern and
		// synthesizes `r`). The same `r` flows into the handlers.
		// Bare `:id` (Express-style) is rejected; `{id}` without a
		// type is rejected.
		"pageDynamic": TForall{
			Vars: []int{a.ID, b.ID, -19, -20, -21},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{
						"path":          TPath(TVar{ID: -19}),
						"init":          TArrow{From: TVar{ID: -19}, To: TTuple{Members: []Type{a, TCmd(b)}}},
						"update":        TArrow{From: TVar{ID: -19}, To: TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TCmd(b)}}}}},
						"view":          TArrow{From: TVar{ID: -19}, To: TArrow{From: a, To: TView(b)}},
						"subscriptions": TArrow{From: TVar{ID: -19}, To: TArrow{From: a, To: TSub(b)}},
					},
					Order: []string{"path", "init", "update", "view", "subscriptions"},
					Tail:  TVar{ID: -21},
				},
				To: TPage(),
			},
		},

		// Page.dynamicProtected — like Page.dynamic but auth-gated.
		// init/update/view receive User AND Params, in that order
		// (mirrors Page.protected which puts User first).
		"pageDynamicProtected": TForall{
			Vars: []int{a.ID, b.ID, -22, -23, -24, -25},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{
						"path":          TPath(TVar{ID: -22}),
						"init":          TArrow{From: TVar{ID: -23}, To: TArrow{From: TVar{ID: -22}, To: TTuple{Members: []Type{a, TCmd(b)}}}},
						"update":        TArrow{From: TVar{ID: -23}, To: TArrow{From: TVar{ID: -22}, To: TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TCmd(b)}}}}}},
						"view":          TArrow{From: TVar{ID: -23}, To: TArrow{From: TVar{ID: -22}, To: TArrow{From: a, To: TView(b)}}},
						"subscriptions": TArrow{From: TVar{ID: -23}, To: TArrow{From: TVar{ID: -22}, To: TArrow{From: a, To: TSub(b)}}},
					},
					Order: []string{"path", "init", "update", "view", "subscriptions"},
					Tail:  TVar{ID: -25},
				},
				To: TPage(),
			},
		},

		// Admin sign-in flow. Unlike the introspection functions above, these
		// do NOT take an AdminSession — they're the bootstrap that PRODUCES the
		// session (login can't require what it mints). They POST to the
		// existing /_mar/admin/auth/* endpoints. Shaped like the user Auth.*.

		// Mar.Admin.requestCode : { email : String } -> (Result String () -> msg) -> Effect e msg
		"marAdminRequestCode": TForall{
			Vars: []int{-50, -51},
			Body: TArrow{
				From: TRecord{Fields: map[string]Type{"email": TString}, Order: []string{"email"}},
				To: TArrow{
					From: TArrow{From: TResult(TString, TUnit{}), To: TVar{ID: -51}},
					To:   TCmd(TVar{ID: -51}),
				},
			},
		},

		// Mar.Admin.verifyCode : { email, code } -> (Result String { email : String } -> msg) -> Effect e msg
		// On success the server sets the admin session cookie.
		"marAdminVerifyCode": TForall{
			Vars: []int{-52, -53},
			Body: TArrow{
				From: TRecord{Fields: map[string]Type{"email": TString, "code": TString}, Order: []string{"email", "code"}},
				To: TArrow{
					From: TArrow{From: TResult(TString, TRecord{Fields: map[string]Type{"email": TString}, Order: []string{"email"}}), To: TVar{ID: -53}},
					To:   TCmd(TVar{ID: -53}),
				},
			},
		},

		// Mar.Admin.signOut : (Result String () -> msg) -> Effect e msg
		"marAdminSignOut": TForall{
			Vars: []int{-54, -55},
			Body: TArrow{
				From: TArrow{From: TResult(TString, TUnit{}), To: TVar{ID: -55}},
				To:   TCmd(TVar{ID: -55}),
			},
		},

		// Page.dynamicAdminProtected — like Page.dynamicProtected, but gated
		// by the framework admin session (AdminSession) instead of the app's
		// User. Threads AdminSession + Params (in that order) into
		// init/update/view. Powers the admin panel's per-table drill-down
		// sub-screen (path "/_mar/admin/mar/table/{name:String}").
		"pageDynamicAdminProtected": TForall{
			Vars: []int{a.ID, b.ID, -40, -41, -42},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{
						"path":          TPath(TVar{ID: -40}),
						"init":          TArrow{From: TAdminSession(), To: TArrow{From: TVar{ID: -40}, To: TTuple{Members: []Type{a, TCmd(b)}}}},
						"update":        TArrow{From: TAdminSession(), To: TArrow{From: TVar{ID: -40}, To: TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TCmd(b)}}}}}},
						"view":          TArrow{From: TAdminSession(), To: TArrow{From: TVar{ID: -40}, To: TArrow{From: a, To: TView(b)}}},
						"subscriptions": TArrow{From: TAdminSession(), To: TArrow{From: TVar{ID: -40}, To: TArrow{From: a, To: TSub(b)}}},
					},
					Order: []string{"path", "init", "update", "view", "subscriptions"},
					Tail:  TVar{ID: -42},
				},
				To: TPage(),
			},
		},

		// Page.sheet : Page -> Page
		//
		// Marks a page as PRESENTED rather than pushed: navigating to it
		// keeps the page you came from on screen and lays this one over
		// it in a sheet. The route is unchanged — same path, same history
		// entry, same deep link — only the presentation differs. Back,
		// Escape, and a tap outside dismiss it.
		//
		//   page = Page.sheet (Page.dynamicProtected { path = ..., ... })
		//
		// A decorator instead of six more constructors: `Page.sheet` wraps
		// any of create / protected / dynamic / dynamicProtected without
		// multiplying their names, and reads as what it is — a
		// presentation choice layered on a page that already exists.
		//
		// Use it for a bounded task the user finishes or abandons (take
		// attendance, compose, edit). Keep the push for places the user
		// browses INTO, where going deeper is the point.
		//
		// Opened cold (deep link, reload, shared URL) there is no page
		// underneath to lay it over, so it renders as an ordinary
		// full-screen page. That is the graceful shape: the URL always
		// resolves to something whole.
		"pageSheet": TForall{
			Vars: []int{},
			Body: TArrow{From: TPage(), To: TPage()},
		},

		// App.shared / Page.withShared / Cmd.toShared — one app-wide client
		// model that outlives navigation.
		//
		// Page models live on the nav stack and die on every forward
		// navigation (ADR 0009). That is deliberate, and it leaves one
		// question open: where does state that must survive the trip live?
		// Here. The profile fetched once at boot, the theme, the unread
		// count — anything twenty screens would otherwise each refetch in
		// their own init.
		//
		//   App.shared :
		//       { init          : (model, Cmd msg)
		//       , update        : msg -> model -> (model, Cmd msg)
		//       , subscriptions : model -> Sub msg
		//       }
		//       -> App.Shared model msg
		//
		//   Page.withShared : App.Shared model msg -> (model -> Page) -> Page
		//   Cmd.toShared    : App.Shared model msg -> msg -> Cmd pageMsg
		//
		// The def value is the whole type story. It is an ordinary top-level
		// binding in an ordinary app module, and every use site names THAT
		// binding, so a page reading the model and a page sending a message
		// cannot disagree about either type — there is nothing to keep in
		// sync. Same shape as Auth.config, Entity.define and Service.declare:
		// a capability you hold, not a module the framework looks up by name.
		//
		// `Cmd.toShared` returns `Cmd pageMsg` for any pageMsg: sending to
		// the shared update tells the PAGE nothing, so it composes into
		// whatever the page's own update was already returning.
		//
		// There is no registration step and no `shared` field on the App
		// config. The runtime discovers the def through the pages that use
		// it, which is what keeps this feature invisible to apps that don't
		// want it: no vestigial argument, no changed signature, no migration.
		"appShared": TForall{
			Vars: []int{-103, -104},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{
						"init": TTuple{Members: []Type{TVar{ID: -103}, TCmd(TVar{ID: -104})}},
						"update": TArrow{
							From: TVar{ID: -104},
							To: TArrow{
								From: TVar{ID: -103},
								To:   TTuple{Members: []Type{TVar{ID: -103}, TCmd(TVar{ID: -104})}},
							},
						},
						"subscriptions": TArrow{From: TVar{ID: -103}, To: TSub(TVar{ID: -104})},
					},
					Order: []string{"init", "update", "subscriptions"},
				},
				To: TShared(TVar{ID: -103}, TVar{ID: -104}),
			},
		},

		// Page.withShared wraps ANY of the six page constructors rather than
		// adding a seventh and eighth flavor of each. The builder receives
		// the model and returns a Page, so the shared value reaches
		// init/update/view by ordinary partial application — `view global`
		// already has the type Page.create wants.
		//
		// The builder is re-applied whenever the shared model changes, so a
		// page renders the LIVE value, never a snapshot taken at its init.
		"pageWithShared": TForall{
			Vars: []int{-105, -106},
			Body: TArrow{
				From: TShared(TVar{ID: -105}, TVar{ID: -106}),
				To: TArrow{
					From: TArrow{From: TVar{ID: -105}, To: TPage()},
					To:   TPage(),
				},
			},
		},

		// Cmd.toShared is how a page WRITES: it sends a message, it does not
		// assign. The shared module owns its update and its exhaustive case,
		// exactly like a page owns its own.
		//
		// A message issued by page A that resolves after navigating to page B
		// still applies — shared is page-independent, so unlike a page msg it
		// has no stale-foreign-message problem and the dispatch boundary does
		// not drop it.
		"cmdToShared": TForall{
			Vars: []int{-107, -108, -109},
			Body: TArrow{
				From: TShared(TVar{ID: -107}, TVar{ID: -108}),
				To: TArrow{
					From: TVar{ID: -108},
					To:   TCmd(TVar{ID: -109}),
				},
			},
		},

		// Nav.push : String -> Effect e msg
		// Pushes a URL onto the browser history and re-renders the
		// matching Page. For dynamic pages prefer Nav.pushTo, which
		// builds the URL from a typed Path + record so refactors of
		// the path pattern catch all callers in compile time.
		"navPush": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: TString, To: TCmd(b)},
		},

		// Nav.dismiss : Effect e msg
		//
		// Closes a route that is being PRESENTED (see Page.sheet): the
		// verb a sheet's own Cancel / Done button needs, matching what
		// the backdrop, Escape and Back already do.
		//
		// With nothing presented it steps back one entry inside the app,
		// and at the app's first entry it does nothing — so a sheet route
		// opened cold (full-screen, nothing behind it) cannot walk the
		// reader out of the site.
		//
		// Deliberately not a general `Nav.back`: it goes one step, never
		// N, and never past where the app began.
		"navDismiss": TForall{
			Vars: []int{b.ID},
			Body: TCmd(b),
		},

		// Nav.replace : String -> Effect e msg
		// Like Nav.push but replaces the current history entry — the
		// back button won't return to the previous URL. Right for
		// post-login / post-logout flows.
		"navReplace": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: TString, To: TCmd(b)},
		},

		// Auth.completeSignIn : Effect e msg
		// Use as the navigation step after Auth.verifyCode succeeds.
		// Reads the framework-managed `next` slot — set when a 401
		// from a Service.call sent the user here, or when a deep link
		// landed on the sign-in page — and goes there. Falls back to
		// "/" when no return target was captured. Web validates that
		// the captured path is same-origin to prevent open-redirect
		// abuse via crafted ?next= parameters.
		//
		// Lives under Auth.* (not Nav.*) because it bundles auth-
		// specific cleanup (resetting the auth-expired redirect
		// coalescer) with the navigation step. Nav stays focused on
		// pure navigation; Auth owns the post-login transition.
		"authCompleteSignIn": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TCmd(b),
		},

		// Nav.pushTo : Path r -> r -> Effect e msg
		// Type-safe alternative to Nav.push for dynamic pages. The
		// record `r` carries exactly the params declared by the path
		// pattern, so refactoring `"/notes/{id:Int}"` into
		// `"/notes/{slug:String}"` flips every Nav.pushTo call into
		// a compile error pointing at the wrong field name/type.
		"navPushTo": TForall{
			Vars: []int{-30, a.ID, b.ID},
			Body: TArrow{
				From: TPath(TVar{ID: -30}),
				To:   TArrow{From: TVar{ID: -30}, To: TCmd(b)},
			},
		},

		// Nav.replaceTo : Path r -> r -> Effect e msg
		// Same as Nav.pushTo but uses history.replaceState — for
		// post-login / post-logout flows where the previous URL
		// shouldn't be reachable via "back".
		"navReplaceTo": TForall{
			Vars: []int{-31, a.ID, b.ID},
			Body: TArrow{
				From: TPath(TVar{ID: -31}),
				To:   TArrow{From: TVar{ID: -31}, To: TCmd(b)},
			},
		},

		// linkTo : Path r -> r -> String
		// Build a URL string from a typed Path + the params record.
		// Pure (no Effect) — meant for `href` attributes on anchor
		// tags. Compile-time fails if the record is missing fields,
		// has extras, or has the wrong types.
		"linkTo": TForall{
			Vars: []int{-32},
			Body: TArrow{
				From: TPath(TVar{ID: -32}),
				To:   TArrow{From: TVar{ID: -32}, To: TString},
			},
		},

		// App.frontend : List Page -> Effect String ()
		// Pure frontend: ships an MVU app (one or many pages) to the browser.
		// Port comes from <projectDir>/mar.json (server.port, default 3000).
		"appFrontend": TArrow{From: TList(TPage()), To: TCmd(TUnit{})},

		// App.backend : { services } -> Effect String ()
		// Headless API server: `services` exposes typed RPC services, each
		// mounted at the verb and path it was declared with. Port from
		// mar.json (server.port, default 3000).
		"appBackend": TArrow{
			From: TRecord{
				Fields: map[string]Type{
					"services": TList(TExposedService()),
				},
				Order: []string{"services"},
			},
			To: TCmd(TUnit{}),
		},

		// App.fullstack : { services, pages } -> Effect String ()
		// Unified server. `services` mounts typed RPC services at the verb
		// and path each was declared with; `pages` ships the browser MVU
		// app. Port from mar.json.
		"appFullstack": TArrow{
			From: TRecord{
				Fields: map[string]Type{
					"services": TList(TExposedService()),
					"pages":    TList(TPage()),
				},
				Order: []string{"services", "pages"},
			},
			To: TCmd(TUnit{}),
		},

		// Service.declare : Method -> String -> Service req resp
		//
		// A typed RPC contract: an HTTP verb and a URL path, with no
		// handler attached. Bound at the top level in the shared module
		// so the frontend can pass it to Service.call; the backend pairs
		// it with a handler via Service.implement (or Auth.protect).
		//
		//   getNote : Service { id : Int } (Maybe Note)
		//   getNote = Service.declare GET "/notes/{id:Int}"
		//
		// The path may carry typed `{name:Type}` params, which must name
		// fields of req. GET handlers are held to read-only by the
		// compiler; the verb also drives where req travels on the wire
		// (path params in the URL, the rest in the query for GET/DELETE
		// or the JSON body for POST/PUT/PATCH).
		"serviceDeclare": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: TMethod, To: TArrow{From: TString, To: TService(a, b)}},
		},

		// Service.implement : Service req resp -> (req -> Effect String resp) -> ExposedService
		//
		// Pairs a contract with its handler, returning an
		// already-exposed value for the services list. Reads
		// contract-first, handler-second so the call site reads as a
		// sentence:
		//
		//   Service.implement Shared.foo handler
		//
		// The mounted URL comes from the contract's binding identity,
		// not the implementation's.
		"serviceImplement": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TService(a, b),
				To: TArrow{
					From: TArrow{From: a, To: TTask(b)},
					To:   TExposedService(),
				},
			},
		},

		// Service.call : Service req resp -> req -> (Result String resp -> msg) -> Effect e msg
		// Client-side: encodes req as JSON, fetches, decodes resp.
		// Returns Effect that dispatches msg with Ok resp / Err message.
		// Service.call : Service req resp -> req -> (Result Service.Error resp -> msg) -> Effect msg
		// The toMsg receives a Result whose Err is a Service.Error union
		// (Offline / Unauthorized / ServerError String), not a bare String:
		// transport failure is a value you case on, the Elm way.
		"serviceCall": TForall{
			Vars: []int{a.ID, b.ID, -12},
			Body: TArrow{
				From: TService(a, b),
				To: TArrow{
					From: a,
					To: TArrow{
						From: TArrow{From: TResult(TServiceError, b), To: TVar{ID: -12}},
						To:   TCmd(TVar{ID: -12}),
					},
				},
			},
		},
		// Service.errorToString : Service.Error -> String
		// Convenience for the common "just show it" case. Apps that need
		// to act differently per case (retry on Offline, redirect on
		// Unauthorized) match the union directly instead.
		"serviceErrorToString": TForall{
			Body: TArrow{From: TServiceError, To: TString},
		},

		// --- Auth ---
		//
		// Auth.config : { entity, identify, email, signup, sessionDuration } -> Auth user
		//
		// The record's `entity` field carries the user entity; the runtime
		// also reads `identify`, `email`, `signup`, `sessionDuration`. Type
		// row is intentionally permissive — every field is opaque to the
		// type checker and we don't reject extra fields, since the runtime
		// only inspects the known names.
		"authConfig": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TVar{ID: a.ID},
				To:   TAuth(TVar{ID: b.ID}),
			},
		},

		// Auth.protect : Service req resp -> (req -> user -> Effect String resp) -> ExposedService
		//
		// Auth analog of Service.implement. Reads contract-first,
		// handler-second so the call site reads as a sentence:
		//
		//   Auth.protect Shared.listMine listMine
		//
		// Returns an ExposedService whose dispatch path loads the
		// current user from the session before calling the handler.
		"authProtect": TForall{
			Vars: []int{a.ID, b.ID, -13},
			Body: TArrow{
				From: TService(a, b),
				To: TArrow{
					From: TArrow{
						From: a,
						To: TArrow{
							From: TVar{ID: -13},
							To:   TTask(b),
						},
					},
					To: TExposedService(),
				},
			},
		},

		// Auth.requireRole : role -> ExposedService -> ExposedService
		//
		// See docs/authorization-proposal.md. Decorator that adds an RBAC
		// gate. The role argument's type unifies with whatever
		// `Auth.config.role` returns, so misspelled enum values fail at
		// compile time.
		//
		// Enforcement is live: the runtime decorator marks the service
		// RequiresUser=true and the dispatcher (ExposedServiceToRoute)
		// checks the role before invoking the handler. Applying this to a
		// raw `Service.implement` (not an `Auth.protect`ed one) now still
		// requires auth — the decorator sets RequiresUser itself, so a
		// role-gated service can never serve public.
		"authRequireRole": TForall{
			Vars: []int{-30},
			Body: TArrow{
				From: TVar{ID: -30},
				To: TArrow{
					From: TExposedService(),
					To:   TExposedService(),
				},
			},
		},

		// Auth.authorize :
		//   (input -> User -> Effect String (Maybe resource))
		//   -> (input -> User -> resource -> Bool)
		//   -> ExposedService
		//   -> ExposedService
		//
		// ABAC decorator. Loads the resource (Nothing → 404), runs the
		// policy; rejects 403 on False. Enforcement is live and the
		// decorator marks the service RequiresUser=true (a policy gate
		// implies an authenticated user), so it can never serve public.
		"authAuthorize": TForall{
			Vars: []int{-31, -32, -33},
			Body: TArrow{
				// loader: input -> User -> Effect String (Maybe resource)
				From: TArrow{
					From: TVar{ID: -31},
					To: TArrow{
						From: TVar{ID: -32},
						To:   TTask(TMaybe(TVar{ID: -33})),
					},
				},
				To: TArrow{
					// policy: input -> User -> resource -> Bool
					From: TArrow{
						From: TVar{ID: -31},
						To: TArrow{
							From: TVar{ID: -32},
							To:   TArrow{From: TVar{ID: -33}, To: TBool},
						},
					},
					To: TArrow{
						From: TExposedService(),
						To:   TExposedService(),
					},
				},
			},
		},

		// Auth.requireOwner :
		//   (input -> User -> Effect String (Maybe resource))
		//   -> (resource -> Int)
		//   -> ExposedService
		//   -> ExposedService
		//
		// Sugar for the common ABAC case "this resource has an ownerId
		// field that must equal user.id". Enforcement is live and the
		// decorator marks the service RequiresUser=true, so it can never
		// serve public.
		"authRequireOwner": TForall{
			Vars: []int{-34, -35, -36},
			Body: TArrow{
				From: TArrow{
					From: TVar{ID: -34},
					To: TArrow{
						From: TVar{ID: -35},
						To:   TTask(TMaybe(TVar{ID: -36})),
					},
				},
				To: TArrow{
					From: TArrow{From: TVar{ID: -36}, To: TInt},
					To: TArrow{
						From: TExposedService(),
						To:   TExposedService(),
					},
				},
			},
		},

		// Auth.requestCode : { email : String } -> (Result String () -> msg) -> Effect e msg
		// Auth.requestCode : { email } -> (Result Service.Error Auth.RequestOutcome -> msg) -> Effect msg
		// Domain outcomes (CodeSent / InvalidEmail / RateLimited) ride in
		// the Ok; the Err stays pure transport. CodeSent never reveals
		// whether the email has an account.
		"authRequestCode": TForall{
			Vars: []int{-15},
			Body: TArrow{
				From: TRecord{Fields: map[string]Type{"email": TString}, Order: []string{"email"}},
				To: TArrow{
					From: TArrow{From: TResult(TServiceError, TAuthRequestOutcome), To: TVar{ID: -15}},
					To:   TCmd(TVar{ID: -15}),
				},
			},
		},

		// Auth.verifyCode : { email, code } -> (Result Service.Error (Auth.VerifyOutcome user) -> msg) -> Effect msg
		// SignedIn carries the app's own user record; WrongCode /
		// TooManyAttempts are the only other outcomes this endpoint
		// produces.
		"authVerifyCode": TForall{
			Vars: []int{a.ID, -17},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{"email": TString, "code": TString},
					Order:  []string{"email", "code"},
				},
				To: TArrow{
					From: TArrow{From: TResult(TServiceError, TAuthVerifyOutcome(a)), To: TVar{ID: -17}},
					To:   TCmd(TVar{ID: -17}),
				},
			},
		},

		// Auth.logout : (Result String () -> msg) -> Effect e msg
		"authLogout": TForall{
			Vars: []int{-18, -19},
			Body: TArrow{
				From: TArrow{From: TResult(TString, TUnit{}), To: TVar{ID: -19}},
				To:   TCmd(TVar{ID: -19}),
			},
		},

		// Auth.me : (Result String (Maybe user) -> msg) -> Effect e msg
		"authMe": TForall{
			Vars: []int{a.ID, -20, -21},
			Body: TArrow{
				From: TArrow{From: TResult(TString, TMaybe(a)), To: TVar{ID: -21}},
				To:   TCmd(TVar{ID: -21}),
			},
		},
	}
}

// qualifiedAliases returns Module.name aliases for stdlib (so `List.map`
// works just like `listMap`).
func qualifiedAliases(flat map[string]Type) map[string]Type {
	mapping := map[string]string{
		"List.length":       "listLength",
		"List.map":          "listMap",
		"List.filter":       "listFilter",
		"List.foldl":        "listFoldl",
		"List.foldr":        "listFoldr",
		"List.sum":          "listSum",
		"List.product":      "listProduct",
		"List.range":        "listRange",
		"List.reverse":      "listReverse",
		"List.head":         "listHead",
		"List.tail":         "listTail",
		"List.isEmpty":      "listIsEmpty",
		"List.concat":       "listConcat",
		"List.take":         "listTake",
		"List.drop":         "listDrop",
		"List.move":         "listMove",
		"List.member":       "listMember",
		"List.any":          "listAny",
		"List.all":          "listAll",
		"List.indexedMap":   "listIndexedMap",
		"List.repeat":       "listRepeat",
		"List.intersperse":  "listIntersperse",
		"List.partition":    "listPartition",
		"List.concatMap":    "listConcatMap",
		"List.filterMap":    "listFilterMap",
		"List.maximum":      "listMaximum",
		"List.minimum":      "listMinimum",
		"List.sort":         "listSort",
		"List.sortBy":       "listSortBy",
		"List.sortWith":     "listSortWith",
		"String.length":     "stringLength",
		"String.contains":   "stringContains",
		"String.startsWith": "stringStartsWith",
		"String.endsWith":   "stringEndsWith",
		"String.fromInt":    "stringFromInt",

		"Decimal.fromInt":       "decimalFromInt",
		"Decimal.fromCents":     "decimalFromCents",
		"Decimal.toCents":       "decimalToCents",
		"Decimal.truncate":      "decimalTruncate",
		"Decimal.round":         "decimalRound",
		"Decimal.floor":         "decimalFloor",
		"Decimal.ceiling":       "decimalCeiling",
		"Decimal.toIntWith":     "decimalToIntWith",
		"Decimal.toScale":       "decimalToScale",
		"Decimal.abs":           "decimalAbs",
		"Decimal.negate":        "decimalNegate",
		"Decimal.compare":       "decimalCompare",
		"Decimal.zero":          "decimalZero",
		"Decimal.fromString":    "decimalFromString",
		"Decimal.toString":      "decimalToString",
		"Decimal.rounded":       "decimalRounded",
		"Decimal.withRemainder": "decimalWithRemainder",
		"String.toInt":          "stringToInt",
		"String.toUpper":        "stringToUpper",
		"String.toLower":        "stringToLower",
		"String.replace":        "stringReplace",
		"String.repeat":         "stringRepeat",
		"String.padLeft":        "stringPadLeft",
		"String.padRight":       "stringPadRight",
		"String.indexes":        "stringIndexes",
		"String.toList":         "stringToList",
		"String.fromList":       "stringFromList",
		"String.cons":           "stringCons",
		"String.map":            "stringMap",
		"String.filter":         "stringFilter",
		"String.foldl":          "stringFoldl",
		"String.any":            "stringAny",
		// Char
		"Char.toCode":        "charToCode",
		"Char.fromCode":      "charFromCode",
		"Char.isDigit":       "charIsDigit",
		"Char.isAlpha":       "charIsAlpha",
		"Char.isUpper":       "charIsUpper",
		"Char.isLower":       "charIsLower",
		"Char.toUpper":       "charToUpper",
		"Char.toLower":       "charToLower",
		"Maybe.withDefault":  "maybeWithDefault",
		"Maybe.map":          "maybeMap",
		"Maybe.andThen":      "maybeAndThen",
		"Maybe.map2":         "maybeMap2",
		"Maybe.map3":         "maybeMap3",
		"Maybe.andMap":       "maybeAndMap",
		"Maybe.filter":       "maybeFilter",
		"Result.map":         "resultMap",
		"Result.andThen":     "resultAndThen",
		"Result.mapError":    "resultMapError",
		"Result.withDefault": "resultWithDefault",
		"Result.fromMaybe":   "resultFromMaybe",
		"Result.toMaybe":     "resultToMaybe",
		"Tuple.first":        "tupleFirst",
		"Tuple.second":       "tupleSecond",
		"Tuple.pair":         "tuplePair",
		"Tuple.mapFirst":     "tupleMapFirst",
		"Tuple.mapSecond":    "tupleMapSecond",
		"Tuple.mapBoth":      "tupleMapBoth",
		"String.split":       "stringSplit",
		"String.join":        "stringJoin",
		"String.trim":        "stringTrim",
		"Task.succeed":       "effectSucceed",
		"Task.fail":          "effectFail",
		"Task.map":           "effectMap",
		"Task.andThen":       "effectAndThen",
		"Task.forEach":       "effectForEach",
		"Task.sequence":      "effectSequence",
		"Cmd.batch":          "effectBatch",
		"Cmd.none":           "effectNone",
		"Cmd.perform":        "cmdPerform",
		"Cmd.toShared":       "cmdToShared",
		"Sub.batch":          "subBatch",
		"Sub.none":           "subNone",
		"Random.generate":    "randomGenerate",
		"Random.int":         "randomInt",
		"Random.uniform":     "randomUniform",
		"Random.constant":    "randomConstant",
		"Random.list":        "randomList",
		"Random.pair":        "randomPair",
		"Random.map":         "randomMap",
		"Random.map2":        "randomMap2",
		"Random.map3":        "randomMap3",
		"Random.andThen":     "randomAndThen",
		"Random.initialSeed": "randomInitialSeed",
		"Random.step":        "randomStep",
		"Random.seed":        "randomSeed",
		"Time.millis":        "timeMillis",
		"Time.seconds":       "timeSeconds",
		"Time.minutes":       "timeMinutes",
		"Time.hours":         "timeHours",
		"Time.days":          "timeDays",
		"Time.weeks":         "timeWeeks",
		"Time.toSeconds":     "timeToSeconds",
		"Time.now":           "timeNow",
		"Time.every":         "timeEvery",
		"Keyboard.watch":     "keyboardWatch",
		"Gamepad.watch":      "gamepadWatch",
		"Device.watch":       "deviceWatch",
		"Device.touchOnly":   "deviceTouchOnly",
		"Device.canHover":    "deviceCanHover",
		"Sound.tone":         "soundTone",
		"Sound.volume":       "soundVolume",
		"Sound.sweep":        "soundSweep",
		"Sound.lowCut":       "soundLowCut",
		"Sound.highCut":      "soundHighCut",
		"Sound.holdPitch":    "soundHoldPitch",
		"Sound.attack":       "soundAttack",
		"Sound.release":      "soundRelease",
		"Sound.duty":         "soundDuty",
		"Sound.vibrato":      "soundVibrato",
		"Sound.arp":          "soundArp",
		"Sound.rest":         "soundRest",
		"Sound.chord":        "soundChord",
		"Sound.sequence":     "soundSequence",
		"Sound.play":         "soundPlay",
		"Sound.loop":         "soundLoop",
		"Sound.voice":        "soundVoice",
		"Sound.glide":        "soundGlide",
		"Sound.once":         "soundOnce",
		"Sound.setMuted":     "soundSetMuted",
		"Sound.master":       "soundMaster",
		"Sound.c":            "soundPitch_c",
		"Sound.cs":           "soundPitch_cs",
		"Sound.d":            "soundPitch_d",
		"Sound.ds":           "soundPitch_ds",
		"Sound.e":            "soundPitch_e",
		"Sound.f":            "soundPitch_f",
		"Sound.fs":           "soundPitch_fs",
		"Sound.g":            "soundPitch_g",
		"Sound.gs":           "soundPitch_gs",
		"Sound.a":            "soundPitch_a",
		"Sound.as_":          "soundPitch_as_",
		"Sound.b":            "soundPitch_b",
		"Time.add":           "timeAdd",
		"Time.sub":           "timeSub",
		"Time.diff":          "timeDiff",
		"Time.before":        "timeBefore",
		"Time.after":         "timeAfter",
		"Time.toIso":         "timeToIso",
		"Time.fromIso":       "timeFromIso",
		"Time.toMillis":      "timeToMillis",
		"Time.fromYMD":       "timeFromYMD",
		"Time.addDays":       "timeAddDays",
		"Time.addMonths":     "timeAddMonths",
		"Time.addYears":      "timeAddYears",
		"Time.year":          "timeYear",
		"Time.month":         "timeMonth",
		"Time.day":           "timeDay",
		"Time.hour":          "timeHour",
		"Time.minute":        "timeMinute",
		"Time.second":        "timeSecond",
		"Http.get":           "httpGet",
		"Http.post":          "httpPost",
		"JSON.encode":        "jsonEncode",
		"JSON.decode":        "jsonDecode",
		// Entity (record-literal form)
		"Entity.define":    "entityDefine",
		"Entity.serial":    "entitySerial",
		"Entity.int":       "entityInt",
		"Entity.text":      "entityText",
		"Entity.bool":      "entityBool",
		"Entity.decimal":   "entityDecimal",
		"Entity.enum":      "entityEnum",
		"Entity.timestamp": "entityTimestamp",
		"Entity.notNull":   "entityNotNull",
		// Repo
		"Repo.all":        "repoAll",
		"Repo.findById":   "repoFindByID",
		"Repo.findBy":     "repoFindBy",
		"Repo.create":     "repoCreate",
		"Repo.update":     "repoUpdate",
		"Repo.deleteById": "repoDeleteByID",
		// UI module: SwiftUI-style declarative vocabulary.
		"UI.navigationStack": "navigationStack",
		"UI.form":            "form",
		"UI.list":            "list",
		"UI.section":         "uiSection",
		"UI.keyedList":       "uiKeyedList",
		"UI.hstack":          "hstack",
		"UI.vstack":          "vstack",
		"UI.textField":       "textField",
		"UI.textArea":        "textArea",
		"UI.picker":          "picker",
		"UI.datePicker":      "datePicker",
		"UI.navigationTitle": "navigationTitle",
		"UI.topBarTrailing":  "uiTopBarTrailing",
		"UI.topBarLeading":   "uiTopBarLeading",
		"UI.header":          "header",
		"UI.footer":          "footer",
		"UI.numericCode":     "numericCode",
		"UI.disabled":        "uiDisabled",
		"UI.keyed":           "uiKeyed",
		"UI.onMove":          "uiOnMove",
		"UI.onDelete":        "uiOnDelete",
		"UI.text":            "uiText",
		"UI.button":          "uiButton",
		"UI.title":           "uiTitle",
		"UI.subtitle":        "uiSubtitle",
		"UI.errorText":       "uiErrorText",
		"UI.image":           "uiImage",
		"UI.paragraph":       "uiParagraph",
		"UI.span":            "uiSpan",
		"UI.bold":            "inlineBold",
		"UI.italic":          "inlineItalic",
		"UI.strikethrough":   "inlineStrikethrough",
		"UI.code":            "inlineCode",
		"UI.link":            "inlineLink",
		"UI.navigationLink":  "uiNavigationLink",
		"UI.spacer":          "uiSpacer",
		"UI.toggle":          "uiToggle",
		"UI.sheet":           "uiSheet",
		"UI.confirm":         "uiConfirm",
		"UI.empty":           "uiEmpty",
		"UI.centered":        "uiCentered",
		// Re-expose a handful of View.* attrs under UI.* so user code
		// that lives entirely in the SwiftUI-style vocabulary doesn't
		// need a second `import View exposing (...)`. These are pure
		// aliases — same runtime builtin, same shape.
		"UI.email":       "viewEmail",
		"UI.password":    "viewPassword",
		"UI.newPassword": "viewNewPassword",
		"UI.numeric":     "viewNumeric",
		"UI.oneTimeCode": "viewOneTimeCode",
		"UI.submit":      "viewSubmit",
		// Sizing — width / height accept Size values built via chars /
		// lines / fill. Type-safe axes: `chars` only builds Size Width,
		// `lines` only Size Height (`height (chars 6)` is a compile
		// error); `fill` is axis-polymorphic so the one constant works
		// in both. Alignment — cross-axis position for stack children.
		"UI.chars":    "uiChars",
		"UI.lines":    "uiLines",
		"UI.fill":     "uiFill",
		"UI.width":    "uiWidth",
		"UI.height":   "uiHeight",
		"UI.align":    "uiAlign",
		"UI.leading":  "uiLeading",
		"UI.center":   "uiCenter",
		"UI.trailing": "uiTrailing",
		"UI.top":      "uiTop",
		"UI.bottom":   "uiBottom",
		// Image sizing: px builds a Pixels value; size/fit/cover are
		// Image-hosted attrs.
		"UI.px":    "uiPx",
		"UI.size":  "uiSize",
		"UI.fit":   "uiFit",
		"UI.cover": "uiCover",
		// Canvas (v0.0.7) — the 2D draw-list module. Functions only; the
		// Color / Shape / Transform / Align type names resolve globally as
		// opaque TCons, and the Transform / Align constructors come in via
		// `exposing (Transform(..), Align(..))`.
		"Canvas.canvas":        "canvas",
		"Canvas.rect":          "rect",
		"Canvas.circle":        "circle",
		"Canvas.triangle":      "triangle",
		"Canvas.text":          "canvasText",
		"Canvas.group":         "group",
		"Canvas.rgb":           "rgb",
		"Canvas.rgba":          "rgba",
		"Canvas.onTap":         "onTap",
		"Canvas.watchSize":     "watchSize",
		"Canvas.watchPointers": "watchPointers",
		"Canvas.onRelease":     "onRelease",
		"Canvas.onDrag":        "onDrag",
		"Canvas.onHover":       "onHover",
		"Canvas.onAltTap":      "onAltTap",
		"Canvas.onWheel":       "onWheel",
		"App.frontend":         "appFrontend",
		"App.backend":          "appBackend",
		"App.fullstack":        "appFullstack",
		"App.shared":           "appShared",
		// Service: typed RPC contracts.
		"Service.declare":            "serviceDeclare",
		"Service.implement":          "serviceImplement",
		"Service.call":               "serviceCall",
		"Service.errorToString":      "serviceErrorToString",
		"Page.create":                "pageCreate",
		"Page.protected":             "pageProtected",
		"Page.adminProtected":        "pageAdminProtected",
		"Page.dynamic":               "pageDynamic",
		"Page.dynamicProtected":      "pageDynamicProtected",
		"Page.dynamicAdminProtected": "pageDynamicAdminProtected",
		"Page.sheet":                 "pageSheet",
		"Page.withShared":            "pageWithShared",
		"Mar.Admin.serverInfo":       "marAdminServerInfo",
		"Mar.Admin.dbStats":          "marAdminDbStats",
		"Mar.Admin.recentRequests":   "marAdminRecentRequests",
		"Mar.Admin.listEntities":     "marAdminListEntities",
		"Mar.Admin.listEntityRows":   "marAdminListEntityRows",
		"Mar.Admin.listBackups":      "marAdminListBackups",
		"Mar.Admin.requestCode":      "marAdminRequestCode",
		"Mar.Admin.verifyCode":       "marAdminVerifyCode",
		"Mar.Admin.signOut":          "marAdminSignOut",
		"Nav.push":                   "navPush",
		"Nav.replace":                "navReplace",
		"Nav.dismiss":                "navDismiss",
		"Auth.completeSignIn":        "authCompleteSignIn",
		"Nav.pushTo":                 "navPushTo",
		"Nav.replaceTo":              "navReplaceTo",
		// linkTo is a top-level builtin (no qualifier) — same vibe as
		// the standalone `text`, `column`, etc. that the View module
		// exports without a prefix. It's the everyday way to build a
		// URL from a typed Path.
		"linkTo": "linkTo",
		// always : a -> b -> a — Elm's Basics.always (bare, no module).
		"always": "always",
		// not : Bool -> Bool — Elm's Basics.not (bare, no module).
		"not": "not",
		// The numeric kit, also bare and also Elm-named.
		"max":         "max",
		"min":         "min",
		"clamp":       "clamp",
		"abs":         "abs",
		"modBy":       "modBy",
		"remainderBy": "remainderBy",
		// Auth: passwordless email-code authentication.
		"Auth.config":       "authConfig",
		"Auth.protect":      "authProtect",
		"Auth.requireRole":  "authRequireRole",
		"Auth.authorize":    "authAuthorize",
		"Auth.requireOwner": "authRequireOwner",
		"Auth.requestCode":  "authRequestCode",
		"Auth.verifyCode":   "authVerifyCode",
		"Auth.logout":       "authLogout",
		"Auth.me":           "authMe",
		// Dict: Elm-style polymorphic ordered map. Comparable-key
		// constraint enforced at runtime (the HM core doesn't yet
		// model type-class constraints).
		"Dict.empty":     "dictEmpty",
		"Dict.singleton": "dictSingleton",
		"Dict.insert":    "dictInsert",
		"Dict.update":    "dictUpdate",
		"Dict.remove":    "dictRemove",
		"Dict.isEmpty":   "dictIsEmpty",
		"Dict.member":    "dictMember",
		"Dict.get":       "dictGet",
		"Dict.size":      "dictSize",
		"Dict.keys":      "dictKeys",
		"Dict.values":    "dictValues",
		"Dict.toList":    "dictToList",
		"Dict.fromList":  "dictFromList",
		"Dict.map":       "dictMap",
		"Dict.foldl":     "dictFoldl",
		"Dict.foldr":     "dictFoldr",
		"Dict.filter":    "dictFilter",
		"Dict.partition": "dictPartition",
		"Dict.union":     "dictUnion",
		"Dict.intersect": "dictIntersect",
		"Dict.diff":      "dictDiff",
		// Set: Dict-but-keys-only.
		"Set.empty":     "setEmpty",
		"Set.singleton": "setSingleton",
		"Set.insert":    "setInsert",
		"Set.remove":    "setRemove",
		"Set.isEmpty":   "setIsEmpty",
		"Set.member":    "setMember",
		"Set.size":      "setSize",
		"Set.toList":    "setToList",
		"Set.fromList":  "setFromList",
		"Set.map":       "setMap",
		"Set.foldl":     "setFoldl",
		"Set.foldr":     "setFoldr",
		"Set.filter":    "setFilter",
		"Set.partition": "setPartition",
		"Set.union":     "setUnion",
		"Set.intersect": "setIntersect",
		"Set.diff":      "setDiff",
	}
	out := map[string]Type{}
	for q, f := range mapping {
		if t, ok := flat[f]; ok {
			out[q] = t
		}
	}
	return out
}
