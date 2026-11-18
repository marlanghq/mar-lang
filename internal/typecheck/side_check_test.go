package typecheck

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
)

func parseModules(t *testing.T, sources ...string) []*ast.Module {
	t.Helper()
	var mods []*ast.Module
	for _, src := range sources {
		m, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v\n%s", err, src)
		}
		mods = append(mods, m)
	}
	return mods
}

// The app from docs/proposals/where-code-runs.md, which compiled and shipped a
// broken bundle. Without this the check proves nothing.
func TestPageReachingTheDatabaseIsRejected(t *testing.T) {
	mods := parseModules(t, `
module Main exposing (main)

import UI exposing (navigationStack, navigationTitle, text)

tasks = Entity.define { name = "task", columns = { id = Entity.serial }, uniques = [] }

init = ( 0, Cmd.perform (\rows -> Loaded rows) (Repo.all tasks) )

home =
    Page.create
        { path = "/", title = "t", init = init, update = update
        , view = view, subscriptions = always Sub.none
        }

main = App.frontend [ home ]
`)
	issues := RunSideCheck(mods)
	if len(issues) == 0 {
		t.Fatal("a page reaching Repo.all was accepted")
	}
	if !strings.Contains(issues[0].Message, "Repo.all") {
		t.Errorf("message should name Repo.all, got: %s", issues[0].Message)
	}
	if !strings.Contains(issues[0].Message, "`home`") {
		t.Errorf("message should name the page, got: %s", issues[0].Message)
	}
}

// One hop further out: the page is clean, the helper it calls is not.
func TestLeakThroughAHelperIsRejected(t *testing.T) {
	mods := parseModules(t, `
module Main exposing (main)

tasks = Entity.define { name = "task", columns = { id = Entity.serial }, uniques = [] }

load = Repo.all tasks

init = ( 0, Cmd.perform (\rows -> Loaded rows) load )

home =
    Page.create
        { path = "/", title = "t", init = init, update = update
        , view = view, subscriptions = always Sub.none
        }

main = App.frontend [ home ]
`)
	issues := RunSideCheck(mods)
	if len(issues) == 0 {
		t.Fatal("a page reaching Repo.all through a helper was accepted")
	}
	if !strings.Contains(issues[0].Message, "through") {
		t.Errorf("message should name the hop, got: %s", issues[0].Message)
	}
}

// A fullstack module may hold both halves: the build prunes to what the pages
// reach, and the handler is not among them. examples/guestbook is exactly this
// shape, and a module-level version of this check rejected it, which is why
// the granularity is the declaration.
func TestHandlerBesideAPageIsAccepted(t *testing.T) {
	mods := parseModules(t, `
module Main exposing (main)

tasks = Entity.define { name = "task", columns = { id = Entity.serial }, uniques = [] }

listTasks = Service.declare GET "/api/tasks"

handler = Service.implement listTasks (\_ -> Repo.all tasks)

init = ( 0, Cmd.perform (\rows -> Loaded rows) (Service.call listTasks) )

home =
    Page.create
        { path = "/", title = "t", init = init, update = update
        , view = view, subscriptions = always Sub.none
        }

main = App.fullstack { pages = [ home ], services = [ handler ] }
`)
	if issues := RunSideCheck(mods); len(issues) > 0 {
		t.Fatalf("a page next to a handler was rejected: %s", issues[0].Message)
	}
}

// Cross-module: the page imports a module that touches the database.
func TestLeakThroughAnImportIsRejected(t *testing.T) {
	mods := parseModules(t, `
module Store exposing (load)

tasks = Entity.define { name = "task", columns = { id = Entity.serial }, uniques = [] }

load = Repo.all tasks
`, `
module Main exposing (main)

import Store exposing (load)

init = ( 0, Cmd.perform (\rows -> Loaded rows) load )

home =
    Page.create
        { path = "/", title = "t", init = init, update = update
        , view = view, subscriptions = always Sub.none
        }

main = App.frontend [ home ]
`)
	if issues := RunSideCheck(mods); len(issues) == 0 {
		t.Fatal("a page importing a database-touching module was accepted")
	}
}

// Two declarations with the same simple name on different sides is ordinary in
// a fullstack app. Keying the call graph by simple name would flag the page's
// `load`, which is the false positive docs/proposals/where-code-runs.md warns
// about, and an earlier draft of this file reproduced it through its bare-name
// resolver.
func TestSameNameOnBothSidesIsNotConfused(t *testing.T) {
	mods := parseModules(t, `
module Backend exposing (load)

tasks = Entity.define { name = "task", columns = { id = Entity.serial }, uniques = [] }

load = Repo.all tasks
`, `
module Page exposing (home)

load = Cmd.none

init = ( 0, load )

home =
    Page.create
        { path = "/", title = "t", init = init, update = update
        , view = view, subscriptions = always Sub.none
        }
`, `
module Main exposing (main)

import Page
import Backend

main = App.fullstack { pages = [ Page.home ], services = [] }
`)
	if issues := RunSideCheck(mods); len(issues) > 0 {
		t.Fatalf("a page's own `load` inherited the backend one: %s", issues[0].Message)
	}
}

// No pages, no roots, nothing to say: a backend-only project must not trip
// over its own `Repo` calls.
func TestBackendOnlyProjectIsAccepted(t *testing.T) {
	mods := parseModules(t, `
module Main exposing (main)

tasks = Entity.define { name = "task", columns = { id = Entity.serial }, uniques = [] }

listTasks = Service.declare GET "/api/tasks"

handler = Service.implement listTasks (\_ -> Repo.all tasks)

main = App.backend { services = [ handler ] }
`)
	if issues := RunSideCheck(mods); len(issues) > 0 {
		t.Fatalf("a backend-only project was rejected: %s", issues[0].Message)
	}
}

// The mirror of TestPageReachingTheDatabaseIsRejected, and the case the first
// version of this check walked straight past: a service handler reaching a
// browser-only builtin compiled clean and died on the first request instead of
// in the browser. The side table already said Canvas was frontend; only the
// second walk was missing.
func TestHandlerReachingTheBrowserIsRejected(t *testing.T) {
	mods := parseModules(t, `
module Main exposing (main)

ping = Service.declare GET "/api/ping"

render n = Canvas.rect 0 0 n n (Canvas.rgb 1 2 3)

handler = Service.implement ping (\_ -> Task.succeed (render 3))

main = App.backend { services = [ handler ] }
`)
	issues := RunSideCheck(mods)
	if len(issues) == 0 {
		t.Fatal("a handler drawing on the server was accepted")
	}
	if !strings.Contains(issues[0].Message, "Canvas.rect") ||
		!strings.Contains(issues[0].Message, "only exists in the browser") {
		t.Fatalf("wrong message: %s", issues[0].Message)
	}
}

// Auth.protect binds a handler too, so it roots the server walk the same way.
func TestProtectedHandlerReachingTheBrowserIsRejected(t *testing.T) {
	mods := parseModules(t, `
module Main exposing (main)

me = Service.declare GET "/me"

greet = UI.text [] "hello"

handler = Auth.protect me (\_ _ -> Task.succeed greet)

main = App.backend { services = [ handler ] }
`)
	if issues := RunSideCheck(mods); len(issues) == 0 {
		t.Fatal("a protected handler reaching UI was accepted")
	}
}

// A fullstack `main` names the pages next to the services. Rooting the server
// walk at App.fullstack would follow it into every page's UI and report the
// whole app, so the roots are the service binders and nothing else.
func TestFullstackMainIsNotAServerRoot(t *testing.T) {
	mods := parseModules(t, `
module Main exposing (main)

home = Page.create { path = "/", title = "t", init = init, update = update, view = view, subscriptions = sub }

view m = UI.text [] "hi"

tasks = Entity.define { name = "task", columns = { id = Entity.serial }, uniques = [] }

listTasks = Service.declare GET "/api/tasks"

handler = Service.implement listTasks (\_ -> Repo.all tasks)

main = App.fullstack { services = [ handler ], pages = [ home ] }
`)
	if issues := RunSideCheck(mods); len(issues) > 0 {
		t.Fatalf("a legitimate fullstack app was rejected: %s", issues[0].Message)
	}
}

// Date.today reads the device's clock in the device's time zone. Server code
// that reaches it, directly, through a helper, through a Shared module or
// through the loader an Auth.authorize declared on its own runs, is a compile
// error naming the name and saying what to do instead; a page may use it.
func TestDateTodayInServerCodeIsRejected(t *testing.T) {
	shared := `
module Shared exposing (stamp, clear)

stamp = Date.today

clear = Service.declare DELETE "/api/day"
`
	for _, c := range []struct{ name, main, module string }{
		{"in a handler", `
module Main exposing (main)

import Shared

handler = Service.implement Shared.clear (\_ -> Date.today |> Task.map (\_ -> ()))

main = App.backend { services = [ handler ] }
`, "Main"},
		{"through a helper", `
module Main exposing (main)

import Shared

today = Date.today

handler = Service.implement Shared.clear (\_ -> today |> Task.map (\_ -> ()))

main = App.backend { services = [ handler ] }
`, "Main"},
		{"through a Shared helper", `
module Main exposing (main)

import Shared

handler = Service.implement Shared.clear (\_ -> Shared.stamp |> Task.map (\_ -> ()))

main = App.backend { services = [ handler ] }
`, "Shared"},
		{"in an Auth.authorize loader declared apart", `
module Main exposing (main)

import Shared

loadDay input user = Date.today |> Task.map Just

protected = Auth.protect Shared.clear (\_ _ -> Task.succeed ())

guarded = Auth.authorize loadDay (\input user day -> True) protected

main = App.backend { services = [ guarded ] }
`, "Main"},
	} {
		issues := RunSideCheck(parseModules(t, shared, c.main))
		if len(issues) == 0 {
			t.Errorf("%s: Date.today on the server was accepted", c.name)
			continue
		}
		m := issues[0].Message
		if !strings.Contains(m, "Date.today") || !strings.Contains(m, "no time zone of its own") {
			t.Errorf("%s: wrong message: %s", c.name, m)
		}
		if issues[0].Module != c.module {
			t.Errorf("%s: reported in %s, where it is written is %s", c.name, issues[0].Module, c.module)
		}
	}

	page := parseModules(t, `
module Main exposing (main)

init = ( 0, Cmd.perform (\d -> GotToday d) Date.today )

home =
    Page.create
        { path = "/", title = "t", init = init, update = update
        , view = view, subscriptions = always Sub.none
        }

main = App.frontend [ home ]
`)
	if issues := RunSideCheck(page); len(issues) > 0 {
		t.Fatalf("a page reading Date.today was rejected: %s", issues[0].Message)
	}
}

// sideCheckProgram type-checks the modules the way the project pipeline does,
// then runs the side check on them. A case that does not type-check would say
// nothing about the side check: what it has to agree with is how the type
// checker resolves each name.
func sideCheckProgram(t *testing.T, mods ...modSrc) []ShapeIssue {
	t.Helper()
	if err := checkProject(t, mods); err != nil {
		t.Fatalf("the program does not type-check, so it says nothing about the side check: %v", err)
	}
	var srcs []string
	for _, m := range mods {
		srcs = append(srcs, m.src)
	}
	return RunSideCheck(parseModules(t, srcs...))
}

// The service every handler below implements, and a helper whose parameter is
// named like a UI builtin.
var sharedForSides = modSrc{"Shared", `
module Shared exposing (clear, count)

import UI exposing (..)

clear : Service () ()
clear = Service.declare GET "/api/day"

count : List a -> Int
count list = List.length list
`}

// A builtin written bare, after `import M exposing (..)`, is the same builtin:
// the type checker binds the module's whole vocabulary, so the side check must
// resolve the bare name to it too. It used to bind only a user module's names,
// so every bare builtin was invisible to it: a frontend-only name in a handler,
// a backend-only one in a page, and even the builders that make a declaration
// a page or a handler, so the declaration was never checked at all.
func TestBareNamesFromABuiltinModuleKeepTheirSide(t *testing.T) {
	page := func(imports, init, builder string) string {
		return `
module Main exposing (main)
` + imports + `

type alias Task1 =
    { id : Int }


type Msg
    = Loaded (List Task1)


tasks : Entity Task1
tasks = Entity.define { name = "task", columns = { id = Entity.serial }, uniques = [] }


init : (Int, Cmd Msg)
init = ` + init + `


update : Msg -> Int -> (Int, Cmd Msg)
update _ model = ( model, Cmd.none )


view : Int -> View Msg
view _ = UI.text [] ""


home : Page
home =
    ` + builder + `
        { path = "/", title = "t", init = init, update = update
        , view = view, subscriptions = always Sub.none
        }


main : Cmd ()
main = App.frontend [ home ]
`
	}
	for _, c := range []struct{ name, src, wants string }{
		{"a frontend-only name in a handler", `
module Main exposing (main)

import Date exposing (..)
import Shared

handler : ExposedService
handler = Service.implement Shared.clear (\_ -> today |> Task.map (\_ -> ()))

main : Cmd ()
main = App.backend { services = [ handler ] }
`, "Date.today"},
		{"a handler built with a bare builder", `
module Main exposing (main)

import Service exposing (..)
import Shared

handler : ExposedService
handler = implement Shared.clear (\_ -> Date.today |> Task.map (\_ -> ()))

main : Cmd ()
main = App.backend { services = [ handler ] }
`, "Date.today"},
		{"a backend-only name in a page",
			page("\nimport Repo exposing (..)\n", "( 0, Cmd.perform Loaded (all tasks) )", "Page.create"),
			"Repo.all"},
		{"a page built with a bare builder",
			page("\nimport Page exposing (..)\n", "( 0, Cmd.perform Loaded (Repo.all tasks) )", "create"),
			"Repo.all"},
	} {
		issues := sideCheckProgram(t, sharedForSides, modSrc{"Main", c.src})
		if len(issues) == 0 {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(issues[0].Message, c.wants) {
			t.Errorf("%s: the message should name %s, got: %s", c.name, c.wants, issues[0].Message)
		}
	}

	// The other half: a bare builtin on its own side is still fine.
	ok := sideCheckProgram(t, modSrc{"Main", `
module Main exposing (main)

import Date exposing (..)


type Msg
    = GotToday Date


init : (Int, Cmd Msg)
init = ( 0, Cmd.perform GotToday today )


home : Page
home =
    Page.create
        { path = "/", title = "t", init = init, update = \_ model -> ( model, Cmd.none )
        , view = \_ -> UI.text [] "", subscriptions = always Sub.none
        }


main : Cmd ()
main = App.frontend [ home ]
`})
	if len(ok) > 0 {
		t.Fatalf("a page reading a bare today was rejected: %s", ok[0].Message)
	}
}

// A name bound around an expression, a parameter, a lambda argument, a let
// binding or a case pattern, shadows a builtin of the same name that an
// `exposing (..)` brought in, and a top-level declaration too, as it does for
// the type checker. UI alone exposes list, text, form and header; reading a
// parameter called `list` as UI.list would turn every helper that counts a
// list into a leak.
//
// A let is read in order: a binding sees the ones before it and never itself.
// `let today = today` names the today from outside the block, so the cases at
// the end are leaks.
func TestALocalNameShadowsTheBuiltinItIsNamedLike(t *testing.T) {
	handler := func(imports, decls, body string) string {
		return `
module Main exposing (main)

import Shared
` + imports + `
` + decls + `

handler : ExposedService
handler = Service.implement Shared.clear (\input -> ` + body + `)

main : Cmd ()
main = App.backend { services = [ handler ] }
`
	}
	accepted := []struct{ name, src string }{
		{"a parameter in a helper the handler calls",
			handler("", "", "Task.succeed (Shared.count [ 1 ]) |> Task.map (\\_ -> ())")},
		{"a lambda argument",
			handler("import Date exposing (..)", "", "Task.succeed () |> Task.map (\\today -> today)")},
		{"a let binding and a case pattern",
			handler("import UI exposing (..)", "", "let\n        text = \"x\"\n    in\n    case Just text of\n        Just header -> Task.succeed ()\n        Nothing -> Task.succeed ()")},
		{"a let binding that the next one reads",
			handler("import Date exposing (..)", "", "let\n        today = ()\n        later = today\n    in\n    Task.succeed later")},
		{"a parameter named like a leaking top-level declaration",
			handler("", "stamp : Task Date\nstamp = Date.today\n\nfinish : () -> Task ()\nfinish stamp = Task.succeed stamp\n", "finish ()")},
	}
	for _, c := range accepted {
		if issues := sideCheckProgram(t, sharedForSides, modSrc{"Main", c.src}); len(issues) > 0 {
			t.Errorf("%s: a local name was read as the builtin or declaration it shadows: %s",
				c.name, issues[0].Message)
		}
	}

	rejected := []struct{ name, src string }{
		{"a let binding that reads the builtin it is named like",
			handler("import Date exposing (..)", "", "let\n        today = today\n    in\n    today |> Task.map (\\_ -> ())")},
		{"a let binding read before it is bound",
			handler("import Date exposing (..)", "", "let\n        start = today\n        today = ()\n    in\n    start |> Task.map (\\_ -> today)")},
		{"a let binding that reads the declaration it is named like",
			handler("", "stamp : Task Date\nstamp = Date.today\n", "let\n        stamp = stamp\n    in\n    stamp |> Task.map (\\_ -> ())")},
	}
	for _, c := range rejected {
		issues := sideCheckProgram(t, sharedForSides, modSrc{"Main", c.src})
		if len(issues) == 0 {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if !strings.Contains(issues[0].Message, "Date.today") {
			t.Errorf("%s: the message should name Date.today, got: %s", c.name, issues[0].Message)
		}
	}
}
