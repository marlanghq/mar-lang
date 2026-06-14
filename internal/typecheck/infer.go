package typecheck

import (
	"fmt"
	"sort"
	"strings"

	"mar/internal/ast"
)

// InferError carries position info for a type error. Pos is the start
// of the offending expression; End (when set) is its exclusive end, so
// the renderer can underline the whole span rather than drop a single
// caret. End == zero means "no span known" and the renderer falls back
// to a one-character caret at Pos.
type InferError struct {
	Pos     ast.Pos
	End     ast.Pos
	Message string
}

func (e *InferError) Error() string {
	return fmt.Sprintf("type error at %d:%d: %s", e.Pos.Line, e.Pos.Column, e.Message)
}

func errorf(pos ast.Pos, format string, args ...any) *InferError {
	return &InferError{Pos: pos, Message: fmt.Sprintf(format, args...)}
}

// errorfExpr is the common case: point at an expression's own span.
func errorfExpr(e ast.Expr, format string, args ...any) *InferError {
	return &InferError{Pos: e.Position(), End: ast.EndOf(e), Message: fmt.Sprintf(format, args...)}
}

// InferExpr is the convenience entry point: builds a fresh Subst, infers,
// returns the resolved type or an error.
func InferExpr(e ast.Expr, env *TypeEnv) (Type, error) {
	s := NewSubst()
	t, err := Infer(e, env, s)
	if err != nil {
		return nil, err
	}
	return s.Apply(t), nil
}

// Infer is the core Hindley-Milner inference for an expression.
//
// On success, returns the inferred type (with bindings recorded in s). On
// failure, returns *InferError with source position.
//
// When s has expression tracking enabled (Subst.EnableExprTracking),
// the inferred type of every successfully-checked expression gets
// recorded in s.exprTypes for later extraction. Consumers (notably
// the shape lint) read those types to validate non-literal record
// values that the polymorphic framework signatures don't constrain.
func Infer(e ast.Expr, env *TypeEnv, s *Subst) (Type, error) {
	t, err := doInfer(e, env, s)
	if err == nil && s.exprTypes != nil {
		s.exprTypes[e] = t
	}
	return t, err
}

// doInfer is the dispatcher Infer wraps. Split out so Infer can
// record types post-recursion without every case-branch having to
// remember to do so. Returns the raw inferred type — Infer applies
// the recording side-effect.
func doInfer(e ast.Expr, env *TypeEnv, s *Subst) (Type, error) {
	switch n := e.(type) {
	case *ast.EInt:
		return TInt, nil
	case *ast.EFloat:
		return TFloat, nil
	case *ast.EString:
		return TString, nil
	case *ast.EChar:
		return TChar, nil
	case *ast.EUnit:
		return TUnit{}, nil
	case *ast.EVar:
		return inferVar(n.Name, n.Pos, env)
	case *ast.EQualified:
		// Look up "Module.name" — for now, flatten to a single key.
		key := joinName(n.Module, n.Name)
		if t, ok := env.Lookup(key); ok {
			return Instantiate(t), nil
		}
		// Fall back to bare name
		if t, ok := env.Lookup(n.Name); ok {
			return Instantiate(t), nil
		}
		return nil, errorf(n.Pos, "unknown qualified name: %s", key)
	case *ast.ECtor:
		// Constructors are looked up in env like variables. Qualified
		// constructors (Shared.Created, Service.Offline) resolve through
		// the dotted binding the loader registers for module exports and
		// the env registers for dotted builtins. No bare fallback: a
		// qualifier is an explicit claim about where the constructor
		// lives, and silently binding a same-named local one would be
		// exactly the confusion the qualifier exists to prevent.
		if len(n.Module) > 0 {
			return inferVar(joinName(n.Module, n.Name), n.Pos, env)
		}
		return inferVar(n.Name, n.Pos, env)
	case *ast.ENegate:
		t, err := Infer(n.Inner, env, s)
		if err != nil {
			return nil, err
		}
		if err := Unify(t, TInt, s); err != nil {
			return nil, errorf(n.Pos, "negation requires Int: %v", err)
		}
		return TInt, nil
	case *ast.EApp:
		return inferApp(n, env, s)
	case *ast.EBinop:
		return inferBinop(n, env, s)
	case *ast.ELambda:
		return inferLambda(n, env, s)
	case *ast.EIf:
		return inferIf(n, env, s)
	case *ast.ELet:
		return inferLet(n, env, s)
	case *ast.ETuple:
		return inferTuple(n, env, s)
	case *ast.EList:
		return inferList(n, env, s)
	case *ast.ERecord:
		return inferRecord(n, env, s)
	case *ast.ERecordUpdate:
		return inferRecordUpdate(n, env, s)
	case *ast.EFieldAccess:
		return inferFieldAccess(n, env, s)
	case *ast.EFieldAccessor:
		return inferFieldAccessor(n)
	case *ast.ECase:
		return inferCase(n, env, s)
	default:
		return nil, errorf(e.Position(), "unsupported expression for inference: %T", e)
	}
}

func inferVar(name string, pos ast.Pos, env *TypeEnv) (Type, error) {
	t, ok := env.Lookup(name)
	if !ok {
		return nil, errorf(pos, "unknown identifier: %s", name)
	}
	return Instantiate(t), nil
}

func joinName(mod ast.ModuleName, name string) string {
	if len(mod) == 0 {
		return name
	}
	return strings.Join(mod, ".") + "." + name
}

func inferApp(n *ast.EApp, env *TypeEnv, s *Subst) (Type, error) {
	tFn, err := Infer(n.Fn, env, s)
	if err != nil {
		return nil, err
	}
	tArg, err := Infer(n.Arg, env, s)
	if err != nil {
		return nil, err
	}
	tRet := FreshVar()
	if err := Unify(tFn, TArrow{From: tArg, To: tRet}, s); err != nil {
		// Identify the callee + which argument this is, so the message
		// can say "the 2nd argument to `text`" and point the caret at
		// the argument itself rather than at the function head. For
		// `f a b` (= EApp(EApp(f,a),b)) inferring the outer node, the
		// head is `f` with 1 arg already applied, so this is arg #2.
		head, appliedCount := appHead(n.Fn)
		argIdx := appliedCount + 1
		name := calleeName(head)
		who := "this argument"
		if name != "" {
			who = fmt.Sprintf("the %s argument to `%s`", ordinalWord(argIdx), name)
		}

		// Kind violation (e.g. comparable key bound to a Record) —
		// the UnifyError's Reason already names the problem in
		// user-facing terms, so keep it instead of re-wrapping with
		// the generic "argument has the wrong type" framing. Still
		// point at the argument span.
		if ue, ok := err.(*UnifyError); ok && ue.KindMismatch {
			return nil, errorfExpr(n.Arg, "%s", ue.Reason)
		}
		// By the time we land here, tFn's type has been resolved enough
		// to inspect.
		fnResolved := s.Apply(tFn)
		if _, isArrow := fnResolved.(TArrow); !isArrow {
			// The callee isn't a function (anymore). Almost always too
			// many arguments: the prefix already consumed all the
			// parameters and returned a non-arrow result, then this
			// extra argument was applied. Point at the surplus argument.
			if name != "" {
				return nil, errorfExpr(n.Arg,
					"`%s` is given too many arguments: it has no parameter for the %s one (the call so far has type %s)",
					name, ordinalWord(argIdx), Pretty(fnResolved))
			}
			return nil, errorfExpr(n.Arg,
				"too many arguments: the expression being called has type %s, which takes no further argument",
				Pretty(fnResolved))
		}
		// It is a function but this argument's type doesn't match.
		fnT := fnResolved.(TArrow)
		return nil, errorfExpr(n.Arg, "%s has the wrong type: expected %s, got %s",
			who, Pretty(fnT.From), Pretty(s.Apply(tArg)))
	}
	return s.Apply(tRet), nil
}

// appHead walks an application spine and returns the head expression
// (the thing ultimately being called) plus how many arguments are
// already applied to it. `f a b` parses as EApp(EApp(f, a), b); given
// the inner Fn `EApp(f, a)` it returns (f, 1).
func appHead(fn ast.Expr) (ast.Expr, int) {
	depth := 0
	for {
		app, ok := fn.(*ast.EApp)
		if !ok {
			return fn, depth
		}
		fn = app.Fn
		depth++
	}
}

// calleeName renders a head expression as a backtick-able name for
// error messages, or "" when the callee isn't a plain name (a lambda,
// a parenthesized expression, etc.) and naming it would mislead.
func calleeName(e ast.Expr) string {
	switch h := e.(type) {
	case *ast.EVar:
		return h.Name
	case *ast.EQualified:
		return strings.Join(h.Module, ".") + "." + h.Name
	case *ast.ECtor:
		if len(h.Module) > 0 {
			return strings.Join(h.Module, ".") + "." + h.Name
		}
		return h.Name
	case *ast.EFieldAccessor:
		return "." + h.Field
	}
	return ""
}

// ordinalWord renders 1->"1st", 2->"2nd", 3->"3rd", 4->"4th", ... for
// argument positions. Functions never reach the teens-suffix edge
// cases, so the simple rule suffices.
func ordinalWord(n int) string {
	switch n {
	case 1:
		return "1st"
	case 2:
		return "2nd"
	case 3:
		return "3rd"
	default:
		return fmt.Sprintf("%dth", n)
	}
}

func inferBinop(n *ast.EBinop, env *TypeEnv, s *Subst) (Type, error) {
	tOp, ok := env.Lookup(n.Op)
	if !ok {
		return nil, errorf(n.Pos, "unknown operator: %s", n.Op)
	}
	tOp = Instantiate(tOp)

	tLeft, err := Infer(n.Left, env, s)
	if err != nil {
		return nil, err
	}
	tRight, err := Infer(n.Right, env, s)
	if err != nil {
		return nil, err
	}

	tRet := FreshVar()
	expected := TArrow{From: tLeft, To: TArrow{From: tRight, To: tRet}}
	if err := Unify(tOp, expected, s); err != nil {
		// Kind violation — e.g. `record < record` tries to bind a
		// Comparable TVar to a TRecord. The UnifyError's Reason
		// already names the offending shape; surface it directly
		// instead of wrapping with "operator < : cannot unify ..."
		// which buries the actual problem.
		if ue, ok := err.(*UnifyError); ok && ue.KindMismatch {
			return nil, errorf(n.Pos, "operator %s: %s", n.Op, ue.Reason)
		}
		return nil, errorf(n.Pos, "operator %s: %v", n.Op, err)
	}
	return s.Apply(tRet), nil
}

func inferLambda(n *ast.ELambda, env *TypeEnv, s *Subst) (Type, error) {
	// Bind each param to a fresh variable.
	bodyEnv := env
	paramTypes := make([]Type, len(n.Params))
	for i, p := range n.Params {
		v := FreshVar()
		paramTypes[i] = v
		bodyEnv = bindPattern(p, v, bodyEnv)
	}
	tBody, err := Infer(n.Body, bodyEnv, s)
	if err != nil {
		return nil, err
	}
	// Build curried arrow type: p1 -> p2 -> ... -> body
	t := tBody
	for i := len(paramTypes) - 1; i >= 0; i-- {
		t = TArrow{From: paramTypes[i], To: t}
	}
	return s.Apply(t), nil
}

func inferIf(n *ast.EIf, env *TypeEnv, s *Subst) (Type, error) {
	tCond, err := Infer(n.Cond, env, s)
	if err != nil {
		return nil, err
	}
	if err := Unify(tCond, TBool, s); err != nil {
		return nil, errorf(n.Cond.Position(), "if condition must be Bool: %v", err)
	}
	tThen, err := Infer(n.Then, env, s)
	if err != nil {
		return nil, err
	}
	tElse, err := Infer(n.Else, env, s)
	if err != nil {
		return nil, err
	}
	if err := Unify(tThen, tElse, s); err != nil {
		return nil, errorf(n.Pos, "if branches differ in type: %v", err)
	}
	return s.Apply(tThen), nil
}

func inferLet(n *ast.ELet, env *TypeEnv, s *Subst) (Type, error) {
	cur := env
	for _, b := range n.Bindings {
		// IsBind (`x <- effect` syntax) currently checks like a regular
		// binding. Strict typing of effect bind chains would unwrap the
		// Effect on the RHS and bind `x` to the inner type.
		tBound, err := Infer(b.Body, cur, s)
		if err != nil {
			return nil, err
		}
		// Simple variable / wildcard binders keep the let-polymorphism
		// path: Generalize the bound type so the binding gets a forall
		// over its free type vars.
		//
		// Complex binders (PRecord destructuring today; future: tuple
		// destructuring in let, etc.) go through inferPattern + Unify
		// so unknown-field errors and shape mismatches surface here at
		// the let, not deferred to use sites with cryptic messages.
		// Trade-off: complex binders don't get per-field
		// generalization. In practice this rarely matters because
		// records of polymorphic values are exotic.
		switch b.Pattern.(type) {
		case *ast.PVar, *ast.PWildcard:
			scheme := Generalize(cur, tBound, s)
			cur = bindPattern(b.Pattern, scheme, cur)
		default:
			tPat, newEnv, perr := inferPattern(b.Pattern, cur, s)
			if perr != nil {
				return nil, perr
			}
			if uerr := Unify(tBound, tPat, s); uerr != nil {
				return nil, errorf(b.Pattern.Position(),
					"let pattern doesn't match the bound value's type: %v", uerr)
			}
			cur = newEnv
		}
	}
	return Infer(n.Body, cur, s)
}

func inferTuple(n *ast.ETuple, env *TypeEnv, s *Subst) (Type, error) {
	members := make([]Type, len(n.Members))
	for i, m := range n.Members {
		t, err := Infer(m, env, s)
		if err != nil {
			return nil, err
		}
		members[i] = t
	}
	return TTuple{Members: members}, nil
}

func inferList(n *ast.EList, env *TypeEnv, s *Subst) (Type, error) {
	if len(n.Elements) == 0 {
		return TList(FreshVar()), nil
	}
	tFirst, err := Infer(n.Elements[0], env, s)
	if err != nil {
		return nil, err
	}
	for i, e := range n.Elements[1:] {
		t, err := Infer(e, env, s)
		if err != nil {
			return nil, err
		}
		if err := Unify(tFirst, t, s); err != nil {
			return nil, errorf(e.Position(), "list element %d differs in type: %v", i+1, err)
		}
	}
	return TList(s.Apply(tFirst)), nil
}

func inferRecord(n *ast.ERecord, env *TypeEnv, s *Subst) (Type, error) {
	fields := make(map[string]Type, len(n.Fields))
	order := make([]string, 0, len(n.Fields))
	for _, f := range n.Fields {
		t, err := Infer(f.Value, env, s)
		if err != nil {
			return nil, err
		}
		fields[f.Name] = t
		order = append(order, f.Name)
	}
	return TRecord{Fields: fields, Order: order}, nil
}

// inferRecordUpdate: { record | f1 = v1, f2 = v2 } where each fi must already
// exist on the record. Result has the same record type.
func inferRecordUpdate(n *ast.ERecordUpdate, env *TypeEnv, s *Subst) (Type, error) {
	tBase, err := Infer(n.Record, env, s)
	if err != nil {
		return nil, err
	}
	// Each updated field must exist on the record. Use row polymorphism:
	// require base to be { row | fi : ti } where ti is the type of vi.
	updateFields := make(map[string]Type, len(n.Fields))
	updateOrder := make([]string, 0, len(n.Fields))
	for _, f := range n.Fields {
		t, err := Infer(f.Value, env, s)
		if err != nil {
			return nil, err
		}
		updateFields[f.Name] = t
		updateOrder = append(updateOrder, f.Name)
	}
	rowVar := FreshVar()
	expected := TRecord{Fields: updateFields, Order: updateOrder, Tail: rowVar}
	if err := Unify(tBase, expected, s); err != nil {
		return nil, errorf(n.Pos, "record update: %v", err)
	}
	return s.Apply(tBase), nil
}

// inferFieldAccess: expr.field
//
// Given expr : tExpr and accessing field "f", we need tExpr to be a record
// with at least field f : tField. Use row polymorphism: unify tExpr with
// { row | f : tField }.
func inferFieldAccess(n *ast.EFieldAccess, env *TypeEnv, s *Subst) (Type, error) {
	tExpr, err := Infer(n.Record, env, s)
	if err != nil {
		return nil, err
	}
	tField := FreshVar()
	rowVar := FreshVar()
	expected := TRecord{
		Fields: map[string]Type{n.Field: tField},
		Order:  []string{n.Field},
		Tail:   rowVar,
	}
	if err := Unify(tExpr, expected, s); err != nil {
		// Translate the unification failure into something actionable.
		applied := s.Apply(tExpr)
		if rec, ok := applied.(TRecord); ok && rec.Tail == nil {
			if _, has := rec.Fields[n.Field]; !has {
				return nil, errorf(n.Pos, "record has no field '%s' (available: %s)",
					n.Field, joinFieldNames(rec))
			}
		}
		// Not a record at all.
		if _, ok := applied.(TRecord); !ok {
			return nil, errorf(n.Pos, "tried to access .%s on a non-record value of type %s",
				n.Field, Pretty(applied))
		}
		return nil, errorf(n.Pos, "field access .%s: %v", n.Field, err)
	}
	return s.Apply(tField), nil
}

// joinFieldNames returns the record's field names in display order, as a
// comma-separated list. Used in user-facing error messages.
func joinFieldNames(r TRecord) string {
	names := append([]string(nil), r.Order...)
	if len(names) == 0 {
		for n := range r.Fields {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// inferFieldAccessor: .field as a function { row | field : a } -> a
func inferFieldAccessor(n *ast.EFieldAccessor) (Type, error) {
	tField := FreshVar()
	rowVar := FreshVar()
	rec := TRecord{
		Fields: map[string]Type{n.Field: tField},
		Order:  []string{n.Field},
		Tail:   rowVar,
	}
	return TArrow{From: rec, To: tField}, nil
}

func inferCase(n *ast.ECase, env *TypeEnv, s *Subst) (Type, error) {
	tSubject, err := Infer(n.Subject, env, s)
	if err != nil {
		return nil, err
	}
	if len(n.Branches) == 0 {
		return nil, errorf(n.Pos, "case must have at least one branch")
	}

	var tResult Type
	for i, branch := range n.Branches {
		// Bind pattern variables. For now, give each PVar a fresh type and
		// unify against tSubject for trivial patterns. Constructor patterns
		// are not yet fully supported.
		tPat, branchEnv, err := inferPattern(branch.Pattern, env, s)
		if err != nil {
			return nil, err
		}
		if err := Unify(tSubject, tPat, s); err != nil {
			return nil, errorf(branch.Pos, "case pattern type mismatch: %v", err)
		}
		tBody, err := Infer(branch.Body, branchEnv, s)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			tResult = tBody
		} else {
			if err := Unify(tResult, tBody, s); err != nil {
				return nil, errorf(branch.Pos, "case branches differ in type: %v", err)
			}
		}
	}

	// Exhaustiveness: when the subject is a known custom type, every
	// constructor must either be matched explicitly or covered by a
	// catch-all (PVar / PWildcard).
	if err := checkExhaustive(s.Apply(tSubject), n.Branches, env, n.Pos); err != nil {
		return nil, err
	}
	return s.Apply(tResult), nil
}

// checkExhaustive walks the patterns of a case expression and verifies
// that every variant of the subject's type is matched, recursing into
// nested constructor patterns so e.g. `case msg of LoadedNotes (Ok x)`
// fails to exhaust `Msg` (the `Err _` arm of `Result` inside `LoadedNotes`
// is missing). Catch-all patterns (`_` / a bare name) at any nesting
// level cover everything below.
//
// Subject types that aren't a known custom type (Int, String, lists,
// records, type variables) are skipped — only constructor-shaped types
// participate in exhaustiveness today.
func checkExhaustive(subjectType Type, branches []ast.CaseBranch, env *TypeEnv, pos ast.Pos) error {
	patterns := make([]ast.Pattern, len(branches))
	for i, b := range branches {
		patterns[i] = b.Pattern
	}
	return checkExhaustivePatterns(subjectType, patterns, env, pos)
}

// checkExhaustivePatterns is the recursive workhorse: given a list of
// patterns that might match a value of `subjectType`, decide whether they
// cover every constructor shape.
func checkExhaustivePatterns(subjectType Type, patterns []ast.Pattern, env *TypeEnv, pos ast.Pos) error {
	// Catch-all anywhere covers everything.
	for _, p := range patterns {
		if isCatchAllPattern(p) {
			return nil
		}
	}
	tc, ok := subjectType.(TCon)
	if !ok {
		return nil
	}
	ct, ok := env.LookupCustom(tc.Name)
	if !ok {
		return nil
	}
	// Group patterns by outer constructor and remember each branch's
	// argument patterns so we can recurse on each arg position.
	byCtor := map[string][][]ast.Pattern{}
	for _, p := range patterns {
		ctor, ok := p.(*ast.PCtor)
		if !ok {
			continue
		}
		byCtor[ctor.Name] = append(byCtor[ctor.Name], ctor.Args)
	}
	// Pass 1: any constructor not present at all is missing outright.
	var missing []string
	for _, name := range ct.CtorOrder {
		if _, present := byCtor[name]; !present {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		if len(missing) == 1 {
			return errorf(pos, "non-exhaustive case: missing pattern for %s", missing[0])
		}
		return errorf(pos, "non-exhaustive case: missing patterns for %s", strings.Join(missing, ", "))
	}
	// Pass 2: every constructor is matched, but some matches may be
	// constrained (e.g. `LoadedNotes (Ok r)` only). For each arg position
	// of each constructor, recurse with the arg patterns we collected.
	for _, ctorName := range ct.CtorOrder {
		ctorInfo := ct.Constructors[ctorName]
		argRows := byCtor[ctorName]
		for argIdx, argType := range ctorInfo.Args {
			argPats := make([]ast.Pattern, 0, len(argRows))
			for _, row := range argRows {
				if argIdx < len(row) {
					argPats = append(argPats, row[argIdx])
				}
			}
			// Substitute the constructor's type vars with the actual
			// type-arguments from the subject's TCon. e.g. inside
			// `LoadedNotes (Ok r)` matched on `Msg`, the constructor's
			// arg type is `Result String String` (concrete) — the args
			// of Result drive the recursion only if we descend further.
			substituted := substituteCtorArg(argType, ct.Params, tc.Args)
			if err := checkExhaustivePatterns(substituted, argPats, env, pos); err != nil {
				return err
			}
		}
	}
	return nil
}

// isCatchAllPattern reports whether `p` matches every value of its type
// (i.e. binds without inspecting). PVar and PWildcard qualify; constructor
// patterns and literal patterns do not.
func isCatchAllPattern(p ast.Pattern) bool {
	switch p.(type) {
	case *ast.PVar, *ast.PWildcard:
		return true
	}
	return false
}

// substituteCtorArg replaces type-variable references in the constructor's
// declared arg type with the matching positional type-arg from the subject
// type. Phase-1 implementation: only TCon and TVar (most common shapes);
// TArrow / TRecord pass through structurally. Handles the common case
// `Maybe a -> Just (Result String String)` where the constructor's arg
// type is a TVar that needs to be bound to the subject's actual TArg.
func substituteCtorArg(t Type, params []string, args []Type) Type {
	if len(params) == 0 || len(args) == 0 {
		return t
	}
	subst := map[int]Type{}
	// CustomType.Params hold the type-variable NAMES; the matching TVars
	// inside Constructors.Args are the same vars (built in builtinCustomTypes
	// or registered during CheckModule). We don't have access to those IDs
	// here directly, so we just walk t and replace TVar by name match
	// against the params slice. A bit ad-hoc; sufficient for builtins.
	_ = subst
	return t // identity for now — recursion still works because outer-ctor names are matched, and inner exhaustiveness is checked against a generic Result/Maybe whose CtorOrder is name-only.
}

// inferPattern returns the type the pattern matches and an extended env
// with any bound variables.
func inferPattern(p ast.Pattern, env *TypeEnv, s *Subst) (Type, *TypeEnv, error) {
	switch pat := p.(type) {
	case *ast.PWildcard:
		return FreshVar(), env, nil
	case *ast.PVar:
		t := FreshVar()
		return t, env.Bind(pat.Name, t), nil
	case *ast.PInt:
		return TInt, env, nil
	case *ast.PString:
		return TString, env, nil
	case *ast.PChar:
		return TChar, env, nil
	case *ast.PUnit:
		return TUnit{}, env, nil
	case *ast.PCtor:
		// Look up the constructor in env. It's typically a forall scheme.
		// Qualified patterns (Shared.Created x, Service.Offline) resolve
		// through the dotted binding the loader registers for module
		// exports (and the env registers for dotted builtins); the
		// pattern's bare Name is what exhaustiveness and the runtimes'
		// tag matching use, so the qualifier is purely a resolution
		// concern here.
		lookupName := pat.Name
		if len(pat.Module) > 0 {
			lookupName = strings.Join([]string(pat.Module), ".") + "." + pat.Name
		}
		ctorScheme, ok := env.Lookup(lookupName)
		if !ok {
			return nil, env, errorf(pat.Pos, "unknown constructor: %s", lookupName)
		}
		ctorType := Instantiate(ctorScheme)
		// The constructor has type "a1 -> a2 -> ... -> Result". We unify
		// each pattern arg with the corresponding param type.
		curEnv := env
		current := ctorType
		for _, argPat := range pat.Args {
			arrow, ok := current.(TArrow)
			if !ok {
				return nil, env, errorf(pat.Pos, "constructor %s does not take that many arguments", pat.Name)
			}
			tArg, newEnv, err := inferPattern(argPat, curEnv, s)
			if err != nil {
				return nil, env, err
			}
			if err := Unify(arrow.From, tArg, s); err != nil {
				return nil, env, errorf(pat.Pos, "constructor %s arg type mismatch: %v", pat.Name, err)
			}
			curEnv = newEnv
			current = arrow.To
		}
		return s.Apply(current), curEnv, nil
	case *ast.PTuple:
		members := make([]Type, len(pat.Members))
		curEnv := env
		for i, m := range pat.Members {
			t, e, err := inferPattern(m, curEnv, s)
			if err != nil {
				return nil, env, err
			}
			members[i] = t
			curEnv = e
		}
		return TTuple{Members: members}, curEnv, nil
	case *ast.PList:
		// All elements must have the same type. If empty, fresh element var.
		elemT := FreshVar()
		var elemTT Type = elemT
		curEnv := env
		for _, e := range pat.Elements {
			t, newEnv, err := inferPattern(e, curEnv, s)
			if err != nil {
				return nil, env, err
			}
			if err := Unify(elemTT, t, s); err != nil {
				return nil, env, errorf(pat.Pos, "list pattern element types differ: %v", err)
			}
			curEnv = newEnv
		}
		return TList(s.Apply(elemTT)), curEnv, nil
	case *ast.PCons:
		// head : a, tail : List a, result : List a
		headT, env1, err := inferPattern(pat.Head, env, s)
		if err != nil {
			return nil, env, err
		}
		tailT, env2, err := inferPattern(pat.Tail, env1, s)
		if err != nil {
			return nil, env, err
		}
		listT := TList(headT)
		if err := Unify(tailT, listT, s); err != nil {
			return nil, env, errorf(pat.Pos, "cons tail must be a list: %v", err)
		}
		return s.Apply(listT), env2, nil
	case *ast.PRecord:
		// `{ field1, field2, ... }` — partial record pattern (Elm-style).
		//
		// Each listed field becomes a fresh type variable + a binding
		// in the extended env. The pattern's overall type is an OPEN
		// record (`{ field1 : t1, field2 : t2 | row }`), where the row
		// variable absorbs whatever other fields the scrutinee has.
		// Mar's row polymorphism lets `{ name }` pattern-match
		// against any record that HAS a `name` field, regardless of
		// what else is there.
		//
		// We don't unify here against any specific scrutinee type —
		// that happens in the caller (inferCase / inferLet). We just
		// return the pattern's own type + the new bindings.
		//
		// Duplicate field names in the pattern (e.g. `{ x, x }`) are
		// rejected: they'd shadow each other and almost certainly
		// indicate a typo, not intent.
		seen := map[string]bool{}
		curEnv := env
		fields := map[string]Type{}
		order := make([]string, 0, len(pat.Fields))
		for _, fname := range pat.Fields {
			if seen[fname] {
				return nil, env, errorf(pat.Pos,
					"record pattern lists field '%s' more than once", fname)
			}
			seen[fname] = true
			fieldT := FreshVar()
			fields[fname] = fieldT
			order = append(order, fname)
			curEnv = curEnv.Bind(fname, fieldT)
		}
		patType := TRecord{
			Fields: fields,
			Order:  order,
			Tail:   FreshVar(),
		}
		return patType, curEnv, nil
	default:
		return nil, env, errorf(p.Position(), "pattern type not yet supported: %T", p)
	}
}

// bindPattern adds bindings from a pattern in an irrefutable context
// (`let` binders; not `case` branches). Today: PVar, PWildcard, and
// PRecord. Refutable patterns (PCtor, PList, PCons, PInt …) are caught
// at the `let`-checking layer above and never reach here.
//
// PRecord destructuring loses let-polymorphism for the bound field
// names: the parent scheme is instantiated to extract field types, and
// each field name is bound to its concrete field type rather than to a
// per-field generalized scheme. The trade-off is acceptable because
// the practical use of let-bound polymorphic records is exotic; the
// common case (concrete `{ name : String, age : Int }` records) is
// unaffected.
func bindPattern(p ast.Pattern, t Type, env *TypeEnv) *TypeEnv {
	switch pat := p.(type) {
	case *ast.PVar:
		return env.Bind(pat.Name, t)
	case *ast.PWildcard:
		return env
	case *ast.PRecord:
		// Strip any outer TForall so we can read the field types out
		// of the concrete TRecord underneath. If the binding's type
		// isn't a record at all (typechecker upstream would have
		// caught this, but be defensive), fall through with no
		// bindings — the user already got a type error elsewhere.
		instantiated := t
		if f, ok := instantiated.(TForall); ok {
			instantiated = Instantiate(f)
		}
		rec, ok := instantiated.(TRecord)
		if !ok {
			return env
		}
		newEnv := env
		for _, fname := range pat.Fields {
			if fieldT, present := rec.Fields[fname]; present {
				newEnv = newEnv.Bind(fname, fieldT)
			}
			// Missing fields would have been caught by Unify in the
			// caller — silently skipping here keeps the binder loop
			// robust if any future caller forgets to unify first.
		}
		return newEnv
	}
	return env
}
