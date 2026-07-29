package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// runDeploy routes by which deploy block mar.json declares:
// deploy.fly → Fly, deploy.cloudflare-pages → Cloudflare Pages. The
// two error branches (no block, both blocks) return exit code 2
// WITHOUT touching Fly or Cloudflare, so they're safe to unit-test
// here. The happy paths call the real provider flows (build + network)
// and are covered by the fly_* / cloudflarepages_* tests plus manual
// smoke, not here.

func writeDeployManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if body != "" {
		if err := os.WriteFile(filepath.Join(dir, "mar.json"), []byte(body), 0o644); err != nil {
			t.Fatalf("write mar.json: %v", err)
		}
	}
	return dir
}

// A directory with no mar.json at all means "wrong directory", not
// "add a deploy block": the error must say the file wasn't found and
// hint that this isn't a Mar project (regression: it used to print
// "mar.json has no deploy block" for a file that didn't exist).
func TestRunDeploy_noManifest(t *testing.T) {
	dir := writeDeployManifest(t, "") // empty dir, no mar.json

	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	got := runDeploy([]string{dir})
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	msg := stripANSI(string(out))

	if got != 2 {
		t.Errorf("no manifest: exit = %d, want 2", got)
	}
	if !strings.Contains(msg, "no mar.json found") {
		t.Errorf("stderr should say the manifest is missing; got:\n%s", msg)
	}
	if !strings.Contains(msg, "doesn't look like a Mar project") {
		t.Errorf("stderr should hint this isn't a Mar project; got:\n%s", msg)
	}
	if strings.Contains(msg, "has no deploy block") {
		t.Errorf("stderr must NOT coach adding a deploy block to a missing file; got:\n%s", msg)
	}
}

func TestRunDeploy_noDeployBlock(t *testing.T) {
	dir := writeDeployManifest(t, `{"name":"demo"}`)
	if got := runDeploy([]string{dir}); got != 2 {
		t.Errorf("no deploy block: exit = %d, want 2", got)
	}
}

// Both targets declared is ambiguous — an app deploys to exactly one.
// runDeploy must reject it (exit 2) rather than silently picking one.
func TestRunDeploy_bothBlocksRejected(t *testing.T) {
	dir := writeDeployManifest(t, `{
	  "name": "demo",
	  "deploy": {
	    "fly": {"app": "demo", "region": "gru", "memory": "256mb"},
	    "cloudflare-pages": {
	      "app": "demo",
	      "account": "0123456789abcdef0123456789abcdef",
	      "apiToken": "env:CLOUDFLARE_API_TOKEN"
	    }
	  }
	}`)
	if got := runDeploy([]string{dir}); got != 2 {
		t.Errorf("both blocks: exit = %d, want 2 (ambiguous, must reject)", got)
	}
}

var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiEscape.ReplaceAllString(s, "") }

// The "no deploy block" hint is tailored to the app's shape: a frontend
// app is pointed only at Cloudflare Pages, a fullstack/backend app only
// at Fly. It must never offer the wrong target.
func TestMissingDeployHint_PerTopology(t *testing.T) {
	frontend := stripANSI(missingDeployHint("frontend", nil))
	if !strings.Contains(frontend, `"cloudflare-pages"`) {
		t.Errorf("frontend hint must show the cloudflare-pages block:\n%s", frontend)
	}
	if strings.Contains(frontend, `"fly"`) {
		t.Errorf("frontend hint must NOT show the fly block:\n%s", frontend)
	}

	for _, topo := range []string{"fullstack", "backend"} {
		h := stripANSI(missingDeployHint(topo, nil))
		if !strings.Contains(h, `"fly"`) {
			t.Errorf("%s hint must show the fly block:\n%s", topo, h)
		}
		if strings.Contains(h, "cloudflare-pages") {
			t.Errorf("%s hint must NOT show the cloudflare-pages block:\n%s", topo, h)
		}
		if !strings.Contains(h, topo+" app") {
			t.Errorf("%s hint should name the topology:\n%s", topo, h)
		}
	}
}

// When the topology can't be determined we list both targets, and the two
// descriptions must line up in a column (the original alignment bug).
func TestMissingDeployHint_FallbackAligned(t *testing.T) {
	both := stripANSI(missingDeployHint("", errors.New("no main")))
	if !strings.Contains(both, "deploy.fly") || !strings.Contains(both, "deploy.cloudflare-pages") {
		t.Fatalf("fallback must list both targets:\n%s", both)
	}
	flyCol := descColumn(both, "deploy.fly", "a Fly VM")
	cfCol := descColumn(both, "deploy.cloudflare-pages", "a Cloudflare")
	if flyCol < 0 || cfCol < 0 {
		t.Fatalf("could not locate both descriptions:\n%s", both)
	}
	if flyCol != cfCol {
		t.Errorf("fallback descriptions misaligned: fly@%d, cloudflare@%d\n%s", flyCol, cfCol, both)
	}
}

// descColumn is the byte offset where desc begins on the line holding label.
func descColumn(text, label, desc string) int {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, label) {
			return strings.Index(line, desc)
		}
	}
	return -1
}
