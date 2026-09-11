package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// `mar/check` is the question a per-document analysis cannot answer: does the
// whole project compile. The errors that matter most in a Mar project are the
// ones no single file holds -- a name a sibling does not export, two pages on
// one path -- and the second kind needs `main` EVALUATED, because the page
// list is a value the program builds.
//
// It exists for a caller that cannot read squiggles: a language model writing
// Mar in the editor. mar_reference stops it inventing a stdlib; this is what
// stops it believing it got the rest right.
func TestCheckAnswersForTheWholeProject(t *testing.T) {
	t.Run("a project that compiles says so, and counts its modules", func(t *testing.T) {
		res := (&Server{}).checkProject(writeProject(t, okProject))
		if !res.OK {
			t.Fatalf("a clean project reported errors: %+v", res.Errors)
		}
		if res.Modules < 2 {
			t.Errorf("counted %d modules, expected the two written", res.Modules)
		}
	})

	// The error the whole feature is for: it is in no file, and it only exists
	// once `main` has run.
	t.Run("two pages on one path is a boot error", func(t *testing.T) {
		dir := writeProject(t, okProject)
		replaceIn(t, filepath.Join(dir, "Main.mar"), "[ page ]", "[ page, page ]")
		res := (&Server{}).checkProject(dir)
		if res.OK {
			t.Fatal("two pages on `/` passed")
		}
		e := res.Errors[0]
		if e.Stage != "boot" {
			t.Errorf("stage is %q, and this error only exists after main runs", e.Stage)
		}
		if !strings.Contains(e.Message, "two pages are declared on `/`") {
			t.Errorf("message is not the CLI's: %q", e.Message)
		}
	})

	// A type error carries a place, and the place has to be the right FILE.
	// An earlier draft filled in Main.mar whenever there was a line number,
	// which is right for a one-module project and a lie for every other.
	t.Run("a type error carries file, line and column", func(t *testing.T) {
		dir := writeProject(t, okProject)
		replaceIn(t, filepath.Join(dir, "Main.mar"), "t.title", "t.nome")
		res := (&Server{}).checkProject(dir)
		if res.OK {
			t.Fatal("a field that does not exist passed")
		}
		e := res.Errors[0]
		if e.Stage != "typecheck" {
			t.Errorf("stage is %q", e.Stage)
		}
		if filepath.Base(e.File) != "Main.mar" {
			t.Errorf("file is %q, and the error is in Main.mar", e.File)
		}
		if e.Line == 0 {
			t.Error("no line, so an editor cannot jump to it")
		}
	})

	t.Run("a path outside any project says that, rather than failing to load", func(t *testing.T) {
		res := (&Server{}).checkProject(t.TempDir())
		if res.OK {
			t.Fatal("a directory with no project reported clean")
		}
		if !strings.Contains(res.Errors[0].Message, "not inside a Mar project") {
			t.Errorf("unhelpful message: %q", res.Errors[0].Message)
		}
	})
}

// The reason this is a request answered from a goroutine rather than a line in
// analyze(): the message loop is single-threaded, and a check that took the
// loop with it would freeze hover and completion for its whole duration.
//
// Checking a project evaluates its top level, which on the biggest example
// here is about a second and a half. This asserts the loop stays available
// while one is running.
func TestACheckDoesNotFreezeTheEditor(t *testing.T) {
	dir := writeProject(t, okProject)

	var out lockedBuffer
	s := &Server{
		in:   bufio.NewReader(strings.NewReader("")),
		out:  &out,
		docs: map[string]string{},
		idx:  map[string]*DocIndex{},
	}

	// Hold the evaluation lock, so the check cannot finish: it stands in for a
	// slow project without needing one.
	s.evalMu.Lock()

	// Bounded, because the failure mode being tested is a HANG: run the check
	// inline instead of in a goroutine and handle blocks here forever on the
	// lock. A test that hangs tells CI nothing and a reader less; this one
	// says what went wrong.
	accepted := make(chan struct{})
	go func() {
		s.handle(decode(t, `{"jsonrpc":"2.0","id":1,"method":"mar/check","params":{"root":"`+
			filepath.ToSlash(dir)+`"}}`))
		close(accepted)
	}()
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		s.evalMu.Unlock()
		t.Fatal("mar/check did not return from handle: it is running INLINE, " +
			"so the message loop is blocked for as long as the check takes")
	}

	// And the loop must be free afterwards.
	done := make(chan struct{})
	go func() {
		s.handle(decode(t, `{"jsonrpc":"2.0","id":2,"method":"mar/describe","params":{"name":"List.map"}}`))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		s.evalMu.Unlock()
		t.Fatal("a second request could not be served while a check was running: " +
			"the check is taking the message loop with it")
	}
	if !strings.Contains(out.String(), "List.map") {
		t.Error("the second request was accepted and not answered")
	}
	s.evalMu.Unlock()
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func decode(t *testing.T, raw string) *message {
	t.Helper()
	var m message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatal(err)
	}
	return &m
}

const okProject = `Main.mar
module Main exposing (main)

import Shared


type Msg
    = Noop


init : ( List Shared.Todo, Cmd Msg )
init =
    ( [], Cmd.none )


update : Msg -> List Shared.Todo -> ( List Shared.Todo, Cmd Msg )
update _ m =
    ( m, Cmd.none )


view : List Shared.Todo -> View Msg
view tasks =
    UI.text [] (String.join ", " (List.map (\t -> t.title) tasks))


subscriptions : List Shared.Todo -> Sub Msg
subscriptions _ =
    Sub.none


page : Page
page =
    Page.create
        { path = "/"
        , title = "Demo"
        , init = init
        , update = update
        , view = view
        , subscriptions = subscriptions
        }


main : Cmd ()
main =
    App.frontend [ page ]
===
Shared.mar
module Shared exposing (Todo)


type alias Todo =
    { id : Int, title : String }
===
mar.json
{"name":"demo","locale":"en"}
`

func writeProject(t *testing.T, spec string) string {
	t.Helper()
	dir := t.TempDir()
	for _, part := range strings.Split(spec, "===\n") {
		part = strings.TrimPrefix(part, "\n")
		nl := strings.Index(part, "\n")
		if nl < 0 {
			continue
		}
		name, body := strings.TrimSpace(part[:nl]), part[nl+1:]
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func replaceIn(t *testing.T, path, old, new string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, old) {
		t.Fatalf("%s does not contain %q", filepath.Base(path), old)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(s, old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}
