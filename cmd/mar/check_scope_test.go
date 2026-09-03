package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `mar check` gave two different answers for the same project depending on how
// the argument was spelled, and both spellings of the wrong one were wrong in
// a different direction. Measured in examples/notes-auth before the fix:
//
//	mar check Main.mar   Type error: unknown qualified name: Backend.users
//	mar check .          project .: OK (4 modules)
//
// The file mode parses ONE module, so a name from a sibling reads as unknown
// and the tool reports an error the project does not have. It also never
// evaluates `main`, so the boot checks never run: two pages declared on `/`
// passed the file mode and were refused by the project mode.
//
// A false error and a false OK from one command, decided by the argument. The
// file now selects the project it belongs to.
func TestCheckingAFileInAProjectChecksTheProject(t *testing.T) {
	// A file with no mar.json above it is still a lone module -- that mode has
	// a real audience, and this is the half that a careless fix would break.
	t.Run("a file outside any project is still checked alone", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "Solo.mar"), `module Solo exposing (..)


double : Int -> Int
double n =
    n * 2
`)
		if root, ok := projectRootFor(filepath.Join(dir, "Solo.mar")); ok {
			t.Fatalf("found a project at %s for a file that is not in one", root)
		}
	})

	t.Run("a file beside a mar.json belongs to it", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "mar.json"), `{"name":"p","locale":"en"}`)
		writeFile(t, filepath.Join(dir, "Main.mar"), "module Main exposing (..)\n")
		root, ok := projectRootFor(filepath.Join(dir, "Main.mar"))
		if !ok {
			t.Fatal("a file beside a mar.json was not recognised as part of a project")
		}
		if !sameDir(t, root, dir) {
			t.Errorf("found %s, want %s", root, dir)
		}
	})

	// Nested source directories are the normal shape -- Backend/, Frontend/ --
	// so the walk has to go up rather than look in one place.
	t.Run("a file in a subdirectory belongs to the project above it", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "mar.json"), `{"name":"p","locale":"en"}`)
		sub := filepath.Join(dir, "Backend")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(sub, "Data.mar"), "module Backend.Data exposing (..)\n")
		root, ok := projectRootFor(filepath.Join(sub, "Data.mar"))
		if !ok {
			t.Fatal("a file under a project's directory was not recognised as part of it")
		}
		if !sameDir(t, root, dir) {
			t.Errorf("found %s, want %s", root, dir)
		}
	})

	// The walk must terminate. A bug here is an infinite loop rather than a
	// wrong answer, so it is worth its own case.
	t.Run("the walk stops at the filesystem root", func(t *testing.T) {
		done := make(chan bool, 1)
		go func() {
			_, ok := projectRootFor(filepath.Join(t.TempDir(), "None.mar"))
			done <- ok
		}()
		if <-done {
			t.Error("a temp dir with no mar.json reported a project")
		}
	})
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		t.Fatal(err)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		t.Fatal(err)
	}
	return strings.EqualFold(ra, rb)
}
