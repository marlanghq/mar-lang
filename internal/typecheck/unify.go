package typecheck

import (
	"fmt"
	"sort"
)

// UnifyError is returned by Unify when two types cannot be made equal.
type UnifyError struct {
	A, B   Type
	Reason string
	// KindMismatch is true when the failure is a constraint violation
	// (e.g. Comparable var bound to a Record). Callers that wrap this
	// error with a higher-level message: like "argument has the wrong
	// type" in inferApp, should bypass that wrapping when KindMismatch
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
	// "cannot unify Color with Color" would be a worse message than the bug
	// it reports. When two types share a base name but not a module (ADR
	// 0027), the module is the only thing that tells them apart.
	if SameNameDifferentTypes(e.A, e.B) {
		a, b = PrettyQualified(e.A), PrettyQualified(e.B)
	}
	if e.Reason == "" {
		return fmt.Sprintf("cannot unify %s with %s", a, b)
	}
	// A constraint violation reads better without the unification framing.
	// "cannot unify comparable with Time: a Time has no order of its own"
	// asks the reader to care about the machinery; the reason alone is the
	// whole message, and it already names both the offending type and the
	// way out.
	if e.KindMismatch {
		return e.Reason
	}
	return fmt.Sprintf("cannot unify %s with %s: %s", a, b, e.Reason)
}

// Unify makes a and b equal by extending s with the necessary bindings.
// Returns an error if the types are incompatible (occurs check or structural
// mismatch).
func Unify(a, b Type, s *Subst) error {
	// Head only. Each recursive call below resolves its own head, so
	// applying the whole substitution to both sides here would deep-copy
	// the two types once per level of nesting. Errors still carry fully
	// applied types: see mismatch.
	a = s.resolve(a)
	b = s.resolve(b)

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
			return mismatch(a, b, s)
		}
		if av.Name != bv.Name {
			return mismatch(a, b, s)
		}
		if len(av.Args) != len(bv.Args) {
			return mismatch(a, b, s)
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
			return mismatch(a, b, s)
		}
		if err := Unify(av.From, bv.From, s); err != nil {
			return err
		}
		return Unify(av.To, bv.To, s)

	case TUnit:
		if _, ok := b.(TUnit); ok {
			return nil
		}
		return mismatch(a, b, s)

	case TTuple:
		bv, ok := b.(TTuple)
		if !ok {
			return mismatch(a, b, s)
		}
		if len(av.Members) != len(bv.Members) {
			return mismatch(a, b, s)
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
			return mismatch(a, b, s)
		}
		return unifyRecords(av, bv, s)
	}

	return &UnifyError{A: s.Apply(a), B: s.Apply(b), Reason: "unsupported type pair"}
}

// mismatch is the plain "these two do not unify" error. Unify works on
// head-resolved types, so the substitution is applied here, on the failure
// path, to give the message the same fully resolved types it always had.
func mismatch(a, b Type, s *Subst) *UnifyError {
	return &UnifyError{A: s.Apply(a), B: s.Apply(b)}
}

func bindVar(v TVar, t Type, s *Subst) error {
	if other, ok := t.(TVar); ok && other.ID == v.ID {
		return nil // already equal
	}
	if occursIn(v.ID, t, s) {
		return &UnifyError{A: v, B: s.Apply(t), Reason: "occurs check"}
	}

	// Constraint enforcement: Elm-style. If v carries a Kind
	// restriction (Comparable / Appendable), check that t honors it.
	//
	// Two cases:
	//
	//   1. t is a TVar: merge the constraints. If t is weaker
	//      (KindAny), the BOUND var becomes the stronger one so future
	//      unifications stay honest: we bind v -> t', where t' is a
	//      fresh TVar carrying the merged constraint, then bind the
	//      original t.ID to t' too. Two DIFFERENT non-Any kinds (a
	//      value used as both comparable and appendable) have no
	//      representable intersection, Elm's `compappend`, so we
	//      reject the overlap.
	//
	//   2. t is concrete: call satisfiesKind. Fail with a clear
	//      message naming the constraint and the allowed types.
	if v.Constraint != KindAny {
		if other, ok := t.(TVar); ok {
			// Two TVars unifying: keep the stronger constraint and
			// make sure both ends resolve to a var that carries it.
			merged, ok := mergeKinds(v.Constraint, other.Constraint)
			if !ok {
				return &UnifyError{
					A:            v,
					B:            s.Apply(t),
					Reason:       conflictReason(v.Constraint, other.Constraint),
					KindMismatch: true,
				}
			}
			if merged == other.Constraint {
				// `other` is already at least as strong, just bind v to other.
				s.Bind(v.ID, other)
				return nil
			}
			// Promote: mint a FRESH var (new ID) with the merged
			// constraint, then bind both v and other to it. Using a
			// fresh ID matters, binding `other.ID -> TVar{ID:
			// other.ID, Constraint: ...}` would be a self-loop that
			// infinite-recurses inside Apply.
			fresh := TVar{ID: int(atomicNextVarID()), Constraint: merged}
			s.Bind(v.ID, fresh)
			s.Bind(other.ID, fresh)
			return nil
		}
		if err := enforceKind(v, v.Constraint, t, s); err != nil {
			return err
		}
	}

	s.Bind(v.ID, t)
	return nil
}

// satisfiesKind reports whether the concrete type t honors kind k.
// enforceKind checks that t honors constraint k and, for Comparable,
// MAKES it honor it wherever that is possible.
//
// Comparable is the only structural constraint: a tuple is comparable when
// its members are, a list when its element is. Walking that structure has to
// be able to reach a type VARIABLE and promote it — sorting a
// `List ( a, Int )` inside a polymorphic function is what constrains `a` —
// and promoting means binding, which a yes/no predicate cannot do. That is
// why this takes the substitution where satisfiesKind took only a type.
// Number and Appendable stay flat, because they have no structure to walk.
func enforceKind(v TVar, k Kind, t Type, s *Subst) *UnifyError {
	if k == KindComparable {
		return constrainComparable(v, t, s)
	}
	if !satisfiesKind(k, t) {
		return &UnifyError{
			A:            v,
			B:            s.Apply(t),
			Reason:       kindMismatchReason(k, t),
			KindMismatch: true,
		}
	}
	return nil
}

// constrainComparable walks t, demanding comparability all the way down and
// promoting any variable it meets. `origin` is the variable that asked, and
// stays on the error so the message reads as one unification rather than as
// a type failing to unify with itself; the offending INNER type is what the
// reason names, so `( Int, { p : Int } ) < ...` reports the record rather
// than the tuple.
func constrainComparable(origin TVar, t Type, s *Subst) *UnifyError {
	t = s.Apply(t)
	switch n := t.(type) {
	case TVar:
		if n.Constraint == KindComparable {
			// Already carries it. Minting another fresh var here would
			// only add a link to the substitution chain, on every visit.
			return nil
		}
		merged, ok := mergeKinds(KindComparable, n.Constraint)
		if !ok {
			return &UnifyError{
				A:            origin,
				B:            t,
				Reason:       conflictReason(KindComparable, n.Constraint),
				KindMismatch: true,
			}
		}
		s.Bind(n.ID, TVar{ID: int(atomicNextVarID()), Constraint: merged})
		return nil
	case TTuple:
		for _, m := range n.Members {
			if err := constrainComparable(origin, m, s); err != nil {
				return err
			}
		}
		return nil
	case TCon:
		if n.Name == "List" && len(n.Args) == 1 {
			return constrainComparable(origin, n.Args[0], s)
		}
		if IsComparableType(n) {
			return nil
		}
	}
	return &UnifyError{
		A:            origin,
		B:            t,
		Reason:       kindMismatchReason(KindComparable, t),
		KindMismatch: true,
	}
}

func satisfiesKind(k Kind, t Type) bool {
	switch k {
	case KindComparable:
		return IsComparableType(t)
	case KindAppendable:
		return IsAppendableType(t)
	case KindNumber:
		return IsNumberType(t)
	}
	return true
}

// mergeKinds returns the stronger of two Kinds and whether they're
// compatible. KindAny merges with anything (the other side wins).
//
// Comparable and Appendable are still rejected together, but no longer
// because the intersection is empty: now that tuples and lists are
// comparable, String and List-of-comparable are in both. Elm names that
// intersection `compappend` and gives it its own kind; Mar has no such
// kind, so a variable that would need to be both is refused rather than
// silently resolved to one of them. `\a b -> if a < b then a ++ b else b`
// is the program that asks for it.
func mergeKinds(a, b Kind) (Kind, bool) {
	if a == b {
		return a, true
	}
	if a == KindAny {
		return b, true
	}
	if b == KindAny {
		return a, true
	}
	// Comparable ∩ Number is representable: every number (Int, Decimal)
	// is also comparable, so a var that must be both is just a number.
	// (`\a b -> if a < b then a + b else b` needs exactly this.)
	if (a == KindComparable && b == KindNumber) || (a == KindNumber && b == KindComparable) {
		return KindNumber, true
	}
	return KindAny, false
}

// conflictReason explains why two constrained vars can't unify: the
// same value is being used in two ways Mar can't combine in one type
// variable (Elm spells that combination `compappend`).
func conflictReason(a, b Kind) string {
	return fmt.Sprintf("a value can't be both %s and %s", a, b)
}

// kindMismatchReason produces the human-readable explanation when a
// concrete type fails to satisfy a TVar's constraint. The message
// names the constraint and the allowed types so the user can act on
// it without having to know the typechecker internals.
func kindMismatchReason(k Kind, t Type) string {
	// Identify the offending shape in plain words. Record / custom
	// type / tuple all show up here so the message names the general
	// case rather than enumerating each.
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
	switch k {
	case KindComparable:
		// Sorting rows by a timestamp is the commonest way to arrive
		// here, and the fix is one call rather than a comparator, so
		// the message says which call instead of making the reader
		// find it.
		if c, ok := t.(TCon); ok && c.Name == "Time" {
			return "a Time has no order of its own; sort or compare by Time.toMillis instead"
		}
		// Phrasing stays neutral: "key types" was Dict-specific
		// jargon; this message also fires for the ordering operators
		// (`<`, `>`, `<=`, `>=`), where "key" makes no sense.
		return fmt.Sprintf(
			"%s is not comparable; comparable types are Int, Decimal, String and Char, "+
				"and tuples or lists of those. To order anything else, use List.sortWith "+
				"and say how",
			shape,
		)
	case KindAppendable:
		// Fires for `++`. Elm's `appendable` is String and List, which
		// is exactly what the runtime append handles.
		return fmt.Sprintf(
			"%s is not appendable; ++ joins two Strings or two Lists",
			shape,
		)
	case KindNumber:
		// Fires for `+ - *` and negation. The two members are spelled
		// out so the fix (convert, or change the literal) is obvious;
		// an unresolved Decimal.Division names its own way out.
		if c, ok := t.(TCon); ok && c.Name == "Decimal.Division" {
			return "a division has no numeric value until you choose its precision; " +
				"pipe it into Decimal.rounded or Decimal.withRemainder"
		}
		return fmt.Sprintf(
			"%s is not a number; + - * work on two Ints or two Decimals (mix with Decimal.fromInt)",
			shape,
		)
	}
	return ""
}

// occursIn checks whether v appears anywhere in t (after applying s).
func occursIn(id int, t Type, s *Subst) bool {
	t = s.resolve(t)
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
	// Declaration order, not map order. What is unified first decides which
	// mismatch gets reported and how far the two types are resolved when it
	// is, and the leftover names become a row extension whose order is what
	// the type prints with: ranging over the maps made all three differ
	// from one run to the next.
	for _, n := range fieldOrder(a) {
		if _, ok := b.Fields[n]; ok {
			shared = append(shared, n)
		} else {
			aOnly = append(aOnly, n)
		}
	}
	for _, n := range fieldOrder(b) {
		if _, ok := a.Fields[n]; !ok {
			bOnly = append(bOnly, n)
		}
	}

	// A shape mismatch is decided by the field NAMES and the two tails, all
	// known already. Its error has always shown the two records as they stood
	// on entry, before the shared fields below taught the substitution
	// anything, so that is when it is built; it is still only RETURNED after
	// the shared fields, because a field that disagrees is reported first.
	var shapeErr *UnifyError
	switch {
	case a.Tail == nil && b.Tail == nil:
		if len(aOnly) > 0 || len(bOnly) > 0 {
			shapeErr = mismatch(a, b, s)
		}
	case a.Tail == nil && b.Tail != nil:
		if len(bOnly) > 0 {
			shapeErr = mismatch(a, b, s)
		}
	case a.Tail != nil && b.Tail == nil:
		if len(aOnly) > 0 {
			shapeErr = mismatch(a, b, s)
		}
	}

	for _, n := range shared {
		if err := Unify(a.Fields[n], b.Fields[n], s); err != nil {
			return err
		}
	}
	if shapeErr != nil {
		return shapeErr
	}

	switch {
	case a.Tail == nil && b.Tail == nil:
		// closed vs closed: must match exactly (shapeErr above)
		return nil

	case a.Tail == nil && b.Tail != nil:
		// b open, a closed: b's tail = the fields a has that b lacks (i.e. aOnly).
		// If the open side wants fields the closed side doesn't have, bail.
		return Unify(b.Tail, makeRowExtension(aOnly, a, nil), s)

	case a.Tail != nil && b.Tail == nil:
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

// fieldOrder lists a record's field names in a fixed order: the order they
// were declared in, then any name Order does not carry, sorted. Every record
// the checker builds keeps Order in step with Fields, so the first return is
// the one taken; the rest is for a record that does not (a literal that
// repeats a field has a longer Order than it has fields).
func fieldOrder(r TRecord) []string {
	if len(r.Order) == len(r.Fields) {
		inStep := true
		for _, n := range r.Order {
			if _, ok := r.Fields[n]; !ok {
				inStep = false
				break
			}
		}
		if inStep {
			return r.Order
		}
	}
	names := make([]string, 0, len(r.Fields))
	seen := make(map[string]bool, len(r.Fields))
	for _, n := range r.Order {
		if _, ok := r.Fields[n]; ok && !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	if len(names) < len(r.Fields) {
		var rest []string
		for n := range r.Fields {
			if !seen[n] {
				rest = append(rest, n)
			}
		}
		sort.Strings(rest)
		names = append(names, rest...)
	}
	return names
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
// there are no extra fields, it's just the tail itself: wrapping in a
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
