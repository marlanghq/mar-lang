package typecheck

import "fmt"

// UnifyError is returned by Unify when two types cannot be made equal.
type UnifyError struct {
	A, B    Type
	Reason  string
}

func (e *UnifyError) Error() string {
	// Use a shared renamer so the same var has the same letter on both sides.
	r := newRenamer()
	r.collect(e.A)
	r.collect(e.B)
	a := r.format(e.A)
	b := r.format(e.B)
	if e.Reason == "" {
		return fmt.Sprintf("cannot unify %s with %s", a, b)
	}
	return fmt.Sprintf("cannot unify %s with %s: %s", a, b, e.Reason)
}

// Unify makes a and b equal by extending s with the necessary bindings.
// Returns an error if the types are incompatible (occurs check or structural
// mismatch).
func Unify(a, b Type, s *Subst) error {
	a = s.Apply(a)
	b = s.Apply(b)

	// Var on either side: bind (with occurs check)
	if va, ok := a.(TVar); ok {
		return bindVar(va, b, s)
	}
	if vb, ok := b.(TVar); ok {
		return bindVar(vb, a, s)
	}

	// Same shape?
	switch av := a.(type) {
	case TCon:
		bv, ok := b.(TCon)
		if !ok {
			return &UnifyError{A: a, B: b}
		}
		if av.Name != bv.Name {
			return &UnifyError{A: a, B: b, Reason: "different type constructors"}
		}
		if len(av.Args) != len(bv.Args) {
			return &UnifyError{A: a, B: b, Reason: "arity mismatch"}
		}
		for i := range av.Args {
			if err := Unify(av.Args[i], bv.Args[i], s); err != nil {
				return err
			}
		}
		return nil

	case TArrow:
		bv, ok := b.(TArrow)
		if !ok {
			return &UnifyError{A: a, B: b}
		}
		if err := Unify(av.From, bv.From, s); err != nil {
			return err
		}
		return Unify(av.To, bv.To, s)

	case TUnit:
		if _, ok := b.(TUnit); ok {
			return nil
		}
		return &UnifyError{A: a, B: b}

	case TTuple:
		bv, ok := b.(TTuple)
		if !ok {
			return &UnifyError{A: a, B: b}
		}
		if len(av.Members) != len(bv.Members) {
			return &UnifyError{A: a, B: b, Reason: "tuple arity mismatch"}
		}
		for i := range av.Members {
			if err := Unify(av.Members[i], bv.Members[i], s); err != nil {
				return err
			}
		}
		return nil

	case TRecord:
		bv, ok := b.(TRecord)
		if !ok {
			return &UnifyError{A: a, B: b}
		}
		return unifyRecords(av, bv, s)
	}

	return &UnifyError{A: a, B: b, Reason: "unsupported type pair"}
}

func bindVar(v TVar, t Type, s *Subst) error {
	if other, ok := t.(TVar); ok && other.ID == v.ID {
		return nil // already equal
	}
	if occursIn(v.ID, t, s) {
		return &UnifyError{A: v, B: t, Reason: "occurs check"}
	}
	s.Bind(v.ID, t)
	return nil
}

// occursIn checks whether v appears anywhere in t (after applying s).
func occursIn(id int, t Type, s *Subst) bool {
	t = s.Apply(t)
	switch v := t.(type) {
	case TVar:
		return v.ID == id
	case TCon:
		for _, a := range v.Args {
			if occursIn(id, a, s) {
				return true
			}
		}
	case TArrow:
		return occursIn(id, v.From, s) || occursIn(id, v.To, s)
	case TRecord:
		for _, f := range v.Fields {
			if occursIn(id, f, s) {
				return true
			}
		}
		if v.Tail != nil && occursIn(id, v.Tail, s) {
			return true
		}
	case TTuple:
		for _, m := range v.Members {
			if occursIn(id, m, s) {
				return true
			}
		}
	}
	return false
}

// unifyRecords unifies two record types, supporting row polymorphism.
//
//   - closed vs closed: fields must match exactly.
//   - open vs closed: open's fields must be subset; the open's tail unifies
//     with the closed's "remainder" record (the fields the open doesn't have).
//   - open vs open: shared fields unify; remaining fields go into a fresh
//     row variable that becomes the new tail of both.
func unifyRecords(a, b TRecord, s *Subst) error {
	// Unify shared fields first
	shared := []string{}
	aOnly := []string{}
	bOnly := []string{}
	for n := range a.Fields {
		if _, ok := b.Fields[n]; ok {
			shared = append(shared, n)
		} else {
			aOnly = append(aOnly, n)
		}
	}
	for n := range b.Fields {
		if _, ok := a.Fields[n]; !ok {
			bOnly = append(bOnly, n)
		}
	}

	for _, n := range shared {
		if err := Unify(a.Fields[n], b.Fields[n], s); err != nil {
			return err
		}
	}

	switch {
	case a.Tail == nil && b.Tail == nil:
		// closed vs closed: must match exactly
		if len(aOnly) > 0 || len(bOnly) > 0 {
			return &UnifyError{A: a, B: b, Reason: "different field sets"}
		}
		return nil

	case a.Tail == nil && b.Tail != nil:
		// b open, a closed: b's tail = the fields a has that b lacks (i.e. aOnly)
		if len(bOnly) > 0 {
			return &UnifyError{A: a, B: b, Reason: "open record requires fields the closed one lacks"}
		}
		return Unify(b.Tail, makeRowExtension(aOnly, a, nil), s)

	case a.Tail != nil && b.Tail == nil:
		// a open, b closed: symmetric
		if len(aOnly) > 0 {
			return &UnifyError{A: a, B: b, Reason: "open record requires fields the closed one lacks"}
		}
		return Unify(a.Tail, makeRowExtension(bOnly, b, nil), s)

	default:
		// both open: shared unified above; remaining fields combine into a new row
		freshRow := FreshVar()
		// a.Tail = { bOnly fields | freshRow }
		if err := Unify(a.Tail, makeRowExtension(bOnly, b, freshRow), s); err != nil {
			return err
		}
		// b.Tail = { aOnly fields | freshRow }
		return Unify(b.Tail, makeRowExtension(aOnly, a, freshRow), s)
	}
}

// makeRowExtension constructs a record type with the given field names
// (sourced from src) and the given tail.
func makeRowExtension(names []string, src TRecord, tail Type) TRecord {
	fields := make(map[string]Type, len(names))
	order := make([]string, 0, len(names))
	for _, n := range names {
		fields[n] = src.Fields[n]
		order = append(order, n)
	}
	return TRecord{Fields: fields, Order: order, Tail: tail}
}
