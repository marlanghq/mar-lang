package apphost

import (
	"strings"
	"testing"

	"mar/internal/runtime"
)

// The runtime matches a `{name:Type}` path segment through a registry keyed by
// the BARE type name, so two modules that each declare a `Status` write to the
// same key and the last one loaded wins. The checker is not confused — a
// type's identity is `Module.Name` (ADR 0027) — so a booted process holds both
// answers at once, and the URL goes through the wrong one: a `case` the checker
// proved total falls through from an ordinary link.
//
// The guard is a tautology. It compares the checker's answer for THIS param
// against the runtime's answer for that bare name, and where they agree the
// program behaves exactly as before — so it provably cannot refuse a program
// that works today.
//
// TestLoadOrderThatHappensToWorkIsAccepted is the one that pins that property,
// and it is the reason the guard does not simply count declarations: two
// modules can both declare a `Status` while the route still resolves to the
// one that wins. That program works, and has to keep working.

func serviceWith(path string, param string, checkerTags []string) runtime.VExposedService {
	shape := runtime.WireShape{Kind: runtime.WireRecord}
	if param != "" {
		shape.Fields = []runtime.WireField{{
			Name:  param,
			Shape: runtime.WireShape{Kind: runtime.WireCtor, Tags: checkerTags},
		}}
	}
	return runtime.VExposedService{
		Service: runtime.VService{Verb: "GET", Path: path, InputShape: &shape},
	}
}

func TestPathEnumCollisionIsRefusedAtBoot(t *testing.T) {
	runtime.ResetEnumTypes()
	t.Cleanup(runtime.ResetEnumTypes)
	arity := map[string]int{"Open": 0, "Closed": 0, "Draft": 0, "Archived": 0}
	// Two modules declare Status; the LAST one loaded wins the bare key.
	runtime.RegisterEnumType("Status", []string{"Open", "Closed"}, arity)
	runtime.RegisterEnumType("Status", []string{"Draft", "Archived"}, arity)

	// The checker resolved this route's param to the OTHER one.
	err := checkPathEnumsAgree([]runtime.VExposedService{
		serviceWith("/s/{st:Status}", "st", []string{"Open", "Closed"}),
	})
	if err == nil {
		t.Fatal("the route would decode the URL into the wrong type")
	}
	for _, want := range []string{"/s/{st:Status}", "Open", "Draft"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got:\n%s", want, err)
		}
	}
}

func TestLoadOrderThatHappensToWorkIsAccepted(t *testing.T) {
	// Two modules declare Status with different constructors, and the route
	// resolves to the one that WINS the bare key. This program works today,
	// so refusing it would break the promise the whole guard is written
	// under. Counting declarations would refuse it; comparing answers does
	// not.
	runtime.ResetEnumTypes()
	t.Cleanup(runtime.ResetEnumTypes)
	arity := map[string]int{"Open": 0, "Closed": 0, "Draft": 0, "Archived": 0}
	runtime.RegisterEnumType("Status", []string{"Draft", "Archived"}, arity)
	runtime.RegisterEnumType("Status", []string{"Open", "Closed"}, arity)

	err := checkPathEnumsAgree([]runtime.VExposedService{
		serviceWith("/s/{st:Status}", "st", []string{"Open", "Closed"}),
	})
	if err != nil {
		t.Fatalf("the route resolves to the winner, so it works: %v", err)
	}
}

func TestUnrelatedCollisionIsAccepted(t *testing.T) {
	// mar-website registers `Msg` from eleven modules into one bare key, and
	// it is harmless because no path uses `{x:Msg}`. The guard looks only at
	// names a ROUTE actually decodes.
	runtime.ResetEnumTypes()
	t.Cleanup(runtime.ResetEnumTypes)
	arity := map[string]int{"A": 0, "B": 0, "C": 0}
	runtime.RegisterEnumType("Msg", []string{"A", "B"}, arity)
	runtime.RegisterEnumType("Msg", []string{"C"}, arity)

	err := checkPathEnumsAgree([]runtime.VExposedService{
		serviceWith("/x/{id:Int}", "", nil),
		serviceWith("/y", "", nil),
	})
	if err != nil {
		t.Fatalf("a collision no route decodes is nobody's problem: %v", err)
	}
}

func TestBuiltinPathTypesAreUntouched(t *testing.T) {
	runtime.ResetEnumTypes()
	t.Cleanup(runtime.ResetEnumTypes)
	for _, p := range []string{"/a/{id:Int}", "/b/{slug:String}", "/c"} {
		if err := checkPathEnumsAgree([]runtime.VExposedService{serviceWith(p, "", nil)}); err != nil {
			t.Errorf("%s must be fine: %v", p, err)
		}
	}
}
