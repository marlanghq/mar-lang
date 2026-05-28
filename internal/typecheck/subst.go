package typecheck

import "mar/internal/ast"

// Subst is a substitution: a mapping from type variable IDs to types.
//
// Substitutions are accumulated during unification. To apply a substitution
// to a type (replacing all bound vars with their bindings), use Apply.
//
// Substitutions compose: extending an existing subst with a new binding
// applies the existing subst to the new binding's RHS first, so that
// chains like t1 -> t2 -> Int resolve in one step.
type Subst struct {
	bindings map[int]Type

	// exprTypes optionally records the inferred type for every
	// expression Infer visits. Lazy-initialized via EnableExprTracking;
	// nil = recording disabled. Used by the boundary shape lint
	// (shape_lint.go) to look up the type of non-literal record values
	// like `body = input.body` or `createdAt = now`.
	exprTypes map[ast.Expr]Type
}

// NewSubst returns an empty substitution.
func NewSubst() *Subst {
	return &Subst{bindings: map[int]Type{}}
}

// EnableExprTracking turns on per-expression type recording for this
// substitution. Must be called BEFORE Infer is invoked so the very
// first inferred node lands in the map. Idempotent — calling twice
// keeps the existing map (and any types it already holds).
func (s *Subst) EnableExprTracking() {
	if s.exprTypes == nil {
		s.exprTypes = map[ast.Expr]Type{}
	}
}

// ExtractExprTypes returns a map of every recorded expression's
// inferred type, with the substitution already applied so the
// caller doesn't have to. Nil when tracking wasn't enabled.
//
// Two passes happen during inference: Infer records the type AT THE
// MOMENT it ran (often containing fresh type variables), and later
// unifications refine those variables in `s.bindings`. ExtractExprTypes
// applies the final bindings to each recorded type so the consumer
// sees concrete shapes — String, Int, { email : String, … } — not
// dangling t37 placeholders.
func (s *Subst) ExtractExprTypes() map[ast.Expr]Type {
	if s.exprTypes == nil {
		return nil
	}
	out := make(map[ast.Expr]Type, len(s.exprTypes))
	for e, t := range s.exprTypes {
		out[e] = s.Apply(t)
	}
	return out
}

// Bind records that variable `id` should resolve to `t`. Performs no
// occurs check or composition; callers (e.g., Unify) are responsible.
func (s *Subst) Bind(id int, t Type) {
	s.bindings[id] = t
}

// Apply walks t, replacing every variable that has a binding (transitively)
// with its resolved type.
func (s *Subst) Apply(t Type) Type {
	switch v := t.(type) {
	case TVar:
		if bound, ok := s.bindings[v.ID]; ok {
			return s.Apply(bound)
		}
		return v
	case TCon:
		if len(v.Args) == 0 {
			return v
		}
		args := make([]Type, len(v.Args))
		for i, a := range v.Args {
			args[i] = s.Apply(a)
		}
		return TCon{Name: v.Name, Args: args}
	case TArrow:
		return TArrow{From: s.Apply(v.From), To: s.Apply(v.To)}
	case TRecord:
		fields := make(map[string]Type, len(v.Fields))
		for n, f := range v.Fields {
			fields[n] = s.Apply(f)
		}
		var tail Type
		if v.Tail != nil {
			tail = s.Apply(v.Tail)
		}
		return TRecord{Fields: fields, Order: v.Order, Tail: tail}
	case TUnit:
		return v
	case TTuple:
		members := make([]Type, len(v.Members))
		for i, m := range v.Members {
			members[i] = s.Apply(m)
		}
		return TTuple{Members: members}
	case TForall:
		// Don't substitute under bound vars
		bound := map[int]bool{}
		for _, id := range v.Vars {
			bound[id] = true
		}
		return TForall{Vars: v.Vars, Body: s.applyAvoiding(v.Body, bound)}
	}
	return t
}

func (s *Subst) applyAvoiding(t Type, bound map[int]bool) Type {
	switch v := t.(type) {
	case TVar:
		if bound[v.ID] {
			return v
		}
		if x, ok := s.bindings[v.ID]; ok {
			return s.applyAvoiding(x, bound)
		}
		return v
	case TCon:
		if len(v.Args) == 0 {
			return v
		}
		args := make([]Type, len(v.Args))
		for i, a := range v.Args {
			args[i] = s.applyAvoiding(a, bound)
		}
		return TCon{Name: v.Name, Args: args}
	case TArrow:
		return TArrow{From: s.applyAvoiding(v.From, bound), To: s.applyAvoiding(v.To, bound)}
	case TRecord:
		fields := make(map[string]Type, len(v.Fields))
		for n, f := range v.Fields {
			fields[n] = s.applyAvoiding(f, bound)
		}
		var tail Type
		if v.Tail != nil {
			tail = s.applyAvoiding(v.Tail, bound)
		}
		return TRecord{Fields: fields, Order: v.Order, Tail: tail}
	case TUnit:
		return v
	case TTuple:
		members := make([]Type, len(v.Members))
		for i, m := range v.Members {
			members[i] = s.applyAvoiding(m, bound)
		}
		return TTuple{Members: members}
	}
	return t
}

// FreeVars returns the set of unbound type variable IDs in t (relative to s).
func (s *Subst) FreeVars(t Type) map[int]bool {
	out := map[int]bool{}
	collectFreeVars(s.Apply(t), out, map[int]bool{})
	return out
}

func collectFreeVars(t Type, out, bound map[int]bool) {
	switch v := t.(type) {
	case TVar:
		if !bound[v.ID] {
			out[v.ID] = true
		}
	case TCon:
		for _, a := range v.Args {
			collectFreeVars(a, out, bound)
		}
	case TArrow:
		collectFreeVars(v.From, out, bound)
		collectFreeVars(v.To, out, bound)
	case TRecord:
		for _, f := range v.Fields {
			collectFreeVars(f, out, bound)
		}
		if v.Tail != nil {
			collectFreeVars(v.Tail, out, bound)
		}
	case TTuple:
		for _, m := range v.Members {
			collectFreeVars(m, out, bound)
		}
	case TForall:
		newBound := map[int]bool{}
		for k := range bound {
			newBound[k] = true
		}
		for _, id := range v.Vars {
			newBound[id] = true
		}
		collectFreeVars(v.Body, out, newBound)
	}
}
