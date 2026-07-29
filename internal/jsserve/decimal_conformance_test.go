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

// The same Mar module is evaluated by the Go runtime (LoadModule) and
// by runtime.js (node + the __marEvalValue dev hook), and the rendered
// results must agree byte-for-byte. This is the cross-runtime proof
// that Decimal arithmetic, // division, and the three Division
// resolvers cannot drift between server and browser.
const decimalConformanceSrc = `module Conform exposing (..)


fmtMaybe : Maybe Decimal -> String
fmtMaybe m =
    case m of
        Nothing ->
            "Nothing"

        Just d ->
            "Just " ++ Decimal.toString d


fmtQR : { quotient : Decimal, remainder : Decimal } -> String
fmtQR r =
    Decimal.toString r.quotient ++ "|" ++ Decimal.toString r.remainder


boolStr : Bool -> String
boolStr b =
    if b then
        "T"

    else
        "F"


orderStr : Order -> String
orderStr o =
    case o of
        LT ->
            "LT"

        EQ ->
            "EQ"

        GT ->
            "GT"


results : List String
results =
    [ Decimal.toString (0.1 + 0.2)
    , Decimal.toString (1.10 + 2.20)
    , Decimal.toString (0.5 - 0.25)
    , Decimal.toString (1.5 * 1.5)
    , Decimal.toString (0.10 * 0.10)
    , Decimal.toString (-(12.50))
    , Decimal.toString (Decimal.abs (-2.5))
    , Decimal.toString (Decimal.negate 1.50)
    , String.fromInt (7 // 2)
    , String.fromInt (-7 // 2)
    , String.fromInt (7 // 0)
    , boolStr (1.50 == 1.5)
    , boolStr (1.5 < 1.55)
    , Decimal.toString (Decimal.rounded Decimal.HalfEven 4 (1.0 / 3.0))
    , Decimal.toString (1.0 / 3.0 |> Decimal.rounded Decimal.HalfEven 4)
    , Decimal.toString (Decimal.rounded Decimal.HalfUp 0 (5.0 / 2.0))
    , Decimal.toString (Decimal.rounded Decimal.HalfEven 0 (5.0 / 2.0))
    , Decimal.toString (Decimal.rounded Decimal.Down 0 (-7.0 / 2.0))
    , Decimal.toString (Decimal.rounded Decimal.Up 0 (-7.0 / 2.0))
    , Decimal.toString (Decimal.rounded Decimal.Floor 0 (-7.0 / 2.0))
    , Decimal.toString (Decimal.rounded Decimal.Ceiling 0 (-7.0 / 2.0))
    , Decimal.toString (Decimal.rounded Decimal.HalfEven 2 (5.0 / 0.0))
    , fmtQR (Decimal.withRemainder 2 (100.00 / Decimal.fromInt 3))
    , fmtQR (Decimal.withRemainder 2 (5.0 / 0.0))
    , Decimal.toString (Decimal.toScale Decimal.HalfEven 2 1.005)
    , Decimal.toString (Decimal.toScale Decimal.HalfEven 2 1.015)
    , Decimal.toString (Decimal.fromCents 1234)
    , String.fromInt (Decimal.toCents 12.34)
    , String.fromInt (Decimal.truncate (-2.9))
    , String.fromInt (Decimal.round 2.5)
    , String.fromInt (Decimal.round 3.5)
    , String.fromInt (Decimal.floor (-2.1))
    , String.fromInt (Decimal.ceiling (-2.1))
    , String.fromInt (Decimal.toIntWith Decimal.Up 2.1)
    , orderStr (Decimal.compare 1.5 1.50)
    , fmtMaybe (Decimal.fromString "12.50")
    , fmtMaybe (Decimal.fromString "nope")
    , Decimal.toString Decimal.zero
    , Decimal.toString (List.sum [ 1.50, 2.25 ])
    , Decimal.toString (List.sum [ 0.1, 0.2 ])
    , Decimal.toString (List.sum [ 19.99 ])
    , Decimal.toString (List.sum emptyMoney)
    , Decimal.toString (List.product [ 1.5, 2.0 ])
    , Decimal.toString (List.product emptyMoney)
    , String.fromInt (List.sum [ 1, 2, 3 ])
    , String.fromInt (List.product [ 2, 3, 4 ])
    , Decimal.toString (List.sum (List.map .amount rows))
    ]


-- Declared below the list that reads them, on purpose: the checker orders
-- value declarations by dependency, so source order carries no meaning.
emptyMoney : List Decimal
emptyMoney =
    []


rows : List { amount : Decimal }
rows =
    [ { amount = 10.00 }, { amount = 2.50 }, { amount = 0.05 } ]

`

func TestDecimalGoJSConformance(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; skipping JS conformance run")
	}

	mod, err := parser.Parse(decimalConformanceSrc)
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
const src = fs.readFileSync(process.argv[2], 'utf8');
(0, eval)(src);
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
	// Stdout only — node prints unrelated warnings (localStorage
	// availability) on stderr even on success.
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node run: %v\n%s", err, stderr.String())
	}
	jsOut := strings.TrimSpace(string(out))

	if goOut != jsOut {
		t.Fatalf("Go and JS runtimes disagree.\nGo: %s\nJS: %s", goOut, jsOut)
	}
	// Spot-check a few load-bearing digits so a shared bug in both
	// runtimes can't hide behind agreement.
	for _, want := range []string{`"0.3"`, `"0.3333"`, `"33.33|0.01"`, `"-3"`, `"1.00"`, `"-1.50"`} {
		if !strings.Contains(goOut, want) {
			t.Errorf("expected %s in results, got: %s", want, goOut)
		}
	}
}
