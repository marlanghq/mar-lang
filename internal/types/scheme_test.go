package types

import "testing"

func TestTypeEnvLookup(t *testing.T) {
	env := NewTypeEnv().Bind("x", TInt())
	got, ok := env.Lookup("x")
	if !ok {
		t.Fatal("Lookup returned false")
	}
	if got.String() != "int" {
		t.Errorf("got %q, want int", got)
	}
}

func TestTypeEnvShadowing(t *testing.T) {
	env := NewTypeEnv().Bind("x", TInt()).Bind("x", TString())
	got, _ := env.Lookup("x")
	if got.String() != "string" {
		t.Errorf("got %q, want string", got)
	}
}

func TestTypeEnvParentVisible(t *testing.T) {
	outer := NewTypeEnv().Bind("x", TInt())
	inner := outer.Bind("y", TBool())
	if got, ok := inner.Lookup("x"); !ok || got.String() != "int" {
		t.Errorf("x = %v, want int", got)
	}
	if got, ok := inner.Lookup("y"); !ok || got.String() != "bool" {
		t.Errorf("y = %v, want bool", got)
	}
}

func TestTypeEnvBindIsImmutable(t *testing.T) {
	outer := NewTypeEnv()
	_ = outer.Bind("x", TInt())
	if _, ok := outer.Lookup("x"); ok {
		t.Error("Bind mutated the parent env")
	}
}

func TestGeneralizeQuantifiesFreeVars(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	env := NewTypeEnv()
	s := NewSubst()
	// Generalize α → α should give ∀α. α → α
	scheme := Generalize(env, s, TArrow([]Type{a}, a))
	got, ok := scheme.(TForall)
	if !ok {
		t.Fatalf("expected TForall, got %T", scheme)
	}
	if len(got.Vars) != 1 || got.Vars[0] != a.ID {
		t.Errorf("vars = %v, want [%d]", got.Vars, a.ID)
	}
}

func TestGeneralizeSkipsEnvVars(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar() // free in env
	b := FreshVar() // not in env
	env := NewTypeEnv().Bind("x", a)
	s := NewSubst()
	// Generalize (α → β) → only β should be quantified.
	scheme := Generalize(env, s, TArrow([]Type{a}, b))
	got, ok := scheme.(TForall)
	if !ok {
		t.Fatalf("expected TForall, got %T", scheme)
	}
	if len(got.Vars) != 1 || got.Vars[0] != b.ID {
		t.Errorf("vars = %v, want [%d]", got.Vars, b.ID)
	}
}

func TestGeneralizeNoFreeVarsReturnsBareType(t *testing.T) {
	env := NewTypeEnv()
	s := NewSubst()
	got := Generalize(env, s, TInt())
	if _, isForall := got.(TForall); isForall {
		t.Errorf("expected bare type, got TForall")
	}
}

func TestInstantiateGivesFreshVars(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	scheme := TForall{Vars: []int{a.ID}, Body: TArrow([]Type{a}, a)}
	inst1 := Instantiate(scheme)
	inst2 := Instantiate(scheme)
	// Both should be `(αN -> αN)` with fresh, distinct N's.
	if inst1.String() == inst2.String() {
		t.Errorf("two instantiations gave same vars: %v vs %v", inst1, inst2)
	}
}

func TestInstantiateNonForallPassthrough(t *testing.T) {
	got := Instantiate(TInt())
	if got.String() != "int" {
		t.Errorf("got %q, want int", got)
	}
}

func TestInstantiateUnifiable(t *testing.T) {
	// ∀α. α → α instantiated, then unified with (int → β), should bind β to int.
	resetVarIDsForTesting()
	a := FreshVar()
	scheme := TForall{Vars: []int{a.ID}, Body: TArrow([]Type{a}, a)}
	inst := Instantiate(scheme)
	b := FreshVar()
	target := TArrow([]Type{TInt()}, b)
	s := NewSubst()
	if err := Unify(inst, target, s); err != nil {
		t.Fatalf("unify failed: %v", err)
	}
	if s.Apply(b).String() != "int" {
		t.Errorf("β = %v, want int", s.Apply(b))
	}
}
