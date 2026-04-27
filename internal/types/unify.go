package types

import "fmt"

// Unify attempts to make a and b structurally equal by recording new bindings
// in s. Returns an error if no such substitution exists.
//
// Unify is the heart of Hindley-Milner. After a successful call, s.Apply(a)
// equals s.Apply(b).
func Unify(a, b Type, s *Subst) error {
	a = s.Resolve(a)
	b = s.Resolve(b)

	// Variable on either side: bind it (after occurs check).
	if av, ok := a.(TVar); ok {
		return bindVar(av, b, s)
	}
	if bv, ok := b.(TVar); ok {
		return bindVar(bv, a, s)
	}

	switch at := a.(type) {
	case TCon:
		bt, ok := b.(TCon)
		if !ok {
			return mismatch(a, b)
		}
		if at.Name != bt.Name {
			return mismatch(a, b)
		}
		if len(at.Args) != len(bt.Args) {
			return fmt.Errorf("cannot unify %s with %s: arity mismatch (%d vs %d)",
				a, b, len(at.Args), len(bt.Args))
		}
		for i := range at.Args {
			if err := Unify(at.Args[i], bt.Args[i], s); err != nil {
				return err
			}
		}
		return nil

	case TRecord:
		bt, ok := b.(TRecord)
		if !ok {
			return mismatch(a, b)
		}
		return unifyRecords(at, bt, s)

	case TUnion:
		bt, ok := b.(TUnion)
		if !ok {
			return mismatch(a, b)
		}
		// Nominal unions unify by name.
		if at.Name != "" || bt.Name != "" {
			if at.Name != bt.Name {
				return mismatch(a, b)
			}
			return nil
		}
		// Anonymous: same set of tags, unify payloads pairwise.
		if len(at.Variants) != len(bt.Variants) {
			return mismatch(a, b)
		}
		for tag, payload := range at.Variants {
			other, ok := bt.Variants[tag]
			if !ok {
				return mismatch(a, b)
			}
			if len(payload) != len(other) {
				return fmt.Errorf("cannot unify %s with %s: variant %s has different arity", a, b, tag)
			}
			for i := range payload {
				if err := Unify(payload[i], other[i], s); err != nil {
					return err
				}
			}
		}
		return nil

	case TForall:
		// TForall should only appear at the top of a scheme in the env. If we
		// see one here, something instantiated it incorrectly. Refuse rather
		// than silently muddle.
		return fmt.Errorf("unexpected polymorphic type during unification: %s", a)

	default:
		return mismatch(a, b)
	}
}

// bindVar binds variable v to t after an occurs check. If v == t, no-op.
func bindVar(v TVar, t Type, s *Subst) error {
	// Resolved equality: if t is the same variable, succeed.
	if other, ok := t.(TVar); ok && other.ID == v.ID {
		return nil
	}
	if occursIn(v.ID, t, s) {
		return fmt.Errorf("cannot construct infinite type: %s = %s", v, s.Apply(t))
	}
	s.Bind(v.ID, t)
	return nil
}

// occursIn reports whether variable id appears anywhere in t after resolving
// bindings in s. Used by the occurs check to prevent cycles like α = α → β.
func occursIn(id int, t Type, s *Subst) bool {
	t = s.Resolve(t)
	switch tt := t.(type) {
	case TVar:
		return tt.ID == id
	case TCon:
		for _, a := range tt.Args {
			if occursIn(id, a, s) {
				return true
			}
		}
		return false
	case TRecord:
		for _, ft := range tt.Fields {
			if occursIn(id, ft, s) {
				return true
			}
		}
		return false
	case TUnion:
		for _, payload := range tt.Variants {
			for _, p := range payload {
				if occursIn(id, p, s) {
					return true
				}
			}
		}
		return false
	case TForall:
		// Skip quantified vars when checking inside the body.
		quantified := map[int]bool{}
		for _, v := range tt.Vars {
			quantified[v] = true
		}
		if quantified[id] {
			return false
		}
		return occursIn(id, tt.Body, s)
	default:
		return false
	}
}

func mismatch(a, b Type) error {
	return fmt.Errorf("cannot unify %s with %s", a, b)
}

// unifyRecords handles unification between any two records (open or closed,
// nominal or anonymous). This is the row-polymorphism core.
//
// Semantics:
//   - Two closed nominal (Tail==nil, Name!=""): names must match.
//   - Two closed anonymous (Tail==nil, Name==""): same field set, unify pairwise.
//   - Closed (any) vs open: open's fields must all exist in closed; open's
//     tail unifies with a closed record of the closed side's remaining fields.
//   - Open vs open: unify common fields; tail of each gets the other's
//     extra fields plus a fresh shared tail.
func unifyRecords(a, b TRecord, s *Subst) error {
	aClosed := a.Tail == nil
	bClosed := b.Tail == nil

	// Nominal closed vs nominal closed: by-name match.
	if aClosed && bClosed && (a.Name != "" || b.Name != "") {
		if a.Name != b.Name {
			return mismatch(a, b)
		}
		return nil
	}

	// Closed anonymous vs closed anonymous: structural exact match.
	if aClosed && bClosed {
		if len(a.Fields) != len(b.Fields) {
			return mismatch(a, b)
		}
		for name, ft := range a.Fields {
			other, ok := b.Fields[name]
			if !ok {
				return mismatch(a, b)
			}
			if err := Unify(ft, other, s); err != nil {
				return err
			}
		}
		return nil
	}

	// At least one side is open. Pair up common fields, then handle tails
	// and missing fields.
	commonNames := []string{}
	missingFromA := map[string]Type{}
	missingFromB := map[string]Type{}
	for name, ta := range a.Fields {
		if tb, ok := b.Fields[name]; ok {
			if err := Unify(ta, tb, s); err != nil {
				return fmt.Errorf("field %s: %w", name, err)
			}
			commonNames = append(commonNames, name)
		} else {
			missingFromB[name] = ta
		}
	}
	for name, tb := range b.Fields {
		if _, ok := a.Fields[name]; !ok {
			missingFromA[name] = tb
		}
	}

	// Case: a closed, b open. b's missing fields must be in a (already true
	// since they're not "missing from a" if missing from b; missingFromA are
	// fields in b not in a — those would need to come from a's tail, but a
	// is closed → fail).
	if aClosed {
		if len(missingFromA) > 0 {
			names := mapKeys(missingFromA)
			return fmt.Errorf("record %s missing field(s) %s required by open record", a.Name, joinNames(names))
		}
		// Bind b's tail to a closed record of a's missing fields.
		tailRec := TRecord{Fields: missingFromB, Order: mapKeys(missingFromB), Tail: nil}
		return Unify(b.Tail, tailRec, s)
	}
	if bClosed {
		if len(missingFromB) > 0 {
			names := mapKeys(missingFromB)
			return fmt.Errorf("record %s missing field(s) %s required by open record", b.Name, joinNames(names))
		}
		tailRec := TRecord{Fields: missingFromA, Order: mapKeys(missingFromA), Tail: nil}
		return Unify(a.Tail, tailRec, s)
	}

	// Both open. Introduce a shared fresh tail; a.Tail gets b's extras + tail,
	// b.Tail gets a's extras + tail.
	freshTail := FreshVar()
	aTailRec := TRecord{Fields: missingFromA, Order: mapKeys(missingFromA), Tail: freshTail}
	bTailRec := TRecord{Fields: missingFromB, Order: mapKeys(missingFromB), Tail: freshTail}
	if err := Unify(a.Tail, aTailRec, s); err != nil {
		return err
	}
	if err := Unify(b.Tail, bTailRec, s); err != nil {
		return err
	}
	_ = commonNames
	return nil
}

func mapKeys(m map[string]Type) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
