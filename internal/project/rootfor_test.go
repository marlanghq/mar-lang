package project_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/project"
)

// The two answers this function replaced disagreed on exactly one input, and
// it is a shape people actually have: a directory holding Main.mar and no
// mar.json. Measured before the merge, on two pages declared on `/`:
//
//	mar check .          refused    (project mode needs no manifest)
//	the editor           refused    (Main.mar was a marker there)
//	mar check Main.mar   OK         (mar.json only)
//
// So the editor and the CLI disagreed about the same file, which is the thing
// the CLI fix had just set out to end.
func TestTheEditorAndTheCLIAgreeOnWhereAProjectStarts(t *testing.T) {
	t.Run("a manifest marks a project", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "mar.json"), `{"name":"p","locale":"en"}`)
		write(t, filepath.Join(dir, "Main.mar"), "module Main exposing (..)\n")
		assertRoot(t, filepath.Join(dir, "Main.mar"), dir)
	})

	// The case the two implementations disagreed about.
	t.Run("Main.mar alone marks a project", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "Main.mar"), "module Main exposing (..)\n")
		assertRoot(t, filepath.Join(dir, "Main.mar"), dir)
	})

	t.Run("a file in a subdirectory belongs to the project above", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "mar.json"), `{"name":"p","locale":"en"}`)
		sub := filepath.Join(dir, "Backend")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(sub, "Data.mar"), "module Backend.Data exposing (..)\n")
		assertRoot(t, filepath.Join(sub, "Data.mar"), dir)
	})

	// Deeper than the twenty levels the editor's version stopped at. Such a
	// file used to get no project and therefore single-file diagnostics, which
	// is the false-error mode this whole thread is about.
	t.Run("depth does not end the search", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "mar.json"), `{"name":"p","locale":"en"}`)
		deep := dir
		for i := 0; i < 25; i++ {
			deep = filepath.Join(deep, "d")
		}
		if err := os.MkdirAll(deep, 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(deep, "Deep.mar"), "module Deep exposing (..)\n")
		assertRoot(t, filepath.Join(deep, "Deep.mar"), dir)
	})

	t.Run("a file outside any project has none", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "Solo.mar"), "module Solo exposing (..)\n")
		if root, ok := project.RootFor(filepath.Join(dir, "Solo.mar")); ok {
			t.Errorf("a lone file reported the project %s", root)
		}
	})

	// A directory argument answers for itself rather than for its parent.
	t.Run("a directory is its own root", func(t *testing.T) {
		dir := t.TempDir()
		write(t, filepath.Join(dir, "mar.json"), `{"name":"p","locale":"en"}`)
		assertRoot(t, dir, dir)
	})
}

// And the reason the merge is worth keeping: a second walk drifts from the
// first, which is what happened here within the hour.
func TestNobodyKeepsATheirOwnCopyOfTheWalk(t *testing.T) {
	for _, f := range []string{"../lsp/server.go", "../../cmd/mar/main.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		body := string(b)
		for _, marker := range []string{`Join(dir, "mar.json")`, `Join(dir, "Main.mar")`} {
			if strings.Contains(body, marker) {
				t.Errorf("%s walks for a project root itself (%s).\n"+
					"    Call project.RootFor: two walks answered differently once already,\n"+
					"    and the editor and the CLI disagreed about the same file.",
					filepath.Base(f), marker)
			}
		}
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertRoot(t *testing.T, file, want string) {
	t.Helper()
	got, ok := project.RootFor(file)
	if !ok {
		t.Fatalf("%s was not recognised as part of a project", file)
	}
	rg, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatal(err)
	}
	rw, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatal(err)
	}
	if rg != rw {
		t.Errorf("found %s, want %s", rg, rw)
	}
}
