package typecheck

import "testing"

// TestPrettyPrintsConstraints locks the display of constrained type variables.
// Pretty is not just for humans: it is what the LSP shows on hover and what the
// /reference generator publishes as each stdlib signature. A constrained
// variable rendered as a bare letter would advertise `List.sum : List a -> a`,
// a type the compiler rejects — so this is a correctness test for the docs,
// not a formatting preference.
func TestPrettyPrintsConstraints(t *testing.T) {
	num := TVar{ID: 1, Constraint: KindNumber}
	cmp := TVar{ID: 2, Constraint: KindComparable}
	app := TVar{ID: 3, Constraint: KindAppendable}
	free := TVar{ID: 4}

	cases := []struct {
		name string
		in   Type
		want string
	}{
		{"number twice is one choice", TArrow{From: num, To: TArrow{From: num, To: num}}, "number -> number -> number"},
		{"comparable", TArrow{From: cmp, To: TArrow{From: cmp, To: TBool}}, "comparable -> comparable -> Bool"},
		{"appendable", TArrow{From: app, To: TArrow{From: app, To: app}}, "appendable -> appendable -> appendable"},
		{"unconstrained stays a letter", TArrow{From: free, To: free}, "a -> a"},
		{"constrained inside a container", TArrow{From: TList(num), To: num}, "List number -> number"},
		// Two INDEPENDENT number vars are two different choices, so they must
		// not both read `number` — Elm's numbering keeps them apart.
		{"independent vars get numbered", TArrow{From: num, To: TArrow{From: TVar{ID: 9, Constraint: KindNumber}, To: TBool}}, "number -> number2 -> Bool"},
		// Mixed: the letter counter and the constraint counter are separate,
		// so a free var beside a constrained one still starts at `a`.
		{"mixed", TArrow{From: free, To: TArrow{From: num, To: free}}, "a -> number -> a"},
	}
	for _, c := range cases {
		if got := Pretty(c.in); got != c.want {
			t.Errorf("%s: Pretty = %q, want %q", c.name, got, c.want)
		}
	}
}
