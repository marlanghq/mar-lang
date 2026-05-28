package main

import (
	"os"
	"path/filepath"
	"testing"
)

// `mar deploy` is the only sub shared between the fly and
// cloudflare-pages dispatchers. The typo-hint engine peeks at
// ./mar.json to suggest the right one — these tests pin that logic.

func writeManifest(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mar.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write mar.json: %v", err)
	}
	t.Chdir(dir)
}

func TestPickDeployParent_noManifest(t *testing.T) {
	t.Chdir(t.TempDir()) // empty dir, no mar.json
	if got := pickDeployParent(); got != "fly" {
		t.Errorf("no manifest: got %q, want %q", got, "fly")
	}
}

func TestPickDeployParent_onlyCloudflarePages(t *testing.T) {
	writeManifest(t, `{
	  "name": "demo",
	  "deploy": {
	    "cloudflare-pages": {
	      "app": "demo",
	      "account": "acc-id",
	      "apiToken": "env:CF_API_TOKEN"
	    }
	  }
	}`)
	if got := pickDeployParent(); got != "cloudflare-pages" {
		t.Errorf("only cloudflare-pages: got %q, want %q", got, "cloudflare-pages")
	}
}

func TestPickDeployParent_onlyFly(t *testing.T) {
	writeManifest(t, `{
	  "name": "demo",
	  "deploy": {
	    "fly": {
	      "app": "demo",
	      "region": "gru"
	    }
	  }
	}`)
	if got := pickDeployParent(); got != "fly" {
		t.Errorf("only fly: got %q, want %q", got, "fly")
	}
}

func TestPickDeployParent_bothConfigured(t *testing.T) {
	// Ambiguous — both targets declared. Falls back to fly, matching
	// the docstring: "fly is the older deploy target and the safer
	// default when intent is ambiguous."
	writeManifest(t, `{
	  "name": "demo",
	  "deploy": {
	    "fly": {"app": "demo", "region": "gru"},
	    "cloudflare-pages": {
	      "app": "demo",
	      "account": "acc-id",
	      "apiToken": "env:CF_API_TOKEN"
	    }
	  }
	}`)
	if got := pickDeployParent(); got != "fly" {
		t.Errorf("both configured: got %q, want %q (fly wins on ambiguity)", got, "fly")
	}
}

func TestPickDeployParent_emptyDeployBlock(t *testing.T) {
	writeManifest(t, `{"name": "demo", "deploy": {}}`)
	if got := pickDeployParent(); got != "fly" {
		t.Errorf("empty deploy block: got %q, want %q", got, "fly")
	}
}

// parentForSubcommand routes through pickDeployParent for "deploy"
// but stays static for every other sub. Pin the static cases so a
// future refactor doesn't accidentally route them through manifest
// lookups (which would slow down a hot CLI path).
func TestParentForSubcommand_staticSubs(t *testing.T) {
	t.Chdir(t.TempDir())
	cases := map[string]string{
		"preview":  "fly",
		"destroy":  "fly",
		"logs":     "fly",
		"status":   "fly",
		"secrets":  "fly",
		"backup":   "fly database",
		"backups":  "fly database",
		"database": "fly",
		"db":       "fly",
		"plan":     "migrate",
		"frobnitz": "",
	}
	for sub, want := range cases {
		if got := parentForSubcommand(sub); got != want {
			t.Errorf("parentForSubcommand(%q) = %q, want %q", sub, got, want)
		}
	}
}
