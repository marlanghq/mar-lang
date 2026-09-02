package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A program's entry point produces no value: `Cmd ()` says the runtime performs
// it and there is nothing to hand back. `main : Cmd Int` type-checks as an
// ordinary value and then has nowhere to go.
//
// The check existed, in `mar dev` alone, with no test. Measured before this
// moved: `mar check .` said OK, and `mar build .` refused for a different
// reason — "main didn't call any of App.frontend / App.backend / App.fullstack
// — nothing to build", which is true and is not the problem the reader has.
// That is the sixth time in this work a check turned out to be live on one
// command and absent on another.
//
// It is a fact about a TYPE, so it now sits on both load paths and waits for
// nothing.
func TestMainHasToBeACmdOfUnit(t *testing.T) {
	project := func(t *testing.T, mainDecl string) string {
		t.Helper()
		dir := t.TempDir()
		write := func(name, body string) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("mar.json", `{"name":"m","locale":"en"}`)
		write("Main.mar", `module Main exposing (main)


`+mainDecl+`
`)
		return dir
	}

	refused := []struct{ name, decl, want string }{
		{"a Cmd of something else", `main : Cmd Int
main =
    Cmd.none
`, "expected `Cmd ()`"},
		{"not a Cmd at all", `main : Int
main =
    1
`, "expected `Cmd ()`"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(project(t, tc.decl))
			if err == nil {
				t.Fatal("this loaded; the entry point produces a value with nowhere to go")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refused for the wrong reason: %v", err)
			}
		})
	}

	t.Run("a real entry point still loads", func(t *testing.T) {
		if _, err := Load(project(t, `main : Cmd ()
main =
    App.backend { services = [] }
`)); err != nil {
			t.Fatalf("this is what every program in the repo writes: %v", err)
		}
	})

	// A directory of modules that is not an entry point is not broken.
	t.Run("no main is not an error", func(t *testing.T) {
		dir := t.TempDir()
		for name, body := range map[string]string{
			"mar.json": `{"name":"m","locale":"en"}`,
			"Main.mar": "module Main exposing (greeting)\n\n\ngreeting : String\ngreeting =\n    \"hi\"\n",
		} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := Load(dir); err != nil {
			t.Fatalf("a module without `main` is not a broken program: %v", err)
		}
	})
}
