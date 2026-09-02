package typecheck

import "testing"

// Applying a substitution under a `forall` must leave the BOUND variables
// alone. `forall a. a -> b` with `a := Int` in hand is still `forall a. a -> b`:
// the `a` inside is a different `a`, and replacing it captures the binder — the
// classic way a type system starts proving things that are not true.
//
// `applyAvoiding` is the code that gets this right, and measured, it never
// runs: not in the test suite, and not while loading all 40 examples, because
// nothing hands `Apply` a `TForall` today. That makes it defensive code whose
// correctness rested on reading it. The case is real the moment anything stores
// a scheme where a type is expected, so the behaviour is pinned here rather
// than hoped for.
func TestSubstitutionDoesNotReachUnderAForall(t *testing.T) {
	const bound, free = 1, 2

	s := NewSubst()
	s.Bind(bound, TInt)
	s.Bind(free, TString)

	forall := func(body Type) Type {
		return TForall{Vars: []int{bound}, Body: body}
	}
	// What the scheme should look like afterwards: the bound variable
	// untouched, the free one replaced.
	cases := []struct {
		name string
		in   Type
		want string
	}{
		{"an arrow", forall(TArrow{From: TVar{ID: bound}, To: TVar{ID: free}}),
			"forall t1. t1 -> String"},
		{"a type constructor's arguments", forall(TCon{Name: "Result", Args: []Type{TVar{ID: free}, TVar{ID: bound}}}),
			"forall t1. Result String t1"},
		{"a tuple", forall(TTuple{Members: []Type{TVar{ID: bound}, TVar{ID: free}}}),
			"forall t1. (t1, String)"},
		{"a record's fields", forall(TRecord{
			Fields: map[string]Type{"a": TVar{ID: bound}, "b": TVar{ID: free}},
			Order:  []string{"a", "b"},
		}), "forall t1. { a : t1, b : String }"},
		{"a record's tail", forall(TRecord{
			Fields: map[string]Type{"a": TInt},
			Order:  []string{"a"},
			Tail:   TVar{ID: bound},
		}), "forall t1. { t1 | a : Int }"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.Apply(tc.in).String()
			if got != tc.want {
				t.Errorf("\n  got:  %s\n  want: %s", got, tc.want)
			}
		})
	}

	// The guard has to be about being BOUND, not about the number: the same
	// variable is replaced when nothing binds it.
	t.Run("the same variable outside the forall", func(t *testing.T) {
		got := s.Apply(TArrow{From: TVar{ID: bound}, To: TVar{ID: free}}).String()
		if got != "Int -> String" {
			t.Errorf("a free variable is exactly what a substitution is for, got %s", got)
		}
	})
}
