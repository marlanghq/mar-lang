package typecheck

// Generalize quantifies the free variables of t that are NOT free in env.
//
//	generalize(env, t) = forall (FV(t) - FV(env)). t
//
// This is the "let-generalization" step: when a let-bound expression has been
// inferred to have type t, the variables free in t that don't appear in the
// outer environment are made polymorphic.
func Generalize(env *TypeEnv, t Type, s *Subst) Type {
	t = s.Apply(t)
	tFree := s.FreeVars(t)
	envFree := envFreeVars(env, s)

	var qVars []int
	for id := range tFree {
		if !envFree[id] {
			qVars = append(qVars, id)
		}
	}
	if len(qVars) == 0 {
		return t
	}
	return TForall{Vars: qVars, Body: t}
}

// Instantiate strips a TForall and replaces each quantified variable
// with a fresh one. If t is not a TForall, it's returned as-is.
//
// Kind preservation: when a quantified var is Comparable (or any
// future restricted Kind), the fresh var that replaces it MUST carry
// the same Kind. The forall header stores only IDs (`[]int`), so we
// walk the body once to harvest each ID's Kind before generating
// replacements. Without this, instantiating `forall k:Comparable. Dict
// k v` would produce `Dict t999 v` with `t999` unconstrained — and
// the comparable check would never fire at the call site.
func Instantiate(t Type) Type {
	f, ok := t.(TForall)
	if !ok {
		return t
	}
	kinds := harvestKinds(f.Body, f.Vars)
	mapping := make(map[int]Type, len(f.Vars))
	for _, id := range f.Vars {
		mapping[id] = TVar{ID: int(atomicNextVarID()), Constraint: kinds[id]}
	}
	return substituteVars(f.Body, mapping)
}

// harvestKinds walks t looking for every TVar whose ID is in `ids`
// and records its Constraint. Returns a map[id]Kind suitable for
// constructing replacement TVars.
//
// Each quantified var should appear at least once in the body (else
// quantifying it was pointless), so the lookup is well-defined.
// Vars that don't appear default to KindAny — harmless, since they
// won't show up in the substituted result anyway.
func harvestKinds(t Type, ids []int) map[int]Kind {
	want := make(map[int]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	out := make(map[int]Kind, len(ids))
	var walk func(Type)
	walk = func(t Type) {
		switch v := t.(type) {
		case TVar:
			if want[v.ID] {
				// First wins; subsequent occurrences should match
				// because the same ID can't have two different
				// constraints in a well-formed scheme.
				if _, seen := out[v.ID]; !seen {
					out[v.ID] = v.Constraint
				}
			}
		case TCon:
			for _, a := range v.Args {
				walk(a)
			}
		case TArrow:
			walk(v.From)
			walk(v.To)
		case TRecord:
			for _, f := range v.Fields {
				walk(f)
			}
			if v.Tail != nil {
				walk(v.Tail)
			}
		case TTuple:
			for _, m := range v.Members {
				walk(m)
			}
		case TForall:
			walk(v.Body)
		}
	}
	walk(t)
	return out
}

func substituteVars(t Type, m map[int]Type) Type {
	switch v := t.(type) {
	case TVar:
		if r, ok := m[v.ID]; ok {
			return r
		}
		return v
	case TCon:
		if len(v.Args) == 0 {
			return v
		}
		args := make([]Type, len(v.Args))
		for i, a := range v.Args {
			args[i] = substituteVars(a, m)
		}
		return TCon{Name: v.Name, Args: args}
	case TArrow:
		return TArrow{From: substituteVars(v.From, m), To: substituteVars(v.To, m)}
	case TRecord:
		fields := make(map[string]Type, len(v.Fields))
		for n, f := range v.Fields {
			fields[n] = substituteVars(f, m)
		}
		var tail Type
		if v.Tail != nil {
			tail = substituteVars(v.Tail, m)
		}
		return TRecord{Fields: fields, Order: v.Order, Tail: tail}
	case TUnit:
		return v
	case TTuple:
		members := make([]Type, len(v.Members))
		for i, mem := range v.Members {
			members[i] = substituteVars(mem, m)
		}
		return TTuple{Members: members}
	case TForall:
		// Skip variables that are already quantified in the inner forall.
		inner := map[int]Type{}
		quant := map[int]bool{}
		for _, id := range v.Vars {
			quant[id] = true
		}
		for k, repl := range m {
			if !quant[k] {
				inner[k] = repl
			}
		}
		return TForall{Vars: v.Vars, Body: substituteVars(v.Body, inner)}
	}
	return t
}

func envFreeVars(env *TypeEnv, s *Subst) map[int]bool {
	out := map[int]bool{}
	for _, t := range env.bindings {
		for id := range s.FreeVars(t) {
			out[id] = true
		}
	}
	return out
}
