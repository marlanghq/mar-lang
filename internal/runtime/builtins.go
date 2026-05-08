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
	for name, v := range ioBuiltins() {
		env.Define(name, v)
	}
	for name, v := range jsonBuiltins() {
		env.Define(name, v)
	}
	for name, v := range serverBuiltins() {
		env.Define(name, v)
	}
	for name, v := range entityBuiltins() {
		env.Define(name, v)
	}
	for name, v := range repoBuiltins() {
		env.Define(name, v)
	}
	for name, v := range endpointBuiltins() {
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
	env = extendBaseEnv(env)
	// Register qualified aliases (List.map etc.) that point to the same values.
	for q, f := range qualifiedAliasMapping() {
		if v, ok := env.Lookup(f); ok {
			env.Define(q, v)
		}
	}
	return env
}

// qualifiedAliasMapping returns Module.name -> flat name for stdlib aliases.
func qualifiedAliasMapping() map[string]string {
	return map[string]string{
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
		// Low-level endpoint builders + Endpoint.implement.
		"Endpoint.get":       "endpointGet",
		"Endpoint.post":      "endpointPost",
		"Endpoint.implement": "endpointImplement",
		"Endpoint.call":      "endpointCall",
		// REST sugar.
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
		"Entity.enum":      "entityEnum",
		"Entity.timestamp": "entityTimestamp",
		"Entity.notNull": "entityNotNull",
		// Repo
		"Repo.all":        "repoAll",
		"Repo.findById":   "repoFindByID",
		"Repo.findBy":     "repoFindBy",
		"Repo.create":     "repoCreate",
		"Repo.update":     "repoUpdate",
		"Repo.deleteById": "repoDeleteByID",
		"App.frontend":      "appFrontend",
		"App.backend":       "appBackend",
		"App.fullstack":     "appFullstack",
		"Page.create":       "pageCreate",
		"Page.protected":         "pageProtected",
		"Page.dynamic":           "pageDynamic",
		"Page.dynamicProtected":  "pageDynamicProtected",
		"Nav.push":          "navPush",
		"Nav.replace":       "navReplace",
		"Nav.afterSignIn":   "navAfterSignIn",
		"Service.declare":   "serviceDeclare",
		"Service.implement": "serviceImplement",
		"Service.call":      "serviceCall",
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
	}
}

func builtins() map[string]Value {
	return map[string]Value{
		// Bool literals
		"True":  VBool{V: true},
		"False": VBool{V: false},

		// Maybe constructors
		"Nothing": VCtor{Tag: "Nothing"},
		"Just":    nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "Just", Args: []Value{args[0]}}, nil
		}),

		// Result constructors
		"Ok": nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "Ok", Args: []Value{args[0]}}, nil
		}),
		"Err": nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "Err", Args: []Value{args[0]}}, nil
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
		b := args[1].(VInt)
		return VInt{V: a.V - b.V}, nil
	case VFloat:
		b := args[1].(VFloat)
		return VFloat{V: a.V - b.V}, nil
	}
	return nil, fmt.Errorf("-: unsupported")
}

func mulOp(args []Value) (Value, error) {
	switch a := args[0].(type) {
	case VInt:
		b := args[1].(VInt)
		return VInt{V: a.V * b.V}, nil
	case VFloat:
		b := args[1].(VFloat)
		return VFloat{V: a.V * b.V}, nil
	}
	return nil, fmt.Errorf("*: unsupported")
}

func divOp(args []Value) (Value, error) {
	switch a := args[0].(type) {
	case VInt:
		b := args[1].(VInt)
		if b.V == 0 {
			return nil, fmt.Errorf("/: division by zero")
		}
		return VInt{V: a.V / b.V}, nil
	case VFloat:
		b := args[1].(VFloat)
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
		bv := b.(VInt)
		switch {
		case av.V < bv.V:
			return -1, nil
		case av.V > bv.V:
			return 1, nil
		}
		return 0, nil
	case VFloat:
		bv := b.(VFloat)
		switch {
		case av.V < bv.V:
			return -1, nil
		case av.V > bv.V:
			return 1, nil
		}
		return 0, nil
	case VString:
		bv := b.(VString)
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
	a, b := args[0].(VBool), args[1].(VBool)
	return VBool{V: a.V && b.V}, nil
}

func orOp(args []Value) (Value, error) {
	a, b := args[0].(VBool), args[1].(VBool)
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
