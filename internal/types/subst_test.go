package types

import "testing"

func TestSubstApplyVar(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	s := NewSubst()
	s.Bind(a.ID, TInt())
	got := s.Apply(a)
	if got.String() != "int" {
		t.Errorf("Apply(α) = %q, want int", got)
	}
}

func TestSubstApplyUnboundVar(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	s := NewSubst()
	got := s.Apply(a)
	if got.String() != "α" {
		t.Errorf("Apply(unbound α) = %q, want α", got)
	}
}

func TestSubstApplyArrow(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	b := FreshVar()
	// (α -> β) with α=int, β=bool
	fn := TArrow([]Type{a}, b)
	s := NewSubst()
	s.Bind(a.ID, TInt())
	s.Bind(b.ID, TBool())
	got := s.Apply(fn)
	want := "(int -> bool)"
	if got.String() != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSubstApplyTransitive(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	b := FreshVar()
	// α -> β, then β -> int → α resolves to int.
	s := NewSubst()
	s.Bind(a.ID, b)
	s.Bind(b.ID, TInt())
	got := s.Apply(a)
	if got.String() != "int" {
		t.Errorf("got %q, want int", got)
	}
}

func TestSubstApplyForallShadows(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar() // α — quantified inside
	b := FreshVar() // β — free, will be substituted
	// scheme: ∀α. (α -> β)
	scheme := TForall{Vars: []int{a.ID}, Body: TArrow([]Type{a}, b)}
	s := NewSubst()
	s.Bind(a.ID, TInt()) // should NOT propagate inside the forall
	s.Bind(b.ID, TString())
	got := s.Apply(scheme)
	want := "∀α. (α -> string)"
	if got.String() != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSubstResolveChain(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	b := FreshVar()
	c := FreshVar()
	s := NewSubst()
	s.Bind(a.ID, b)
	s.Bind(b.ID, c)
	s.Bind(c.ID, TInt())
	got := s.Resolve(a)
	if got.String() != "int" {
		t.Errorf("got %q, want int", got)
	}
}

func TestSubstResolveTerminatesAtUnboundVar(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	b := FreshVar()
	s := NewSubst()
	s.Bind(a.ID, b)
	// b is unbound
	got := s.Resolve(a)
	if got.String() != "β" {
		t.Errorf("got %q, want β", got)
	}
}
