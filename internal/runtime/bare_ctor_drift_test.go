package runtime_test

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode"

	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// The drift family had a hole one level over from the one just closed.
//
// The three runtimes' drift tests iterate the QUALIFIED symbol table, so a
// name written without a module was checked by nothing: `Just`, `Nothing`,
// `True`, `GET`, `EQ`, `Coarse`, `Pixelated`. A constructor missing from a
// client is the exact failure that has bitten here before -- unregistered
// qualified ctors once killed the keyboard in canvas games -- and these were
// the ones nobody was asking about.
//
// Measured when this was written: all three carry all eighteen. Nothing was
// missing; nothing was checking either.
func TestEveryBareConstructorExistsInAllThreeRuntimes(t *testing.T) {
	var ctors []string
	for name := range typecheck.AllBareSymbols() {
		if name == "" || !unicode.IsUpper(rune(name[0])) {
			continue
		}
		ctors = append(ctors, name)
	}
	sort.Strings(ctors)
	if len(ctors) < 15 {
		t.Fatalf("only %d bare constructors found; the filter is wrong, not the language", len(ctors))
	}

	env := runtime.BaseEnv()
	var missingGo []string
	for _, n := range ctors {
		if _, ok := env.Lookup(n); !ok {
			missingGo = append(missingGo, n)
		}
	}
	if len(missingGo) > 0 {
		t.Errorf("the server cannot resolve %d bare constructor(s): %s",
			len(missingGo), strings.Join(missingGo, " "))
	}

	js, err := os.ReadFile(filepath.Join("..", "jsserve", "runtime.js"))
	if err != nil {
		t.Fatal(err)
	}
	var missingJS []string
	for _, n := range ctors {
		// The browser registers a bare ctor by name, in either quote style.
		if !strings.Contains(string(js), "'"+n+"'") && !strings.Contains(string(js), `"`+n+`"`) {
			missingJS = append(missingJS, n)
		}
	}
	if len(missingJS) > 0 {
		t.Errorf("runtime.js never names %d bare constructor(s): %s\n"+
			"A program using one loads and dies with `unbound name`.",
			len(missingJS), strings.Join(missingJS, " "))
	}

	swiftFiles, err := filepath.Glob(filepath.Join("..", "iosbundle", "template", "Sources", "*.swift"))
	if err != nil || len(swiftFiles) == 0 {
		t.Fatalf("no Swift sources: %v", err)
	}
	var swift strings.Builder
	for _, f := range swiftFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		swift.Write(b)
	}
	var missingSwift []string
	for _, n := range ctors {
		// env.define("Name", ...) is how the phone registers one. Matching the
		// define, not the bare string, so a mention in a COMMENT does not pass
		// for a registration -- measured, four of these appear only in prose
		// in the files a narrower scan looked at.
		if !strings.Contains(swift.String(), `define("`+n+`"`) {
			missingSwift = append(missingSwift, n)
		}
	}
	if len(missingSwift) > 0 {
		t.Errorf("the phone never defines %d bare constructor(s): %s",
			len(missingSwift), strings.Join(missingSwift, " "))
	}
	if !t.Failed() {
		t.Logf("%d bare constructors, all three runtimes carry every one", len(ctors))
	}
}
