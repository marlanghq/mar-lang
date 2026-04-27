package types

// TypeEnv maps identifier names to their inferred types. A type may be a
// monomorphic Type (lambda parameters, let-bound non-functions) or a TForall
// scheme (top-level definitions, builtins, generalized let bindings).
//
// TypeEnv is immutable from the outside: Bind returns a new env that shares
// underlying storage where possible. Use Extend to add many bindings at once.
type TypeEnv struct {
	parent   *TypeEnv
	bindings map[string]Type
}

// NewTypeEnv returns an empty environment.
func NewTypeEnv() *TypeEnv {
	return &TypeEnv{bindings: map[string]Type{}}
}

// Lookup returns the type bound to name, walking up parent envs.
func (e *TypeEnv) Lookup(name string) (Type, bool) {
	for env := e; env != nil; env = env.parent {
		if t, ok := env.bindings[name]; ok {
			return t, true
		}
	}
	return nil, false
}

// Bind returns a new env with name → t added on top of the current scope.
// The original env is not modified.
func (e *TypeEnv) Bind(name string, t Type) *TypeEnv {
	return &TypeEnv{
		parent:   e,
		bindings: map[string]Type{name: t},
	}
}

// Extend returns a new env with multiple bindings added. Useful when binding
// all parameters of a function in one step.
func (e *TypeEnv) Extend(bindings map[string]Type) *TypeEnv {
	if len(bindings) == 0 {
		return e
	}
	cloned := make(map[string]Type, len(bindings))
	for k, v := range bindings {
		cloned[k] = v
	}
	return &TypeEnv{parent: e, bindings: cloned}
}

// FreeVars collects free type variables across all bindings in this env and
// its parents. Used by Generalize to know which vars are "in scope" and must
// not be quantified.
func (e *TypeEnv) FreeVars(s *Subst) []int {
	seen := map[int]struct{}{}
	for env := e; env != nil; env = env.parent {
		for _, t := range env.bindings {
			collectFreeVars(s.Apply(t), nil, seen)
		}
	}
	out := make([]int, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}

// Generalize quantifies the free variables of t that are not free in env. The
// result is a TForall scheme suitable for storing in an env.
//
// If no quantification is needed, returns t unchanged (not wrapped in TForall
// with empty Vars).
func Generalize(env *TypeEnv, s *Subst, t Type) Type {
	t = s.Apply(t)
	envFree := map[int]struct{}{}
	for _, id := range env.FreeVars(s) {
		envFree[id] = struct{}{}
	}
	free := FreeVars(t)
	quantified := make([]int, 0, len(free))
	for _, id := range free {
		if _, inEnv := envFree[id]; !inEnv {
			quantified = append(quantified, id)
		}
	}
	if len(quantified) == 0 {
		return t
	}
	return TForall{Vars: quantified, Body: t}
}

// Instantiate returns a fresh-variable copy of a type scheme. If t is not a
// TForall, returns t unchanged (the type is already monomorphic).
//
// Each quantified variable is replaced by a freshly-allocated TVar, so each
// instantiation is independent — distinct call sites get distinct variables.
func Instantiate(t Type) Type {
	scheme, ok := t.(TForall)
	if !ok {
		return t
	}
	fresh := NewSubst()
	for _, id := range scheme.Vars {
		fresh.Bind(id, FreshVar())
	}
	return fresh.Apply(scheme.Body)
}
