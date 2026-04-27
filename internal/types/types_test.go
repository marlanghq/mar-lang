package types

import (
	"testing"
)

func TestNullaryConPrint(t *testing.T) {
	cases := []struct {
		typ  Type
		want string
	}{
		{TBool(), "bool"},
		{TInt(), "int"},
		{TString(), "string"},
		{TUnit(), "unit"},
	}
	for _, c := range cases {
		if got := c.typ.String(); got != c.want {
			t.Errorf("String(%v) = %q, want %q", c.typ, got, c.want)
		}
	}
}

func TestArrowPrint(t *testing.T) {
	resetVarIDsForTesting()
	cases := []struct {
		typ  Type
		want string
	}{
		{TArrow([]Type{TInt()}, TInt()), "(int -> int)"},
		{TArrow([]Type{TInt(), TInt()}, TBool()), "(int int -> bool)"},
		{TArrow(nil, TUnit()), "(-> unit)"},
		{TArrow([]Type{TList(TInt())}, TInt()), "((list int) -> int)"},
	}
	for _, c := range cases {
		if got := c.typ.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}

func TestVarNaming(t *testing.T) {
	cases := []struct {
		id   int
		want string
	}{
		{1, "α"},
		{2, "β"},
		{24, "ω"},
		{25, "α1"},
		{49, "α2"},
	}
	for _, c := range cases {
		if got := varName(c.id); got != c.want {
			t.Errorf("varName(%d) = %q, want %q", c.id, got, c.want)
		}
	}
}

func TestFreshVarUnique(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	b := FreshVar()
	if a.ID == b.ID {
		t.Fatalf("FreshVar returned duplicate IDs %d", a.ID)
	}
}

func TestForallPrint(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar() // α
	scheme := TForall{Vars: []int{a.ID}, Body: TArrow([]Type{a}, a)}
	want := "∀α. (α -> α)"
	if got := scheme.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestIsArrow(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	fn := TArrow([]Type{TInt(), a}, TBool())
	params, ret, ok := IsArrow(fn)
	if !ok {
		t.Fatal("IsArrow returned false for arrow type")
	}
	if len(params) != 2 {
		t.Fatalf("len(params) = %d, want 2", len(params))
	}
	if params[0].String() != "int" {
		t.Errorf("params[0] = %v", params[0])
	}
	if ret.String() != "bool" {
		t.Errorf("ret = %v", ret)
	}

	// Non-arrow returns ok=false.
	if _, _, ok := IsArrow(TInt()); ok {
		t.Error("IsArrow returned true for int")
	}
}

func TestFreeVarsSimple(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar() // α
	b := FreshVar() // β
	// (α int) -> β
	t1 := TArrow([]Type{a, TInt()}, b)
	free := FreeVars(t1)
	if len(free) != 2 {
		t.Fatalf("expected 2 free vars, got %d: %v", len(free), free)
	}
	if free[0] != a.ID || free[1] != b.ID {
		t.Errorf("free vars = %v, want [%d %d]", free, a.ID, b.ID)
	}
}

func TestFreeVarsExcludesQuantified(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar() // α — will be quantified
	b := FreshVar() // β — free
	scheme := TForall{Vars: []int{a.ID}, Body: TArrow([]Type{a, b}, a)}
	free := FreeVars(scheme)
	if len(free) != 1 || free[0] != b.ID {
		t.Errorf("free vars = %v, want [%d]", free, b.ID)
	}
}

func TestRecordPrintNominal(t *testing.T) {
	r := TRecord{
		Name:   "post",
		Fields: map[string]Type{"id": TInt(), "title": TString()},
		Order:  []string{"id", "title"},
	}
	if got := r.String(); got != "post" {
		t.Errorf("got %q, want %q", got, "post")
	}
}

func TestRecordPrintAnonymous(t *testing.T) {
	r := TRecord{
		Name:   "",
		Fields: map[string]Type{"id": TInt(), "title": TString()},
		Order:  []string{"id", "title"},
	}
	want := "{id: int, title: string}"
	if got := r.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestUnionPrintAnonymous(t *testing.T) {
	resetVarIDsForTesting()
	a := FreshVar()
	u := TUnion{
		Variants:     map[string][]Type{"just": {a}, "nothing": {}},
		VariantOrder: []string{"just", "nothing"},
	}
	want := "(just α) | (nothing)"
	if got := u.String(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
