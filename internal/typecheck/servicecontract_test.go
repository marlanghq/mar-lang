package typecheck

import (
	"fmt"
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parityfixture"
	"mar/internal/parser"
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

// A PARAMETERISED union used to reduce to WireAny — the shape lookup turned
// one away for want of a use site, and the use site is right there in the
// type being reduced. `Result e a` is the commonest one in the language.
//
// Measured on a service taking `Box Int`, before this:
//
//	{"box":{"__ctor":"Roxo","__args":[7]}}    500, "no case branch matched"
//	{"box":{"__ctor":"Full","__args":["x"]}}  200 OK, a string in an Int field
//
// The first breaks the totality guarantee of ADR 0022 on a `case` the
// checker proved total. The second answers success with a value that is not
// the declared type.

func TestAParameterisedUnionIsReducedToACheckableShape(t *testing.T) {
	env := BaseEnv()
	res, err := checkSourceIn(t, env, `module M exposing (..)
type Box a
    = Full a
    | Empty


unwrap : Service { box : Box Int } Int
unwrap =
    Service.declare POST "/api/unwrap"
`)
	if err != nil {
		t.Fatalf("this is an ordinary contract: %v", err)
	}
	shape := requestShapeOf(t, res, env, "unwrap")
	field := fieldShape(t, shape, "box")
	if field.Kind != ast.WireCtor {
		t.Fatalf("`Box Int` must reduce to a constructor shape, got %q", field.Kind)
	}
	if len(field.Tags) != 2 || field.Tags[0] != "Full" || field.Tags[1] != "Empty" {
		t.Fatalf("the tags are the type's own constructors, got %v", field.Tags)
	}
	// The payload is `a` substituted for `Int`, which is what makes it
	// checkable at all.
	if len(field.Args) != 2 || len(field.Args[0]) != 1 || field.Args[0][0].Kind != ast.WireInt {
		t.Fatalf("`Full a` at `Box Int` carries an Int, got %+v", field.Args)
	}
	if len(field.Args[1]) != 0 {
		t.Fatalf("`Empty` carries nothing, got %+v", field.Args[1])
	}
}

func TestARecursiveParameterisedUnionTerminates(t *testing.T) {
	// The cycle guard keys on the type's name, so a tree of its own kind
	// closes rather than unfolding forever.
	if _, err := checkSource(t, `module M exposing (..)
type Tree a
    = Leaf a
    | Branch (List (Tree a))


plant : Service { tree : Tree Int } Int
plant =
    Service.declare POST "/api/plant"
`); err != nil {
		t.Fatalf("a recursive type is not an error: %v", err)
	}
}

// requestShapeOf reduces a contract's request type the way the loader does.
func checkSourceIn(t *testing.T, env *TypeEnv, src string) (*CheckResult, error) {
	t.Helper()
	resetVarIDsForTesting()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	return CheckModuleWith(mod, env, nil, nil)
}

func requestShapeOf(t *testing.T, res *CheckResult, env *TypeEnv, name string) ast.WireShape {
	t.Helper()
	con, ok := StripForall(res.ValueTypes[name]).(TCon)
	if !ok || con.Name != "Service" || len(con.Args) != 2 {
		t.Fatalf("%s is not a Service", name)
	}
	return WireShapeOf(con.Args[0], CtorsFromEnv(env))
}

func fieldShape(t *testing.T, s ast.WireShape, field string) ast.WireShape {
	t.Helper()
	for _, f := range s.Fields {
		if f.Name == field {
			return f.Shape
		}
	}
	t.Fatalf("no field %q in %+v", field, s)
	return ast.WireShape{}
}

// A URL segment matches a constructor case-INSENSITIVELY, so two that differ
// only in case make one unreachable. Measured on
// `type Role = Admin | ADmin | Member`, before this: `mar check` passed, and
// `/api/who/admin`, `/api/who/ADmin` and `/api/who/Admin` all answered as
// `Admin`. The program declared three roles and the URL could name two.

func TestTwoCtorsDifferingOnlyInCaseCannotBeRouted(t *testing.T) {
	_, err := checkSource(t, `module M exposing (..)
type Role
    = Admin
    | ADmin


whoIs : Service { role : Role } String
whoIs =
    Service.declare GET "/api/who/{role:Role}"
`)
	if err == nil {
		t.Fatal("one of these constructors has no URL that reaches it")
	}
	for _, want := range []string{"Admin", "ADmin", "case"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestTheSameTypeIsFineWhenNothingRoutesOnIt(t *testing.T) {
	// Two such constructors are perfectly legal in a type nobody puts in a
	// URL, which is why the check lives at the path and not at the
	// declaration.
	if _, err := checkSource(t, `module M exposing (..)
type Role
    = Admin
    | ADmin


describe : Role -> String
describe r =
    case r of
        Admin ->
            "a"

        ADmin ->
            "b"
`); err != nil {
		t.Fatalf("nothing routes on Role here: %v", err)
	}
}

func TestDistinctCtorsStillRoute(t *testing.T) {
	if _, err := checkSource(t, `module M exposing (..)
type Role
    = Admin
    | Member


whoIs : Service { role : Role } String
whoIs =
    Service.declare GET "/api/who/{role:Role}"
`); err != nil {
		t.Fatalf("this is the ordinary shape: %v", err)
	}
}

// `Auth.config` took a bare type variable, so the checker asserted NOTHING
// about its record and every field requirement was hand-checked at boot:
// `missing \`signup\``, `missing \`identify\``, `email.subject is required`,
// "`entity` must be an Entity". Same shape as the `Service a b` hole — a
// variable where a structure belongs.
//
// The record is OPEN, which is what makes typing it possible at all: `role`
// and `signInPage` are optional, and a closed type could not have them both
// absent and present.

const authConfigEntity = `module M exposing (..)

type alias User =
    { id : Int
    , email : String
    }


users : Entity User
users =
    Entity.define
        { name = "users"
        , columns = { id = Entity.serial, email = Entity.text Entity.notNull }
        , uniques = []
        }


auth =
    Auth.config
`

func TestAuthConfigRequiresItsFieldsAtCompileTime(t *testing.T) {
	full := map[string]string{
		"entity":          "users",
		"identify":        `\u -> u.email`,
		"signup":          `\e -> { email = e }`,
		"email":           `{ subject = "Sign in" }`,
		"sessionDuration": "Time.days 30",
	}
	order := []string{"entity", "identify", "signup", "email", "sessionDuration"}

	build := func(skip string) string {
		var b strings.Builder
		b.WriteString(authConfigEntity + "        {")
		first := true
		for _, k := range order {
			if k == skip {
				continue
			}
			if first {
				b.WriteString(" ")
				first = false
			} else {
				b.WriteString("        , ")
			}
			b.WriteString(k + " = " + full[k] + "\n")
		}
		b.WriteString("        }\n")
		return b.String()
	}

	if _, err := checkSource(t, build("")); err != nil {
		t.Fatalf("the complete record is what every auth example ships: %v", err)
	}
	for _, missing := range order {
		if _, err := checkSource(t, build(missing)); err == nil {
			t.Errorf("`%s` is required, and leaving it out checked clean", missing)
		}
	}
}

func TestAuthConfigStillTakesItsOptionalFields(t *testing.T) {
	// `role` and `signInPage` ride in the row variable. A closed record type
	// would refuse every app that does not use them.
	src := authConfigEntity + `        { entity = users
        , identify = \u -> u.email
        , signup = \e -> { email = e }
        , email = { subject = "Sign in", body = \code ttl -> code }
        , sessionDuration = Time.days 30
        , role = \u -> u.email
        }
`
	if _, err := checkSource(t, src); err != nil {
		t.Fatalf("the optional fields are optional: %v", err)
	}
}

// A GET or DELETE request has no body: it travels in the URL, so its type is a
// record (path parameters plus the `q` object of the other fields) or `()`.
// Every kind of value the wire carries is tried on every verb.
func TestABodilessRequestIsARecord(t *testing.T) {
	for _, c := range parityfixture.WireCases {
		if c.NoContract {
			continue
		}
		recordOrUnit := strings.HasPrefix(c.Type, "{") || c.Type == "()"
		for _, verb := range []string{"GET", "DELETE", "POST", "PUT", "PATCH"} {
			src := parityfixture.WireTypesSource() + fmt.Sprintf(
				"\n\nsvc : Service (%s) ()\nsvc =\n    Service.declare %s \"/svc\"\n", c.Type, verb)
			_, err := checkSource(t, src)
			if recordOrUnit || !ast.BodilessVerb(verb) {
				if err != nil {
					t.Errorf("%s with a %s request (%s) is an ordinary contract: %v", verb, c.Type, c.Name, err)
				}
				continue
			}
			if err == nil {
				t.Errorf("%s with a %s request (%s) type-checked; no client can send it", verb, c.Type, c.Name)
				continue
			}
			for _, want := range []string{"`svc`", "declared " + verb, "not a record", "Service { value : "} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("%s / %s: the message must mention %q, got:\n%v", verb, c.Name, want, err)
				}
			}
		}
	}
}

// The rule reads the type after aliases are expanded.
func TestABodilessRequestSeesThroughAliases(t *testing.T) {
	cases := []struct {
		name, decls, req string
		refused          bool
	}{
		{"an alias of Int", "type alias Id = Int", "Id", true},
		{"an alias of a record", "type alias Query = { term : String }", "Query", false},
		{"a Maybe of a record alias", "type alias Query = { term : String }", "(Maybe Query)", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "module M exposing (..)\n\n\n" + tc.decls + "\n\n\nsvc : Service " + tc.req +
				" ()\nsvc =\n    Service.declare GET \"/svc\"\n"
			_, err := checkSource(t, src)
			if tc.refused && (err == nil || !strings.Contains(err.Error(), "not a record")) {
				t.Fatalf("refused for being no record, got: %v", err)
			}
			if !tc.refused && err != nil {
				t.Fatalf("a record request is fine: %v", err)
			}
		})
	}
}
