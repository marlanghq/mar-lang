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

// Int, String and Char have more values than any list of patterns can name,
// so no set of literals is exhaustive and the case has to carry a catch-all.
// Elm answers the same way, and for the same reason: this is the ordinary
// exhaustiveness question meeting a type with an unbounded set of values, not
// a rule invented for literals.
//
// Before this, `case n of 1 -> ...` compiled and produced `no case branch
// matched` at runtime: the last error a checked program could raise that was
// a mistake in its own logic rather than a budget being exceeded.
func TestCaseOverUnboundedScalarsNeedsACatchAll(t *testing.T) {
	subjects := map[string][2]string{
		"Int":    {"Int", "1"},
		"String": {"String", `"a"`},
		"Char":   {"Char", "'a'"},
	}
	for name, sub := range subjects {
		typeName, literal := sub[0], sub[1]
		t.Run(name+" without a catch-all is refused", func(t *testing.T) {
			_, err := checkSource(t, "module M exposing (f)\n\n\nf : "+typeName+" -> Int\nf x =\n    case x of\n        "+literal+" -> 1\n")
			if err == nil {
				t.Fatalf("a case over %s with only literals was accepted", typeName)
			}
			if !strings.Contains(err.Error(), "catch-all") {
				t.Errorf("the message should ask for a catch-all, got: %v", err)
			}
		})
		t.Run(name+" with a catch-all is fine", func(t *testing.T) {
			if _, err := checkSource(t, "module M exposing (f)\n\n\nf : "+typeName+" -> Int\nf x =\n    case x of\n        "+literal+" -> 1\n        _ -> 0\n"); err != nil {
				t.Fatalf("a case with a catch-all was refused: %v", err)
			}
		})
		t.Run(name+" with a named catch-all is fine", func(t *testing.T) {
			if _, err := checkSource(t, "module M exposing (f)\n\n\nf : "+typeName+" -> Int\nf x =\n    case x of\n        "+literal+" -> 1\n        other -> 0\n"); err != nil {
				t.Fatalf("a bare name should count as a catch-all: %v", err)
			}
		})
	}
}

// The rule reaches inside constructors too: an Int sitting in an argument
// position is still an Int, and matching only some of its values still leaves
// the rest unanswered.
func TestUnboundedScalarInsideAConstructorNeedsACatchAll(t *testing.T) {
	_, err := checkSource(t, `module M exposing (f)


f : Maybe Int -> Int
f m =
    case m of
        Just 1 ->
            1

        Nothing ->
            0
`)
	if err == nil {
		t.Fatal("`Just 1` with no other Just branch was accepted")
	}
	// The witness names the shape that is unmatched rather than asking for a
	// catch-all: `Just _` is the useful thing to say, because a `Just other`
	// branch is usually the right fix, not a blanket `_`.
	if !strings.Contains(err.Error(), "Just _") {
		t.Errorf("the message should name the unmatched shape, got: %v", err)
	}
}

// A list's patterns CAN be exhaustive, `[]` together with `x :: rest` names
// every list there is, which is exactly why leaving one out is an error
// rather than something the checker has to tolerate. This was the last way a
// checked program could still reach `no case branch matched`.
func TestListPatternsMustCoverEveryLength(t *testing.T) {
	for _, tc := range []struct {
		name, branches string
		ok             bool
	}{
		{"empty and cons", "        [] -> 0\n        x :: rest -> x\n", true},
		{"a wildcard closes it", "        [] -> 0\n        _ -> 1\n", true},
		{"one element only", "        [ x ] -> x\n", false},
		{"cons without empty", "        x :: rest -> x\n", false},
		{"empty without cons", "        [] -> 0\n", false},
		{"fixed lengths only", "        [] -> 0\n        [ x ] -> x\n        [ x, y ] -> x\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := checkSource(t, "module M exposing (f)\n\n\nf : List Int -> Int\nf xs =\n    case xs of\n"+tc.branches)
			if tc.ok && err != nil {
				t.Fatalf("an exhaustive list match was refused: %v", err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatal("a list match that leaves lengths out was accepted")
				}
				if !strings.Contains(err.Error(), "non-exhaustive") {
					t.Errorf("expected a coverage error, got: %v", err)
				}
			}
		})
	}
}

// The witness names a value nobody matched, so the message points at the shape
// that was forgotten instead of only saying that something was.
func TestTheMessageNamesWhatIsMissing(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"an empty list", "f : List Int -> Int\nf xs =\n    case xs of\n        x :: rest -> x\n", "[]"},
		{"a longer list", "f : List Int -> Int\nf xs =\n    case xs of\n        [] -> 0\n        [ x ] -> x\n", "::"},
		{"a union constructor", "type Color = Red | Green | Blue\n\n\nf : Color -> Int\nf c =\n    case c of\n        Red -> 1\n        Green -> 2\n", "Blue"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := checkSource(t, "module M exposing (f)\n\n\n"+tc.source)
			if err == nil {
				t.Fatal("expected a coverage error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the message should mention %q, got: %v", tc.want, err)
			}
		})
	}
}

// A branch the ones above it already cover can never run. Reporting it is the
// same usefulness question asked against the rows before it instead of against
// all of them, which is why lists and reachability landed together.
func TestUnreachableBranchesAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name, branches string
		ok             bool
	}{
		{"after a catch-all", "        _ -> 0\n        1 -> 1\n", false},
		{"a repeated literal", "        1 -> 1\n        1 -> 2\n        _ -> 0\n", false},
		{"before a catch-all is fine", "        1 -> 1\n        _ -> 0\n", true},
		{"distinct literals are fine", "        1 -> 1\n        2 -> 2\n        _ -> 0\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := checkSource(t, "module M exposing (f)\n\n\nf : Int -> Int\nf n =\n    case n of\n"+tc.branches)
			if tc.ok {
				if err != nil {
					t.Fatalf("a reachable branch was refused: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("a branch that can never match was accepted")
			}
			if !strings.Contains(err.Error(), "can never match") {
				t.Errorf("expected an unreachable-branch error, got: %v", err)
			}
		})
	}
}

// A repeated constructor is dead for the same reason, and this is the shape
// that actually shows up: a `case` grown one branch at a time until a name
// appears twice.
func TestRepeatedConstructorBranchIsRefused(t *testing.T) {
	_, err := checkSource(t, `module M exposing (f)


type Color = Red | Green


f : Color -> Int
f c =
    case c of
        Red -> 1
        Green -> 2
        Red -> 3
`)
	if err == nil {
		t.Fatal("a repeated constructor branch was accepted")
	}
	if !strings.Contains(err.Error(), "can never match") {
		t.Errorf("expected an unreachable-branch error, got: %v", err)
	}
}
