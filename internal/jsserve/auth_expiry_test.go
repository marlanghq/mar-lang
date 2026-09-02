package jsserve

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parityfixture"
)

// The browser's half of what a 401 decides, against the same table the phone
// answers. See parityfixture.AuthExpiryCases.
func TestTheBrowserDecidesAnExpiredSessionLikeThePhone(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
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

	type row struct {
		SignInPath  string `json:"signInPath"`
		Redirecting bool   `json:"redirecting"`
		CurrentPath string `json:"currentPath"`
	}
	var rows []row
	for _, c := range parityfixture.AuthExpiryCases {
		rows = append(rows, row{c.SignInPath, c.AlreadyRedirecting, c.CurrentPath})
	}
	in, _ := json.Marshal(rows)

	drv := `
const fs = require('fs');
globalThis.window = globalThis; globalThis.document = undefined;
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const decide = globalThis.__marDecideAuthExpiry;
if (typeof decide !== 'function') { console.error('__marDecideAuthExpiry is not exported'); process.exit(1); }
const rows = ` + string(in) + `;
process.stdout.write(JSON.stringify(rows.map(r =>
  decide(r.signInPath, r.redirecting, r.currentPath))));
`
	if err := os.WriteFile(filepath.Join(dir, "driver.js"), []byte(drv), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join(dir, "driver.js"), filepath.Join(dir, "runtime.js"))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var got []struct {
		Handled  bool    `json:"handled"`
		Navigate bool    `json:"navigate"`
		Capture  *string `json:"capture"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("driver output: %v\n%s", err, out)
	}
	if len(got) != len(parityfixture.AuthExpiryCases) {
		t.Fatalf("asked about %d cases and got %d answers",
			len(parityfixture.AuthExpiryCases), len(got))
	}
	for i, c := range parityfixture.AuthExpiryCases {
		g := got[i]
		capture := ""
		if g.Capture != nil {
			capture = *g.Capture
		}
		if g.Handled != c.Handled || g.Navigate != c.Navigate || capture != c.Capture {
			t.Errorf("%s\n  signInPath=%q redirecting=%v currentPath=%q\n"+
				"  want: handled=%v navigate=%v capture=%q\n"+
				"  got:  handled=%v navigate=%v capture=%q",
				c.Why, c.SignInPath, c.AlreadyRedirecting, c.CurrentPath,
				c.Handled, c.Navigate, c.Capture, g.Handled, g.Navigate, capture)
		}
	}
}
