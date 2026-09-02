package typecheck

import (
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
)

// A GET endpoint is safe by HTTP contract: it must not change data. Mar holds
// every GET handler to that statically, over the whole call graph, so a write
// hidden behind two helpers is still caught.
//
// The pass had no test at all. It is wired into both load paths and it works —
// but a safety check whose correctness rests on nothing is one refactor away
// from passing everything, and it would pass quietly: a GET that writes looks
// exactly like a GET that does not until someone replays a request.
//
// The bias is deliberate and worth pinning too: the call graph is keyed by
// simple name, so the pass may over-report on a cross-module collision and can
// never miss a real write. Tests below cover both directions.
func TestAGetHandlerCannotReachAWrite(t *testing.T) {
	const entity = `
notes : Entity { id : Int, body : String }
notes =
    Entity.define
        { name = "notes"
        , columns = { id = Entity.serial, body = Entity.text Entity.notNull }
        , uniques = []
        }
`
	mod := func(verb, body string) string {
		return "module Main exposing (..)\n" + entity + `

listNotes : Service () (List Int)
listNotes =
    Service.declare ` + verb + ` "/api/notes"

` + body + `

exposed : ExposedService
exposed =
    Service.implement listNotes listImpl
`
	}

	refused := []struct{ name, verb, body string }{
		{"a write in the handler itself", "GET", `
listImpl : () -> Task (List Int)
listImpl _ =
    Task.andThen (\_ -> Task.succeed []) (Task.map (\_ -> ()) (Repo.create notes { body = "x" }))`},

		{"a write two helpers deep", "GET", `
deep : String -> Task ()
deep b =
    Task.map (\_ -> ()) (Repo.create notes { body = b })


mid : String -> Task ()
mid b =
    deep b


listImpl : () -> Task (List Int)
listImpl _ =
    Task.andThen (\_ -> Task.succeed []) (mid "x")`},

		{"a writer passed as a function value", "GET", `
runIt : (String -> Task ()) -> Task ()
runIt f =
    f "x"


writer : String -> Task ()
writer b =
    Task.map (\_ -> ()) (Repo.create notes { body = b })


listImpl : () -> Task (List Int)
listImpl _ =
    Task.andThen (\_ -> Task.succeed []) (runIt writer)`},

		{"a delete rather than a create", "GET", `
listImpl : () -> Task (List Int)
listImpl _ =
    Task.andThen (\_ -> Task.succeed []) (Repo.deleteById notes 1)`},
	}

	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			issues := getReadOnlyIssues(t, mod(tc.verb, tc.body))
			if len(issues) == 0 {
				t.Fatal("a GET that changes data passed; the endpoint is unsafe and nothing says so")
			}
			if !strings.Contains(issues[0].Message, "must be read-only") {
				t.Fatalf("reported the wrong thing: %s", issues[0].Message)
			}
		})
	}

	accepted := []struct{ name, verb, body string }{
		{"the same write under POST", "POST", `
listImpl : () -> Task (List Int)
listImpl _ =
    Task.andThen (\_ -> Task.succeed []) (Task.map (\_ -> ()) (Repo.create notes { body = "x" }))`},

		{"a GET that only reads", "GET", `
listImpl : () -> Task (List Int)
listImpl _ =
    Task.map (\_ -> []) (Repo.all notes)`},

		{"a GET that calls a read-only helper", "GET", `
count : Task Int
count =
    Task.map (\_ -> 0) (Repo.all notes)


listImpl : () -> Task (List Int)
listImpl _ =
    Task.andThen (\_ -> Task.succeed []) count`},
	}

	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			if issues := getReadOnlyIssues(t, mod(tc.verb, tc.body)); len(issues) > 0 {
				t.Fatalf("this endpoint changes nothing under GET: %s", issues[0].Message)
			}
		})
	}
}

// The pass is project-wide: the write can live in a module the handler's own
// module imports, and the summary is a fixpoint over all of them.
func TestAGetHandlerCannotReachAWriteInAnotherModule(t *testing.T) {
	writer := `module Backend.Notes exposing (..)


notes : Entity { id : Int, body : String }
notes =
    Entity.define
        { name = "notes"
        , columns = { id = Entity.serial, body = Entity.text Entity.notNull }
        , uniques = []
        }


save : String -> Task ()
save b =
    Task.map (\_ -> ()) (Repo.create notes { body = b })
`
	main := `module Main exposing (..)


import Backend.Notes


listNotes : Service () (List Int)
listNotes =
    Service.declare GET "/api/notes"


listImpl : () -> Task (List Int)
listImpl _ =
    Task.andThen (\_ -> Task.succeed []) (Backend.Notes.save "x")


exposed : ExposedService
exposed =
    Service.implement listNotes listImpl
`
	issues := getReadOnlyIssues(t, writer, main)
	if len(issues) == 0 {
		t.Fatal("the write is one module away, and a GET that changes data is unsafe wherever the write lives")
	}
}

func getReadOnlyIssues(t *testing.T, sources ...string) []ShapeIssue {
	t.Helper()
	mods := make([]*ast.Module, 0, len(sources))
	for _, src := range sources {
		m, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		mods = append(mods, m)
	}
	return RunGetReadOnlyCheck(mods)
}
