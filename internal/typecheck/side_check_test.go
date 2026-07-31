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
// shape, and a module-level version of this check rejected it — which is why
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

// No pages, no roots, nothing to say — a backend-only project must not trip
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
