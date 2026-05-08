package typecheck

import "strings"

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

// IsBackendOnlyBuiltin reports whether a qualified-name builtin
// (e.g. "Repo.create") is intentionally never reachable from frontend
// code. These are the names that exist in BaseEnv() but should be
// implemented only in the Go runtime — JS/Swift runtimes don't need
// to ship them, and runtime-coverage tests treat them as expected
// gaps.
//
// Covers server topology (App.fullstack), persistence (Repo, Entity,
// Db), auth wiring evaluated at server boot (Auth.config / .protect /
// .authorize / .requireRole / .requireOwner), service declaration on
// the server side (Service.declare / .implement), and HTTP endpoint
// registration (Endpoint.* / Response.*).
//
// Adding a new entry here is a deliberate statement: "this builtin
// runs server-only; clients don't need to implement it." Don't add a
// name just to silence a coverage test — first confirm frontend code
// can't reach it.
func IsBackendOnlyBuiltin(name string) bool {
	for _, prefix := range []string{
		"Repo.",     // SQLite repository
		"Entity.",   // schema-defining helpers
		"Db.",       // raw query escape hatch
		"Server.",   // HTTP server config
		"Endpoint.", // REST endpoint registration
		"Response.", // server-side response building
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	switch name {
	case "Auth.config", "Auth.protect",
		"Auth.authorize", "Auth.requireOwner", "Auth.requireRole",
		"Service.declare", "Service.implement",
		"App.frontend", "App.backend", "App.fullstack":
		return true
	}
	return false
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
	}
}

func baseBindings() map[string]Type {
	a := TVar{ID: -1}
	b := TVar{ID: -2}

	out := map[string]Type{}

	// Arithmetic operators (monomorphic to Int; numeric type classes
	// would let these generalize across Int/Float).
	out["+"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TInt}}
	out["-"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TInt}}
	out["*"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TInt}}
	out["/"] = TArrow{From: TInt, To: TArrow{From: TInt, To: TInt}}

	// Comparisons: forall a. a -> a -> Bool
	out["=="] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{From: a, To: TArrow{From: a, To: TBool}},
	}
	out["/="] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{From: a, To: TArrow{From: a, To: TBool}},
	}
	out["<"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{From: a, To: TArrow{From: a, To: TBool}},
	}
	out[">"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{From: a, To: TArrow{From: a, To: TBool}},
	}
	out["<="] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{From: a, To: TArrow{From: a, To: TBool}},
	}
	out[">="] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{From: a, To: TArrow{From: a, To: TBool}},
	}

	// Logical
	out["&&"] = TArrow{From: TBool, To: TArrow{From: TBool, To: TBool}}
	out["||"] = TArrow{From: TBool, To: TArrow{From: TBool, To: TBool}}

	// String/list append
	out["++"] = TForall{
		Vars: []int{a.ID},
		Body: TArrow{From: a, To: TArrow{From: a, To: a}},
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

	// --- stdlib (List, String, Maybe) ---
	for k, v := range stdlibBindings() {
		out[k] = v
	}

	return out
}

func stdlibBindings() map[string]Type {
	a := TVar{ID: -3}
	b := TVar{ID: -4}

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
				From: TArrow{From: b, To: TArrow{From: a, To: b}},
				To: TArrow{
					From: b,
					To:   TArrow{From: TList(a), To: b},
				},
			},
		},
		"listSum":     TArrow{From: TList(TInt), To: TInt},
		"listRange":   TArrow{From: TInt, To: TArrow{From: TInt, To: TList(TInt)}},
		"listReverse": TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(a), To: TList(a)}},
		"listHead":    TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(a), To: TMaybe(a)}},
		"listTail":    TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(a), To: TMaybe(TList(a))}},
		"listIsEmpty": TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(a), To: TBool}},
		"listConcat":  TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(TList(a)), To: TList(a)}},

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
		// String extras
		"stringSplit": TArrow{From: TString, To: TArrow{From: TString, To: TList(TString)}},
		"stringJoin":  TArrow{From: TString, To: TArrow{From: TList(TString), To: TString}},
		"stringTrim":  TArrow{From: TString, To: TString},

		// Effect
		"effectSucceed": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: b, To: TEffect(a, b)},
		},
		"effectFail": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: a, To: TEffect(a, b)},
		},
		"effectMap": TForall{
			Vars: []int{a.ID, b.ID, -5},
			Body: TArrow{
				From: TArrow{From: b, To: TVar{ID: -5}},
				To: TArrow{
					From: TEffect(a, b),
					To:   TEffect(a, TVar{ID: -5}),
				},
			},
		},
		"effectAndThen": TForall{
			Vars: []int{a.ID, b.ID, -5},
			Body: TArrow{
				From: TArrow{From: b, To: TEffect(a, TVar{ID: -5})},
				To: TArrow{
					From: TEffect(a, b),
					To:   TEffect(a, TVar{ID: -5}),
				},
			},
		},
		"effectForEach": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TArrow{From: b, To: TEffect(a, TUnit{})},
				To:   TArrow{From: TList(b), To: TEffect(a, TUnit{})},
			},
		},
		"effectSequence": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TList(TEffect(a, b)),
				To:   TEffect(a, TList(b)),
			},
		},
		"effectNone": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TEffect(a, b),
		},

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
		"timeSeconds":   TArrow{From: TInt, To: TDuration},
		"timeMinutes":   TArrow{From: TInt, To: TDuration},
		"timeHours":     TArrow{From: TInt, To: TDuration},
		"timeDays":      TArrow{From: TInt, To: TDuration},
		"timeWeeks":     TArrow{From: TInt, To: TDuration},
		"timeToSeconds": TArrow{From: TDuration, To: TInt},

		// Time — absolute moments. Stored as Unix milliseconds.
		// Time.now is an Effect because it reads the wall clock;
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
		"timeNow": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TEffect(a, TTime),
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
					To:   TEffect(a, b),
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
						To:   TEffect(a, b),
					},
				},
			},
		},

		// HTTP responses (records used directly).
		// Response : { status : Int, body : String }
		// Route    : { method : String, path : String, handler : Request -> Effect String Response }
		// (Server.serve / Server.get etc. were removed when mar narrowed to
		// full-stack — apps host themselves through App.frontend / App.backend
		// / App.fullstack. Endpoint.implement is the typed builder that
		// produces routes today.)
		"responseOk": TArrow{From: TString, To: serverResponseType()},
		"responseNotFound": serverResponseType(),
		"responseStatus":   TArrow{From: TInt, To: TArrow{From: TString, To: serverResponseType()}},

		// Endpoint: typed contract shared between backend and frontend.
		//
		// Two layers coexist:
		//
		//  - Low-level (Endpoint.get / .post + Endpoint.implement): used for
		//    custom paths or non-CRUD shapes (action endpoints like /sign
		//    or /login). Handler is `Request -> Effect e Response` and
		//    constructs the Response by hand.
		//
		//  - REST sugar (Endpoint.list / .show / .create / .update / .delete):
		//    each function constrains the handler shape and the runtime fills
		//    in path-param parsing, body decode, status code, and JSON encode
		//    of the response. Saves boilerplate for the common 5 REST verbs.
		"endpointGet":  TArrow{From: TString, To: TEndpoint()},
		"endpointPost": TArrow{From: TString, To: TEndpoint()},
		"endpointImplement": TArrow{
			From: TArrow{From: serverRequestType(), To: TEffect(TString, serverResponseType())},
			To: TArrow{
				From: TEndpoint(),
				To:   serverRouteType(),
			},
		},

		// Endpoint.list : String -> Effect String (List a) -> Route
		"endpointList": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TString,
				To: TArrow{
					From: TEffect(TString, TList(a)),
					To:   serverRouteType(),
				},
			},
		},
		// Endpoint.show : String -> (Int -> Effect String (Maybe a)) -> Route
		"endpointShow": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TString,
				To: TArrow{
					From: TArrow{From: TInt, To: TEffect(TString, TMaybe(a))},
					To:   serverRouteType(),
				},
			},
		},
		// Endpoint.create : String -> (b -> Effect String a) -> Route
		"endpointCreate": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TString,
				To: TArrow{
					From: TArrow{From: b, To: TEffect(TString, a)},
					To:   serverRouteType(),
				},
			},
		},
		// Endpoint.update : String -> (Int -> b -> Effect String (Maybe a)) -> Route
		"endpointUpdate": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TString,
				To: TArrow{
					From: TArrow{
						From: TInt,
						To:   TArrow{From: b, To: TEffect(TString, TMaybe(a))},
					},
					To: serverRouteType(),
				},
			},
		},
		// Endpoint.delete : String -> (Int -> Effect String ()) -> Route
		"endpointDelete": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TString,
				To: TArrow{
					From: TArrow{From: TInt, To: TEffect(TString, TUnit{})},
					To:   serverRouteType(),
				},
			},
		},
		// Endpoint.call : String -> Endpoint -> String -> (Result String String -> msg) -> Effect e msg
		//                 base    endpoint   body     toMsg
		"endpointCall": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TString,
				To: TArrow{
					From: TEndpoint(),
					To: TArrow{
						From: TString,
						To: TArrow{
							From: TArrow{From: TResult(TString, TString), To: b},
							To:   TEffect(a, b),
						},
					},
				},
			},
		},

		// Entity declaration (record-literal form)
		//
		//   notes : Entity Note
		//   notes =
		//       Entity.define "notes"
		//           { id   = Entity.serial
		//           , body = Entity.text Entity.notNull
		//           }
		//
		// Entity.define is fully polymorphic in both the schema record and
		// the row type — the runtime cross-checks at first Repo call that
		// the schema's keys/types are compatible with the row record. Trade-
		// off documented in mar.md: a one-time per-entity assertion gives
		// every downstream Repo call full type safety on field names and
		// types.
		"entityDefine": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TString,
				To:   TArrow{From: b, To: TEntity(a)},
			},
		},
		// Column constructors. Each carries the value type stored in that
		// column; `Entity.serial` is special-cased as the auto-incrementing
		// integer primary key.
		"entitySerial": TColumn(TInt),
		"entityInt":    TArrow{From: TConstraint(), To: TColumn(TInt)},
		"entityText":   TArrow{From: TConstraint(), To: TColumn(TString)},
		"entityBool":   TArrow{From: TConstraint(), To: TColumn(TBool)},
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
		// Constraints. Only `notNull` is exposed today; optional / unique /
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
			Body: TArrow{From: TEntity(a), To: TEffect(TString, TList(a))},
		},
		"repoFindByID": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TEntity(a),
				To:   TArrow{From: TInt, To: TEffect(TString, TMaybe(a))},
			},
		},
		"repoFindBy": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TEntity(a),
				To:   TArrow{From: b, To: TEffect(TString, TList(a))},
			},
		},
		"repoCreate": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TEntity(a),
				To:   TArrow{From: b, To: TEffect(TString, a)},
			},
		},
		"repoUpdate": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{
				From: TEntity(a),
				To: TArrow{
					From: TInt,
					To:   TArrow{From: b, To: TEffect(TString, TMaybe(a))},
				},
			},
		},
		"repoDeleteByID": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TEntity(a),
				To:   TArrow{From: TInt, To: TEffect(TString, TUnit{})},
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
		"viewSubmit": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: a, To: TAttr()},
		},
		"viewEmail":       TAttr(),
		"viewPassword":    TAttr(),
		"viewNewPassword": TAttr(),
		"viewNumeric":     TAttr(),
		"viewOneTimeCode": TAttr(),

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
				From: TList(TAttr()),
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

		// UI.list : List (View msg) -> View msg
		// Vertical list of rows or sections. iOS: SwiftUI List (with
		// dividers, hover, swipe-actions hooks). Web: <ul> with
		// list-style CSS. Use for content (notes, items); use form
		// for input groupings.
		"list": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TList(TView(a)), To: TView(a)},
		},

		// UI.section : List Attr -> List (View msg) -> View msg
		// A logical group inside form/list. Optional `header` /
		// `footer` attrs label the group. iOS: Section { } with
		// header/footer text. Web: <section> with <h2>/<p>.
		"uiSection": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr()),
				To:   TArrow{From: TList(TView(a)), To: TView(a)},
			},
		},

		// UI.hstack / UI.vstack : List Attr -> List (View msg) -> View msg
		// Free composition. Use when section/form don't fit (e.g. a
		// row of input + button inside a section).
		"hstack": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr()),
				To:   TArrow{From: TList(TView(a)), To: TView(a)},
			},
		},
		"vstack": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr()),
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
				From: TList(TAttr()),
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

		// UI attrs.

		// navigationTitle : String -> Attr
		// Sets the navigation bar title (iOS) / page heading (web).
		"navigationTitle": TArrow{From: TString, To: TAttr()},

		// trailing / leading : View msg -> Attr
		// Add a toolbar item to the navigation stack. iOS: ToolbarItem
		// with .topBarTrailing / .topBarLeading placement. Web: button
		// rendered to the right / left of the page heading.
		"trailing": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TView(a), To: TAttr()},
		},
		"leading": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TView(a), To: TAttr()},
		},

		// header / footer : String -> Attr
		// Text label for a UI.section's top / bottom. iOS: Section's
		// header/footer slots. Web: <h2>/<small> within the section.
		"header": TArrow{From: TString, To: TAttr()},
		"footer": TArrow{From: TString, To: TAttr()},

		// UI.text : String -> View msg
		// Plain text. Like View.text but without the leading attrs
		// list — UI vocabulary doesn't pass per-leaf style attrs
		// (those live on the section / form parent).
		"uiText": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TString, To: TView(a)},
		},

		// UI.button : List Attr -> msg -> String -> View msg
		// A button that dispatches `msg` on tap. The attrs list lets
		// modifier attrs (like `disabled`) tune the button's behavior
		// without bloating the positional signature.
		"uiButton": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TList(TAttr()),
				To: TArrow{
					From: a,
					To:   TArrow{From: TString, To: TView(a)},
				},
			},
		},

		// UI.disabled : Bool -> Attr
		// Attached to a button (and in the future, other interactive
		// views), this attr greys it out and suppresses the click
		// dispatch. iOS: `.disabled(flag)` modifier. Web: `disabled`
		// attribute on <button>.
		"uiDisabled": TArrow{From: TBool, To: TAttr()},

		// numericCode : Attr
		// Convenience attr combining `numeric` (10-key pad) +
		// `oneTimeCode` (Code-from-Mail / SMS autofill). The common
		// case for OTP / 2FA inputs. iOS: keyboardType .numberPad +
		// textContentType .oneTimeCode. Web: inputmode="numeric" +
		// autocomplete="one-time-code".
		"numericCode": TAttr(),

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

		// UI.link : Path r -> r -> String -> View msg
		// Clickable navigation link. The Path + record produce the
		// destination URL via the same machinery as `linkTo`, so
		// refactoring a route catches all link sites in compile time.
		// iOS: NavigationLink. Web: <a href="...">.
		"uiLink": TForall{
			Vars: []int{-40, a.ID},
			Body: TArrow{
				From: TPath(TVar{ID: -40}),
				To: TArrow{
					From: TVar{ID: -40},
					To:   TArrow{From: TString, To: TView(a)},
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

		// UI.centered : View msg -> View msg
		// Wraps a view in a container that fills the available space
		// and centers its child both horizontally and vertically. Use
		// for full-screen states (Loading, Empty, Error). iOS:
		// frame(maxWidth: .infinity, maxHeight: .infinity, alignment:
		// .center). Web: flex container that fills + centers.
		"uiCentered": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TView(a), To: TView(a)},
		},

		// Page — a single MVU screen bound to a URL path.
		//
		// Page.create takes a record describing the page:
		//
		//   { path   : String                              -- URL pattern (use "/" for single-page)
		//   , title  : String                              -- (optional) browser tab / nav title
		//   , init   : () -> (Model, Effect e Msg)
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
						"path":   TString,
						"init":   TArrow{From: TUnit{}, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -8}, b)}}},
						"update": TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -8}, b)}}}},
						"view":   TArrow{From: a, To: TView(b)},
					},
					Order: []string{"path", "init", "update", "view"},
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
		//   , init     : User -> () -> (Model, Effect e Msg)
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
						"path":   TString,
						"init":   TArrow{From: TVar{ID: -16}, To: TArrow{From: TUnit{}, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -17}, b)}}}},
						"update": TArrow{From: TVar{ID: -16}, To: TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -17}, b)}}}}},
						"view":   TArrow{From: TVar{ID: -16}, To: TArrow{From: a, To: TView(b)}},
					},
					Order: []string{"path", "init", "update", "view"},
					Tail:  TVar{ID: -18},
				},
				To: TPage(),
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
						"path":   TPath(TVar{ID: -19}),
						"init":   TArrow{From: TVar{ID: -19}, To: TArrow{From: TUnit{}, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -20}, b)}}}},
						"update": TArrow{From: TVar{ID: -19}, To: TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -20}, b)}}}}},
						"view":   TArrow{From: TVar{ID: -19}, To: TArrow{From: a, To: TView(b)}},
					},
					Order: []string{"path", "init", "update", "view"},
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
						"path":   TPath(TVar{ID: -22}),
						"init":   TArrow{From: TVar{ID: -23}, To: TArrow{From: TVar{ID: -22}, To: TArrow{From: TUnit{}, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -24}, b)}}}}},
						"update": TArrow{From: TVar{ID: -23}, To: TArrow{From: TVar{ID: -22}, To: TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -24}, b)}}}}}},
						"view":   TArrow{From: TVar{ID: -23}, To: TArrow{From: TVar{ID: -22}, To: TArrow{From: a, To: TView(b)}}},
					},
					Order: []string{"path", "init", "update", "view"},
					Tail:  TVar{ID: -25},
				},
				To: TPage(),
			},
		},

		// Nav.push : String -> Effect e msg
		// Pushes a URL onto the browser history and re-renders the
		// matching Page. For dynamic pages prefer Nav.pushTo, which
		// builds the URL from a typed Path + record so refactors of
		// the path pattern catch all callers in compile time.
		"navPush": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: TString, To: TEffect(a, b)},
		},

		// Nav.replace : String -> Effect e msg
		// Like Nav.push but replaces the current history entry — the
		// back button won't return to the previous URL. Right for
		// post-login / post-logout flows.
		"navReplace": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TArrow{From: TString, To: TEffect(a, b)},
		},

		// Nav.afterSignIn : Effect e msg
		// Use as the navigation step after Auth.verifyCode succeeds.
		// Reads the framework-managed `next` slot — set when a 401
		// from a Service.call sent the user here, or when a deep link
		// landed on the sign-in page — and goes there. Falls back to
		// "/" when no return target was captured. Web validates that
		// the captured path is same-origin to prevent open-redirect
		// abuse via crafted ?next= parameters.
		"navAfterSignIn": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TEffect(a, b),
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
				To:   TArrow{From: TVar{ID: -30}, To: TEffect(a, b)},
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
				To:   TArrow{From: TVar{ID: -31}, To: TEffect(a, b)},
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
		"appFrontend": TArrow{From: TList(TPage()), To: TEffect(TString, TUnit{})},

		// App.backend : { routes, services } -> Effect String ()
		// Pure API server. `routes` exposes low-level Endpoint.* — for custom
		// paths, SSR, webhooks. `services` exposes typed RPC services with
		// auto-derived URLs (Service / Service.expose / Service.call).
		"appBackend": TArrow{
			From: TRecord{
				Fields: map[string]Type{
					"routes":   TList(serverRouteType()),
					"services": TList(TExposedService()),
				},
				Order: []string{"routes", "services"},
			},
			To: TEffect(TString, TUnit{}),
		},

		// App.fullstack : { api, services, pages } -> Effect String ()
		// Unified server. `api` mounts low-level routes under /api/*.
		// `services` mounts typed RPC services under /services/*. `pages`
		// ships browser MVU. Port from mar.json.
		"appFullstack": TArrow{
			From: TRecord{
				Fields: map[string]Type{
					"api":      TList(serverRouteType()),
					"services": TList(TExposedService()),
					"pages":    TList(TPage()),
				},
				Order: []string{"api", "services", "pages"},
			},
			To: TEffect(TString, TUnit{}),
		},

		// Service.declare : Service req resp
		//
		// A typed RPC contract with no handler attached. Bound at the
		// top level in the shared module so frontend can pass it to
		// Service.call; backend pairs it with a handler via
		// Service.implement (or Auth.protect).
		"serviceDeclare": TForall{
			Vars: []int{a.ID, b.ID},
			Body: TService(a, b),
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
					From: TArrow{From: a, To: TEffect(TString, b)},
					To:   TExposedService(),
				},
			},
		},

		// Service.call : Service req resp -> req -> (Result String resp -> msg) -> Effect e msg
		// Client-side: encodes req as JSON, fetches, decodes resp.
		// Returns Effect that dispatches msg with Ok resp / Err message.
		"serviceCall": TForall{
			Vars: []int{a.ID, b.ID, -11, -12},
			Body: TArrow{
				From: TService(a, b),
				To: TArrow{
					From: a,
					To: TArrow{
						From: TArrow{From: TResult(TString, b), To: TVar{ID: -12}},
						To:   TEffect(TVar{ID: -11}, TVar{ID: -12}),
					},
				},
			},
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
							To:   TEffect(TString, b),
						},
					},
					To: TExposedService(),
				},
			},
		},

		// Auth.requireRole : role -> ExposedService -> ExposedService
		//
		// PROPOSAL — see docs/authorization-proposal.md.
		// Decorator that adds an RBAC gate. The session must already
		// be valid (Auth.protect ran first) AND the user's role must
		// match. The role argument's type unifies with whatever
		// `Auth.config.role` returns, so misspelled enum values
		// fail at compile time.
		//
		// Today this is a no-op pass-through: the example type-checks
		// and runs, but no enforcement happens. Enforcement lands in
		// the future PR per the proposal.
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
		// PROPOSAL. ABAC decorator. Loads the resource (Maybe lifts
		// to 404 on Nothing in the future), runs the policy; rejects
		// 403 on False. Today: no-op pass-through.
		"authAuthorize": TForall{
			Vars: []int{-31, -32, -33},
			Body: TArrow{
				// loader: input -> User -> Effect String (Maybe resource)
				From: TArrow{
					From: TVar{ID: -31},
					To: TArrow{
						From: TVar{ID: -32},
						To:   TEffect(TString, TMaybe(TVar{ID: -33})),
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
		// PROPOSAL. Sugar for the common ABAC case "this resource has
		// an ownerId field that must equal user.id". Today: no-op
		// pass-through.
		"authRequireOwner": TForall{
			Vars: []int{-34, -35, -36},
			Body: TArrow{
				From: TArrow{
					From: TVar{ID: -34},
					To: TArrow{
						From: TVar{ID: -35},
						To:   TEffect(TString, TMaybe(TVar{ID: -36})),
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
		"authRequestCode": TForall{
			Vars: []int{-14, -15},
			Body: TArrow{
				From: TRecord{Fields: map[string]Type{"email": TString}, Order: []string{"email"}},
				To: TArrow{
					From: TArrow{From: TResult(TString, TUnit{}), To: TVar{ID: -15}},
					To:   TEffect(TVar{ID: -14}, TVar{ID: -15}),
				},
			},
		},

		// Auth.verifyCode : { email, code } -> (Result String user -> msg) -> Effect e msg
		"authVerifyCode": TForall{
			Vars: []int{a.ID, -16, -17},
			Body: TArrow{
				From: TRecord{
					Fields: map[string]Type{"email": TString, "code": TString},
					Order:  []string{"email", "code"},
				},
				To: TArrow{
					From: TArrow{From: TResult(TString, a), To: TVar{ID: -17}},
					To:   TEffect(TVar{ID: -16}, TVar{ID: -17}),
				},
			},
		},

		// Auth.logout : (Result String () -> msg) -> Effect e msg
		"authLogout": TForall{
			Vars: []int{-18, -19},
			Body: TArrow{
				From: TArrow{From: TResult(TString, TUnit{}), To: TVar{ID: -19}},
				To:   TEffect(TVar{ID: -18}, TVar{ID: -19}),
			},
		},

		// Auth.me : (Result String (Maybe user) -> msg) -> Effect e msg
		"authMe": TForall{
			Vars: []int{a.ID, -20, -21},
			Body: TArrow{
				From: TArrow{From: TResult(TString, TMaybe(a)), To: TVar{ID: -21}},
				To:   TEffect(TVar{ID: -20}, TVar{ID: -21}),
			},
		},
	}
}

func serverRequestType() Type {
	// params is `String -> Maybe String`: lookup by name. We use a function
	// rather than a row record because routes with different param shapes
	// must coexist in `List Route`, which requires uniform Route type.
	return TRecord{
		Fields: map[string]Type{
			"url":    TString,
			"method": TString,
			"body":   TString,
			"params": TArrow{From: TString, To: TMaybe(TString)},
		},
		Order: []string{"url", "method", "body", "params"},
	}
}

func serverResponseType() Type {
	return TRecord{
		Fields: map[string]Type{
			"status": TInt,
			"body":   TString,
		},
		Order: []string{"status", "body"},
	}
}

func serverRouteType() Type {
	return TRecord{
		Fields: map[string]Type{
			"method":  TString,
			"path":    TString,
			"handler": TArrow{From: serverRequestType(), To: TEffect(TString, serverResponseType())},
		},
		Order: []string{"method", "path", "handler"},
	}
}

// qualifiedAliases returns Module.name aliases for stdlib (so `List.map`
// works just like `listMap`).
func qualifiedAliases(flat map[string]Type) map[string]Type {
	mapping := map[string]string{
		"List.length":   "listLength",
		"List.map":      "listMap",
		"List.filter":   "listFilter",
		"List.foldl":    "listFoldl",
		"List.sum":      "listSum",
		"List.range":    "listRange",
		"List.reverse":  "listReverse",
		"List.head":     "listHead",
		"List.tail":     "listTail",
		"List.isEmpty":  "listIsEmpty",
		"List.concat":   "listConcat",
		"String.length":     "stringLength",
		"String.contains":   "stringContains",
		"String.startsWith": "stringStartsWith",
		"String.fromInt":    "stringFromInt",
		"String.toUpper":    "stringToUpper",
		"String.toLower":    "stringToLower",
		"Maybe.withDefault": "maybeWithDefault",
		"Maybe.map":         "maybeMap",
		"Maybe.andThen":     "maybeAndThen",
		"Result.map":        "resultMap",
		"Result.andThen":    "resultAndThen",
		"Result.mapError":   "resultMapError",
		"String.split":      "stringSplit",
		"String.join":       "stringJoin",
		"String.trim":       "stringTrim",
		"Effect.succeed":    "effectSucceed",
		"Effect.fail":       "effectFail",
		"Effect.map":        "effectMap",
		"Effect.andThen":    "effectAndThen",
		"Effect.forEach":    "effectForEach",
		"Effect.sequence":   "effectSequence",
		"Effect.none":       "effectNone",
		"Time.seconds":      "timeSeconds",
		"Time.minutes":      "timeMinutes",
		"Time.hours":        "timeHours",
		"Time.days":         "timeDays",
		"Time.weeks":        "timeWeeks",
		"Time.toSeconds":    "timeToSeconds",
		"Time.now":          "timeNow",
		"Time.add":          "timeAdd",
		"Time.sub":          "timeSub",
		"Time.diff":         "timeDiff",
		"Time.before":       "timeBefore",
		"Time.after":        "timeAfter",
		"Time.toIso":        "timeToIso",
		"Time.fromIso":      "timeFromIso",
		"Time.toMillis":     "timeToMillis",
		"Time.fromYMD":      "timeFromYMD",
		"Time.addDays":      "timeAddDays",
		"Time.addMonths":    "timeAddMonths",
		"Time.addYears":     "timeAddYears",
		"Time.year":         "timeYear",
		"Time.month":        "timeMonth",
		"Time.day":          "timeDay",
		"Time.hour":         "timeHour",
		"Time.minute":       "timeMinute",
		"Time.second":       "timeSecond",
		"Http.get":    "httpGet",
		"Http.post":   "httpPost",
		"JSON.encode": "jsonEncode",
		"JSON.decode": "jsonDecode",
		// Low-level endpoint builders (paired with Endpoint.implement) — for
		// custom paths like `/sign`, SSR routes, etc.
		"Endpoint.get":       "endpointGet",
		"Endpoint.post":      "endpointPost",
		"Endpoint.implement": "endpointImplement",
		"Endpoint.call":      "endpointCall",
		// REST sugar — common shapes with auto path-param parse, JSON
		// decode/encode, and method-derived status code.
		"Endpoint.list":   "endpointList",
		"Endpoint.show":   "endpointShow",
		"Endpoint.create": "endpointCreate",
		"Endpoint.update": "endpointUpdate",
		"Endpoint.delete": "endpointDelete",
		"Response.ok":       "responseOk",
		"Response.notFound": "responseNotFound",
		"Response.status":   "responseStatus",
		// Entity (record-literal form)
		"Entity.define":  "entityDefine",
		"Entity.serial":  "entitySerial",
		"Entity.int":     "entityInt",
		"Entity.text":    "entityText",
		"Entity.bool":    "entityBool",
		"Entity.enum":    "entityEnum",
		"Entity.timestamp": "entityTimestamp",
		"Entity.notNull": "entityNotNull",
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
		"UI.hstack":          "hstack",
		"UI.vstack":          "vstack",
		"UI.textField":       "textField",
		"UI.navigationTitle": "navigationTitle",
		"UI.trailing":        "trailing",
		"UI.leading":         "leading",
		"UI.header":          "header",
		"UI.footer":          "footer",
		"UI.numericCode":     "numericCode",
		"UI.disabled":        "uiDisabled",
		"UI.text":            "uiText",
		"UI.button":          "uiButton",
		"UI.title":           "uiTitle",
		"UI.subtitle":        "uiSubtitle",
		"UI.link":            "uiLink",
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
		"App.frontend":      "appFrontend",
		"App.backend":       "appBackend",
		"App.fullstack":     "appFullstack",
		// Service: typed RPC contracts.
		"Service.declare":   "serviceDeclare",
		"Service.implement": "serviceImplement",
		"Service.call":      "serviceCall",
		"Page.create":       "pageCreate",
		"Page.protected":         "pageProtected",
		"Page.dynamic":           "pageDynamic",
		"Page.dynamicProtected":  "pageDynamicProtected",
		"Nav.push":          "navPush",
		"Nav.replace":       "navReplace",
		"Nav.afterSignIn":   "navAfterSignIn",
		"Nav.pushTo":        "navPushTo",
		"Nav.replaceTo":     "navReplaceTo",
		// linkTo is a top-level builtin (no qualifier) — same vibe as
		// the standalone `text`, `column`, etc. that the View module
		// exports without a prefix. It's the everyday way to build a
		// URL from a typed Path.
		"linkTo":            "linkTo",
		// Auth: passwordless email-code authentication.
		"Auth.config":      "authConfig",
		"Auth.protect":       "authProtect",
		"Auth.requireRole":   "authRequireRole",
		"Auth.authorize":     "authAuthorize",
		"Auth.requireOwner":  "authRequireOwner",
		"Auth.requestCode":   "authRequestCode",
		"Auth.verifyCode":  "authVerifyCode",
		"Auth.logout":      "authLogout",
		"Auth.me":          "authMe",
	}
	out := map[string]Type{}
	for q, f := range mapping {
		if t, ok := flat[f]; ok {
			out[q] = t
		}
	}
	return out
}
