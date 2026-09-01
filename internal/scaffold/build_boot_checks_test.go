package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/runtime"
)

// A page the checks accept as real: declared as a top-level binding, which is
// what gives it provenance and what an inline `Page.create ...` would lack.
func pageAt(path, name string) runtime.VPage {
	return runtime.VPage{Path: path, Title: name, OriginModule: "Main", OriginName: name}
}

// Likewise for a service: a top-level contract, with the request shape the
// compiler attaches to one. Without the shape the checks stop at "declared
// inline" and never reach the route table.
func svcAt(path, name string) runtime.VExposedService {
	return runtime.VExposedService{Service: runtime.VService{
		Verb: "POST", Path: path, OriginModule: "Main", OriginName: name,
		InputShape: &ast.WireShape{Kind: ast.WireUnit},
	}}
}

// `mar dev` and `mar build` install DIFFERENT `App.*` builtins: the dev server
// wires the real ones, and the build wires captures that record the page list
// and return, because a build must not open a port or a database. The checks
// lived inside the dev builtins, so the build ran none of them.
//
// The consequence is the worst shape this recurrence has taken: the path that
// runs the checks is the one you try locally, and the path that skips them is
// the one that ships. Two pages on `/` — whose own message says the browser
// keeps the last and the phone keeps the first — was refused by `mar dev` and
// written to `dist` by `mar build`, silently.
//
// This is the fifth time a check has turned out to be live on one entry point
// and absent on another (see internal/apphost/entrypoint_parity_test.go for
// the first four), so the check now lives in apphost, in one function both
// call.
func TestTheBuildRunsTheSameBootChecksAsDev(t *testing.T) {
	twoPagesOnOnePath := runtime.VList{Elements: []runtime.Value{
		pageAt("/", "first"),
		pageAt("/", "second"),
	}}

	t.Run("App.frontend", func(t *testing.T) {
		bc := &buildCtx{}
		capture := makeFrontendCapture(nil, bc).(runtime.VFn)
		_, err := capture.Native([]runtime.Value{twoPagesOnOnePath})
		if err == nil {
			t.Fatal("the build accepted two pages on `/`, which `mar dev` refuses: " +
				"a program you cannot run locally would ship")
		}
		if !strings.Contains(err.Error(), "two pages are declared on `/`") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	})

	// And a program with nothing wrong still builds, or the guard is a wall.
	t.Run("a sound page list still builds", func(t *testing.T) {
		bc := &buildCtx{}
		capture := makeFrontendCapture(nil, bc).(runtime.VFn)
		ok := runtime.VList{Elements: []runtime.Value{
			pageAt("/", "home"),
			pageAt("/about", "about"),
		}}
		if _, err := capture.Native([]runtime.Value{ok}); err != nil {
			t.Fatalf("two pages on two paths is an ordinary app: %v", err)
		}
		if bc.kind != kindFrontend {
			t.Fatal("the capture has to record the app kind, or the build has nothing to write")
		}
	})

	// The iOS build is a third pipeline with a third set of captures, and it
	// skipped the checks the same way. It is the sharpest case of all: the
	// check that catches two pages on one path exists BECAUSE the phone keeps
	// the first and the browser keeps the last, and this is the path that
	// builds the phone.
	//
	// Driven end to end, through BuildIOS, because that pipeline loads and
	// evaluates the project itself: there is no capture to hand a page list to.
	t.Run("the iOS build", func(t *testing.T) {
		dir := t.TempDir()
		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("mar.json", `{"name":"twin","locale":"en","ios":{"bundleId":"com.example.twin",`+
			`"displayName":"Twin","marketingVersion":"1.0","buildNumber":"1"}}`)
		write("Main.mar", twoPagesOnOnePathSource)

		_, err := BuildIOS(filepath.Join(dir, "Main.mar"), t.TempDir(), "")
		if err == nil {
			t.Fatal("the iOS build wrote an Xcode project for a program `mar dev` refuses to start")
		}
		if !strings.Contains(err.Error(), "two pages are declared on `/`") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	})

	// App.fullstack on the web/binary path. The iOS pipeline reaches the same
	// checks through apphost.CheckProgram, and both pipelines share
	// makeBackendCapture outright, so App.backend below covers iOS too.
	t.Run("App.fullstack", func(t *testing.T) {
		bc := &buildCtx{env: runtime.NewEnv()}
		capture := makeFullstackCapture(bc).(runtime.VFn)
		rec := runtime.VRecord{
			Fields: map[string]runtime.Value{
				"pages": runtime.VList{Elements: []runtime.Value{pageAt("/", "home")}},
				"services": runtime.VList{Elements: []runtime.Value{
					svcAt("/api/notes", "createNote"), svcAt("/api/notes", "alsoCreateNote"),
				}},
			},
			Order: []string{"pages", "services"},
		}
		_, err := capture.Native([]runtime.Value{rec})
		if err == nil {
			t.Fatal("the build accepted two services on one path in a fullstack app")
		}
		if !strings.Contains(err.Error(), "two services are declared on") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	})

	t.Run("App.backend", func(t *testing.T) {
		bc := &buildCtx{}
		capture := makeBackendCapture(bc).(runtime.VFn)
		rec := runtime.VRecord{
			Fields: map[string]runtime.Value{"services": runtime.VList{Elements: []runtime.Value{
				svcAt("/api/notes", "createNote"), svcAt("/api/notes", "alsoCreateNote"),
			}}},
			Order: []string{"services"},
		}
		_, err := capture.Native([]runtime.Value{rec})
		if err == nil {
			t.Fatal("the build accepted two services on one path; the second can never run, " +
				"and if the two differ in whether they are protected, the reachable one may be the open half")
		}
		if !strings.Contains(err.Error(), "two services are declared on") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	})
}

// Two pages declared on the same path, written the way a program has to write
// them: top-level bindings, so they carry the provenance the checks require.
const twoPagesOnOnePathSource = `module Main exposing (main)


import UI exposing (text, vstack)


type alias Model = Int


type Msg
    = Noop


init : (Model, Cmd Msg)
init =
    (0, Cmd.none)


update : Msg -> Model -> (Model, Cmd Msg)
update _ model =
    (model, Cmd.none)


view : Model -> View Msg
view _ =
    vstack [] [ text [] "hi" ]


home : Page
home =
    Page.create
        { path = "/"
        , title = "Home"
        , init = init
        , update = update
        , view = view
        , subscriptions = always Sub.none
        }


twin : Page
twin =
    Page.create
        { path = "/"
        , title = "Twin"
        , init = init
        , update = update
        , view = view
        , subscriptions = always Sub.none
        }


main : Cmd ()
main =
    App.frontend [ home, twin ]
`
