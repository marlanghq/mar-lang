package runtime

import (
	"math"
	"testing"
)

// Regression tests for docs/security-audit-2026-07-15.md. Helpers
// (runDispatch, statusOf, vJust, vNothing) live in auth_test.go.

// rawService returns an ExposedService as produced by a bare
// Service.implement — i.e. NOT run through Auth.protect, so RequiresUser
// starts false. Its handler returns Unit; for the bypass tests it should
// never be reached (the 401 short-circuits before the handler runs).
func rawService() VExposedService {
	noop := nativeFn(2, func(args []Value) (Value, error) { return VUnit{}, nil })
	return VExposedService{Service: VService{
		Handler:      noop,
		OriginModule: "Test",
		OriginName:   "raw",
		RequiresUser: false,
	}}
}

// #1 (Alta) — decorating a raw service with an authorization gate must
// still require authentication. Before the fix the decorators left
// RequiresUser=false, the dispatcher skipped every gate, and the
// "protected" service served public.
func TestSecurityAudit_DecoratorsImplyAuth(t *testing.T) {
	cases := []struct {
		name string
		make func() (Value, error)
	}{
		{"requireRole", func() (Value, error) {
			return makeAuthRequireRole([]Value{VString{V: "admin"}, rawService()})
		}},
		{"authorize", func() (Value, error) {
			loader := nativeFn(2, func(a []Value) (Value, error) { return vJust(VUnit{}), nil })
			policy := nativeFn(3, func(a []Value) (Value, error) { return VBool{V: true}, nil })
			return makeAuthAuthorize([]Value{loader, policy, rawService()})
		}},
		{"requireOwner", func() (Value, error) {
			loader := nativeFn(2, func(a []Value) (Value, error) { return vJust(VUnit{}), nil })
			selector := nativeFn(1, func(a []Value) (Value, error) { return VInt{V: 1}, nil })
			return makeAuthRequireOwner([]Value{loader, selector, rawService()})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := tc.make()
			if err != nil {
				t.Fatalf("decorator: %v", err)
			}
			exposed := out.(VExposedService)
			if !exposed.Service.RequiresUser {
				t.Fatal("decorator left RequiresUser=false — a gated service would serve public")
			}
			resp := runDispatch(t, exposed, "", vNothing())
			if got := statusOf(t, resp); got != 401 {
				t.Fatalf("unauthenticated request: status = %d, want 401 (auth bypass!)", got)
			}
		})
	}
}

// #1 defense-in-depth — even if a service carries a gate but somehow has
// RequiresUser=false, ExposedServiceToRoute coerces it so it can't serve
// public through the dispatcher.
func TestSecurityAudit_GateImpliesAuthAtDispatch(t *testing.T) {
	noop := nativeFn(2, func(args []Value) (Value, error) { return VUnit{}, nil })
	exposed := VExposedService{Service: VService{
		Handler:      noop,
		RequireRole:  VString{V: "admin"}, // gate present...
		RequiresUser: false,               // ...but the flag was forgotten
	}}
	resp := runDispatch(t, exposed, "", vNothing())
	if got := statusOf(t, resp); got != 401 {
		t.Fatalf("gate with RequiresUser=false: status = %d, want 401", got)
	}
}

// #2 (Alta) — List.range / List.repeat / String.repeat must reject a
// caller-controlled count that would exhaust memory, and List.range must
// not spin forever on Int overflow. The overflow case has to reject
// promptly (no 10M-element allocation before erroring).
func TestSecurityAudit_ExpansionBounded(t *testing.T) {
	listRange := stdlib()["listRange"].(VFn)
	listRepeat := stdlib()["listRepeat"].(VFn)
	stringRepeat := stdlib()["stringRepeat"].(VFn)

	// Normal ranges still work.
	out, err := listRange.Native([]Value{VInt{V: 1}, VInt{V: 3}})
	if err != nil {
		t.Fatalf("List.range 1..3: %v", err)
	}
	if l, ok := out.(VList); !ok || len(l.Elements) != 3 {
		t.Fatalf("List.range 1..3 = %v, want 3 elements", out)
	}
	// from > to is empty, not an error.
	out, err = listRange.Native([]Value{VInt{V: 5}, VInt{V: 1}})
	if err != nil {
		t.Fatalf("List.range 5..1: %v", err)
	}
	if l, ok := out.(VList); !ok || len(l.Elements) != 0 {
		t.Fatalf("List.range 5..1 = %v, want empty", out)
	}
	// The overflow span that used to loop forever must error.
	if _, err := listRange.Native([]Value{VInt{V: 0}, VInt{V: math.MaxInt64}}); err == nil {
		t.Fatal("List.range 0..MaxInt64 should be rejected, not materialized")
	}
	// A merely-huge (non-overflowing) span is also rejected.
	if _, err := listRange.Native([]Value{VInt{V: 0}, VInt{V: 500_000_000}}); err == nil {
		t.Fatal("List.range over the cap should be rejected")
	}
	// List.repeat with a huge count errors instead of allocating.
	if _, err := listRepeat.Native([]Value{VInt{V: math.MaxInt64}, VUnit{}}); err == nil {
		t.Fatal("List.repeat MaxInt64 should be rejected")
	}
	// String.repeat bounds total output bytes.
	if _, err := stringRepeat.Native([]Value{VInt{V: math.MaxInt64}, VString{V: "ab"}}); err == nil {
		t.Fatal("String.repeat MaxInt64 should be rejected")
	}
	// A small repeat still works.
	sr, err := stringRepeat.Native([]Value{VInt{V: 3}, VString{V: "x"}})
	if err != nil {
		t.Fatalf("String.repeat 3 x: %v", err)
	}
	if s, ok := sr.(VString); !ok || s.V != "xxx" {
		t.Fatalf("String.repeat 3 x = %v, want xxx", sr)
	}
}
