package runtime

import "fmt"

// BaseEnv returns the initial runtime environment with all built-ins bound.
func BaseEnv() *Env {
	env := NewEnv()
	for name, v := range builtins() {
		env.Define(name, v)
	}
	for name, v := range effectBuiltins() {
		env.Define(name, v)
	}
	for name, v := range subBuiltins() {
		env.Define(name, v)
	}
	for name, v := range randomBuiltins() {
		env.Define(name, v)
	}
	for name, v := range ioBuiltins() {
		env.Define(name, v)
	}
	for name, v := range jsonBuiltins() {
		env.Define(name, v)
	}
	for name, v := range entityBuiltins() {
		env.Define(name, v)
	}
	for name, v := range repoBuiltins() {
		env.Define(name, v)
	}
	for name, v := range serviceBuiltins() {
		env.Define(name, v)
	}
	for name, v := range authBuiltins() {
		env.Define(name, v)
	}
	for name, v := range viewBuiltins() {
		env.Define(name, v)
	}
	for name, v := range appBuiltins() {
		env.Define(name, v)
	}
	for name, v := range timeBuiltins() {
		env.Define(name, v)
	}
	for name, v := range dictBuiltins() {
		env.Define(name, v)
	}
	for name, v := range setBuiltins() {
		env.Define(name, v)
	}
	for name, v := range charBuiltins() {
		env.Define(name, v)
	}
	for name, v := range adminBuiltins() {
		env.Define(name, v)
	}
	env = extendBaseEnv(env)
	// Register qualified aliases (List.map etc.) that point to the same values.
	for q, f := range qualifiedAliasMapping() {
		if v, ok := env.Lookup(f); ok {
			env.Define(q, v)
		}
	}
	return env
}

// QualifiedAliasNames returns the set of `Module.name` qualified
// stdlib bindings the runtime knows about. Exposed for drift tests
// that compare this set against typecheck.BaseQualifiedSymbols —
// without coverage here, a builtin can typecheck but fail at runtime
// with "unbound qualified name: Foo.bar".
func QualifiedAliasNames() map[string]bool {
	out := make(map[string]bool)
	for q := range qualifiedAliasMapping() {
		out[q] = true
	}
	return out
}

// qualifiedAliasMapping returns Module.name -> flat name for stdlib aliases.
func qualifiedAliasMapping() map[string]string {
	return map[string]string{
		// List
		"List.length":      "listLength",
		"List.map":         "listMap",
		"List.filter":      "listFilter",
		"List.foldl":       "listFoldl",
		"List.foldr":       "listFoldr",
		"List.sum":         "listSum",
		"List.product":     "listProduct",
		"List.range":       "listRange",
		"List.reverse":     "listReverse",
		"List.head":        "listHead",
		"List.tail":        "listTail",
		"List.isEmpty":     "listIsEmpty",
		"List.concat":      "listConcat",
		"List.take":        "listTake",
		"List.drop":        "listDrop",
		"List.move":        "listMove",
		"List.member":      "listMember",
		"List.any":         "listAny",
		"List.all":         "listAll",
		"List.indexedMap":  "listIndexedMap",
		"List.repeat":      "listRepeat",
		"List.intersperse": "listIntersperse",
		"List.partition":   "listPartition",
		"List.concatMap":   "listConcatMap",
		"List.filterMap":   "listFilterMap",
		"List.maximum":     "listMaximum",
		"List.minimum":     "listMinimum",
		"List.sort":        "listSort",
		"List.sortBy":      "listSortBy",
		"List.sortWith":    "listSortWith",
		// String
		"String.length":     "stringLength",
		"String.contains":   "stringContains",
		"String.startsWith": "stringStartsWith",
		"String.endsWith":   "stringEndsWith",
		"String.fromInt":    "stringFromInt",
		"String.toInt":      "stringToInt",
		"String.fromFloat":  "stringFromFloat",
		"String.toFloat":    "stringToFloat",
		"String.toUpper":    "stringToUpper",
		"String.toLower":    "stringToLower",
		"String.split":      "stringSplit",
		"String.join":       "stringJoin",
		"String.trim":       "stringTrim",
		"String.replace":    "stringReplace",
		"String.repeat":     "stringRepeat",
		"String.padLeft":    "stringPadLeft",
		"String.padRight":   "stringPadRight",
		"String.indexes":    "stringIndexes",
		"String.toList":     "stringToList",
		"String.fromList":   "stringFromList",
		"String.cons":       "stringCons",
		"String.map":        "stringMap",
		"String.filter":     "stringFilter",
		"String.foldl":      "stringFoldl",
		"String.any":        "stringAny",
		// Char
		"Char.toCode":   "charToCode",
		"Char.fromCode": "charFromCode",
		"Char.isDigit":  "charIsDigit",
		"Char.isAlpha":  "charIsAlpha",
		"Char.isUpper":  "charIsUpper",
		"Char.isLower":  "charIsLower",
		"Char.toUpper":  "charToUpper",
		"Char.toLower":  "charToLower",
		// Maybe
		"Maybe.withDefault": "maybeWithDefault",
		"Maybe.map":         "maybeMap",
		"Maybe.andThen":     "maybeAndThen",
		"Maybe.map2":        "maybeMap2",
		"Maybe.map3":        "maybeMap3",
		"Maybe.andMap":      "maybeAndMap",
		"Maybe.filter":      "maybeFilter",
		// Result
		"Result.map":         "resultMap",
		"Result.andThen":     "resultAndThen",
		"Result.mapError":    "resultMapError",
		"Result.withDefault": "resultWithDefault",
		"Result.fromMaybe":   "resultFromMaybe",
		"Result.toMaybe":     "resultToMaybe",
		// Tuple
		"Tuple.first":     "tupleFirst",
		"Tuple.second":    "tupleSecond",
		"Tuple.pair":      "tuplePair",
		"Tuple.mapFirst":  "tupleMapFirst",
		"Tuple.mapSecond": "tupleMapSecond",
		"Tuple.mapBoth":   "tupleMapBoth",
		"Task.succeed":    "effectSucceed",
		"Task.fail":       "effectFail",
		"Task.map":        "effectMap",
		"Task.andThen":    "effectAndThen",
		"Task.forEach":    "effectForEach",
		"Task.sequence":   "effectSequence",
		"Cmd.batch":       "effectBatch",
		"Cmd.none":        "effectNone",
		"Cmd.perform":     "cmdPerform",
		"Sub.batch":       "subBatch",
		"Sub.none":        "subNone",
		"Random.generate": "randomGenerate",
		"Random.int":      "randomInt",
		"Random.uniform":  "randomUniform",
		"Random.constant": "randomConstant",
		"Random.list":     "randomList",
		"Random.pair":     "randomPair",
		"Random.map":      "randomMap",
		"Random.map2":     "randomMap2",
		"Random.map3":     "randomMap3",
		"Random.andThen":  "randomAndThen",
		"Time.seconds":    "timeSeconds",
		"Time.minutes":    "timeMinutes",
		"Time.hours":      "timeHours",
		"Time.days":       "timeDays",
		"Time.weeks":      "timeWeeks",
		"Time.toSeconds":  "timeToSeconds",
		"Time.now":        "timeNow",
		"Time.every":      "timeEvery",
		"Time.add":        "timeAdd",
		"Time.sub":        "timeSub",
		"Time.diff":       "timeDiff",
		"Time.before":     "timeBefore",
		"Time.after":      "timeAfter",
		"Time.toIso":      "timeToIso",
		"Time.fromIso":    "timeFromIso",
		"Time.toMillis":   "timeToMillis",
		"Time.fromYMD":    "timeFromYMD",
		"Time.addDays":    "timeAddDays",
		"Time.addMonths":  "timeAddMonths",
		"Time.addYears":   "timeAddYears",
		"Time.year":       "timeYear",
		"Time.month":      "timeMonth",
		"Time.day":        "timeDay",
		"Time.hour":       "timeHour",
		"Time.minute":     "timeMinute",
		"Time.second":     "timeSecond",
		"Http.get":        "httpGet",
		"Http.post":       "httpPost",
		"JSON.encode":     "jsonEncode",
		"JSON.decode":     "jsonDecode",
		// Entity (record-literal form)
		"Entity.define":    "entityDefine",
		"Entity.serial":    "entitySerial",
		"Entity.int":       "entityInt",
		"Entity.text":      "entityText",
		"Entity.bool":      "entityBool",
		"Entity.enum":      "entityEnum",
		"Entity.timestamp": "entityTimestamp",
		"Entity.notNull":   "entityNotNull",
		// Repo
		"Repo.all":                   "repoAll",
		"Repo.findById":              "repoFindByID",
		"Repo.findBy":                "repoFindBy",
		"Repo.create":                "repoCreate",
		"Repo.update":                "repoUpdate",
		"Repo.deleteById":            "repoDeleteByID",
		"App.frontend":               "appFrontend",
		"App.backend":                "appBackend",
		"App.fullstack":              "appFullstack",
		"Page.create":                "pageCreate",
		"Page.protected":             "pageProtected",
		"Page.adminProtected":        "pageAdminProtected",
		"Page.dynamic":               "pageDynamic",
		"Page.dynamicProtected":      "pageDynamicProtected",
		"Page.dynamicAdminProtected": "pageDynamicAdminProtected",
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
		"Auth.completeSignIn":        "authCompleteSignIn",
		"Nav.pushTo":                 "navPushTo",
		"Nav.replaceTo":              "navReplaceTo",
		"linkTo":                     "linkTo",
		"Service.declare":            "serviceDeclare",
		"Service.implement":          "serviceImplement",
		"Service.call":               "serviceCall",
		"Service.errorToString":      "serviceErrorToString",
		// Auth (passwordless email-code authentication)
		"Auth.config":       "authConfig",
		"Auth.protect":      "authProtect",
		"Auth.requireRole":  "authRequireRole",
		"Auth.authorize":    "authAuthorize",
		"Auth.requireOwner": "authRequireOwner",
		"Auth.requestCode":  "authRequestCode",
		"Auth.verifyCode":   "authVerifyCode",
		"Auth.logout":       "authLogout",
		"Auth.me":           "authMe",
		// UI module: SwiftUI-style declarative vocabulary. The flat
		// names (uiText / navigationStack / etc.) are already bound by
		// viewBuiltins(); these aliases make `UI.text` and friends
		// resolvable too, so `import UI exposing (text)` works the
		// same way it does in the typecheck env, JS runtime, and iOS
		// loader. Without these, an `import UI exposing (text)`
		// typechecks but explodes at runtime: project.loadIntoEnv
		// looks up `UI.text`, finds nothing, and never binds bare
		// `text`.
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
		"UI.chars":           "uiChars",
		"UI.lines":           "uiLines",
		"UI.fill":            "uiFill",
		"UI.width":           "uiWidth",
		"UI.height":          "uiHeight",
		"UI.align":           "uiAlign",
		"UI.leading":         "uiLeading",
		"UI.center":          "uiCenter",
		"UI.trailing":        "uiTrailing",
		"UI.top":             "uiTop",
		"UI.bottom":          "uiBottom",
		"UI.px":              "uiPx",
		"UI.size":            "uiSize",
		"UI.fit":             "uiFit",
		"UI.cover":           "uiCover",
		"UI.navigationLink":  "uiNavigationLink",
		"UI.spacer":          "uiSpacer",
		"UI.toggle":          "uiToggle",
		"UI.sheet":           "uiSheet",
		"UI.confirm":         "uiConfirm",
		"UI.empty":           "uiEmpty",
		"UI.centered":        "uiCentered",
		"UI.email":           "viewEmail",
		"UI.password":        "viewPassword",
		"UI.newPassword":     "viewNewPassword",
		"UI.numeric":         "viewNumeric",
		"UI.oneTimeCode":     "viewOneTimeCode",
		"UI.submit":          "viewSubmit",
		// Dict: Elm-style polymorphic ordered map. Keys must be a
		// comparable type at runtime (Int / Float / String). Sorted
		// internally so toList / keys / values iteration is stable.
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
		// Set: Dict-but-keys-only. Same comparable-key constraint.
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
}

func builtins() map[string]Value {
	return map[string]Value{
		// Bool literals
		"True":  VBool{V: true},
		"False": VBool{V: false},

		// Maybe constructors
		"Nothing": VCtor{Tag: "Nothing"},
		"Just": nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "Just", Args: []Value{args[0]}}, nil
		}),

		// Result constructors
		"Ok": nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "Ok", Args: []Value{args[0]}}, nil
		}),
		"Err": nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "Err", Args: []Value{args[0]}}, nil
		}),

		// Order constructors — nullary, like Nothing. Mirrors Elm's
		// Order type for List.sortWith comparators.
		"LT": VCtor{Tag: "LT"},
		"EQ": VCtor{Tag: "EQ"},
		"GT": VCtor{Tag: "GT"},

		// Method constructors — the HTTP verbs, nullary. The first
		// argument to Service.declare; stored on the contract so the
		// server mounts and the client calls on the right verb.
		"GET":    VCtor{Tag: "GET"},
		"POST":   VCtor{Tag: "POST"},
		"PUT":    VCtor{Tag: "PUT"},
		"PATCH":  VCtor{Tag: "PATCH"},
		"DELETE": VCtor{Tag: "DELETE"},

		// Service.Error constructors — the transport failure a Service.call
		// delivers in its Err. The frontend builds these (JS/Swift HTTP
		// clients); the Go runtime registers them so the values exist and
		// Service.errorToString can fold them to a string.
		"Service.Offline":      VCtor{Tag: "Offline"},
		"Service.Unauthorized": VCtor{Tag: "Unauthorized"},
		"Service.ServerError": nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "ServerError", Args: []Value{args[0]}}, nil
		}),

		// Auth outcome constructors — qualified-only, like Service.Error.
		// The JS/Swift HTTP clients build these at the auth boundary; the
		// Go runtime registers them so the values exist everywhere.
		"Auth.CodeSent":        VCtor{Tag: "CodeSent"},
		"Auth.InvalidEmail":    VCtor{Tag: "InvalidEmail"},
		"Auth.RateLimited":     VCtor{Tag: "RateLimited"},
		"Auth.WrongCode":       VCtor{Tag: "WrongCode"},
		"Auth.TooManyAttempts": VCtor{Tag: "TooManyAttempts"},
		"Auth.SignedIn": nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "SignedIn", Args: []Value{args[0]}}, nil
		}),
		"serviceErrorToString": nativeFn(1, func(args []Value) (Value, error) {
			return VString{V: serviceErrorString(args[0])}, nil
		}),

		// Arithmetic
		"+": nativeFn(2, addOp),
		"-": nativeFn(2, subOp),
		"*": nativeFn(2, mulOp),
		"/": nativeFn(2, divOp),

		// Comparison
		"==": nativeFn(2, eqOp),
		"/=": nativeFn(2, neqOp),
		"<":  nativeFn(2, ltOp),
		">":  nativeFn(2, gtOp),
		"<=": nativeFn(2, lteOp),
		">=": nativeFn(2, gteOp),

		// Logic
		"&&": nativeFn(2, andOp),
		"||": nativeFn(2, orOp),

		// String/list append
		"++": nativeFn(2, appendOp),

		// Cons: a -> List a -> List a
		"::": nativeFn(2, func(args []Value) (Value, error) {
			head := args[0]
			tail, ok := args[1].(VList)
			if !ok {
				return nil, fmt.Errorf("::: tail not a list")
			}
			out := make([]Value, 0, len(tail.Elements)+1)
			out = append(out, head)
			out = append(out, tail.Elements...)
			return VList{Elements: out}, nil
		}),

		// Pipes
		"|>": nativeFn(2, func(args []Value) (Value, error) {
			// x |> f = f x
			return apply(args[1], args[0])
		}),
		"<|": nativeFn(2, func(args []Value) (Value, error) {
			// f <| x = f x
			return apply(args[0], args[1])
		}),
	}
}

func nativeFn(arity int, fn func([]Value) (Value, error)) VFn {
	return VFn{Native: fn, Arity: arity}
}

// --- arithmetic ---

func addOp(args []Value) (Value, error) {
	switch a := args[0].(type) {
	case VInt:
		b, ok := args[1].(VInt)
		if !ok {
			return nil, fmt.Errorf("+: expected Int")
		}
		return VInt{V: a.V + b.V}, nil
	case VFloat:
		b, ok := args[1].(VFloat)
		if !ok {
			return nil, fmt.Errorf("+: expected Float")
		}
		return VFloat{V: a.V + b.V}, nil
	}
	return nil, fmt.Errorf("+: unsupported types")
}

func subOp(args []Value) (Value, error) {
	switch a := args[0].(type) {
	case VInt:
		b, ok := args[1].(VInt)
		if !ok {
			return nil, fmt.Errorf("-: expected Int")
		}
		return VInt{V: a.V - b.V}, nil
	case VFloat:
		b, ok := args[1].(VFloat)
		if !ok {
			return nil, fmt.Errorf("-: expected Float")
		}
		return VFloat{V: a.V - b.V}, nil
	}
	return nil, fmt.Errorf("-: unsupported")
}

func mulOp(args []Value) (Value, error) {
	switch a := args[0].(type) {
	case VInt:
		b, ok := args[1].(VInt)
		if !ok {
			return nil, fmt.Errorf("*: expected Int")
		}
		return VInt{V: a.V * b.V}, nil
	case VFloat:
		b, ok := args[1].(VFloat)
		if !ok {
			return nil, fmt.Errorf("*: expected Float")
		}
		return VFloat{V: a.V * b.V}, nil
	}
	return nil, fmt.Errorf("*: unsupported")
}

func divOp(args []Value) (Value, error) {
	switch a := args[0].(type) {
	case VInt:
		b, ok := args[1].(VInt)
		if !ok {
			return nil, fmt.Errorf("/: expected Int")
		}
		// Integer division is total: dividing by zero yields 0. This
		// matches the web (runtime.js) and iOS runtimes (both return 0)
		// and Elm's `//`. Erroring here would diverge by platform — the
		// server would 500 while the client returns 0 — and there is no
		// error channel in pure client-side eval.
		if b.V == 0 {
			return VInt{V: 0}, nil
		}
		return VInt{V: a.V / b.V}, nil
	case VFloat:
		b, ok := args[1].(VFloat)
		if !ok {
			return nil, fmt.Errorf("/: expected Float")
		}
		return VFloat{V: a.V / b.V}, nil
	}
	return nil, fmt.Errorf("/: unsupported")
}

// --- comparison ---

func eqOp(args []Value) (Value, error) {
	return VBool{V: equalValues(args[0], args[1])}, nil
}

func neqOp(args []Value) (Value, error) {
	return VBool{V: !equalValues(args[0], args[1])}, nil
}

func ltOp(args []Value) (Value, error) {
	c, err := compareValues(args[0], args[1])
	if err != nil {
		return nil, err
	}
	return VBool{V: c < 0}, nil
}

func gtOp(args []Value) (Value, error) {
	c, err := compareValues(args[0], args[1])
	if err != nil {
		return nil, err
	}
	return VBool{V: c > 0}, nil
}

func lteOp(args []Value) (Value, error) {
	c, err := compareValues(args[0], args[1])
	if err != nil {
		return nil, err
	}
	return VBool{V: c <= 0}, nil
}

func gteOp(args []Value) (Value, error) {
	c, err := compareValues(args[0], args[1])
	if err != nil {
		return nil, err
	}
	return VBool{V: c >= 0}, nil
}

func equalValues(a, b Value) bool {
	switch av := a.(type) {
	case VInt:
		bv, ok := b.(VInt)
		return ok && av.V == bv.V
	case VFloat:
		bv, ok := b.(VFloat)
		return ok && av.V == bv.V
	case VString:
		bv, ok := b.(VString)
		return ok && av.V == bv.V
	case VChar:
		bv, ok := b.(VChar)
		return ok && av.V == bv.V
	case VBool:
		bv, ok := b.(VBool)
		return ok && av.V == bv.V
	case VUnit:
		_, ok := b.(VUnit)
		return ok
	case VCtor:
		bv, ok := b.(VCtor)
		if !ok || av.Tag != bv.Tag || len(av.Args) != len(bv.Args) {
			return false
		}
		for i := range av.Args {
			if !equalValues(av.Args[i], bv.Args[i]) {
				return false
			}
		}
		return true
	case VTuple:
		bv, ok := b.(VTuple)
		if !ok || len(av.Members) != len(bv.Members) {
			return false
		}
		for i := range av.Members {
			if !equalValues(av.Members[i], bv.Members[i]) {
				return false
			}
		}
		return true
	case VList:
		bv, ok := b.(VList)
		if !ok || len(av.Elements) != len(bv.Elements) {
			return false
		}
		for i := range av.Elements {
			if !equalValues(av.Elements[i], bv.Elements[i]) {
				return false
			}
		}
		return true
	case VRecord:
		bv, ok := b.(VRecord)
		if !ok || len(av.Fields) != len(bv.Fields) {
			return false
		}
		for n, v := range av.Fields {
			bf, ok := bv.Fields[n]
			if !ok || !equalValues(v, bf) {
				return false
			}
		}
		return true
	}
	return false
}

func compareValues(a, b Value) (int, error) {
	switch av := a.(type) {
	case VInt:
		bv, ok := b.(VInt)
		if !ok {
			return 0, fmt.Errorf("comparison: type mismatch")
		}
		switch {
		case av.V < bv.V:
			return -1, nil
		case av.V > bv.V:
			return 1, nil
		}
		return 0, nil
	case VFloat:
		bv, ok := b.(VFloat)
		if !ok {
			return 0, fmt.Errorf("comparison: type mismatch")
		}
		switch {
		case av.V < bv.V:
			return -1, nil
		case av.V > bv.V:
			return 1, nil
		}
		return 0, nil
	case VString:
		bv, ok := b.(VString)
		if !ok {
			return 0, fmt.Errorf("comparison: type mismatch")
		}
		switch {
		case av.V < bv.V:
			return -1, nil
		case av.V > bv.V:
			return 1, nil
		}
		return 0, nil
	case VChar:
		bv, ok := b.(VChar)
		if !ok {
			return 0, fmt.Errorf("comparison: type mismatch")
		}
		switch {
		case av.V < bv.V:
			return -1, nil
		case av.V > bv.V:
			return 1, nil
		}
		return 0, nil
	}
	return 0, fmt.Errorf("comparison: unsupported types")
}

// --- logic / strings ---

func andOp(args []Value) (Value, error) {
	a, ok := args[0].(VBool)
	b, ok2 := args[1].(VBool)
	if !ok || !ok2 {
		return nil, fmt.Errorf("&&: expected Bool")
	}
	return VBool{V: a.V && b.V}, nil
}

func orOp(args []Value) (Value, error) {
	a, ok := args[0].(VBool)
	b, ok2 := args[1].(VBool)
	if !ok || !ok2 {
		return nil, fmt.Errorf("||: expected Bool")
	}
	return VBool{V: a.V || b.V}, nil
}

func appendOp(args []Value) (Value, error) {
	switch a := args[0].(type) {
	case VString:
		b, ok := args[1].(VString)
		if !ok {
			return nil, fmt.Errorf("++: expected String")
		}
		return VString{V: a.V + b.V}, nil
	case VList:
		b, ok := args[1].(VList)
		if !ok {
			return nil, fmt.Errorf("++: expected List")
		}
		out := make([]Value, 0, len(a.Elements)+len(b.Elements))
		out = append(out, a.Elements...)
		out = append(out, b.Elements...)
		return VList{Elements: out}, nil
	}
	return nil, fmt.Errorf("++: unsupported")
}
