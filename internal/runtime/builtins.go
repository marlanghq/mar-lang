package runtime

import (
	"fmt"

	"mar/internal/stdnames"
)

// BaseEnv returns the initial runtime environment with all built-ins bound.
func BaseEnv() *Env {
	env := NewEnv()
	// Generated from the typecheck union tables: see internal/ctorgen. It
	// goes in first so a hand-written binding below still wins if one ever
	// needs to; the generator is the source of truth for the SET.
	for name, v := range builtinCtors() {
		env.Define(name, v)
	}
	for name, v := range builtins() {
		env.Define(name, v)
	}
	for name, v := range effectBuiltins() {
		env.Define(name, v)
	}
	for name, v := range subBuiltins() {
		env.Define(name, v)
	}
	for name, v := range soundBuiltins() {
		env.Define(name, v)
	}
	for name, v := range deviceBuiltins() {
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
	for name, v := range decimalBuiltins() {
		env.Define(name, v)
	}
	for name, v := range mathBuiltins() {
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
// that compare this set against typecheck.BaseQualifiedSymbols:
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
	return stdnames.QualifiedAliases()
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

		// Order constructors: nullary, like Nothing. Mirrors Elm's
		// Order type for List.sortWith comparators.
		"LT": VCtor{Tag: "LT"},
		"EQ": VCtor{Tag: "EQ"},
		"GT": VCtor{Tag: "GT"},

		// Method constructors: the HTTP verbs, nullary. The first
		// argument to Service.declare; stored on the contract so the
		// server mounts and the client calls on the right verb.
		"GET":    VCtor{Tag: "GET"},
		"POST":   VCtor{Tag: "POST"},
		"PUT":    VCtor{Tag: "PUT"},
		"PATCH":  VCtor{Tag: "PATCH"},
		"DELETE": VCtor{Tag: "DELETE"},

		// Service.Error constructors: the transport failure a Service.call
		// delivers in its Err. The frontend builds these (JS/Swift HTTP
		// clients); the Go runtime registers them so the values exist and
		// Service.errorToString can fold them to a string.
		"Service.Offline":      VCtor{Tag: "Offline"},
		"Service.Unauthorized": VCtor{Tag: "Unauthorized"},
		"Service.RateLimited":  VCtor{Tag: "RateLimited"},
		"Service.ServerError": nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "ServerError", Args: []Value{args[0]}}, nil
		}),

		// Auth outcome constructors: qualified-only, like Service.Error.
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

		// Canvas Transform / Align constructors: qualified-only (used as
		// `Canvas.Translate`, `Canvas.Center`, ...). The runtime tag is the
		// bare ctor name, matching how the renderers read draw-list nodes.
		"Canvas.Translate": nativeFn(2, func(args []Value) (Value, error) {
			return VCtor{Tag: "Translate", Args: []Value{args[0], args[1]}}, nil
		}),
		"Canvas.Scale": nativeFn(2, func(args []Value) (Value, error) {
			return VCtor{Tag: "Scale", Args: []Value{args[0], args[1]}}, nil
		}),
		"Canvas.Rotate": nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "Rotate", Args: []Value{args[0]}}, nil
		}),
		"Canvas.Alpha": nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "Alpha", Args: []Value{args[0]}}, nil
		}),
		"Canvas.Blend": nativeFn(1, func(args []Value) (Value, error) {
			return VCtor{Tag: "Blend", Args: []Value{args[0]}}, nil
		}),
		"Canvas.Normal":   VCtor{Tag: "Normal"},
		"Canvas.Add":      VCtor{Tag: "Add"},
		"Canvas.Multiply": VCtor{Tag: "Multiply"},
		"Canvas.Screen":   VCtor{Tag: "Screen"},
		"Canvas.Erase":    VCtor{Tag: "Erase"},
		"Canvas.Left":     VCtor{Tag: "Left"},
		"Canvas.Center":   VCtor{Tag: "Center"},
		"Canvas.Right":    VCtor{Tag: "Right"},

		// Decimal.Rounding constructors: qualified-only
		// (`Decimal.HalfEven`), consumed by the Decimal resolvers.
		"Decimal.HalfEven": VCtor{Tag: "HalfEven"},
		"Decimal.HalfUp":   VCtor{Tag: "HalfUp"},
		"Decimal.Down":     VCtor{Tag: "Down"},
		"Decimal.Up":       VCtor{Tag: "Up"},
		"Decimal.Floor":    VCtor{Tag: "Floor"},
		"Decimal.Ceiling":  VCtor{Tag: "Ceiling"},

		"serviceErrorToString": nativeFn(1, func(args []Value) (Value, error) {
			return VString{V: serviceErrorString(args[0])}, nil
		}),

		// Arithmetic
		"+":  nativeFn(2, addOp),
		"-":  nativeFn(2, subOp),
		"*":  nativeFn(2, mulOp),
		"//": nativeFn(2, intDivOp),
		"/":  nativeFn(2, divOp),

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
		"::": nativeFn(2, func(args []Value) (Value, error) { return cons2(args[0], args[1]) }),

		// Pipes
		"|>": nativeFn(2, func(args []Value) (Value, error) { return pipeForward2(args[0], args[1]) }),
		"<|": nativeFn(2, func(args []Value) (Value, error) { return apply(args[0], args[1]) }),
	}
}

// binopImpl maps an operator to its implementation, or nil for a name that is
// not one: the same Go functions the entries above wrap, taking the two
// operands directly so that evaluating `a + b` needs neither an argument slice
// nor a walk of the environment to find out what `+` means. `&&` and `||` are
// not here: the evaluator short-circuits them itself.
//
// A switch rather than a map: it compiles to a couple of comparisons, and this
// runs once per operator evaluated.
func binopImpl(op string) func(a, b Value) (Value, error) {
	switch op {
	case "+":
		return add2
	case "-":
		return sub2
	case "*":
		return mul2
	case "//":
		return intDiv2
	case "/":
		return div2
	case "==":
		return eq2
	case "/=":
		return neq2
	case "<":
		return lt2
	case ">":
		return gt2
	case "<=":
		return lte2
	case ">=":
		return gte2
	case "++":
		return append2
	case "::":
		return cons2
	case "|>":
		return pipeForward2
	case "<|":
		// f <| x = f x
		return apply
	}
	return nil
}

// cons2: a -> List a -> List a
func cons2(head, rest Value) (Value, error) {
	tail, ok := rest.(VList)
	if !ok {
		return nil, fmt.Errorf("::: tail not a list")
	}
	out := make([]Value, 0, len(tail.Elements)+1)
	out = append(out, head)
	out = append(out, tail.Elements...)
	return VList{Elements: out}, nil
}

// pipeForward2: x |> f = f x
func pipeForward2(x, f Value) (Value, error) { return apply(f, x) }

func nativeFn(arity int, fn func([]Value) (Value, error)) VFn {
	return VFn{Native: fn, Arity: arity}
}

// --- arithmetic ---

func addOp(args []Value) (Value, error) { return add2(args[0], args[1]) }

func add2(a0, a1 Value) (Value, error) {
	switch a := a0.(type) {
	case VInt:
		b, ok := a1.(VInt)
		if !ok {
			return nil, fmt.Errorf("+: expected Int")
		}
		return checkedVInt(addInts(a.V, b.V))
	case VDecimal:
		b, ok := a1.(VDecimal)
		if !ok {
			return nil, fmt.Errorf("+: expected Decimal")
		}
		return decAdd(a, b)
	}
	return nil, fmt.Errorf("+: unsupported types")
}

func subOp(args []Value) (Value, error) { return sub2(args[0], args[1]) }

func sub2(a0, a1 Value) (Value, error) {
	switch a := a0.(type) {
	case VInt:
		b, ok := a1.(VInt)
		if !ok {
			return nil, fmt.Errorf("-: expected Int")
		}
		return checkedVInt(subInts(a.V, b.V))
	case VDecimal:
		b, ok := a1.(VDecimal)
		if !ok {
			return nil, fmt.Errorf("-: expected Decimal")
		}
		return decSub(a, b)
	}
	return nil, fmt.Errorf("-: unsupported")
}

func mulOp(args []Value) (Value, error) { return mul2(args[0], args[1]) }

func mul2(a0, a1 Value) (Value, error) {
	switch a := a0.(type) {
	case VInt:
		b, ok := a1.(VInt)
		if !ok {
			return nil, fmt.Errorf("*: expected Int")
		}
		return checkedVInt(mulInts(a.V, b.V))
	case VDecimal:
		b, ok := a1.(VDecimal)
		if !ok {
			return nil, fmt.Errorf("*: expected Decimal")
		}
		return decMul(a, b)
	}
	return nil, fmt.Errorf("*: unsupported")
}

// intDivOp: `//`, truncating integer division wearing its loss in
// its spelling (Elm's `//`).
func intDivOp(args []Value) (Value, error) { return intDiv2(args[0], args[1]) }

func intDiv2(a0, a1 Value) (Value, error) {
	a, ok := a0.(VInt)
	if !ok {
		return nil, fmt.Errorf("//: expected Int")
	}
	b, ok := a1.(VInt)
	if !ok {
		return nil, fmt.Errorf("//: expected Int")
	}
	// Integer division is total: dividing by zero yields 0. This
	// matches the web (runtime.js) and iOS runtimes (both return 0)
	// and Elm's `//`. Erroring here would diverge by platform: the
	// server would 500 while the client returns 0, and there is no
	// error channel in pure client-side eval.
	if b.V == 0 {
		return VInt{V: 0}, nil
	}
	return VInt{V: a.V / b.V}, nil
}

// divOp, `/`, Decimal-only. Produces a QUESTION, not a number: the
// inert exact quotient, resolved only by Decimal.rounded /
// withRemainder, which is where the precision gets written.
func divOp(args []Value) (Value, error) { return div2(args[0], args[1]) }

func div2(a0, a1 Value) (Value, error) {
	a, ok := a0.(VDecimal)
	if !ok {
		return nil, fmt.Errorf("/: expected Decimal")
	}
	b, ok := a1.(VDecimal)
	if !ok {
		return nil, fmt.Errorf("/: expected Decimal")
	}
	return VDivision{Num: a, Den: b}, nil
}

// --- comparison ---

func eqOp(args []Value) (Value, error) { return eq2(args[0], args[1]) }

func eq2(a0, a1 Value) (Value, error) {
	return VBool{V: equalValues(a0, a1)}, nil
}

func neqOp(args []Value) (Value, error) { return neq2(args[0], args[1]) }

func neq2(a0, a1 Value) (Value, error) {
	return VBool{V: !equalValues(a0, a1)}, nil
}

func ltOp(args []Value) (Value, error) { return lt2(args[0], args[1]) }

func lt2(a0, a1 Value) (Value, error) {
	c, err := compareValues(a0, a1)
	if err != nil {
		return nil, err
	}
	return VBool{V: c < 0}, nil
}

func gtOp(args []Value) (Value, error) { return gt2(args[0], args[1]) }

func gt2(a0, a1 Value) (Value, error) {
	c, err := compareValues(a0, a1)
	if err != nil {
		return nil, err
	}
	return VBool{V: c > 0}, nil
}

func lteOp(args []Value) (Value, error) { return lte2(args[0], args[1]) }

func lte2(a0, a1 Value) (Value, error) {
	c, err := compareValues(a0, a1)
	if err != nil {
		return nil, err
	}
	return VBool{V: c <= 0}, nil
}

func gteOp(args []Value) (Value, error) { return gte2(args[0], args[1]) }

func gte2(a0, a1 Value) (Value, error) {
	c, err := compareValues(a0, a1)
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
	case VDecimal:
		bv, ok := b.(VDecimal)
		// Numeric equality: 1.50 == 1.5 (scale is display metadata).
		return ok && decCompare(av, bv) == 0
	case VString:
		bv, ok := b.(VString)
		return ok && av.V == bv.V
	case VChar:
		bv, ok := b.(VChar)
		return ok && av.V == bv.V
	case VAngle:
		// Constructed values are always wrapped into 0..3599, so equality
		// on the representation IS equality of the rotation. An Angle sits
		// in models, a heading, a turret facing, and models go through
		// time travel, App.shared and (in lendas) a rules engine replayed
		// on both sides, so this arm is load-bearing rather than decorative.
		bv, ok := b.(VAngle)
		return ok && av.Deci == bv.Deci
	case VDuration:
		// A Duration is milliseconds, so Time.seconds 60 and
		// Time.minutes 1 ARE the same interval and compare equal.
		bv, ok := b.(VDuration)
		return ok && av.Millis == bv.Millis
	case VTime:
		// Same moment = same Unix millisecond. A Time sits in models
		// and in entity rows (Entity.time), so this arm is reached by
		// `==`, by List.member, and by every container case below.
		bv, ok := b.(VTime)
		return ok && av.Millis == bv.Millis
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
	case VDict:
		// Pairs are kept sorted by key, so equal dicts are equal
		// pairwise in order: no set-difference walk needed. These two
		// arms were missing HERE ONLY: the JS and Swift runtimes have
		// always compared dicts and sets structurally, so the same
		// program answered True in the browser and on iOS and False on
		// the server. Same shape as the record-equality gap, with the
		// runtimes swapped.
		bv, ok := b.(VDict)
		if !ok || len(av.Pairs) != len(bv.Pairs) {
			return false
		}
		for i := range av.Pairs {
			if !equalValues(av.Pairs[i].Key, bv.Pairs[i].Key) {
				return false
			}
			if !equalValues(av.Pairs[i].Value, bv.Pairs[i].Value) {
				return false
			}
		}
		return true
	case VSet:
		bv, ok := b.(VSet)
		if !ok || len(av.Items) != len(bv.Items) {
			return false
		}
		for i := range av.Items {
			if !equalValues(av.Items[i], bv.Items[i]) {
				return false
			}
		}
		return true
	}
	// Reached only by VFn (functions have no equality) and VDivision
	// (an unresolved quotient is opaque by design: see VDivision).
	// Everything else in value.go has an arm above; if you add a value
	// kind, add it here too, or `==` will quietly answer False for it.
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
	case VDecimal:
		bv, ok := b.(VDecimal)
		if !ok {
			return 0, fmt.Errorf("comparison: type mismatch")
		}
		return decCompare(av, bv), nil
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
	case VTuple:
		bv, ok := b.(VTuple)
		if !ok || len(av.Members) != len(bv.Members) {
			return 0, fmt.Errorf("comparison: type mismatch")
		}
		// Component-wise, left to right, first difference wins. Two
		// tuples that compare here always have the same arity: different
		// arities are different types and never unify.
		for i := range av.Members {
			c, err := compareValues(av.Members[i], bv.Members[i])
			if err != nil {
				return 0, err
			}
			if c != 0 {
				return c, nil
			}
		}
		return 0, nil
	case VList:
		bv, ok := b.(VList)
		if !ok {
			return 0, fmt.Errorf("comparison: type mismatch")
		}
		// Lexicographic, and a proper prefix is less than what extends
		// it: [1] < [1,2]. Same rule a dictionary uses for words.
		n := len(av.Elements)
		if len(bv.Elements) < n {
			n = len(bv.Elements)
		}
		for i := 0; i < n; i++ {
			c, err := compareValues(av.Elements[i], bv.Elements[i])
			if err != nil {
				return 0, err
			}
			if c != 0 {
				return c, nil
			}
		}
		switch {
		case len(av.Elements) < len(bv.Elements):
			return -1, nil
		case len(av.Elements) > len(bv.Elements):
			return 1, nil
		}
		return 0, nil
	}
	// Records, custom types and functions have no order, and the type
	// checker's Comparable constraint is what keeps them from arriving
	// here. This stays an error rather than an arbitrary 0: a value kind
	// added later must fail loudly rather than quietly claim equality,
	// which is what Dict and Set would read it as.
	return 0, fmt.Errorf("comparison: unsupported types")
}

// --- logic / strings ---

// andOp and orOp are STRICT, and the evaluator never reaches them for a written
// `&&` or `||`: evalBinop short-circuits those itself, before the right operand
// is evaluated, without looking either up. They stay so that BaseEnv still
// binds every operator a program can write.
//
// Mar has no parenthesised-operator syntax, so `(&&)` cannot be passed as a
// value and there is no route by which a program reaches the strict version.
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

func appendOp(args []Value) (Value, error) { return append2(args[0], args[1]) }

func append2(a0, a1 Value) (Value, error) {
	switch a := a0.(type) {
	case VString:
		b, ok := a1.(VString)
		if !ok {
			return nil, fmt.Errorf("++: expected String")
		}
		return VString{V: a.V + b.V}, nil
	case VList:
		b, ok := a1.(VList)
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
