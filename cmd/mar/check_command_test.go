package main

import (
	"os"
	"path/filepath"
	"testing"
)

// `mar check` is the command CI runs, and its own body had no test: the
// end-to-end coverage came from `make check-examples`, which only ever hands
// it programs that pass.
//
// What the body decides is worth pinning: a project, a single file, a
// directory that is not one, and — since this work moved them here — the boot
// checks. The return code is the whole interface a CI job sees.
func TestCheckCommandReturnCodes(t *testing.T) {
	project := func(t *testing.T, main string) string {
		t.Helper()
		dir := t.TempDir()
		for name, body := range map[string]string{
			"mar.json": `{"name":"c","locale":"en"}`,
			"Main.mar": main,
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return dir
	}

	const sound = `module Main exposing (main)


main : Cmd ()
main =
    Cmd.none
`

	t.Run("a sound project", func(t *testing.T) {
		if code := runCheck(project(t, sound)); code != 0 {
			t.Fatalf("exit %d on a project that is fine", code)
		}
	})

	t.Run("a file on its own", func(t *testing.T) {
		dir := project(t, sound)
		if code := runCheck(filepath.Join(dir, "Main.mar")); code != 0 {
			t.Fatalf("exit %d on a module that type-checks", code)
		}
	})

	t.Run("a type error", func(t *testing.T) {
		if code := runCheck(project(t, "module Main exposing (main)\n\n\nmain : Cmd ()\nmain =\n    1 + \"x\"\n")); code == 0 {
			t.Fatal("a type error has to be a non-zero exit, or CI goes green on it")
		}
	})

	// The boot checks, which `mar check` did not run until this work moved
	// them here. This is the case that made CI greener than the deploy.
	t.Run("a boot-level refusal", func(t *testing.T) {
		code := runCheck(project(t, `module Main exposing (main)


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


exposed : ExposedService
exposed =
    Service.implement listNotes listImpl


main : Cmd ()
main =
    Cmd.none
`))
		if code == 0 {
			t.Fatal("a GET that writes passed `mar check`; CI would be greener than the deploy")
		}
	})

	t.Run("a path that is not there", func(t *testing.T) {
		if code := runCheck(filepath.Join(t.TempDir(), "nope")); code == 0 {
			t.Fatal("checking a path that does not exist is not a success")
		}
	})
}
