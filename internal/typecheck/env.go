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

		// Endpoint: typed contract shared between backend and frontend.
		"endpointGet":    TArrow{From: TString, To: TEndpoint()},
		"endpointPost":   TArrow{From: TString, To: TEndpoint()},
		"endpointPatch":  TArrow{From: TString, To: TEndpoint()},
		"endpointDelete": TArrow{From: TString, To: TEndpoint()},
		"endpointImplement": TArrow{
			From: TArrow{From: serverRequestType(), To: TEffect(TString, serverResponseType())},
			To: TArrow{
				From: TEndpoint(),
				To:   serverRouteType(),
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

		// Database (low-level for MVP)
		// Each row is returned as a record where every field is Maybe (since
		// SQL columns can be NULL). Higher-level entity API can refine later.
		"dbOpen": TArrow{From: TString, To: TEffect(TString, TDb())},
		"dbExec": TArrow{
			From: TDb(),
			To:   TArrow{From: TString, To: TEffect(TString, TUnit{})},
		},
		"dbQuery": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TDb(),
				To:   TArrow{From: TString, To: TEffect(TString, TList(a))},
			},
		},
		"dbQueryOne": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TDb(),
				To:   TArrow{From: TString, To: TEffect(TString, TMaybe(a))},
			},
		},
		"dbExecParams": TArrow{
			From: TDb(),
			To: TArrow{
				From: TString,
				To:   TArrow{From: TList(TString), To: TEffect(TString, TUnit{})},
			},
		},
		"dbQueryParams": TForall{
			Vars: []int{a.ID},
			Body: TArrow{
				From: TDb(),
				To: TArrow{
					From: TString,
					To:   TArrow{From: TList(TString), To: TEffect(TString, TList(a))},
				},
			},
		},

		// Entity builder
		"entityCreate":     TArrow{From: TString, To: TEntity()},
		"entityField":      TArrow{From: TString, To: TArrow{From: TColType(), To: TArrow{From: TEntity(), To: TEntity()}}},
		"entityInt":        TColType(),
		"entityText":       TColType(),
		"entityReal":       TColType(),
		"entityBlob":       TColType(),
		"entityDateTime":   TColType(),
		"entityPrimaryKey": TArrow{From: TString, To: TArrow{From: TEntity(), To: TEntity()}},
		"entityNotNull":    TArrow{From: TString, To: TArrow{From: TEntity(), To: TEntity()}},
		"entityUnique":     TArrow{From: TList(TString), To: TArrow{From: TEntity(), To: TEntity()}},
		"entityForeignKey": TArrow{From: TString, To: TArrow{From: TString, To: TArrow{From: TString, To: TArrow{From: TEntity(), To: TEntity()}}}},
		"entityMigrate":    TArrow{From: TDb(), To: TArrow{From: TList(TEntity()), To: TEffect(TString, TUnit{})}},

		// View — every builder is parametric in msg. Containers (section /
		// row / column / list) are forall over msg; the children must share
		// the same msg type so the whole tree dispatches into one app.
		// Leaves (text / title / link / input / etc.) are also forall:
		// they don't dispatch anything, so they fit any msg context.
		// Buttons and forms pin msg to the value/constructor the user passes.
		"viewSection":  TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(TView(a)), To: TView(a)}},
		"viewRow":      TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(TView(a)), To: TView(a)}},
		"viewColumn":   TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(TView(a)), To: TView(a)}},
		"viewText":     TForall{Vars: []int{a.ID}, Body: TArrow{From: TString, To: TView(a)}},
		"viewTitle":    TForall{Vars: []int{a.ID}, Body: TArrow{From: TString, To: TView(a)}},
		"viewSubtitle": TForall{Vars: []int{a.ID}, Body: TArrow{From: TString, To: TView(a)}},
		// View.button : msg -> String -> View msg   (clickMsg, then label)
		"viewButton":   TForall{Vars: []int{a.ID}, Body: TArrow{From: a, To: TArrow{From: TString, To: TView(a)}}},
		"viewLink":     TForall{Vars: []int{a.ID}, Body: TArrow{From: TString, To: TArrow{From: TString, To: TView(a)}}},
		"viewList":     TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(TView(a)), To: TView(a)}},
		// View.keyedList : List (String, View msg) -> View msg
		// Each item is paired with a stable string key. The browser diff
		// uses keys to track item identity across renders — survives
		// reordering, insertion in the middle, and removal without
		// scrambling DOM nodes (or losing focus on inputs inside items).
		"viewKeyedList": TForall{Vars: []int{a.ID}, Body: TArrow{From: TList(TTuple{Members: []Type{TString, TView(a)}}), To: TView(a)}},
		"viewRender":   TForall{Vars: []int{a.ID}, Body: TArrow{From: TView(a), To: TString}},
		// View.input : String -> (String -> msg) -> View msg
		// (currentValue, onChange) — every keystroke fires onChange with the
		// new value, so the model holds the form state explicitly. No string
		// names, no auto-collected records.
		"viewInput":    TForall{Vars: []int{a.ID}, Body: TArrow{From: TString, To: TArrow{From: TArrow{From: TString, To: a}, To: TView(a)}}},
		"viewTextarea": TForall{Vars: []int{a.ID}, Body: TArrow{From: TString, To: TArrow{From: TArrow{From: TString, To: a}, To: TView(a)}}},
		"viewEmpty":    TForall{Vars: []int{a.ID}, Body: TView(a)},

		// App
		// App.create
		//   : (() -> (Model, Effect Never Msg))
		//   -> (Msg -> Model -> (Model, Effect Never Msg))
		//   -> (Model -> View)
		//   -> App
		//
		// Effect Never Msg is encoded as TEffect(Never, Msg). For now we use
		// a fresh var for "Never" so it accepts any Effect; the convention is
		// the runtime ignores the error track of the returned effect.
		"appCreate": TForall{
			Vars: []int{a.ID, b.ID, -8},
			Body: TArrow{
				From: TArrow{From: TUnit{}, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -8}, b)}}},
				To: TArrow{
					From: TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -8}, b)}}}},
					To: TArrow{
						From: TArrow{From: a, To: TView(b)},
						To:   TApp(),
					},
				},
			},
		},
		"appServe": TArrow{From: TInt, To: TArrow{From: TApp(), To: TEffect(TString, TUnit{})}},

		// App.fullstack : { api : List Route, page : App } -> Effect String ()
		// Unified server: api routes mounted under /api, page (a frontend MVU
		// app) shipped to the browser as JS. Port comes from <projectDir>/mar.json
		// (server.port, default 3000) — not from code, so deployment can
		// reconfigure it without recompiling.
		"appFullstack": TArrow{
			From: TRecord{
				Fields: map[string]Type{
					"api":  TList(serverRouteType()),
					"page": TApp(),
				},
				Order: []string{"api", "page"},
			},
			To: TEffect(TString, TUnit{}),
		},

		// Screen.create — same shape as App.create but with a path string.
		"screenCreate": TForall{
			Vars: []int{a.ID, b.ID, -9},
			Body: TArrow{
				From: TString,
				To: TArrow{
					From: TArrow{From: TUnit{}, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -9}, b)}}},
					To: TArrow{
						From: TArrow{From: b, To: TArrow{From: a, To: TTuple{Members: []Type{a, TEffect(TVar{ID: -9}, b)}}}},
						To: TArrow{
							From: TArrow{From: a, To: TView(b)},
							To:   TScreen(),
						},
					},
				},
			},
		},
		"appServeScreens": TArrow{
			From: TInt,
			To:   TArrow{From: TList(TScreen()), To: TEffect(TString, TUnit{})},
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
		"IO.print":    "ioPrint",
		"IO.println":  "ioPrintln",
		"IO.readLine": "ioReadLine",
		"Http.get":    "httpGet",
		"Http.post":   "httpPost",
		"JSON.encode": "jsonEncode",
		"JSON.decode": "jsonDecode",
		"Server.serve":     "serverServe",
		"Server.get":       "serverGet",
		"Server.post":      "serverPost",
		"Server.patch":     "serverPatch",
		"Server.delete":    "serverDelete",
		"Endpoint.get":       "endpointGet",
		"Endpoint.post":      "endpointPost",
		"Endpoint.patch":     "endpointPatch",
		"Endpoint.delete":    "endpointDelete",
		"Endpoint.implement": "endpointImplement",
		"Endpoint.call":      "endpointCall",
		"Response.ok":       "responseOk",
		"Response.notFound": "responseNotFound",
		"Response.status":   "responseStatus",
		"Db.open":        "dbOpen",
		"Db.exec":        "dbExec",
		"Db.query":       "dbQuery",
		"Db.queryOne":    "dbQueryOne",
		"Db.execParams":  "dbExecParams",
		"Db.queryParams": "dbQueryParams",
		"Entity.create":     "entityCreate",
		"Entity.field":      "entityField",
		"Entity.int":        "entityInt",
		"Entity.text":       "entityText",
		"Entity.real":       "entityReal",
		"Entity.blob":       "entityBlob",
		"Entity.dateTime":   "entityDateTime",
		"Entity.primaryKey": "entityPrimaryKey",
		"Entity.notNull":    "entityNotNull",
		"Entity.unique":     "entityUnique",
		"Entity.foreignKey": "entityForeignKey",
		"Entity.migrate":    "entityMigrate",
		"View.section":  "viewSection",
		"View.row":      "viewRow",
		"View.column":   "viewColumn",
		"View.text":     "viewText",
		"View.title":    "viewTitle",
		"View.subtitle": "viewSubtitle",
		"View.button":   "viewButton",
		"View.link":     "viewLink",
		"View.list":     "viewList",
		"View.keyedList": "viewKeyedList",
		"View.render":   "viewRender",
		"View.input":    "viewInput",
		"View.textarea": "viewTextarea",
		"View.empty":    "viewEmpty",
		"App.create":        "appCreate",
		"App.serve":         "appServe",
		"App.fullstack":     "appFullstack",
		"App.serveScreens":  "appServeScreens",
		"Screen.create":     "screenCreate",
	}
	out := map[string]Type{}
	for q, f := range mapping {
		if t, ok := flat[f]; ok {
			out[q] = t
		}
	}
	return out
}
