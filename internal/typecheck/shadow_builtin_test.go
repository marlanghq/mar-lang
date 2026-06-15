package typecheck

import (
	"strings"
	"testing"
)

// A user type may not redefine a built-in constructor (the 9 bare-exposed
// names) or a built-in type name. Either silently shadows the built-in for
// the module and resolves later references to the wrong thing; Elm rejects
// the same clash, so we do too.

func TestShadowBuiltinCtorTrueRejected(t *testing.T) {
	src := `module M exposing (..)
type MyBool = True | False
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for redefining True/False, got clean check")
	}
	if !strings.Contains(err.Error(), "True") || !strings.Contains(err.Error(), "built-in constructor") {
		t.Fatalf("expected a built-in-constructor error naming True, got: %v", err)
	}
}

func TestShadowBuiltinCtorOkRejected(t *testing.T) {
	src := `module M exposing (..)
type Saved = Ok | Failed String
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for a constructor named Ok, got clean check")
	}
	if !strings.Contains(err.Error(), "Ok") || !strings.Contains(err.Error(), "Result") {
		t.Fatalf("expected an error naming Ok and its owner Result, got: %v", err)
	}
}

func TestShadowBuiltinTypeRejected(t *testing.T) {
	src := `module M exposing (..)
type Result = Pending | Done
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for redefining the Result type, got clean check")
	}
	if !strings.Contains(err.Error(), "Result") || !strings.Contains(err.Error(), "built-in type") {
		t.Fatalf("expected a built-in-type error naming Result, got: %v", err)
	}
}

func TestShadowBuiltinTypeAliasRejected(t *testing.T) {
	src := `module M exposing (..)
type alias Effect = Int
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected error for an alias named Effect, got clean check")
	}
	if !strings.Contains(err.Error(), "Effect") || !strings.Contains(err.Error(), "built-in type") {
		t.Fatalf("expected a built-in-type error naming Effect, got: %v", err)
	}
}

// Names that are NOT reserved must still be accepted. In particular,
// `Offline` / `Online` are fine: the built-in is the qualified
// `Service.Offline`, not a bare `Offline`, so a local union may use them.
func TestNonClashingNamesAccepted(t *testing.T) {
	src := `module M exposing (..)
type Status = Active | Paused | Closed
type Connection = Offline | Online
type alias Profile = { name : String }
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("expected clean check for non-clashing names, got: %v", err)
	}
}
