// Package types — pretty.go renders types for user-facing error messages.
//
// The default Type.String() uses the global TVar counter (α, β, ..., α1, ...).
// That's fine for debugging but leaks internal numbering: by the time the
// user sees an error, the counter may be at θ36 — meaningless to them.
//
// PrettyType walks a type and renumbers vars locally: the first var seen
// becomes "a", the second "b", etc. PrettyTypePair renders a pair sharing the
// same renumbering so the same internal var prints the same letter on both
// sides of an "expected X, got Y" message.
package types

import (
	"fmt"
	"sort"
	"strings"
)

// PrettyType returns a user-friendly string for t with locally-renumbered
// type variables.
func PrettyType(t Type) string {
	r := newPrettyRenamer()
	return r.render(t)
}

// PrettyTypePair returns user-friendly strings for both a and b sharing a
// single renumbering (so an unbound variable that appears on both sides
// prints as the same letter).
func PrettyTypePair(a, b Type) (string, string) {
	r := newPrettyRenamer()
	return r.render(a), r.render(b)
}

// PrettyExpectedGot is a convenience for the common "expected X, got Y" case.
func PrettyExpectedGot(expected, actual Type) string {
	e, g := PrettyTypePair(expected, actual)
	return fmt.Sprintf("expected %s, got %s", e, g)
}

type prettyRenamer struct {
	mapping map[int]string
	next    int
}

func newPrettyRenamer() *prettyRenamer {
	return &prettyRenamer{mapping: map[int]string{}}
}

func (r *prettyRenamer) name(id int) string {
	if n, ok := r.mapping[id]; ok {
		return n
	}
	n := letterAt(r.next)
	r.next++
	r.mapping[id] = n
	return n
}

// letterAt returns "a", "b", ..., "z", "a1", "b1", ...
func letterAt(i int) string {
	letters := "abcdefghijklmnopqrstuvwxyz"
	idx := i % len(letters)
	cycle := i / len(letters)
	if cycle == 0 {
		return string(letters[idx])
	}
	return fmt.Sprintf("%c%d", letters[idx], cycle)
}

func (r *prettyRenamer) render(t Type) string {
	switch tt := t.(type) {
	case TVar:
		return r.name(tt.ID)

	case TCon:
		if tt.Name == "->" {
			params, ret, _ := IsArrow(tt)
			if len(params) == 0 {
				return fmt.Sprintf("(-> %s)", r.render(ret))
			}
			parts := make([]string, 0, len(params))
			for _, p := range params {
				parts = append(parts, r.atom(p))
			}
			return fmt.Sprintf("(%s -> %s)", strings.Join(parts, " "), r.render(ret))
		}
		if len(tt.Args) == 0 {
			return tt.Name
		}
		parts := make([]string, 0, len(tt.Args))
		for _, a := range tt.Args {
			parts = append(parts, r.atom(a))
		}
		return fmt.Sprintf("(%s %s)", tt.Name, strings.Join(parts, " "))

	case TRecord:
		if tt.Name != "" && tt.Tail == nil {
			return tt.Name
		}
		order := tt.Order
		if len(order) == 0 {
			order = make([]string, 0, len(tt.Fields))
			for k := range tt.Fields {
				order = append(order, k)
			}
			sort.Strings(order)
		}
		parts := make([]string, 0, len(order))
		for _, name := range order {
			ft, ok := tt.Fields[name]
			if !ok {
				continue
			}
			parts = append(parts, fmt.Sprintf("%s: %s", name, r.render(ft)))
		}
		body := strings.Join(parts, ", ")
		if tt.Tail != nil {
			return fmt.Sprintf("{%s | %s}", body, r.render(tt.Tail))
		}
		return fmt.Sprintf("{%s}", body)

	case TUnion:
		if tt.Name != "" {
			return tt.Name
		}
		parts := make([]string, 0, len(tt.VariantOrder))
		for _, tag := range tt.VariantOrder {
			payload := tt.Variants[tag]
			if len(payload) == 0 {
				parts = append(parts, fmt.Sprintf("(%s)", tag))
				continue
			}
			args := make([]string, 0, len(payload))
			for _, p := range payload {
				args = append(args, r.atom(p))
			}
			parts = append(parts, fmt.Sprintf("(%s %s)", tag, strings.Join(args, " ")))
		}
		return strings.Join(parts, " | ")

	case TForall:
		if len(tt.Vars) == 0 {
			return r.render(tt.Body)
		}
		names := make([]string, 0, len(tt.Vars))
		for _, id := range tt.Vars {
			names = append(names, r.name(id))
		}
		return fmt.Sprintf("∀%s. %s", strings.Join(names, " "), r.render(tt.Body))
	}
	return t.String()
}

// atom renders a type, parenthesizing when needed for unambiguous nesting.
func (r *prettyRenamer) atom(t Type) string {
	switch tt := t.(type) {
	case TVar:
		return r.name(tt.ID)
	case TCon:
		if len(tt.Args) == 0 {
			return tt.Name
		}
		return r.render(tt)
	default:
		return r.render(t)
	}
}
