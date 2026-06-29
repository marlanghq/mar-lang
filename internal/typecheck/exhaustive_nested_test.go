package typecheck

import (
	"strings"
	"testing"
)

// Nested-pattern exhaustiveness: a parameterized constructor's argument is
// specialized to the concrete subject type before recursing, so a missing
// inner variant is caught even when it sits under Ok / Just / a user ctor.
// Before instantiateCtorArg these checks were skipped (the arg stayed a bare
// type parameter, which the recursion ignores).

func TestNestedExhaustivenessMissingInnerRejected(t *testing.T) {
	src := `module M exposing (..)
type Status = Active | Paused | Closed
check : Result String Status -> Int
check r =
    case r of
        Ok Active -> 1
        Ok Paused -> 2
        Err _ -> 0
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected non-exhaustive error (missing Ok Closed), got clean check")
	}
	if !strings.Contains(err.Error(), "Closed") {
		t.Fatalf("expected missing-pattern error naming Closed, got: %v", err)
	}
}

func TestNestedExhaustivenessFullyCovered(t *testing.T) {
	src := `module M exposing (..)
type Status = Active | Paused | Closed
check : Result String Status -> Int
check r =
    case r of
        Ok Active -> 1
        Ok Paused -> 2
        Ok Closed -> 3
        Err _ -> 0
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("expected clean check for fully-covered nested case, got: %v", err)
	}
}

func TestNestedExhaustivenessCatchAllCovers(t *testing.T) {
	src := `module M exposing (..)
type Status = Active | Paused | Closed
check : Result String Status -> Int
check r =
    case r of
        Ok _  -> 1
        Err _ -> 0
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("expected clean check (Ok _ covers the inner type), got: %v", err)
	}
}

// The argument is a bare type parameter (Box a = Box a). Before the fix the
// parameter stayed a TVar and the inner Bool exhaustiveness was skipped, so
// `Box True` alone was wrongly accepted.
func TestParamArgExhaustivenessRejected(t *testing.T) {
	src := `module M exposing (..)
type Box a = Box a
check : Box Bool -> Int
check b =
    case b of
        Box True -> 1
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected non-exhaustive error (missing Box False), got clean check")
	}
	if !strings.Contains(err.Error(), "False") {
		t.Fatalf("expected missing-pattern error naming False, got: %v", err)
	}
}

// The real-world case the error-handling work introduced: an auth verify
// result. A missing `Ok Auth.TooManyAttempts` must be caught.
func TestAuthVerifyOutcomeNestedExhaustiveness(t *testing.T) {
	src := `module M exposing (..)
type alias User = { id : Int }
check : Result Service.Error (Auth.VerifyOutcome User) -> Int
check r =
    case r of
        Ok (Auth.SignedIn _)  -> 1
        Ok Auth.WrongCode     -> 2
        Err _                 -> 0
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("expected non-exhaustive error (missing Ok Auth.TooManyAttempts), got clean check")
	}
	if !strings.Contains(err.Error(), "TooManyAttempts") {
		t.Fatalf("expected missing-pattern error naming TooManyAttempts, got: %v", err)
	}
}

func TestAuthVerifyOutcomeNestedFullyCovered(t *testing.T) {
	src := `module M exposing (..)
type alias User = { id : Int }
check : Result Service.Error (Auth.VerifyOutcome User) -> Int
check r =
    case r of
        Ok (Auth.SignedIn _)    -> 1
        Ok Auth.WrongCode       -> 2
        Ok Auth.TooManyAttempts -> 3
        Err _                   -> 0
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("expected clean check for fully-covered auth verify case, got: %v", err)
	}
}
