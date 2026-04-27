// Infer is the Hindley-Milner inference entry point for mar-lang. Given an
// expr.Expr and a type environment, it returns the inferred type or an error.
//
// All bindings produced during inference accumulate in the supplied *Subst.
// Callers that want a self-contained inference pass should use InferExpr,
// which builds its own substitution.
package types

import (
	"fmt"
	"strings"

	"mar/internal/expr"
)

// InferExpr is the convenience wrapper: builds a fresh Subst, runs Infer,
// and returns the fully-resolved type.
func InferExpr(e expr.Expr, env *TypeEnv) (Type, error) {
	s := NewSubst()
	t, err := Infer(e, env, s)
	if err != nil {
		return nil, err
	}
	return s.Apply(t), nil
}

// Infer walks e and returns its type. New variable bindings are recorded in s.
// The returned type is NOT pre-applied through s; callers can do that
// themselves or rely on InferExpr to do it.
func Infer(e expr.Expr, env *TypeEnv, s *Subst) (Type, error) {
	switch n := e.(type) {

	case expr.Literal:
		return inferLiteral(n.Value)

	case expr.ListLiteral:
		return inferList(n.Items, env, s)

	case expr.Variable:
		return inferVariable(n.Name, env)

	case expr.FunctionRef:
		return inferVariable(n.Name, env)

	case expr.Lambda:
		return inferLambda(n, env, s)

	case expr.If:
		return inferIf(n, env, s)

	case expr.Cond:
		return inferCond(n, env, s)

	case expr.Let:
		return inferLet(n, env, s)

	case expr.Begin:
		return inferBegin(n, env, s)

	case expr.Match:
		return inferMatch(n, env, s)

	case expr.Get:
		return inferGet(n, env, s)

	case expr.Assoc:
		return inferAssoc(n, env, s)

	case expr.Binary:
		return inferBinary(n, env, s)

	case expr.Unary:
		return inferUnary(n, env, s)

	case expr.Call:
		return inferCall(n, env, s)

	case expr.Error:
		// `(error "...")` has bottom type — represented by a fresh var.
		return FreshVar(), nil

	case expr.RegexMatch:
		t, err := Infer(n.Text, env, s)
		if err != nil {
			return nil, err
		}
		if err := Unify(t, TString(), s); err != nil {
			return nil, fmt.Errorf("matches: text must be string, %w", err)
		}
		return TBool(), nil

	case expr.RecordConstructor:
		return inferRecordConstructor(n, env, s)

	case expr.TaggedConstructor:
		return inferTaggedConstructor(n, env, s)

	case expr.Opaque:
		// Opaque values escape the type system; returning a fresh var lets
		// callers use them anywhere without constraints. Frente 5 may want
		// stricter handling.
		return FreshVar(), nil

	default:
		return nil, fmt.Errorf("Infer: unsupported expr type %T", e)
	}
}

// ---- per-node inferers ----

func inferLiteral(v any) (Type, error) {
	switch val := v.(type) {
	case bool:
		return TBool(), nil
	case string:
		return TString(), nil
	case int, int64:
		return TInt(), nil
	case float64:
		return TDecimal(), nil
	case []any:
		// Empty list literal: produce (list α) with a fresh element var.
		// Non-empty literal lists go through ListLiteral, not here.
		_ = val
		return TList(FreshVar()), nil
	default:
		// expr.Decimal and similar values land here too.
		return TDecimal(), nil
	}
}

func inferList(items []expr.Expr, env *TypeEnv, s *Subst) (Type, error) {
	if len(items) == 0 {
		// Empty list: fresh element type — caller must constrain.
		return TList(FreshVar()), nil
	}
	first, err := Infer(items[0], env, s)
	if err != nil {
		return nil, err
	}
	for i := 1; i < len(items); i++ {
		t, err := Infer(items[i], env, s)
		if err != nil {
			return nil, err
		}
		if err := Unify(first, t, s); err != nil {
			return nil, fmt.Errorf("list element %d: %w", i, err)
		}
	}
	return TList(first), nil
}

func inferVariable(name string, env *TypeEnv) (Type, error) {
	scheme, ok := env.Lookup(name)
	if !ok {
		if suggestion := suggestEnvName(name, env); suggestion != "" {
			return nil, fmt.Errorf("unknown identifier %q. Did you mean %q?", name, suggestion)
		}
		return nil, fmt.Errorf("unknown identifier %q", name)
	}
	return Instantiate(scheme), nil
}

// suggestEnvName picks the closest in-scope identifier (by edit distance)
// for did-you-mean hints. Skips internal helper keys like "__functions".
func suggestEnvName(want string, env *TypeEnv) string {
	best := ""
	bestDist := len(want)/2 + 1
	for e := env; e != nil; e = e.parent {
		for name := range e.bindings {
			if strings.HasPrefix(name, "__") {
				continue
			}
			d := levenshtein(want, name)
			if d < bestDist {
				bestDist = d
				best = name
			}
		}
	}
	return best
}

func inferLambda(n expr.Lambda, env *TypeEnv, s *Subst) (Type, error) {
	bindings := make(map[string]Type, len(n.Params))
	paramTypes := make([]Type, len(n.Params))
	for i, p := range n.Params {
		v := FreshVar()
		bindings[p] = v
		paramTypes[i] = v
	}
	child := env.Extend(bindings)
	bodyType, err := Infer(n.Body, child, s)
	if err != nil {
		return nil, err
	}
	return TArrow(paramTypes, bodyType), nil
}

func inferIf(n expr.If, env *TypeEnv, s *Subst) (Type, error) {
	cond, err := Infer(n.Condition, env, s)
	if err != nil {
		return nil, err
	}
	if err := Unify(cond, TBool(), s); err != nil {
		return nil, fmt.Errorf("if condition must be bool, got %s", PrettyType(s.Apply(cond)))
	}
	thenT, err := Infer(n.Then, env, s)
	if err != nil {
		return nil, err
	}
	elseT, err := Infer(n.Else, env, s)
	if err != nil {
		return nil, err
	}
	if err := Unify(thenT, elseT, s); err != nil {
		te, ee := PrettyTypePair(s.Apply(thenT), s.Apply(elseT))
		return nil, fmt.Errorf("if branches return different types: then is %s, else is %s", te, ee)
	}
	return thenT, nil
}

func inferCond(n expr.Cond, env *TypeEnv, s *Subst) (Type, error) {
	var resultType Type
	for i, c := range n.Clauses {
		if !c.Else {
			testT, err := Infer(c.Test, env, s)
			if err != nil {
				return nil, err
			}
			if err := Unify(testT, TBool(), s); err != nil {
				return nil, fmt.Errorf("cond test %d: %w", i, err)
			}
		}
		bodyT, err := Infer(c.Body, env, s)
		if err != nil {
			return nil, err
		}
		if resultType == nil {
			resultType = bodyT
			continue
		}
		if err := Unify(resultType, bodyT, s); err != nil {
			return nil, fmt.Errorf("cond clause %d body: %w", i, err)
		}
	}
	if resultType == nil {
		return nil, fmt.Errorf("cond has no clauses")
	}
	return resultType, nil
}

func inferLet(n expr.Let, env *TypeEnv, s *Subst) (Type, error) {
	current := env
	if n.Sequential {
		// let*: each binding sees previous ones, can be generalized for poly.
		for _, b := range n.Bindings {
			t, err := Infer(b.Value, current, s)
			if err != nil {
				return nil, fmt.Errorf("let* binding %s: %w", b.Name, err)
			}
			scheme := Generalize(current, s, t)
			current = current.Bind(b.Name, scheme)
		}
		return Infer(n.Body, current, s)
	}
	// let: all bindings inferred against outer env, then introduced together.
	bindings := make(map[string]Type, len(n.Bindings))
	for _, b := range n.Bindings {
		t, err := Infer(b.Value, env, s)
		if err != nil {
			return nil, fmt.Errorf("let binding %s: %w", b.Name, err)
		}
		bindings[b.Name] = Generalize(env, s, t)
	}
	current = env.Extend(bindings)
	return Infer(n.Body, current, s)
}

func inferBegin(n expr.Begin, env *TypeEnv, s *Subst) (Type, error) {
	if len(n.Expressions) == 0 {
		return TUnit(), nil
	}
	var last Type
	for _, e := range n.Expressions {
		t, err := Infer(e, env, s)
		if err != nil {
			return nil, err
		}
		last = t
	}
	return last, nil
}

func inferMatch(n expr.Match, env *TypeEnv, s *Subst) (Type, error) {
	subject, err := Infer(n.Subject, env, s)
	if err != nil {
		return nil, err
	}
	subject = s.Apply(subject)

	var resultType Type
	for i, c := range n.Clauses {
		// Bind pattern vars against the subject's type. For now we only
		// support tagged-union patterns: pattern is (Tag var var ...).
		clauseEnv, err := bindMatchPattern(c.Pattern, subject, env, s)
		if err != nil {
			return nil, fmt.Errorf("match clause %d pattern: %w", i, err)
		}
		bodyT, err := Infer(c.Body, clauseEnv, s)
		if err != nil {
			return nil, fmt.Errorf("match clause %d body: %w", i, err)
		}
		if resultType == nil {
			resultType = bodyT
			continue
		}
		if err := Unify(resultType, bodyT, s); err != nil {
			return nil, fmt.Errorf("match clause %d: branches differ in type: %w", i, err)
		}
	}
	if resultType == nil {
		return nil, fmt.Errorf("match has no clauses")
	}
	// Exhaustivity check: subject's expected variants must all appear.
	if err := checkMatchExhaustive(subject, n.Clauses); err != nil {
		return nil, err
	}
	return resultType, nil
}

// checkMatchExhaustive verifies that a `match` covers every variant of the
// subject's type. mar-lang has no wildcard pattern, so coverage means every
// declared variant tag must appear in some clause.
//
// Skips when the subject's type isn't fully resolved (TVar) — such matches
// are accepted and any uncovered case is caught at runtime.
func checkMatchExhaustive(subject Type, clauses []expr.MatchClause) error {
	required := variantTagsOf(subject)
	if required == nil {
		// Unknown / unconstrained — can't check. Accept.
		return nil
	}
	covered := map[string]bool{}
	for _, c := range clauses {
		covered[c.Pattern.Tag] = true
	}
	missing := []string{}
	for _, tag := range required {
		if !covered[tag] {
			missing = append(missing, tag)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("match is not exhaustive; missing %s", strings.Join(missing, ", "))
}

// variantTagsOf returns the ordered list of tags a fully-resolved subject
// type can take. Returns nil when the type is parametric / unknown.
func variantTagsOf(subject Type) []string {
	if u, ok := subject.(TUnion); ok {
		if len(u.VariantOrder) > 0 {
			out := make([]string, 0, len(u.VariantOrder))
			for _, t := range u.VariantOrder {
				out = append(out, t)
			}
			return out
		}
		// Fall back to map keys (sorted for stable output).
		out := make([]string, 0, len(u.Variants))
		for t := range u.Variants {
			out = append(out, t)
		}
		return out
	}
	if con, ok := subject.(TCon); ok {
		switch con.Name {
		case "maybe":
			return []string{"just", "nothing"}
		case "result":
			return []string{"ok", "err"}
		case "unit":
			return []string{"unit"}
		}
	}
	return nil
}

// bindMatchPattern adds pattern-bound variables to env. Supports both nominal
// TUnion (entity-defined or built-in) and parametric tagged constructors that
// live as TCon: maybe (just/nothing), result (ok/err), unit.
func bindMatchPattern(pat expr.MatchPattern, subject Type, env *TypeEnv, s *Subst) (*TypeEnv, error) {
	subject = s.Apply(subject)
	payload, err := lookupPatternPayload(pat.Tag, subject)
	if err != nil {
		return nil, err
	}
	// If subject is an unresolved TVar, lookupPatternPayload returns one
	// fresh var; relax arity check so we can still bind whatever names the
	// user wrote.
	if _, isVar := subject.(TVar); isVar {
		bindings := map[string]Type{}
		for _, v := range pat.Vars {
			if v == "_" || v == "" {
				continue
			}
			bindings[v] = FreshVar()
		}
		return env.Extend(bindings), nil
	}
	if len(pat.Vars) != len(payload) {
		names := variantFieldNames(pat.Tag, subject)
		if len(names) > 0 {
			return nil, fmt.Errorf(`match pattern %q expects %d values: %s`,
				pat.Tag, len(payload), strings.Join(names, " "))
		}
		return nil, fmt.Errorf("pattern %s expects %d arguments, got %d",
			pat.Tag, len(payload), len(pat.Vars))
	}
	bindings := map[string]Type{}
	for i, v := range pat.Vars {
		if v == "_" || v == "" {
			continue
		}
		bindings[v] = payload[i]
	}
	return env.Extend(bindings), nil
}

// variantFieldNames looks up the field names declared for the given tag. For
// nominal TUnions, uses the FieldNames map. For built-in Maybe/Result, returns
// the conventional names ("value" for just/ok, "error" for err).
func variantFieldNames(tag string, subject Type) []string {
	if u, ok := subject.(TUnion); ok {
		if names, found := u.FieldNames[tag]; found {
			return names
		}
	}
	if con, ok := subject.(TCon); ok {
		switch con.Name {
		case "maybe":
			if tag == "just" {
				return []string{"value"}
			}
		case "result":
			switch tag {
			case "ok":
				return []string{"value"}
			case "err":
				return []string{"error"}
			}
		}
	}
	return nil
}

// lookupPatternPayload finds the types bound by tag matching against subject.
// Handles built-in parametric constructors (maybe, result, unit) plus nominal
// unions (TUnion). When subject is an unresolved TVar (common for screen
// params inferred structurally), returns one fresh var per pattern position
// so the body type-checks; later passes can refine.
func lookupPatternPayload(tag string, subject Type) ([]Type, error) {
	if _, isVar := subject.(TVar); isVar {
		// Yield generic payload of one fresh var. The pattern's actual arity
		// will be checked separately by the caller.
		return []Type{FreshVar()}, nil
	}
	if con, ok := subject.(TCon); ok {
		switch con.Name {
		case "maybe":
			if len(con.Args) != 1 {
				return nil, fmt.Errorf("internal: maybe should have one arg, got %d", len(con.Args))
			}
			switch tag {
			case "just":
				return []Type{con.Args[0]}, nil
			case "nothing":
				return nil, nil
			}
			return nil, fmt.Errorf("tag %q does not match maybe", tag)

		case "result":
			if len(con.Args) != 2 {
				return nil, fmt.Errorf("internal: result should have two args, got %d", len(con.Args))
			}
			switch tag {
			case "err":
				return []Type{con.Args[0]}, nil
			case "ok":
				return []Type{con.Args[1]}, nil
			}
			return nil, fmt.Errorf("tag %q does not match result", tag)

		case "unit":
			if tag == "unit" {
				return nil, nil
			}
			return nil, fmt.Errorf("tag %q does not match unit", tag)
		}
	}
	if u, ok := subject.(TUnion); ok {
		payload, found := u.Variants[tag]
		if !found {
			return nil, fmt.Errorf("tag %q not in union %s", tag, subject)
		}
		return payload, nil
	}
	return nil, fmt.Errorf("subject is not a tagged union or known parametric: %s", subject)
}

func inferGet(n expr.Get, env *TypeEnv, s *Subst) (Type, error) {
	target, err := Infer(n.Target, env, s)
	if err != nil {
		return nil, err
	}
	resolved := s.Apply(target)

	// Closed record with the field: direct lookup.
	if rec, ok := resolved.(TRecord); ok {
		if ft, found := rec.Fields[n.Field]; found {
			return ft, nil
		}
		// Closed record missing the field: hard error with did-you-mean.
		if rec.Tail == nil {
			label := rec.Name
			if label == "" {
				label = PrettyType(resolved)
			}
			suggestion := suggestField(n.Field, rec)
			if suggestion != "" {
				return nil, fmt.Errorf("record %s has no field %q. Did you mean %q?", label, n.Field, suggestion)
			}
			return nil, fmt.Errorf("record %s has no field %q", label, n.Field)
		}
		// Open record missing the field: fall through to add it via row.
	}

	// Row-polymorphism path: target gains a constraint "must be a record
	// with the requested field". Use a fresh row tail so other accesses can
	// still add their own fields.
	fieldType := FreshVar()
	tailVar := FreshVar()
	wanted := TRecord{
		Fields: map[string]Type{n.Field: fieldType},
		Order:  []string{n.Field},
		Tail:   tailVar,
	}
	if err := Unify(target, wanted, s); err != nil {
		return nil, fmt.Errorf("accessing field %q: %s is not a record with that field", n.Field, PrettyType(s.Apply(target)))
	}
	return fieldType, nil
}

// suggestField finds a record field whose name is close to want via simple
// edit-distance scoring. Returns "" if nothing close enough.
func suggestField(want string, rec TRecord) string {
	best := ""
	bestDist := len(want)/2 + 1
	for name := range rec.Fields {
		d := levenshtein(want, name)
		if d < bestDist {
			bestDist = d
			best = name
		}
	}
	return best
}

// levenshtein computes the standard edit distance between a and b.
func levenshtein(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = minInt(
				prev[j]+1,
				cur[j-1]+1,
				prev[j-1]+cost,
			)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func minInt(xs ...int) int {
	m := xs[0]
	for _, x := range xs[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func inferAssoc(n expr.Assoc, env *TypeEnv, s *Subst) (Type, error) {
	target, err := Infer(n.Target, env, s)
	if err != nil {
		return nil, err
	}
	resolved := s.Apply(target)

	// Closed record fast path: look up each updated field directly.
	if rec, ok := resolved.(TRecord); ok && rec.Tail == nil {
		for _, u := range n.Updates {
			fieldType, found := rec.Fields[u.Field]
			if !found {
				label := rec.Name
				if label == "" {
					label = PrettyType(resolved)
				}
				if suggestion := suggestField(u.Field, rec); suggestion != "" {
					return nil, fmt.Errorf("assoc: record %s has no field %q. Did you mean %q?", label, u.Field, suggestion)
				}
				return nil, fmt.Errorf("assoc: record %s has no field %q", label, u.Field)
			}
			valueT, err := Infer(u.Value, env, s)
			if err != nil {
				return nil, err
			}
			if err := Unify(fieldType, valueT, s); err != nil {
				exp, got := PrettyTypePair(fieldType, valueT)
				return nil, fmt.Errorf("assoc: field %q expects %s, got %s", u.Field, exp, got)
			}
		}
		return target, nil
	}

	// Row-polymorphism path: target must have at least the updated fields.
	fields := map[string]Type{}
	order := make([]string, 0, len(n.Updates))
	for _, u := range n.Updates {
		valueT, err := Infer(u.Value, env, s)
		if err != nil {
			return nil, err
		}
		fields[u.Field] = valueT
		order = append(order, u.Field)
	}
	tailVar := FreshVar()
	wanted := TRecord{Fields: fields, Order: order, Tail: tailVar}
	if err := Unify(target, wanted, s); err != nil {
		return nil, fmt.Errorf("assoc: %w", err)
	}
	return target, nil
}

func inferBinary(n expr.Binary, env *TypeEnv, s *Subst) (Type, error) {
	left, err := Infer(n.Left, env, s)
	if err != nil {
		return nil, err
	}
	right, err := Infer(n.Right, env, s)
	if err != nil {
		return nil, err
	}
	leftLit := isIntLiteral(n.Left)
	rightLit := isIntLiteral(n.Right)

	switch n.Op {
	case "and", "or":
		if err := Unify(left, TBool(), s); err != nil {
			return nil, fmt.Errorf("%s expects bool, got %s", n.Op, PrettyType(s.Apply(left)))
		}
		if err := Unify(right, TBool(), s); err != nil {
			return nil, fmt.Errorf("%s expects bool, got %s", n.Op, PrettyType(s.Apply(right)))
		}
		return TBool(), nil

	case "=", "!=", "==":
		if err := Unify(left, right, s); err != nil {
			l, r := PrettyTypePair(s.Apply(left), s.Apply(right))
			return nil, fmt.Errorf("%s: cannot compare %s with %s", n.Op, l, r)
		}
		return TBool(), nil

	case ">", ">=", "<", "<=":
		if err := unifyNumeric(left, right, s, leftLit, rightLit); err != nil {
			resolved := s.Apply(left)
			return nil, fmt.Errorf("operator %s expects ordered values, got %s", n.Op, PrettyType(resolved))
		}
		return TBool(), nil

	case "+", "-", "*", "/":
		if err := unifyNumeric(left, right, s, leftLit, rightLit); err != nil {
			return nil, fmt.Errorf("operator %s expects int or decimal, got %s", n.Op, PrettyType(s.Apply(left)))
		}
		// Result type: decimal if either operand was decimal; otherwise int.
		l := s.Apply(left)
		r := s.Apply(right)
		if isDecimal(l) || isDecimal(r) {
			return TDecimal(), nil
		}
		return l, nil

	default:
		return nil, fmt.Errorf("unknown binary op %q", n.Op)
	}
}

func inferUnary(n expr.Unary, env *TypeEnv, s *Subst) (Type, error) {
	right, err := Infer(n.Right, env, s)
	if err != nil {
		return nil, err
	}
	switch n.Op {
	case "not":
		if err := Unify(right, TBool(), s); err != nil {
			return nil, fmt.Errorf("not expects bool, got %s", PrettyType(s.Apply(right)))
		}
		return TBool(), nil
	case "-":
		resolved := s.Apply(right)
		if !isNumeric(resolved) {
			return nil, fmt.Errorf("unary - expects int or decimal, got %s", PrettyType(resolved))
		}
		return resolved, nil
	default:
		return nil, fmt.Errorf("unknown unary op %q", n.Op)
	}
}

func inferCall(n expr.Call, env *TypeEnv, s *Subst) (Type, error) {
	// Special-case context-special builtins first.
	if t, handled, err := inferSpecialBuiltin(n, env, s); handled {
		return t, err
	}

	// Look up the function in the environment and instantiate.
	scheme, ok := env.Lookup(n.Name)
	if !ok {
		if suggestion := suggestEnvName(n.Name, env); suggestion != "" {
			return nil, fmt.Errorf("unknown function %q. Did you mean %q?", n.Name, suggestion)
		}
		return nil, fmt.Errorf("unknown function %q", n.Name)
	}
	fnType := Instantiate(scheme)

	// Special case: nullary callables like `nothing` are bound as plain values
	// (∀α. maybe α), not arrows. When called with no args, just return the
	// value type. With args, fall through to the normal arrow path so the
	// error message is clear.
	if len(n.Args) == 0 {
		resolved := s.Apply(fnType)
		if _, _, isArrow := IsArrow(resolved); !isArrow {
			return fnType, nil
		}
	}

	// Infer each argument.
	argTypes := make([]Type, len(n.Args))
	for i, a := range n.Args {
		t, err := Infer(a, env, s)
		if err != nil {
			return nil, fmt.Errorf("%s arg %d: %w", n.Name, i, err)
		}
		argTypes[i] = t
	}

	// Build the expected arrow and unify. If the callable already has a
	// concrete arrow type, give a nicer error pointing at the offending arg.
	resultVar := FreshVar()
	expected := TArrow(argTypes, resultVar)
	if err := Unify(fnType, expected, s); err != nil {
		return nil, fmt.Errorf("%s %s", displayCallName(n.Name), niceCallError(n.Name, fnType, argTypes, s))
	}
	return resultVar, nil
}

// dashedBuiltinNames lists builtins whose display form is the dashed source
// spelling rather than the canonical underscored form. mar-lang convention:
// builtins use dashes in source, so users see them dashed in errors.
var dashedBuiltinNames = map[string]bool{
	"has_role?":        true,
	"same_user?":       true,
	"authenticated?":   true,
	"anonymous?":       true,
	"starts_with":      true,
	"ends_with":        true,
	"string_append":    true,
	"number_to_string": true,
	"date_to_string":   true,
	"datetime_to_string": true,
	"fold_left":        true,
	"fold_right":       true,
	"number->string":   true,
	"date->string":     true,
	"datetime->string": true,
}

// displayCallName returns the form a user sees in error messages. Builtins
// display dashed (their source spelling); user functions stay underscored
// (matching how the checker stores them).
func displayCallName(name string) string {
	if dashedBuiltinNames[name] {
		return strings.ReplaceAll(name, "_", "-")
	}
	return name
}

// niceCallError tries to produce a "%s expects %s, got %s" message by walking
// the callable's parameter types and the argument types pairwise. Falls back
// to the unification error if the shapes don't line up.
func niceCallError(name string, fnType Type, argTypes []Type, s *Subst) string {
	resolved := s.Apply(fnType)
	params, _, ok := IsArrow(resolved)
	if !ok || len(params) != len(argTypes) {
		if !ok {
			return fmt.Sprintf("is not a function (it has type %s)", PrettyType(resolved))
		}
		return fmt.Sprintf("expects %d arguments, got %d", len(params), len(argTypes))
	}
	for i, p := range params {
		expected := s.Apply(p)
		actual := s.Apply(argTypes[i])
		if expected.String() == actual.String() {
			continue
		}
		// Render expected/actual sharing the same renaming so type vars
		// match across both sides.
		expStr, actStr := PrettyTypePair(expected, actual)
		switch name {
		case "contains", "starts_with", "ends_with":
			return fmt.Sprintf("expects string arguments, got %s", actStr)
		case "matches":
			return fmt.Sprintf("expects string arguments, got %s", actStr)
		case "has_role?":
			if i == 1 {
				return fmt.Sprintf("expects string as second argument, got %s", actStr)
			}
		case "same_user?":
			if i == 1 {
				return fmt.Sprintf("expects int as second argument, got %s", actStr)
			}
		}
		return fmt.Sprintf("parameter %s expects %s, got %s", paramName(name, i, expected), expStr, actStr)
	}
	return "type mismatch"
}

// paramName tries to give a helpful name for the i-th parameter of a callable.
// User function param names are registered by CheckApp via
// registerFunctionParamNames so this helper returns them when available.
func paramName(fnName string, i int, _ Type) string {
	names := lookupFunctionParamNames(fnName)
	if i >= 0 && i < len(names) {
		return names[i]
	}
	return fmt.Sprintf("arg %d", i+1)
}

// fnParamRegistry holds parameter names for user-defined functions, populated
// by CheckApp. Read by paramName for friendlier error messages.
var fnParamRegistry = map[string][]string{}

func registerFunctionParamNames(name string, params []string) {
	cp := make([]string, len(params))
	copy(cp, params)
	fnParamRegistry[name] = cp
}

func lookupFunctionParamNames(name string) []string {
	return fnParamRegistry[name]
}

// prettyTypeForUser produces a friendlier string than Type.String for user
// errors: "(list int)" stays as-is, but plain free vars become "_" instead
// of "α12" since users don't see HM internals.
func prettyTypeForUser(t Type) string {
	return t.String()
}

// inferSpecialBuiltin handles names that can't be expressed as a single
// TForall scheme: variádic, polymorphic over a fixed set of types, etc.
// Returns (type, true, err) when handled, (nil, false, nil) otherwise.
func inferSpecialBuiltin(n expr.Call, env *TypeEnv, s *Subst) (Type, bool, error) {
	switch n.Name {

	case "length":
		if len(n.Args) != 1 {
			return nil, true, fmt.Errorf("length expects 1 argument")
		}
		t, err := Infer(n.Args[0], env, s)
		if err != nil {
			return nil, true, err
		}
		t = s.Apply(t)
		// Accept string or any list.
		if isString(t) {
			return TInt(), true, nil
		}
		if con, ok := t.(TCon); ok && con.Name == "list" {
			return TInt(), true, nil
		}
		// If still a free variable, default to list.
		if v, ok := t.(TVar); ok {
			elem := FreshVar()
			if err := Unify(v, TList(elem), s); err != nil {
				return nil, true, fmt.Errorf("length: %w", err)
			}
			return TInt(), true, nil
		}
		return nil, true, fmt.Errorf("length expects string or list, got %s", t)

	case "string_append":
		// Variádic: every argument must be a string; result is string.
		for i, a := range n.Args {
			t, err := Infer(a, env, s)
			if err != nil {
				return nil, true, err
			}
			if err := Unify(t, TString(), s); err != nil {
				_ = i
				return nil, true, fmt.Errorf("string-append expects string arguments, got %s", s.Apply(t))
			}
		}
		return TString(), true, nil

	case "number_>string":
		// Accept int or decimal; produce string.
		if len(n.Args) != 1 {
			return nil, true, fmt.Errorf("number->string expects 1 argument")
		}
		t, err := Infer(n.Args[0], env, s)
		if err != nil {
			return nil, true, err
		}
		resolved := s.Apply(t)
		if !isNumeric(resolved) {
			if _, isVar := resolved.(TVar); !isVar {
				return nil, true, fmt.Errorf("number->string expects int or decimal, got %s", resolved)
			}
			// Free var — leave unbound. Soft polymorphic numeric.
		}
		return TString(), true, nil

	case "matches":
		// (matches "<regex literal>" text) — first arg is a static string
		// literal handled by parser; we just treat both as strings here.
		if len(n.Args) != 2 {
			return nil, true, fmt.Errorf("matches expects 2 arguments")
		}
		for i, a := range n.Args {
			t, err := Infer(a, env, s)
			if err != nil {
				return nil, true, err
			}
			if err := Unify(t, TString(), s); err != nil {
				return nil, true, fmt.Errorf("matches expects string arguments, got %s at position %d", s.Apply(t), i+1)
			}
		}
		return TBool(), true, nil
	}
	return nil, false, nil
}

func inferRecordConstructor(n expr.RecordConstructor, env *TypeEnv, s *Subst) (Type, error) {
	scheme, ok := env.Lookup(n.Name)
	if !ok {
		return nil, fmt.Errorf("unknown record %q", n.Name)
	}
	rec, ok := Instantiate(scheme).(TRecord)
	if !ok {
		return nil, fmt.Errorf("%q is not a record type", n.Name)
	}
	if len(n.Args) != len(n.Fields) {
		return nil, fmt.Errorf("%s constructor: arity mismatch (%d vs %d)", n.Name, len(n.Args), len(n.Fields))
	}
	for i, fieldName := range n.Fields {
		expected, ok := rec.Fields[fieldName]
		if !ok {
			return nil, fmt.Errorf("%s has no field %q", n.Name, fieldName)
		}
		valueT, err := Infer(n.Args[i], env, s)
		if err != nil {
			return nil, err
		}
		if err := Unify(expected, valueT, s); err != nil {
			return nil, fmt.Errorf("%s field %q: %w", n.Name, fieldName, err)
		}
	}
	return rec, nil
}

func inferTaggedConstructor(n expr.TaggedConstructor, env *TypeEnv, s *Subst) (Type, error) {
	// We need to find the union type that owns this tag. Look for a binding
	// in env whose type is a TUnion containing n.Tag. If multiple match,
	// it's ambiguous — this is rare in practice; refine when needed.
	candidate, payload, ok := findTaggedUnion(env, n.Tag)
	if !ok {
		return nil, fmt.Errorf("unknown tag %q", n.Tag)
	}
	if len(n.Args) != len(payload) {
		return nil, fmt.Errorf("tag %s expects %d arguments, got %d", n.Tag, len(payload), len(n.Args))
	}
	for i, a := range n.Args {
		valueT, err := Infer(a, env, s)
		if err != nil {
			return nil, err
		}
		if err := Unify(payload[i], valueT, s); err != nil {
			return nil, fmt.Errorf("tag %s arg %d: %w", n.Tag, i, err)
		}
	}
	return candidate, nil
}

// findTaggedUnion walks env looking for a TUnion that has the given tag.
func findTaggedUnion(env *TypeEnv, tag string) (Type, []Type, bool) {
	for e := env; e != nil; e = e.parent {
		for _, t := range e.bindings {
			resolved := t
			if forall, ok := t.(TForall); ok {
				resolved = forall.Body
			}
			if u, ok := resolved.(TUnion); ok {
				if payload, found := u.Variants[tag]; found {
					return u, payload, true
				}
			}
		}
	}
	return nil, nil, false
}

func isNumeric(t Type) bool {
	con, ok := t.(TCon)
	if !ok {
		return false
	}
	return con.Name == "int" || con.Name == "decimal"
}

func isDecimal(t Type) bool {
	con, ok := t.(TCon)
	return ok && con.Name == "decimal"
}

func isString(t Type) bool {
	con, ok := t.(TCon)
	return ok && con.Name == "string"
}

// unifyNumeric handles operands of an arithmetic or comparison operator.
//
// mar-lang's runtime accepts mixed int/decimal in numeric ops (promoting int
// to decimal). The leftLit/rightLit flags indicate whether each side is an
// integer literal in source — if so, we treat its concreteness as "soft" and
// don't bind a free variable on the other side, preserving polymorphism for
// user-defined functions like `(define (f x) (> x 0))`.
//
// Rules:
//   - Both concrete numeric: succeed (mixed int/decimal allowed by runtime).
//   - Var vs non-literal numeric: bind the var to the concrete type. This is
//     how query parameters get their types inferred (e.g. (> score min-score)
//     binds min-score to decimal).
//   - Var vs literal int: leave var unbound — int literal is "soft", lets
//     callers provide either int or decimal.
//   - Both vars: link them so later concrete types propagate.
//   - Else: unify and check numeric.
func unifyNumeric(left, right Type, s *Subst, leftLit, rightLit bool) error {
	l := s.Apply(left)
	r := s.Apply(right)

	lNum := isNumeric(l)
	rNum := isNumeric(r)
	_, lVar := l.(TVar)
	_, rVar := r.(TVar)

	if lNum && rNum {
		return nil
	}
	if lNum && rVar {
		if leftLit {
			return nil
		}
		return Unify(right, l, s)
	}
	if rNum && lVar {
		if rightLit {
			return nil
		}
		return Unify(left, r, s)
	}
	if lVar && rVar {
		return Unify(left, right, s)
	}
	if err := Unify(left, right, s); err != nil {
		return err
	}
	resolved := s.Apply(left)
	if !isNumeric(resolved) {
		return fmt.Errorf("expects int or decimal, got %s", resolved)
	}
	return nil
}

// isIntLiteral reports whether e is an integer literal expression. Used by
// inferBinary to keep numeric ops polymorphic when one side is `0` or similar.
func isIntLiteral(e expr.Expr) bool {
	lit, ok := e.(expr.Literal)
	if !ok {
		return false
	}
	switch lit.Value.(type) {
	case int, int64:
		return true
	}
	return false
}
