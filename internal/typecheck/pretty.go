package typecheck

import (
	"fmt"
	"sort"
	"strings"
)

// Pretty returns a human-friendly string for t. Unlike t.String(), it renames
// type variables to lowercase letters in order of first appearance (a, b, c,
// ..., z, then a1, b1, ...) for readability.
//
// A *constrained* variable prints as its constraint — `number`, `comparable`,
// `appendable` — rather than a letter, so `+` reads `number -> number -> number`
// like Elm's. That is not cosmetic: Pretty is what the LSP shows on hover and
// what the /reference generator publishes as each function's signature, so a
// letter there would advertise `List.sum : List a -> a`, which the compiler
// rejects. Independent variables of the same constraint get Elm's numbering
// (`number`, `number2`), because two of them in one signature are two
// different choices, not one.
func Pretty(t Type) string {
	r := newRenamer()
	r.collect(t)
	return r.format(t)
}

type renamer struct {
	mapping map[int]string
	order   []int
	count   int
	// kindCount tracks how many distinct variables of each constraint have
	// been named, to drive the number/number2 suffixing.
	kindCount map[Kind]int
}

func newRenamer() *renamer {
	return &renamer{mapping: map[int]string{}, kindCount: map[Kind]int{}}
}

// nameForVar picks the display name for a variable: its constraint when it has
// one, otherwise the next letter.
func (r *renamer) nameForVar(v TVar) string {
	if v.Constraint == KindAny {
		n := letterName(r.count)
		r.count++
		return n
	}
	base := v.Constraint.String()
	r.kindCount[v.Constraint]++
	if n := r.kindCount[v.Constraint]; n > 1 {
		return base + fmt.Sprintf("%d", n)
	}
	return base
}

// collect walks t recording each variable in order of first appearance.
func (r *renamer) collect(t Type) {
	switch v := t.(type) {
	case TVar:
		if _, has := r.mapping[v.ID]; !has {
			r.mapping[v.ID] = r.nameForVar(v)
			r.order = append(r.order, v.ID)
		}
	case TCon:
		for _, a := range v.Args {
			r.collect(a)
		}
	case TArrow:
		r.collect(v.From)
		r.collect(v.To)
	case TRecord:
		// Walk fields in declaration order for stable naming
		for _, n := range v.Order {
			r.collect(v.Fields[n])
		}
		// Stable: also fields not in Order map (defensive)
		for n, f := range v.Fields {
			if !contains(v.Order, n) {
				r.collect(f)
			}
		}
		if v.Tail != nil {
			r.collect(v.Tail)
		}
	case TTuple:
		for _, m := range v.Members {
			r.collect(m)
		}
	case TForall:
		// Visit body; don't pre-name forall vars (they appear in body)
		r.collect(v.Body)
	}
}

// name is the by-ID path, used to pre-name forall-quantified variables. A
// variable's constraint lives on its TVar occurrences in the body, which
// collect has already walked, so anything constrained is named by then; an ID
// that reaches here unseen is genuinely unconstrained.
func (r *renamer) name(id int) string {
	if n, ok := r.mapping[id]; ok {
		return n
	}
	r.mapping[id] = letterName(r.count)
	r.count++
	return r.mapping[id]
}

func (r *renamer) format(t Type) string {
	switch v := t.(type) {
	case TVar:
		if n, ok := r.mapping[v.ID]; ok {
			return n
		}
		r.mapping[v.ID] = r.nameForVar(v)
		return r.mapping[v.ID]
	case TCon:
		if len(v.Args) == 0 {
			return v.Name
		}
		parts := make([]string, len(v.Args))
		for i, a := range v.Args {
			parts[i] = r.formatAtom(a)
		}
		return v.Name + " " + strings.Join(parts, " ")
	case TArrow:
		return r.formatArrowFrom(v.From) + " -> " + r.format(v.To)
	case TUnit:
		return "()"
	case TTuple:
		parts := make([]string, len(v.Members))
		for i, m := range v.Members {
			parts[i] = r.format(m)
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case TRecord:
		// Flatten chains like { x | { y | row } } into a single
		// { x, y | row } before rendering. Row poly's internal
		// representation creates these chains during unification; users
		// shouldn't see them.
		fields, order, tail := flattenRecord(v)
		if len(fields) == 0 && tail == nil {
			return "{}"
		}
		var sb strings.Builder
		sb.WriteString("{ ")
		names := order
		if len(names) == 0 {
			for n := range fields {
				names = append(names, n)
			}
			sort.Strings(names)
		}
		for i, n := range names {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(n)
			sb.WriteString(" : ")
			sb.WriteString(r.format(fields[n]))
		}
		// Unbound row vars render as `, …` — internal var name is noise
		// to the user. Concrete tails (rare after flattening) print inline.
		if tail != nil {
			if _, isVar := tail.(TVar); isVar {
				if len(names) > 0 {
					sb.WriteString(", …")
				} else {
					sb.WriteString("…")
				}
			} else {
				sb.WriteString(" | ")
				sb.WriteString(r.format(tail))
			}
		}
		sb.WriteString(" }")
		return sb.String()
	case TForall:
		// Pre-name quant vars first so they get a, b, c... in declaration order.
		for _, id := range v.Vars {
			r.name(id)
		}
		body := r.format(v.Body)
		var names []string
		for _, id := range v.Vars {
			names = append(names, r.mapping[id])
		}
		// We just print the body with its renamed vars; the caller can
		// tell it's polymorphic from the lowercase variable names
		// (no explicit `forall` prefix is rendered).
		_ = names
		return body
	}
	return fmt.Sprintf("%v", t)
}

// formatAtom: used for type-application args. Parens around arrows AND
// applied type constructors (so `Maybe (List Int)` not `Maybe List Int`).
func (r *renamer) formatAtom(t Type) string {
	switch v := t.(type) {
	case TArrow:
		return "(" + r.format(t) + ")"
	case TCon:
		if len(v.Args) > 0 {
			return "(" + r.format(t) + ")"
		}
	}
	return r.format(t)
}

// formatArrowFrom: used for the From side of an arrow. Parens around
// arrows only (since `List a -> b` is unambiguous).
func (r *renamer) formatArrowFrom(t Type) string {
	if _, ok := t.(TArrow); ok {
		return "(" + r.format(t) + ")"
	}
	return r.format(t)
}

// flattenRecord unwraps `{ a | { b | { c | tail } } }` into a single
// record `{ a, b, c | tail }`. Inner-record tails come from the way
// row-polymorphic unification represents "a record with at least these
// fields plus more" — fine internally, confusing in error messages.
// Inner duplicate field names take precedence (shouldn't happen for
// well-typed code, but be defensive).
func flattenRecord(r TRecord) (fields map[string]Type, order []string, tail Type) {
	fields = map[string]Type{}
	for _, n := range r.Order {
		fields[n] = r.Fields[n]
	}
	for n, t := range r.Fields {
		if _, ok := fields[n]; !ok {
			fields[n] = t
		}
	}
	order = append(order, r.Order...)
	for n := range r.Fields {
		if !contains(order, n) {
			order = append(order, n)
		}
	}
	tail = r.Tail
	for {
		nested, ok := tail.(TRecord)
		if !ok {
			break
		}
		for _, n := range nested.Order {
			if _, exists := fields[n]; !exists {
				fields[n] = nested.Fields[n]
				order = append(order, n)
			}
		}
		for n, t := range nested.Fields {
			if _, exists := fields[n]; !exists {
				fields[n] = t
				order = append(order, n)
			}
		}
		tail = nested.Tail
	}
	return fields, order, tail
}

func letterName(n int) string {
	if n < 26 {
		return string(rune('a' + n))
	}
	return string(rune('a'+n%26)) + fmt.Sprintf("%d", n/26)
}

func contains(s []string, t string) bool {
	for _, v := range s {
		if v == t {
			return true
		}
	}
	return false
}
