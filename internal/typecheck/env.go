package typecheck

// TypeEnv maps names to types (or schemes). Implemented as an immutable
// linked list of frames so that scoping works naturally.
type TypeEnv struct {
	bindings map[string]Type
	parent   *TypeEnv
}

// NewEnv returns an empty top-level environment.
func NewEnv() *TypeEnv {
	return &TypeEnv{bindings: map[string]Type{}}
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

// BindMany returns a new env extending this one with all bindings in m.
func (e *TypeEnv) BindMany(m map[string]Type) *TypeEnv {
	if len(m) == 0 {
		return e
	}
	frame := make(map[string]Type, len(m))
	for k, v := range m {
		frame[k] = v
	}
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
	return env
}

func baseBindings() map[string]Type {
	a := TVar{ID: -1}
	b := TVar{ID: -2}

	out := map[string]Type{}

	// Arithmetic operators (monomorphic to Int for MVP; can generalize later).
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

		// IO (effects parameterized in error type for compatibility)
		"ioPrint": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TString, To: TEffect(a, TUnit{})},
		},
		"ioPrintln": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TString, To: TEffect(a, TUnit{})},
		},
		"ioReadLine": TForall{
			Vars: []int{a.ID},
			Body: TEffect(a, TString),
		},

		// JSON (untyped — encode any value, decode produces "any" record/list/etc)
		"jsonEncode": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: a, To: TString},
		},
		"jsonDecode": TForall{
			Vars: []int{a.ID},
			Body: TArrow{From: TString, To: TResult(TString, a)},
		},

		// HTTP server (records used directly for Request/Response/Route)
		// Request  : { url : String, method : String, body : String }
		// Response : { status : Int, body : String }
		// Route    : { method : String, path : String, handler : Request -> Effect String Response }
		"serverServe": TArrow{
			From: TInt,
			To: TArrow{
				From: TList(serverRouteType()),
				To:   TEffect(TString, TUnit{}),
			},
		},
		"serverGet":    serverRouteBuilderType(),
		"serverPost":   serverRouteBuilderType(),
		"serverPatch":  serverRouteBuilderType(),
		"serverDelete": serverRouteBuilderType(),

		"responseOk": TArrow{From: TString, To: serverResponseType()},
		"responseNotFound": serverResponseType(),
		"responseStatus":   TArrow{From: TInt, To: TArrow{From: TString, To: serverResponseType()}},
	}
}

func serverRequestType() Type {
	return TRecord{
		Fields: map[string]Type{
			"url":    TString,
			"method": TString,
			"body":   TString,
		},
		Order: []string{"url", "method", "body"},
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

// serverRouteBuilderType returns the type of Server.get/post/etc:
//
//	String -> (Request -> Effect String Response) -> Route
func serverRouteBuilderType() Type {
	return TArrow{
		From: TString,
		To: TArrow{
			From: TArrow{From: serverRequestType(), To: TEffect(TString, serverResponseType())},
			To:   serverRouteType(),
		},
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
		"Effect.succeed":    "effectSucceed",
		"Effect.fail":       "effectFail",
		"Effect.map":        "effectMap",
		"Effect.andThen":    "effectAndThen",
		"IO.print":    "ioPrint",
		"IO.println":  "ioPrintln",
		"IO.readLine": "ioReadLine",
		"JSON.encode": "jsonEncode",
		"JSON.decode": "jsonDecode",
		"Server.serve":     "serverServe",
		"Server.get":       "serverGet",
		"Server.post":      "serverPost",
		"Server.patch":     "serverPatch",
		"Server.delete":    "serverDelete",
		"Response.ok":       "responseOk",
		"Response.notFound": "responseNotFound",
		"Response.status":   "responseStatus",
	}
	out := map[string]Type{}
	for q, f := range mapping {
		if t, ok := flat[f]; ok {
			out[q] = t
		}
	}
	return out
}
