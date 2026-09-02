package apphost

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/jsserve"
	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// A save that the checks refuse must not take the running app down with it.
// `mar dev` recompiles on every keystroke-ish save, so half the saves in a
// session are momentarily wrong; if a refused one replaced the served program,
// the browser would go blank while you type.
//
// Measured end to end first: with the server up, saving a second page on `/`
// prints the same refusal a cold boot gives, `/` keeps answering 200, and the
// served program.json still has ONE page. A later good save recovers.
//
// The mechanism is the order — the builtin runs the checks and returns before
// it reaches `LiveProgram.Update` — and order is the kind of thing a refactor
// reverses without noticing, so it is asserted rather than read.
func TestARefusedReloadKeepsTheProgramThatWasWorking(t *testing.T) {
	runtime.ResetForReload()
	var notes bytes.Buffer
	routeNoteWriter = &notes
	t.Cleanup(func() { routeNoteWriter = os.Stderr })

	lp := &jsserve.LiveProgram{}
	mods := []*ast.Module{elaboratedModule(t)}
	fn := MakeFrontendBuiltin(mods, 0, lp).(runtime.VFn)

	// A good program first: this is the one that has to survive.
	good := runtime.VList{Elements: []runtime.Value{boundPage("/", "home")}}
	if _, err := fn.Native([]runtime.Value{good}); err != nil {
		t.Fatalf("one page on `/` is an ordinary app: %v", err)
	}
	served := lp.ProgramJSON()
	if len(served) == 0 {
		t.Fatal("the good program was never served, so this test proves nothing")
	}

	// Now the save that cannot work.
	_, err := fn.Native([]runtime.Value{runtime.VList{Elements: []runtime.Value{boundPage("/", "home"), boundPage("/", "twin")}}})
	if err == nil {
		t.Fatal("two pages on `/` should have been refused")
	}
	if !strings.Contains(err.Error(), "two pages are declared on `/`") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}

	if got := lp.ProgramJSON(); !bytes.Equal(got, served) {
		t.Error("the refused save replaced the program being served; the running app would break on a bad keystroke")
	}

	// And a good save after it still takes effect, or the server would be
	// stuck on the last good program forever.
	if _, err := fn.Native([]runtime.Value{runtime.VList{Elements: []runtime.Value{boundPage("/", "home"), boundPage("/twin", "twin")}}}); err != nil {
		t.Fatalf("two pages on two paths is an ordinary app: %v", err)
	}
	if bytes.Equal(lp.ProgramJSON(), served) {
		t.Error("a good save after a refused one did not take effect")
	}
}

// A page the module walk accepts as real: a top-level binding, which is what
// gives it provenance. `pageValues` in this package leaves that off, and the
// tests that use it stop at the route table, before the walk asks.
func boundPage(path, name string) runtime.VPage {
	return runtime.VPage{Path: path, Title: name, OriginModule: "Main", OriginName: name}
}

// The serializer refuses a tree the typechecker has not finished with
// (ADR 0017), so the fixture has to be a real checked module rather than an
// `ast.Module` with a name on it.
func elaboratedModule(t *testing.T) *ast.Module {
	t.Helper()
	mod, err := parser.Parse("module Main exposing (..)\n\n\ngreeting : String\ngreeting =\n    \"hi\"\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check: %v", err)
	}
	return mod
}
