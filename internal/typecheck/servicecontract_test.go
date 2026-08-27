package typecheck

import (
	"strings"
	"testing"
)

// Measured on examples/notes-fullstack before this check existed. With the
// annotation, `POST /notes {}` answers 422 naming the missing field; with the
// annotation removed, the same request answers 500 — the request guard has a
// type variable to check against, which means checking nothing.

func TestAnUnannotatedContractIsRefused(t *testing.T) {
	src := `module M exposing (..)
createNote =
    Service.declare POST "/notes"
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("`Service a b` lets the two sides of the wire disagree")
	}
	for _, want := range []string{"createNote", "request", "Service a b", "annotation"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestAnAnnotatedContractIsAccepted(t *testing.T) {
	src := `module M exposing (..)
type alias NewNote =
    { body : String }

createNote : Service NewNote NewNote
createNote =
    Service.declare POST "/notes"
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("this is the shape every example ships: %v", err)
	}
}

func TestHalfAnAnnotationIsStillHalfAContract(t *testing.T) {
	// The response side alone leaves the caller decoding whatever arrives.
	src := `module M exposing (..)
type alias NewNote =
    { body : String }

createNote : Service NewNote a
createNote =
    Service.declare POST "/notes"
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("an open response type is not agreed by anyone")
	}
	if !strings.Contains(err.Error(), "response") {
		t.Errorf("the message must say which side, got: %v", err)
	}
	// The half that is concrete is kept in the suggestion. It comes back
	// structurally (`{ body : String }`) rather than as the alias name,
	// because that is the type the checker settled on.
	if !strings.Contains(err.Error(), "body : String") {
		t.Errorf("the suggestion should keep the side that is already written: %v", err)
	}
}

func TestAFunctionThatTakesAnyContractIsUntouched(t *testing.T) {
	// Writing a helper over contracts is reasonable; only a value that IS a
	// contract has a wire to be wrong about.
	src := `module M exposing (..)
describe : Service a b -> String
describe _ =
    "a contract"
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("a function is not a contract: %v", err)
	}
}

func TestAValueThatIsNotAServiceIsUntouched(t *testing.T) {
	src := `module M exposing (..)
empty =
    []

pair =
    ( 1, 2 )
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("ordinary polymorphic values are fine: %v", err)
	}
}

// A function has no JSON form, so a contract that puts one in its request
// asks for a field no caller can fill. Measured on notes-fullstack with
// `createNote : Service { body : String, fn : Int -> Int } Note`:
//
//	POST {"body":"oi","fn":null}   200 OK — the note is created
//	POST {"body":"oi"}             422 — "missing the field `fn`"
//
// The guard demands the field and validates nothing in it, because the shape
// reduction has no kind for a function and falls through to "check anything".

func TestAFunctionInTheRequestIsRefused(t *testing.T) {
	src := `module M exposing (..)
run : Service { body : String, fn : Int -> Int } String
run =
    Service.declare POST "/run"
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("no caller can put a function in a JSON body")
	}
	for _, want := range []string{"run", "request", "fn", "Int -> Int"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestAFunctionInTheResponseIsRefused(t *testing.T) {
	src := `module M exposing (..)
run : Service String (Int -> Int)
run =
    Service.declare POST "/run"
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("a response is encoded to JSON too")
	}
	if !strings.Contains(err.Error(), "response") {
		t.Errorf("the message must say which side, got: %v", err)
	}
}

func TestAFunctionHiddenInAUnionPayloadIsFound(t *testing.T) {
	// A record field is the obvious place; a constructor argument hides one
	// just as well, and the shape reduction reads through unions, so the
	// check has to as well.
	src := `module M exposing (..)
type Job
    = Run (Int -> Int)
    | Skip

start : Service Job String
start =
    Service.declare POST "/start"
`
	_, err := checkSource(t, src)
	if err == nil {
		t.Fatal("`Run` carries a function across the wire")
	}
	for _, want := range []string{"Run", "Int -> Int"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestARecursiveRequestTypeTerminates(t *testing.T) {
	// A comment whose replies are comments closes its own cycle. Walking it
	// without a guard does not come back.
	src := `module M exposing (..)
type Comment
    = Comment String (List Comment)

post : Service Comment String
post =
    Service.declare POST "/comment"
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("a recursive type carries no function: %v", err)
	}
}

func TestOrdinaryContractsAreUntouched(t *testing.T) {
	// The shapes the examples actually ship, including a parameterised union
	// in the response — which the shape reduction gives up on, and which is
	// perfectly legal.
	src := `module M exposing (..)
type alias Note =
    { id : Int, body : String }

listNotes : Service () (List Note)
listNotes =
    Service.declare GET "/notes"

getNote : Service { id : Int } (Maybe Note)
getNote =
    Service.declare GET "/note/{id:Int}"

saveNote : Service Note (Result String Note)
saveNote =
    Service.declare POST "/note"
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("these are the shapes the examples ship: %v", err)
	}
}
