package runtime

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// A record missing a field cannot be written in a program that compiles — the
// checker rejects it — so these tests build the record by hand, the way the
// two paths that CAN produce one do: decoded wire data, and a model a dev
// server preserved across a reload. What matters is that the read fails
// loudly and says which field and which record, rather than handing back a
// value that travels and blows up somewhere unrelated.

func recordWithout(t *testing.T) VRecord {
	t.Helper()
	return VRecord{
		Fields: map[string]Value{"name": VString{V: "Alice"}, "age": VInt{V: 30}},
		Order:  []string{"name", "age"},
	}
}

func TestFieldAccessMissingFieldErrors(t *testing.T) {
	env := NewEnv().Bind("r", recordWithout(t))
	expr := &ast.EFieldAccess{
		Record: &ast.EVar{Name: "r"},
		Field:  "email",
	}
	_, err := Eval(expr, env)
	if err == nil {
		t.Fatal("reading a field that is not there returned a value instead of failing")
	}
	msg := err.Error()
	for _, want := range []string{"record has no field `email`", "this record has: name, age"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not contain %q:\n%s", want, msg)
		}
	}
}

func TestFieldAccessorMissingFieldErrors(t *testing.T) {
	acc, err := Eval(&ast.EFieldAccessor{Field: "email"}, NewEnv())
	if err != nil {
		t.Fatalf("building .email: %v", err)
	}
	if _, err := Apply(acc, recordWithout(t)); err == nil {
		t.Fatal(".email on a record without it returned a value instead of failing")
	} else if !strings.Contains(err.Error(), "record has no field `email`") {
		t.Errorf("unexpected message: %s", err)
	}
}

// Without a declared order — a record rebuilt from decoded data — the listing
// still has to be stable, or the same failure prints a different list each run.
func TestMissingFieldListIsStableWithoutOrder(t *testing.T) {
	rec := VRecord{Fields: map[string]Value{"zeta": VUnit{}, "alpha": VUnit{}, "mid": VUnit{}}}
	first := missingFieldMessage("nope", rec)
	if !strings.Contains(first, "this record has: alpha, mid, zeta") {
		t.Fatalf("fields are not sorted:\n%s", first)
	}
	for range 20 {
		if got := missingFieldMessage("nope", rec); got != first {
			t.Fatalf("message varies between calls:\n%s\n---\n%s", first, got)
		}
	}
}

func TestMissingFieldOnEmptyRecord(t *testing.T) {
	got := missingFieldMessage("anything", VRecord{Fields: map[string]Value{}})
	if !strings.Contains(got, "this record has: (no fields)") {
		t.Fatalf("empty record reads badly:\n%s", got)
	}
}

// JSON.decode is the one path in the Go runtime that can build a record whose
// field set is not the declared one — its type is `String -> Result String a`,
// so the checker takes the call site's word for the shape and nothing verifies
// it. That makes it the one path that reaches the message above, and the
// decoded record has no declared order to inherit, so the order it prints has
// to come from somewhere stable.
func TestDecodedRecordOrderIsStable(t *testing.T) {
	seen := map[string]int{}
	for range 30 {
		v, err := convertJSON(map[string]any{
			"zeta": "z", "alpha": "a", "mid": "m", "beta": "b", "omega": "o",
		})
		if err != nil {
			t.Fatal(err)
		}
		seen[v.(VRecord).Display()]++
	}
	if len(seen) != 1 {
		t.Fatalf("a decoded record displays %d different field orders across 30 decodes:\n%v", len(seen), seen)
	}
	for got := range seen {
		want := `{ alpha = "a", beta = "b", mid = "m", omega = "o", zeta = "z" }`
		if got != want {
			t.Errorf("fields are not sorted:\n got %s\nwant %s", got, want)
		}
	}
}

// The reachability proof, from real Mar source rather than a hand-built value.
//
// `JSON.decode : String -> Result String a` unifies `a` with whatever the call
// site reads, so this module type-checks: the checker believes `user` has an
// `email` because the code asks for one. Nothing between the JSON text and the
// field read verifies that. This is the shape of program the message is for.
func TestJSONDecodeReachesTheMissingFieldMessage(t *testing.T) {
	src := `module M exposing (..)

result : String
result =
    case JSON.decode "{\"name\":\"Alice\"}" of
        Ok user  -> user.email
        Err e    -> e
`
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("this module is supposed to type-check — that is the whole point: %v", err)
	}
	// Top-level values evaluate as the module loads, so the read fails here.
	_, err = LoadModule(mod)
	if err == nil {
		t.Fatal("reading a field the JSON did not have should fail")
	}
	for _, want := range []string{"record has no field `email`", "this record has: name"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not contain %q:\n%v", want, err)
		}
	}
}
