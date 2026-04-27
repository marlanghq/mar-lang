package types

import (
	"strings"
	"testing"
)

func TestBaseEnvHasBuiltins(t *testing.T) {
	env := BaseEnv()
	required := []string{
		"map", "filter", "fold_left", "fold_right",
		"cons", "first", "rest", "empty?",
		"=", "!=", "and", "or", "not",
		"true", "false", "unit",
		"current_user", "authenticated?",
	}
	for _, name := range required {
		if _, ok := env.Lookup(name); !ok {
			t.Errorf("BaseEnv missing %q", name)
		}
	}
}

func TestMapInstantiationIsPolymorphic(t *testing.T) {
	env := BaseEnv()
	mapScheme, _ := env.Lookup("map")
	inst1 := Instantiate(mapScheme)
	inst2 := Instantiate(mapScheme)
	if inst1.String() == inst2.String() {
		t.Errorf("two instantiations gave same vars: %v", inst1)
	}
}

func TestMapAppliedToIntList(t *testing.T) {
	// Verify that instantiating `map` and unifying against the call site
	// `map (lambda (x) (= x 0)) [1,2,3]` infers `list bool`.
	env := BaseEnv()
	mapScheme, _ := env.Lookup("map")
	mapType := Instantiate(mapScheme)

	// Call site: map fn list, where fn : int → bool, list : list int
	fn := TArrow([]Type{TInt()}, TBool())
	listInts := TList(TInt())
	resultVar := FreshVar()
	expected := TArrow([]Type{fn, listInts}, resultVar)

	s := NewSubst()
	if err := Unify(mapType, expected, s); err != nil {
		t.Fatalf("unify failed: %v", err)
	}
	got := s.Apply(resultVar).String()
	if got != "(list bool)" {
		t.Errorf("map result = %q, want (list bool)", got)
	}
}

func TestEqualityIsPolymorphic(t *testing.T) {
	env := BaseEnv()
	eqScheme, _ := env.Lookup("=")
	inst := Instantiate(eqScheme)
	// (= 1 1) → bool
	resVar := FreshVar()
	call := TArrow([]Type{TInt(), TInt()}, resVar)
	s := NewSubst()
	if err := Unify(inst, call, s); err != nil {
		t.Fatalf("unify: %v", err)
	}
	if s.Apply(resVar).String() != "bool" {
		t.Errorf("res = %v", s.Apply(resVar))
	}

	// Same scheme, used with strings — must succeed independently.
	inst2 := Instantiate(eqScheme)
	resVar2 := FreshVar()
	call2 := TArrow([]Type{TString(), TString()}, resVar2)
	s2 := NewSubst()
	if err := Unify(inst2, call2, s2); err != nil {
		t.Fatalf("unify with strings: %v", err)
	}
	if s2.Apply(resVar2).String() != "bool" {
		t.Errorf("string res = %v", s2.Apply(resVar2))
	}
}

func TestCurrentUserIsNominalUnion(t *testing.T) {
	env := BaseEnv()
	cu, _ := env.Lookup("current_user")
	u, ok := cu.(TUnion)
	if !ok {
		t.Fatalf("current_user is not TUnion: %T", cu)
	}
	if u.Name != "current-user" {
		t.Errorf("name = %q", u.Name)
	}
	if !strings.Contains(strings.Join(u.VariantOrder, ","), "authenticated") {
		t.Errorf("missing authenticated variant: %v", u.VariantOrder)
	}
}

func TestNumericOverloadFlag(t *testing.T) {
	for _, op := range []string{"+", "-", "*", "/", ">", ">=", "<", "<="} {
		if !IsNumericOverloadedBuiltin(op) {
			t.Errorf("%q should be numeric-overloaded", op)
		}
	}
	if IsNumericOverloadedBuiltin("=") {
		t.Error("= should not be numeric-overloaded (it's polymorphic)")
	}
}
