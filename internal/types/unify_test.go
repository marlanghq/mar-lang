package types

import (
	"strings"
	"testing"
)

func TestUnifyEqualPrimitives(t *testing.T) {
	s := NewSubst()
	if err := Unify(TInt(), TInt(), s); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Size() != 0 {
		t.Errorf("Subst should be empty, has %d bindings", s.Size())
	}
}

func TestUnifyDifferentPrimitivesFails(t *testing.T) {
	s := NewSubst()
	err := Unify(TInt(), TBool(), s)
	if err == nil {
		t.Fatal("expected error unifying int with bool")
	}
	if !strings.Contains(err.Error(), "cannot unify") {
		t.Errorf("error message: %v", err)
	}
}

func TestUnifyVarBindsToConcrete(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	s := NewSubst()
	if err := Unify(a, TInt(), s); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	got := s.Apply(a)
	if got.String() != "int" {
		t.Errorf("a resolved to %q, want int", got)
	}
}

func TestUnifyConcreteVarSymmetric(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	s := NewSubst()
	if err := Unify(TString(), a, s); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if s.Apply(a).String() != "string" {
		t.Error("symmetric var binding failed")
	}
}

func TestUnifyVarToVar(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	b := FreshVar()
	s := NewSubst()
	if err := Unify(a, b, s); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	// Now binding b to int should also resolve a to int.
	if err := Unify(b, TInt(), s); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if s.Apply(a).String() != "int" {
		t.Error("transitive var resolution failed")
	}
}

func TestUnifySameVar(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	s := NewSubst()
	if err := Unify(a, a, s); err != nil {
		t.Fatalf("unifying var with itself failed: %v", err)
	}
	if s.Size() != 0 {
		t.Errorf("expected no bindings, got %d", s.Size())
	}
}

func TestUnifyArrows(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	b := FreshVar()
	// (a -> b) vs (int -> bool)
	left := TArrow([]Type{a}, b)
	right := TArrow([]Type{TInt()}, TBool())
	s := NewSubst()
	if err := Unify(left, right, s); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if s.Apply(a).String() != "int" {
		t.Errorf("a = %v", s.Apply(a))
	}
	if s.Apply(b).String() != "bool" {
		t.Errorf("b = %v", s.Apply(b))
	}
}

func TestUnifyArrowArityMismatch(t *testing.T) {
	s := NewSubst()
	left := TArrow([]Type{TInt()}, TBool())
	right := TArrow([]Type{TInt(), TInt()}, TBool())
	err := Unify(left, right, s)
	if err == nil {
		t.Fatal("expected arity mismatch error")
	}
}

func TestUnifyOccursCheck(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	// a = (a -> int) → infinite type, should fail.
	s := NewSubst()
	err := Unify(a, TArrow([]Type{a}, TInt()), s)
	if err == nil {
		t.Fatal("expected occurs check failure")
	}
	if !strings.Contains(err.Error(), "infinite type") {
		t.Errorf("error: %v", err)
	}
}

func TestUnifyNominalRecord(t *testing.T) {
	r1 := TRecord{Name: "post", Fields: map[string]Type{"id": TInt()}, Order: []string{"id"}}
	r2 := TRecord{Name: "post", Fields: map[string]Type{"id": TInt()}, Order: []string{"id"}}
	s := NewSubst()
	if err := Unify(r1, r2, s); err != nil {
		t.Fatalf("unexpected: %v", err)
	}

	r3 := TRecord{Name: "comment", Fields: map[string]Type{"id": TInt()}, Order: []string{"id"}}
	if err := Unify(r1, r3, NewSubst()); err == nil {
		t.Fatal("expected error unifying different nominal records")
	}
}

func TestUnifyNominalUnion(t *testing.T) {
	u1 := TUnion{Name: "color", Variants: map[string][]Type{"red": nil, "blue": nil}, VariantOrder: []string{"red", "blue"}}
	u2 := TUnion{Name: "color", Variants: map[string][]Type{"red": nil, "blue": nil}, VariantOrder: []string{"red", "blue"}}
	s := NewSubst()
	if err := Unify(u1, u2, s); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestUnifyListOfVar(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	s := NewSubst()
	if err := Unify(TList(a), TList(TInt()), s); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if s.Apply(a).String() != "int" {
		t.Errorf("a = %v", s.Apply(a))
	}
}
