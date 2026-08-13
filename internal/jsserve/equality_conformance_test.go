package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// Equality is implemented once per runtime: equalValues (Go), eqValues
// (runtime.js), equalsMar (Swift): as a switch over value kinds. Nothing forces
// those switches to cover the same kinds, and the drift tests do not catch a gap
// here: they check that each layer DEFINES a builtin, not that the layers AGREE.
// `==` was defined everywhere and answered differently.
//
// It did exactly that for records: runtime.js had no record case, so it fell
// through to `return false` and every record comparison in the browser said
// "different", a value against itself included, while Go and iOS said "equal".
// A rule shared by backend and frontend decided differently on the two sides of
// the wire, with no error anywhere. This pins Go against JS the way
// decimal_conformance_test.go pins arithmetic.
const equalityConformanceSrc = `module Conform exposing (..)


type alias P =
    { a : Int, b : Bool }


type alias Wrap =
    { inner : P, tag : String }


yn : Bool -> String
yn x =
    if x then
        "T"

    else
        "F"


one : P
one =
    { a = 1, b = True }


two : P
two =
    { a = 1, b = True }


other : P
other =
    { a = 2, b = True }


-- The same fields written in the other order. Field order is display metadata —
-- it decides how a record renders — so it must not affect identity.
flipped : P
flipped =
    { b = True, a = 1 }


noneP : Maybe P
noneP =
    Nothing


results : String
results =
    String.join ","
        [ yn (one == one)
        , yn (one == two)
        , yn (one == flipped)
        , yn (one == other)
        , yn (one /= two)
        , yn (one /= other)

        -- records nested in a record
        , yn ({ inner = one, tag = "x" } == { inner = two, tag = "x" })
        , yn ({ inner = one, tag = "x" } == { inner = two, tag = "y" })
        , yn ({ inner = one, tag = "x" } == { inner = other, tag = "x" })

        -- the container cases recurse through record equality, so each of these
        -- broke too while the record case was missing
        , yn (Just one == Just two)
        , yn (Just one == Just other)
        , yn (Just one == noneP)
        , yn (noneP == noneP)
        , yn ([ one, other ] == [ two, other ])
        , yn ([ one ] == [ other ])
        , yn (( one, 2 ) == ( two, 2 ))
        , yn (( one, 2 ) == ( two, 3 ))

        -- List.member is structural equality wearing another name
        , yn (List.member two [ other, one ])
        , yn (List.member other [ one, two ])

        -- Duration: normalized to seconds at construction, so the unit a
        -- Duration was BUILT with is not part of its identity. Missing from
        -- all three switches at once, which is why no drift test saw it: the
        -- runtimes agreed on False.
        , yn (Time.seconds 1 == Time.seconds 1)
        , yn (Time.seconds 60 == Time.minutes 1)
        , yn (Time.hours 1 == Time.minutes 60)
        , yn (Time.seconds 1 == Time.seconds 2)
        , yn (Time.seconds 1 /= Time.seconds 2)
        , yn (Just (Time.seconds 1) == Just (Time.seconds 1))
        , yn ({ d = Time.seconds 1 } == { d = Time.seconds 1 })

        -- Time: same moment = same Unix millisecond.
        , yn (Time.fromYMD 2026 8 11 == Time.fromYMD 2026 8 11)
        , yn (Time.fromYMD 2026 8 11 == Time.fromYMD 2026 8 12)
        , yn ({ at = Time.fromYMD 2026 8 11 } == { at = Time.fromYMD 2026 8 11 })
        , yn (List.member (Time.fromYMD 2026 8 11)
                [ Time.fromYMD 2026 8 12, Time.fromYMD 2026 8 11 ])

        -- Dict / Set: these two WERE handled in runtime.js and Swift and were
        -- missing from Go only, so the same comparison answered True in the
        -- browser and on iOS and False on the server.
        , yn (Dict.fromList [ ( 1, "a" ), ( 2, "b" ) ] == Dict.fromList [ ( 2, "b" ), ( 1, "a" ) ])
        , yn (Dict.fromList [ ( 1, "a" ) ] == Dict.fromList [ ( 1, "b" ) ])
        , yn (Dict.fromList [ ( 1, "a" ) ] == Dict.fromList [ ( 1, "a" ), ( 2, "b" ) ])
        , yn (Set.fromList [ 3, 1, 2 ] == Set.fromList [ 1, 2, 3 ])
        , yn (Set.fromList [ 1 ] == Set.fromList [ 2 ])
        , yn ({ s = Set.fromList [ 1, 2 ] } == { s = Set.fromList [ 2, 1 ] })
        ]
`

// What the answers have to be. Spelled out rather than derived, so a bug shared
// by BOTH runtimes cannot hide behind them agreeing with each other.
const equalityConformanceWant = "T,T,T,F,F,T," + // plain records
	"T,F,F," + // nested in a record
	"T,F,F,T,T,F,T,F," + // Maybe / List / tuple
	"T,F," + // List.member
	"T,T,T,F,T,T,T," + // Duration
	"T,F,T,T," + // Time
	"T,F,F,T,F,T" // Dict / Set

func TestEqualityGoJSConformance(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; skipping JS conformance run")
	}

	mod, err := parser.Parse(equalityConformanceSrc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}

	// Go runtime answer.
	loaded, err := runtime.LoadModule(mod)
	if err != nil {
		t.Fatalf("go runtime load: %v", err)
	}
	goVal, err := loaded.Get("results")
	if err != nil {
		t.Fatalf("go runtime get: %v", err)
	}
	goOut := goVal.Display()

	// JS runtime answer, via node + the __marEvalValue dev hook.
	dir := t.TempDir()
	programJSON, err := json.Marshal(map[string]any{
		"modules": []any{SerializeModule(mod)},
	})
	if err != nil {
		t.Fatalf("marshal program: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.js"), []byte(runtimeJS), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "program.json"), programJSON, 0o644); err != nil {
		t.Fatal(err)
	}
	driver := `
const fs = require('fs');
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const program = JSON.parse(fs.readFileSync(process.argv[3], 'utf8'));
process.stdout.write(String(globalThis.__marEvalValue(program, 'Conform.results')));
`
	if err := os.WriteFile(filepath.Join(dir, "driver.js"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(nodePath, filepath.Join(dir, "driver.js"),
		filepath.Join(dir, "runtime.js"), filepath.Join(dir, "program.json"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// Stdout only: node prints unrelated warnings on stderr even on success.
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node run: %v\n%s", err, stderr.String())
	}
	jsOut := strings.TrimSpace(string(out))

	if goOut != jsOut {
		t.Fatalf("Go and JS runtimes disagree on equality.\nGo: %s\nJS: %s", goOut, jsOut)
	}
	if !strings.Contains(goOut, equalityConformanceWant) {
		t.Fatalf("equality answers are not the expected ones.\n got: %s\nwant to contain: %s",
			goOut, equalityConformanceWant)
	}
}
