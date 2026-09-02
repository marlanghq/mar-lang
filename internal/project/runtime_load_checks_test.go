package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `LoadIntoEnvForRuntime` is the DEPLOYED binary's loader, and its own comment
// said the project-wide passes were unnecessary because "the build pipeline
// already did" them. That holds for a bundle this binary's own CLI produced.
// The stub is pre-compiled and shipped, though, so the bundle and the binary
// running it are two versions that need not match.
//
// Measured on the shipped stub before this: the route-table refusal WAS in it
// (the App.* builtins carry that one, and the stub runs `main`), and
// `must be read-only` and `expected Cmd ()` were not. The path that skipped
// them was the deployed one — the seventh time in this work a check turned out
// to be live somewhere and absent somewhere else.
func TestTheDeployedLoaderRunsTheProjectWideChecks(t *testing.T) {
	load := func(t *testing.T, src string) error {
		t.Helper()
		dir := t.TempDir()
		entry := filepath.Join(dir, "Main.mar")
		if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := LoadIntoEnvForRuntime(entry, nil)
		return err
	}

	t.Run("a GET that writes", func(t *testing.T) {
		err := load(t, `module Main exposing (main)


notes : Entity { id : Int, body : String }
notes =
    Entity.define
        { name = "notes"
        , columns = { id = Entity.serial, body = Entity.text Entity.notNull }
        , uniques = []
        }


listNotes : Service () (List Int)
listNotes =
    Service.declare GET "/api/notes"


listImpl : () -> Task (List Int)
listImpl _ =
    Task.andThen (\_ -> Task.succeed []) (Repo.deleteById notes 1)


main : Cmd ()
main =
    App.backend { services = [ Service.implement listNotes listImpl ] }
`)
		if err == nil {
			t.Fatal("the deployed binary would serve a GET that changes data")
		}
		if !strings.Contains(err.Error(), "must be read-only") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	})

	t.Run("a main that is not a Cmd ()", func(t *testing.T) {
		err := load(t, "module Main exposing (main)\n\n\nmain : Int\nmain =\n    1\n")
		if err == nil {
			t.Fatal("the entry point produces a value with nowhere to go")
		}
		if !strings.Contains(err.Error(), "expected `Cmd ()`") {
			t.Fatalf("refused for the wrong reason: %v", err)
		}
	})

	// A GET that only reads has to keep loading, or the check is a wall.
	// `App.backend` is left out on purpose: this loader installs the real
	// topology builtins from the CLI, and a nil `installBuiltins` gets the
	// stub that refuses. The service and its handler are still top-level
	// bindings, which is what the GET pass reads.
	t.Run("an ordinary read-only service still loads", func(t *testing.T) {
		if err := load(t, `module Main exposing (main)


notes : Entity { id : Int, body : String }
notes =
    Entity.define
        { name = "notes"
        , columns = { id = Entity.serial, body = Entity.text Entity.notNull }
        , uniques = []
        }


listNotes : Service () (List Int)
listNotes =
    Service.declare GET "/api/notes"


listImpl : () -> Task (List Int)
listImpl _ =
    Task.map (\_ -> []) (Repo.all notes)


exposed : ExposedService
exposed =
    Service.implement listNotes listImpl


main : Cmd ()
main =
    Cmd.none
`); err != nil {
			t.Fatalf("this GET only reads: %v", err)
		}
	})
}
