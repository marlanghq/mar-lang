package types

// BaseEnv returns the initial TypeEnv populated with all mar-lang builtins.
//
// Each builtin is stored as a TForall scheme so that instantiation gives
// fresh variables at every use site — this is how `map`, `filter`, etc. become
// genuinely polymorphic across user code.
//
// Builtins with overloaded numeric behavior (`+`, `-`, `*`, `/`, `>`, `>=`,
// `<`, `<=`) are NOT in BaseEnv; they are special-cased in the inferer
// because HM clássico has no type classes. Same for `length` (string or list)
// and `string_append` (variádico). See DESIGN.md.
func BaseEnv() *TypeEnv {
	env := NewTypeEnv()

	// Polymorphic helpers: each entry is generated with a fresh α (and β/etc.)
	// at module load time and then quantified, so every call site of, say,
	// `map`, gets a fresh instantiation.
	env = env.Bind("nothing", forall1(func(a TVar) Type { return TMaybe(a) }))
	env = env.Bind("just", forall1(func(a TVar) Type { return TArrow([]Type{a}, TMaybe(a)) }))

	env = env.Bind("ok", forall2(func(e, a TVar) Type {
		return TArrow([]Type{a}, TResult(e, a))
	}))
	env = env.Bind("err", forall2(func(e, a TVar) Type {
		return TArrow([]Type{e}, TResult(e, a))
	}))

	env = env.Bind("cons", forall1(func(a TVar) Type {
		return TArrow([]Type{a, TList(a)}, TList(a))
	}))
	env = env.Bind("first", forall1(func(a TVar) Type {
		return TArrow([]Type{TList(a)}, TMaybe(a))
	}))
	env = env.Bind("rest", forall1(func(a TVar) Type {
		return TArrow([]Type{TList(a)}, TList(a))
	}))
	env = env.Bind("empty?", forall1(func(a TVar) Type {
		return TArrow([]Type{TList(a)}, TBool())
	}))

	env = env.Bind("map", forall2(func(a, b TVar) Type {
		return TArrow([]Type{TArrow([]Type{a}, b), TList(a)}, TList(b))
	}))
	env = env.Bind("filter", forall1(func(a TVar) Type {
		return TArrow([]Type{TArrow([]Type{a}, TBool()), TList(a)}, TList(a))
	}))
	env = env.Bind("fold_left", forall2(func(a, b TVar) Type {
		return TArrow([]Type{TArrow([]Type{b, a}, b), b, TList(a)}, b)
	}))
	env = env.Bind("fold_right", forall2(func(a, b TVar) Type {
		return TArrow([]Type{TArrow([]Type{a, b}, b), b, TList(a)}, b)
	}))

	// `=` and `!=` are polymorphic equality.
	eqScheme := forall1(func(a TVar) Type {
		return TArrow([]Type{a, a}, TBool())
	})
	env = env.Bind("=", eqScheme)
	env = env.Bind("!=", eqScheme)

	// Boolean ops.
	env = env.Bind("not", TArrow([]Type{TBool()}, TBool()))
	env = env.Bind("and", TArrow([]Type{TBool(), TBool()}, TBool()))
	env = env.Bind("or", TArrow([]Type{TBool(), TBool()}, TBool()))

	// String ops. Names use underscores to match the parser's normalization
	// (e.g. "starts-with" → "starts_with") performed by normalizeSymbol.
	env = env.Bind("contains", TArrow([]Type{TString(), TString()}, TBool()))
	env = env.Bind("starts_with", TArrow([]Type{TString(), TString()}, TBool()))
	env = env.Bind("ends_with", TArrow([]Type{TString(), TString()}, TBool()))

	// Date/datetime → string conversions. Canonical names go through the
	// parser's normalizeSymbol (replaces "-" with "_"), so source
	// `date->string` becomes `date_>string`.
	env = env.Bind("date_>string", TArrow([]Type{TDate()}, TString()))
	env = env.Bind("datetime_>string", TArrow([]Type{TDateTime()}, TString()))
	// number->string is special-cased in inferSpecialBuiltin to accept int
	// or decimal (HM has no numeric subtype yet).

	// Auth helpers.
	currentUser := currentUserType()
	env = env.Bind("authenticated?", TArrow([]Type{currentUser}, TBool()))
	env = env.Bind("anonymous?", TArrow([]Type{currentUser}, TBool()))
	env = env.Bind("same_user?", TArrow([]Type{currentUser, TInt()}, TBool()))
	env = env.Bind("has_role?", TArrow([]Type{currentUser, TString()}, TBool()))

	// Constants.
	env = env.Bind("true", TBool())
	env = env.Bind("false", TBool())
	env = env.Bind("unit", TUnit())
	env = env.Bind("current_user", currentUser)

	return env
}

// IsNumericOverloadedBuiltin reports whether name is one of the operators that
// the inferer must handle specially because they are numerically overloaded
// (int or decimal). These are NOT in BaseEnv.
func IsNumericOverloadedBuiltin(name string) bool {
	switch name {
	case "+", "-", "*", "/", ">", ">=", "<", "<=":
		return true
	}
	return false
}

// IsContextSpecialBuiltin reports whether name needs context-specific handling
// in the inferer rather than a TypeEnv entry. These typically have variádic
// arity or polymorphism over a fixed set (string OR list, etc.).
func IsContextSpecialBuiltin(name string) bool {
	switch name {
	case "length", "string_append", "matches", "number_>string":
		return true
	}
	return false
}

// currentUserType builds the nominal union for the auth state.
func currentUserType() Type {
	return TUnion{
		Name: "current-user",
		Variants: map[string][]Type{
			"authenticated": {TInt(), TString(), TString()},
			"anonymous":     nil,
		},
		VariantOrder: []string{"authenticated", "anonymous"},
		FieldNames: map[string][]string{
			"authenticated": {"id", "email", "role"},
			"anonymous":     nil,
		},
	}
}

// forall1 builds ∀α. body where body is a function of one type variable.
func forall1(build func(a TVar) Type) Type {
	a := FreshVar()
	body := build(a)
	return TForall{Vars: []int{a.ID}, Body: body}
}

// forall2 builds ∀α β. body.
func forall2(build func(a, b TVar) Type) Type {
	a := FreshVar()
	b := FreshVar()
	body := build(a, b)
	return TForall{Vars: []int{a.ID, b.ID}, Body: body}
}
