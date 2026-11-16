package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"mar/internal/parityfixture"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// The browser's half of parityfixture.DecimalBoundCases and
// DecimalLongTextCases: each case is its own one-value program, because a
// value that raises stops the module holding it, and the answer is either what
// the value returns or the sentence it raises. A long-text case also has to
// finish within LongTextBudget. The server's half is
// TestTheServerAnswersTheDecimalBoundCases.
func TestTheBrowserAnswersTheDecimalBoundCases(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	cases := slices.Concat(parityfixture.DecimalBoundCases, parityfixture.DecimalLongTextCases)
	long := len(parityfixture.DecimalBoundCases)
	var programs []json.RawMessage
	for _, c := range cases {
		src := "module Main exposing (..)\n\n\nx : String\nx =\n    " + c.Expr + "\n"
		mod, err := parser.Parse(src)
		if err != nil {
			t.Fatalf("%s: parse: %v", c.Name, err)
		}
		if _, err := typecheck.CheckModule(mod); err != nil {
			t.Fatalf("%s: check: %v", c.Name, err)
		}
		pj, err := json.Marshal(map[string]any{"modules": []any{SerializeModule(mod)}})
		if err != nil {
			t.Fatal(err)
		}
		programs = append(programs, pj)
	}
	dir := t.TempDir()
	rt, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	all, _ := json.Marshal(programs)
	for name, b := range map[string][]byte{"runtime.js": rt, "programs.json": all, "driver.js": []byte(`
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const out = JSON.parse(fs.readFileSync(process.argv[3], 'utf8')).map(program => {
  const start = process.hrtime.bigint();
  let says;
  try { says = globalThis.__marEvalValue(program, 'Main.x'); }
  catch (e) { says = 'THREW: ' + String((e && e.message) || e); }
  return { says, ms: Number(process.hrtime.bigint() - start) / 1e6 };
});
process.stdout.write(JSON.stringify(out));
`)} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(node, filepath.Join(dir, "driver.js"), filepath.Join(dir, "runtime.js"), filepath.Join(dir, "programs.json"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var answers []struct {
		Says string
		Ms   float64
	}
	if err := json.Unmarshal(out, &answers); err != nil {
		t.Fatalf("driver output: %v\n%s", err, out)
	}
	for i, c := range cases {
		if took := time.Duration(answers[i].Ms * float64(time.Millisecond)); i >= long && took > parityfixture.LongTextBudget {
			t.Errorf("%s took %v in the browser; text has to be read in time linear in its length", c.Name, took)
		}
		got := answers[i].Says
		if msg, ok := strings.CutPrefix(got, "THREW: "); ok {
			got = msg
		} else if unquoted, err := strconv.Unquote(got); err == nil {
			got = unquoted
		}
		if got != c.Says {
			t.Errorf("%s:\n  browser: %s\n  want:    %s", c.Name, got, c.Says)
		}
	}
}
