package jsserve

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// program.json arrives over the network, and what crosses that boundary is
// CODE. The phone's decoder refuses a document that is not a program, naming
// what is wrong; the browser used to walk into loading it and fail with
// `entry not found: main` — for a document with no modules at all — or leak
// `mod.decls is not iterable`.
//
// A truncated download, a proxy answering with HTML, a stale cache: all three
// end there, and "entry not found: main" sends someone hunting for a `main`
// that is right in front of them.
func TestTheBrowserRefusesAProgramThatIsNotOne(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	rt, err := os.ReadFile("runtime.js")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime.js"), rt, 0o644); err != nil {
		t.Fatal(err)
	}

	const driver = `import fs from 'fs';
const src = fs.readFileSync(new URL('./runtime.js', import.meta.url), 'utf8');
globalThis.window = globalThis; globalThis.document = undefined;
(0, eval)(src);
const cases = [
  ['notAnObject', 42],
  ['empty', {}],
  ['noModules', { devMode: true }],
  ['modulesNotAList', { modules: "x" }],
  ['moduleWithoutDecls', { modules: [{ name: ["M"] }] }],
];
for (const [label, p] of cases) {
  try { globalThis.marRun(p); console.log(label + '=accepted'); }
  catch (e) { console.log(label + '=' + String((e && e.message) || e)); }
}
`
	if err := os.WriteFile(filepath.Join(dir, "driver.mjs"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", filepath.Join(dir, "driver.mjs")).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}

	// The sentences are the phone's, so the two runtimes answer one question
	// the same way.
	want := map[string]string{
		"notAnObject":        "program.json is not an object",
		"empty":              "missing `modules`",
		"noModules":          "missing `modules`",
		"modulesNotAList":    "no `decls`",
		"moduleWithoutDecls": "no `decls`",
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if i := strings.Index(line, "="); i > 0 {
			got[line[:i]] = line[i+1:]
		}
	}
	for label, fragment := range want {
		answer, ok := got[label]
		if !ok {
			t.Errorf("%s: no answer at all", label)
			continue
		}
		if answer == "accepted" {
			t.Errorf("%s: the browser accepted a document that is not a program", label)
			continue
		}
		if !strings.Contains(answer, fragment) {
			t.Errorf("%s: expected a message about %q, got %q", label, fragment, answer)
		}
		if strings.Contains(answer, "entry not found") || strings.Contains(answer, "not iterable") {
			t.Errorf("%s: the message blames the wrong thing: %q", label, answer)
		}
	}
}
