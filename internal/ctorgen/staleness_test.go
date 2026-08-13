package ctorgen

import (
	"os"
	"strings"
	"testing"
)

// TestGeneratedRegistrationsAreCurrent is the lock on the whole scheme: it
// regenerates both outputs in memory and fails if the committed files
// disagree. This is what makes "add a constructor in typecheck and forget a
// runtime" impossible to merge: the exact drift class that regex-based
// coverage tests missed for qualified-only constructors (146 names were
// invisible to them when this landed).
func TestGeneratedRegistrationsAreCurrent(t *testing.T) {
	js, err := os.ReadFile("../jsserve/runtime.js")
	if err != nil {
		t.Fatalf("reading runtime.js: %v", err)
	}
	spliced, err := SpliceJS(string(js))
	if err != nil {
		t.Fatal(err)
	}
	if spliced != string(js) {
		t.Fatalf("the generated ctor region in internal/jsserve/runtime.js is stale.\nFix: go generate ./internal/ctorgen")
	}

	swift, err := os.ReadFile("../iosbundle/template/Sources/MarBuiltinCtors.swift")
	if err != nil {
		t.Fatalf("reading MarBuiltinCtors.swift (run `go generate ./internal/ctorgen` if it does not exist): %v", err)
	}
	if string(swift) != SwiftFile() {
		t.Fatalf("internal/iosbundle/template/Sources/MarBuiltinCtors.swift is stale.\nFix: go generate ./internal/ctorgen")
	}

	goSrc, err := os.ReadFile("../runtime/builtin_ctors_gen.go")
	if err != nil {
		t.Fatalf("reading builtin_ctors_gen.go (run `go generate ./internal/ctorgen` if it does not exist): %v", err)
	}
	if string(goSrc) != GoFile() {
		t.Fatalf("internal/runtime/builtin_ctors_gen.go is stale.\nFix: go generate ./internal/ctorgen")
	}
}

// TestEntriesShape pins the invariants the emitters rely on: a healthy count,
// no bare-name leaks, and the arities of the few n-ary constructors.
func TestEntriesShape(t *testing.T) {
	entries := Entries()
	if len(entries) < 130 {
		t.Fatalf("suspiciously few generated ctors: %d (Keyboard alone has ~100)", len(entries))
	}
	arity := map[string]int{}
	for _, e := range entries {
		if !strings.Contains(e.Qualified, ".") {
			t.Fatalf("entry %q is not module-qualified", e.Qualified)
		}
		arity[e.Qualified] = e.Arity
	}
	for name, want := range map[string]int{
		"Canvas.Translate":    2,
		"Canvas.Blend":        1,
		"Canvas.Erase":        0,
		"Service.ServerError": 1,
		"Auth.SignedIn":       1,
		"Keyboard.ArrowLeft":  0,
		"Gamepad.A":           0,
		"Sound.Square":        0,
	} {
		got, ok := arity[name]
		if !ok {
			t.Fatalf("expected generated entry %q, not found", name)
		}
		if got != want {
			t.Fatalf("%s: arity %d, want %d", name, got, want)
		}
	}
}
