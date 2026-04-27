package types

import "sort"


// Subst maps type variable IDs to their bound types. Bindings accumulate
// during unification. Variables not in the map are unbound.
//
// Subst is not safe for concurrent use. Each inference run builds its own.
type Subst struct {
	bindings map[int]Type
}

// NewSubst returns an empty substitution.
func NewSubst() *Subst {
	return &Subst{bindings: map[int]Type{}}
}

// Bind records that variable id is now bound to t. Callers must ensure the
// occurs check has already been performed.
func (s *Subst) Bind(id int, t Type) {
	s.bindings[id] = t
}

// Lookup returns the type bound to id, if any.
func (s *Subst) Lookup(id int) (Type, bool) {
	t, ok := s.bindings[id]
	return t, ok
}

// Resolve walks variable bindings to find the current root type. If the
// argument is a bound variable, follows the chain until reaching either an
// unbound variable or a non-variable type.
func (s *Subst) Resolve(t Type) Type {
	for {
		v, ok := t.(TVar)
		if !ok {
			return t
		}
		next, found := s.bindings[v.ID]
		if !found {
			return v
		}
		t = next
	}
}

// Apply returns t with every type variable replaced by its current binding,
// recursively. Unbound variables are left as-is. The original t is not
// modified; structural sharing is preserved when nothing changes.
func (s *Subst) Apply(t Type) Type {
	switch tt := t.(type) {
	case TVar:
		bound, ok := s.bindings[tt.ID]
		if !ok {
			return tt
		}
		return s.Apply(bound)

	case TCon:
		if len(tt.Args) == 0 {
			return tt
		}
		newArgs := make([]Type, len(tt.Args))
		for i, a := range tt.Args {
			newArgs[i] = s.Apply(a)
		}
		return TCon{Name: tt.Name, Args: newArgs}

	case TRecord:
		newFields := make(map[string]Type, len(tt.Fields))
		for name, ft := range tt.Fields {
			newFields[name] = s.Apply(ft)
		}
		var newTail Type
		if tt.Tail != nil {
			newTail = s.Apply(tt.Tail)
		}
		// If tail resolved to another open record, merge its fields into this
		// one (keeps the resulting record canonical: known fields + final tail).
		if newTail != nil {
			if other, ok := newTail.(TRecord); ok {
				for name, t := range other.Fields {
					if _, exists := newFields[name]; !exists {
						newFields[name] = t
					}
				}
				newTail = other.Tail
			}
		}
		order := mergedOrder(tt.Order, newFields)
		return TRecord{Name: tt.Name, Fields: newFields, Order: order, Tail: newTail}

	case TUnion:
		newVariants := make(map[string][]Type, len(tt.Variants))
		for tag, payload := range tt.Variants {
			newPayload := make([]Type, len(payload))
			for i, p := range payload {
				newPayload[i] = s.Apply(p)
			}
			newVariants[tag] = newPayload
		}
		return TUnion{
			Name:         tt.Name,
			Variants:     newVariants,
			VariantOrder: tt.VariantOrder,
			FieldNames:   tt.FieldNames,
		}

	case TForall:
		// Apply to the body, but never substitute the quantified variables.
		// Hide them temporarily so they pass through untouched.
		hidden := map[int]Type{}
		for _, v := range tt.Vars {
			if existing, ok := s.bindings[v]; ok {
				hidden[v] = existing
				delete(s.bindings, v)
			}
		}
		newBody := s.Apply(tt.Body)
		for v, existing := range hidden {
			s.bindings[v] = existing
		}
		return TForall{Vars: tt.Vars, Body: newBody}

	default:
		return t
	}
}

// Size returns the number of bindings in the substitution. Useful for tests
// and debugging.
func (s *Subst) Size() int {
	return len(s.bindings)
}

// mergedOrder produces a stable field-name list for a record after merging
// fields from a tail into the original. Preserves the original Order, then
// appends any new field names alphabetically.
func mergedOrder(originalOrder []string, fields map[string]Type) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(fields))
	for _, name := range originalOrder {
		if _, ok := fields[name]; ok && !seen[name] {
			out = append(out, name)
			seen[name] = true
		}
	}
	extras := []string{}
	for name := range fields {
		if !seen[name] {
			extras = append(extras, name)
		}
	}
	sort.Strings(extras)
	return append(out, extras...)
}
