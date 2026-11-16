package typecheck

import "mar/internal/ast"

// Subst is a substitution: a mapping from type variable IDs to types.
//
// Substitutions are accumulated during unification. To apply a substitution
// to a type (replacing all bound vars with their bindings), use Apply.
//
// A binding is stored as unification makes it: its right-hand side may
// mention variables that are bound too, and Apply (fully) and resolve (the
// head only) chase such chains, t1 -> t2 -> Int, transitively.
type Subst struct {
	bindings map[int]Type

	// exprTypes optionally records the inferred type for every
	// expression Infer visits. Lazy-initialized via EnableExprTracking;
	// nil = recording disabled. Used by the boundary shape lint
	// (shape_lint.go) to look up the type of non-literal record values
	// like `body = input.body` or `createdAt = now`.
	exprTypes map[ast.Expr]Type

	// resolved memoises Apply per variable while ExtractExprTypes runs,
	// and is nil at every other time. It is only sound there: nothing is
	// bound between the first lookup and the last, so a variable's fully
	// applied type cannot change under it.
	resolved map[int]Type
}

// NewSubst returns an empty substitution.
func NewSubst() *Subst {
	return &Subst{bindings: map[int]Type{}}
}

// EnableExprTracking turns on per-expression type recording for this
// substitution. Must be called BEFORE Infer is invoked so the very
// first inferred node lands in the map. Idempotent: calling twice
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
// sees concrete shapes, String, Int, { email : String, … }, not
// dangling t37 placeholders.
func (s *Subst) ExtractExprTypes() map[ast.Expr]Type {
	if s.exprTypes == nil {
		return nil
	}
	out := make(map[ast.Expr]Type, len(s.exprTypes))
	// One function's `model` is one variable read at hundreds of nodes;
	// resolving it once per NODE rebuilt the same record chain each time.
	s.resolved = map[int]Type{}
	for e, t := range s.exprTypes {
		out[e] = s.Apply(t)
	}
	s.resolved = nil
	return out
}

// Bind records that variable `id` should resolve to `t`. Performs no
// occurs check or composition; callers (e.g., Unify) are responsible.
func (s *Subst) Bind(id int, t Type) {
	s.bindings[id] = t
}

// Apply walks t, replacing every variable that has a binding (transitively)
// with its resolved type.
//
// The result shares every subtree the substitution does not touch with t: a
// node is rebuilt only when something below it actually changed. Types are
// immutable values (nothing in the checker writes into a TRecord's field map
// or a TCon's argument slice after building it), so sharing is unobservable,
// and it is what keeps a fully resolved type, a page's Model record, say,
// from being deep-copied every time an expression that mentions it is typed.
func (s *Subst) Apply(t Type) Type {
	out, _ := s.apply(t)
	return out
}

// resolve chases the binding chain at the HEAD of t and nothing else: the
// result is either an unbound variable or a non-variable node whose children
// are left exactly as they were. Unify and occursIn only ever look at the
// head before recursing, so this is all they need.
func (s *Subst) resolve(t Type) Type {
	for {
		v, ok := t.(TVar)
		if !ok {
			return t
		}
		bound, ok := s.bindings[v.ID]
		if !ok {
			return t
		}
		t = bound
	}
}

// apply is Apply plus "did anything change", which is what lets a parent
// return itself instead of a copy.
func (s *Subst) apply(t Type) (Type, bool) {
	switch v := t.(type) {
	case TVar:
		if s.resolved != nil {
			if out, ok := s.resolved[v.ID]; ok {
				return out, true
			}
		}
		bound, ok := s.bindings[v.ID]
		if !ok {
			return t, false
		}
		out, _ := s.apply(bound)
		if s.resolved != nil {
			s.resolved[v.ID] = out
		}
		return out, true
	case TCon:
		for i, a := range v.Args {
			na, changed := s.apply(a)
			if !changed {
				continue
			}
			args := make([]Type, len(v.Args))
			copy(args, v.Args[:i])
			args[i] = na
			for j := i + 1; j < len(v.Args); j++ {
				args[j], _ = s.apply(v.Args[j])
			}
			return TCon{Name: v.Name, Args: args}, true
		}
		return t, false
	case TArrow:
		from, c1 := s.apply(v.From)
		to, c2 := s.apply(v.To)
		if !c1 && !c2 {
			return t, false
		}
		return TArrow{From: from, To: to}, true
	case TRecord:
		var fields map[string]Type
		for n, f := range v.Fields {
			nf, changed := s.apply(f)
			if !changed {
				continue
			}
			if fields == nil {
				fields = make(map[string]Type, len(v.Fields))
				for k, x := range v.Fields {
					fields[k] = x
				}
			}
			fields[n] = nf
		}
		tail, tailChanged := v.Tail, false
		if v.Tail != nil {
			tail, tailChanged = s.apply(v.Tail)
		}
		if fields == nil && !tailChanged {
			return t, false
		}
		if fields == nil {
			fields = v.Fields
		}
		return TRecord{Fields: fields, Order: v.Order, Tail: tail}, true
	case TUnit:
		return t, false
	case TTuple:
		for i, m := range v.Members {
			nm, changed := s.apply(m)
			if !changed {
				continue
			}
			members := make([]Type, len(v.Members))
			copy(members, v.Members[:i])
			members[i] = nm
			for j := i + 1; j < len(v.Members); j++ {
				members[j], _ = s.apply(v.Members[j])
			}
			return TTuple{Members: members}, true
		}
		return t, false
	case TForall:
		// Don't substitute under bound vars
		bound := map[int]bool{}
		for _, id := range v.Vars {
			bound[id] = true
		}
		return TForall{Vars: v.Vars, Body: s.applyAvoiding(v.Body, bound)}, true
	}
	return t, false
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
