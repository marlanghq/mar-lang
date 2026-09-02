package runtime

import (
	"strings"
	"testing"
)

// `Auth.requireOwner` compares `selector(resource)` against the signed-in
// user's key. It asked for a field literally called `id`, while the rest of
// the framework asks `idColumnName` which field `Entity.serial` marked — so an
// app whose users table has a `pk` answered 500 to every request to an
// ownership-gated service, on a program that checked and booted clean.

func ownerPolicy(t *testing.T, ownerID Value) Value {
	t.Helper()
	selector := VFn{Arity: 1, Native: func([]Value) (Value, error) { return ownerID, nil }}
	out, err := makeAuthRequireOwner([]Value{
		VUnit{}, selector, VExposedService{Service: VService{Verb: "GET", Path: "/x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.(VExposedService).Service.Policy
}

func decide(t *testing.T, policy Value, user Value) (bool, error) {
	t.Helper()
	v, err := Apply(policy, VUnit{})
	if err != nil {
		return false, err
	}
	if v, err = Apply(v, user); err != nil {
		return false, err
	}
	v, err = Apply(v, VUnit{})
	if err != nil {
		return false, err
	}
	b, ok := v.(VBool)
	if !ok {
		t.Fatalf("a policy answers Bool, got %T", v)
	}
	return b.V, nil
}

func userWith(field string, id int64) VRecord {
	return VRecord{
		Fields: map[string]Value{field: VInt{V: id}, "email": VString{V: "a@b"}},
		Order:  []string{field, "email"},
	}
}

func entityKeyed(field string) VEntity {
	return VEntity{Table: "people", Fields: []EntityField{
		{Name: field, SQLType: "INTEGER", NotNull: true, Serial: true},
		{Name: "email", SQLType: "TEXT", NotNull: true},
	}}
}

func TestOwnershipFollowsTheKeyTheEntityDeclared(t *testing.T) {
	t.Cleanup(ResetAuth)
	if err := RegisterAuth(VAuth{Entity: entityKeyed("pk")}); err != nil {
		t.Fatal(err)
	}
	policy := ownerPolicy(t, VInt{V: 7})

	owner, err := decide(t, policy, userWith("pk", 7))
	if err != nil {
		t.Fatalf("the key is `pk`, and the framework knows it: %v", err)
	}
	if !owner {
		t.Fatal("this user owns the resource")
	}

	stranger, err := decide(t, policy, userWith("pk", 8))
	if err != nil {
		t.Fatal(err)
	}
	if stranger {
		t.Fatal("a different key is a different owner")
	}
}

func TestOwnershipStillWorksWhenTheKeyIsCalledID(t *testing.T) {
	// The ordinary shape, which must not move.
	t.Cleanup(ResetAuth)
	if err := RegisterAuth(VAuth{Entity: entityKeyed("id")}); err != nil {
		t.Fatal(err)
	}
	owner, err := decide(t, ownerPolicy(t, VInt{V: 7}), userWith("id", 7))
	if err != nil || !owner {
		t.Fatalf("owner=%v err=%v", owner, err)
	}
}

func TestAMissingKeyNamesTheFieldItLookedFor(t *testing.T) {
	t.Cleanup(ResetAuth)
	if err := RegisterAuth(VAuth{Entity: entityKeyed("pk")}); err != nil {
		t.Fatal(err)
	}
	_, err := decide(t, ownerPolicy(t, VInt{V: 7}), userWith("somethingElse", 7))
	if err == nil {
		t.Fatal("there is nothing to compare against")
	}
	if !strings.Contains(err.Error(), "`pk`") {
		t.Errorf("the message must name the field it looked for, got: %v", err)
	}
}
