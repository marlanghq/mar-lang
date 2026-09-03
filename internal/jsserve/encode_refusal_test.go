package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/typecheck"
)

// The browser's half of the refusal the phone got two commits ago, found by
// asking whether that fix had a twin here. It did:
//
//	JSON.encode { f = \x -> x + 1 }   ->   {"f":null}
//
// marToJs ended in `default: return null`, so anything it had no arm for went
// out as null and a receiver read "the field was null" when the field could
// not be written at all.
//
// Wrapped in a record on purpose: the shape lint refuses these passed directly
// to JSON.encode, so the direct call never reaches a runtime. This is the case
// the lint cannot see.
func TestTheBrowserRefusesToEncodeAValueWithNoJSONForm(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}

	refused := []struct{ name, decl, want string }{
		{"a function", `JSON.encode { f = \x -> x + 1 }`, "a function has no JSON form"},
		{"an unresolved division", `JSON.encode { q = 1.0 / 3.0 }`, "cannot encode a Decimal.Division"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			got := encodeInBrowser(t, node, c.decl)
			if !strings.HasPrefix(got, "THREW: ") {
				t.Fatalf("the browser encoded %s instead of refusing it: %s\n\n"+
					"A null on the wire where a value belongs is a wrong answer, not an error,\n"+
					"and the receiver has no way to tell.", c.name, got)
			}
			if !strings.Contains(got, c.want) {
				t.Errorf("refused, but not in the words the other two use:\n  want: %q\n  got:  %s", c.want, got)
			}
		})
	}

	// The half that matters more when a `default` arm changes: nothing
	// legitimate falls into it. A default that refuses too much breaks a
	// program that works, which is the one thing the goal forbids outright.
	t.Run("every kind that does encode still encodes", func(t *testing.T) {
		got := encodeInBrowser(t, node,
			`JSON.encode { a = 1, b = "x", c = True, d = (), e = [ 1, 2 ], `+
				`f = ( 1, "y" ), g = Just 3, h = Nothing, i = 1.50, j = 'z' }`)
		if strings.HasPrefix(got, "THREW: ") {
			t.Fatalf("a record of ordinary values was refused: %s", got)
		}
		for _, want := range []string{`"a":1`, `"b":"x"`, `"c":true`, `"d":null`,
			`"e":[1,2]`, `"__ctor":"Just"`, `"__dec":"1.50"`, `"__char":"z"`} {
			if !strings.Contains(got, want) {
				t.Errorf("the encoding lost %s:\n  %s", want, got)
			}
		}
	})
}

func encodeInBrowser(t *testing.T, node, expr string) string {
	t.Helper()
	src := "module Main exposing (..)\n\n\nwrapped : String\nwrapped =\n    " + expr + "\n"
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check: %v", err)
	}
	dir := t.TempDir()
	rt, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	pj, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}
	write := func(n string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("runtime.js", rt)
	write("program.json", pj)
	write("driver.js", []byte(`
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
try { process.stdout.write(String(globalThis.__marEvalValue(program, 'Main.wrapped'))); }
catch (e) { process.stdout.write('THREW: ' + (e && e.message || e)); }
`))
	out, err := exec.Command(node, filepath.Join(dir, "driver.js"),
		filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json")).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	// __marEvalValue hands back the value the way Mar DISPLAYS it, so a String
	// arrives quoted with its inner quotes escaped. Comparing that against what
	// the encoder produced reports every field as missing. Same trap as
	// decode_shape_parity_test.go, and I walked into it again writing this
	// file, which is why the note is here and not only there.
	got := strings.TrimSpace(string(out))
	if unquoted, uerr := strconv.Unquote(got); uerr == nil {
		return unquoted
	}
	return got
}
