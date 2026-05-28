package typecheck

import "fmt"

// UnifyError is returned by Unify when two types cannot be made equal.
type UnifyError struct {
	A, B   Type
	Reason string
	// KindMismatch is true when the failure is a constraint violation
	// (e.g. Comparable var bound to a Record). Callers that wrap this
	// error with a higher-level message — like "argument has the wrong
	// type" in inferApp — should bypass that wrapping when KindMismatch
	// is set, since the underlying Reason already explains the problem
	// in user-facing terms ("a record is not comparable; allowed key
	// types are Int, Float, String, Char").
	KindMismatch bool
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
			return &UnifyError{A: a, B: b}
		}
		if len(av.Args) != len(bv.Args) {
			return &UnifyError{A: a, B: b}
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
			return &UnifyError{A: a, B: b}
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

	// Constraint enforcement — Elm-style. If v carries a Kind
	// restriction (Comparable today), check that t honors it.
	//
	// Two cases:
	//
	//   1. t is a TVar — propagate the constraint. If t is weaker
	//      (KindAny) and v is stronger (KindComparable), the BOUND
	//      var becomes the stronger one so future unifications stay
	//      honest. We bind v -> t', where t' is a fresh TVar
	//      carrying the merged constraint, then bind the original
	//      t.ID to t' too. Concretely: when comparable t7 meets
	//      free t9, we promote t9 to comparable too.
	//
	//   2. t is concrete — call IsComparableType (etc. for future
	//      kinds). Fail with a clear message if it doesn't fit.
	if v.Constraint == KindComparable {
		if other, ok := t.(TVar); ok {
			// Two TVars unifying — pick the stronger constraint
			// (Comparable beats Any) and make sure both ends
			// resolve to a var that carries it.
			merged := mergeKinds(v.Constraint, other.Constraint)
			if merged == other.Constraint {
				// `other` is already at least as strong — just bind v to other.
				s.Bind(v.ID, other)
				return nil
			}
			// Promote: mint a FRESH var (new ID) with the merged
			// constraint, then bind both v and other to it. Using a
			// fresh ID matters — binding `other.ID -> TVar{ID:
			// other.ID, Constraint: Comparable}` would be a self-loop
			// that infinite-recurses inside Apply.
			fresh := TVar{ID: int(atomicNextVarID()), Constraint: merged}
			s.Bind(v.ID, fresh)
			s.Bind(other.ID, fresh)
			return nil
		}
		if !IsComparableType(t) {
			return &UnifyError{
				A:            v,
				B:            t,
				Reason:       kindMismatchReason(v.Constraint, t),
				KindMismatch: true,
			}
		}
	}

	s.Bind(v.ID, t)
	return nil
}

// mergeKinds returns the stronger of two Kinds (Comparable beats Any).
// Used when unifying two TVars so the resulting binding carries the
// most restrictive constraint either side had.
func mergeKinds(a, b Kind) Kind {
	if a == KindComparable || b == KindComparable {
		return KindComparable
	}
	return KindAny
}

// kindMismatchReason produces the human-readable explanation when a
// concrete type fails to satisfy a TVar's constraint. The message
// names the constraint and the allowed types so the user can act on
// it without having to know the typechecker internals.
func kindMismatchReason(k Kind, t Type) string {
	switch k {
	case KindComparable:
		// Identify the offending shape in plain words. Record / custom
		// type / tuple all show up here so the message names the
		// general case rather than enumerating each.
		shape := "this type"
		switch v := t.(type) {
		case TRecord:
			shape = "a record"
		case TTuple:
			shape = "a tuple"
		case TArrow:
			shape = "a function"
		case TCon:
			shape = "type " + v.Name
		}
		// Phrasing stays neutral — "key types" was Dict-specific
		// jargon; this message also fires for the ordering
		// operators (`<`, `>`, `<=`, `>=`), where "key" makes no
		// sense.
		return fmt.Sprintf(
			"%s is not comparable; comparable types are Int, Float, String, Char",
			shape,
		)
	}
	return ""
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
			return &UnifyError{A: a, B: b}
		}
		return nil

	case a.Tail == nil && b.Tail != nil:
		// b open, a closed: b's tail = the fields a has that b lacks (i.e. aOnly).
		// If the open side wants fields the closed side doesn't have, bail.
		if len(bOnly) > 0 {
			return &UnifyError{A: a, B: b}
		}
		return Unify(b.Tail, makeRowExtension(aOnly, a, nil), s)

	case a.Tail != nil && b.Tail == nil:
		if len(aOnly) > 0 {
			return &UnifyError{A: a, B: b}
		}
		return Unify(a.Tail, makeRowExtension(bOnly, b, nil), s)

	default:
		// both open: shared unified above; remaining fields combine into a new row
		freshRow := FreshVar()
		// a.Tail = { bOnly fields | freshRow }    (just freshRow if no bOnly)
		if err := Unify(a.Tail, extendTail(bOnly, b, freshRow), s); err != nil {
			return err
		}
		// b.Tail = { aOnly fields | freshRow }    (just freshRow if no aOnly)
		return Unify(b.Tail, extendTail(aOnly, a, freshRow), s)
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

// extendTail returns a type that represents "extra fields plus tail." When
// there are no extra fields, it's just the tail itself — wrapping in a
// `{Fields:{}, Tail: tail}` record creates an indirection that, after
// substitution, produces non-canonical types like `{| {| {| ...}}}` which
// makes Apply / Unify / occursIn diverge when two such tails are unified.
//
// Used by unifyRecords (open vs open) so that empty-extension tails stay
// as plain TVars.
func extendTail(extraNames []string, src TRecord, tail Type) Type {
	if len(extraNames) == 0 {
		return tail
	}
	return makeRowExtension(extraNames, src, tail)
}
