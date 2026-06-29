package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// TestDiscoverManifestEnvRefs covers the small regex-based scanner
// that finds env:VAR references in mar.json. The scanner drives
// secrets prompting + the pre-flight "missing on Fly" check inside
// `mar fly deploy` — missing a ref means the secret never reaches
// fly.
func TestDiscoverManifestEnvRefs(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     []string
	}{
		{
			name:     "no env refs",
			manifest: `{"name": "test"}`,
			want:     nil,
		},
		{
			name: "single env ref in mail.smtpPassword",
			manifest: `{
  "name": "test",
  "mail": {
    "smtpPassword": "env:RESEND_API_KEY"
  }
}`,
			want: []string{"RESEND_API_KEY"},
		},
		{
			name: "multiple refs across mail + auth",
			manifest: `{
  "name": "test",
  "mail": {
    "smtpPassword": "env:RESEND_API_KEY",
    "smtpHost": "env:SMTP_HOST"
  },
  "auth": {
    "sessionSecret": "env:SESSION_SECRET"
  }
}`,
			want: []string{"RESEND_API_KEY", "SESSION_SECRET", "SMTP_HOST"},
		},
		{
			name: "duplicate ref returns once",
			manifest: `{
  "name": "test",
  "mail": {
    "smtpPassword": "env:KEY",
    "smtpUsername": "env:KEY"
  }
}`,
			want: []string{"KEY"},
		},
		{
			name: "ignores non-env-prefixed strings",
			manifest: `{
  "name": "test",
  "mail": {
    "from": "ops@example.com",
    "smtpHost": "smtp.example.com"
  }
}`,
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mar.json")
			if err := os.WriteFile(path, []byte(tc.manifest), 0o600); err != nil {
				t.Fatalf("write manifest: %v", err)
			}
			got, err := discoverManifestEnvRefs(path)
			if err != nil {
				t.Fatalf("discoverManifestEnvRefs: %v", err)
			}
			sort.Strings(got)
			sort.Strings(tc.want)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestLoadFlyManifest_SkipsEnvResolution: the loader uses
// LoadManifestStructure (not LoadManifest) so env:VAR refs in
// mar.json don\'t need to be set in the operator\'s local shell.
// Secrets live in Fly Secrets after `mar fly deploy` pushes them;
// requiring them locally first would be backwards.
func TestLoadFlyManifest_SkipsEnvResolution(t *testing.T) {
	dir := t.TempDir()
	manifest := `{
  "name": "test-app",
  "mail": {
    "from": "noreply@test.app",
    "smtpHost": "smtp.example.com",
    "smtpPort": 587,
    "smtpUsername": "u",
    "smtpPassword": "env:SMTP_PASSWORD_THAT_IS_NEVER_SET"
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "mar.json"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("write mar.json: %v", err)
	}
	_ = os.Unsetenv("SMTP_PASSWORD_THAT_IS_NEVER_SET")

	projectDir, m, err := loadFlyManifest(dir)
	if err != nil {
		t.Fatalf("loadFlyManifest should not require env vars; got: %v", err)
	}
	if projectDir != dir {
		t.Errorf("projectDir: got %q, want %q", projectDir, dir)
	}
	if m == nil || m.Name != "test-app" {
		t.Errorf("manifest.Name: got %v, want test-app", m)
	}
}

// TestFlyVolumeName pins the canonical app-name → volume-name
// translation. Fly volume names require [a-zA-Z0-9_]; app names
// allow dashes. The mapping must be stable across deploys so
// re-running deploy mounts the existing volume, not a fresh empty
// one.
func TestFlyVolumeName(t *testing.T) {
	cases := []struct {
		appName string
		want    string
	}{
		{"my-app", "my_app_data"},
		{"my-cool-app", "my_cool_app_data"},
		{"plain", "plain_data"},
	}
	for _, tc := range cases {
		got := flyVolumeName(tc.appName)
		if got != tc.want {
			t.Errorf("flyVolumeName(%q): got %q, want %q", tc.appName, got, tc.want)
		}
	}
}

// TestPluralizeSecrets — tiny helper, but the message it backs gets
// surfaced at deploy-time when secrets are missing; the test pins the
// singular/plural distinction so a future tweak doesn\'t say
// "1 secrets missing" or "3 secret missing".
func TestPluralizeSecrets(t *testing.T) {
	cases := map[int]string{0: "secrets", 1: "secret", 2: "secrets", 10: "secrets"}
	for n, want := range cases {
		if got := pluralizeSecrets(n); got != want {
			t.Errorf("pluralizeSecrets(%d): got %q, want %q", n, got, want)
		}
	}
}

// TestGenerateDockerfile_Frontend — frontend topology should emit
// the Caddy-on-Alpine Dockerfile that serves dist/ as static files.
// No COPY of a Go binary; no debian; no ca-certificates apt step.
func TestGenerateDockerfile_Frontend(t *testing.T) {
	out := generateDockerfile(flyTopologyFrontend, "any", 80)
	if !contains(out, "FROM caddy") {
		t.Errorf("frontend Dockerfile missing Caddy base image:\n%s", out)
	}
	if !contains(out, "COPY dist/ /usr/share/caddy/") {
		t.Errorf("frontend Dockerfile missing static COPY:\n%s", out)
	}
}

// TestGenerateDockerfile_Backend — backend / fullstack should emit
// the debian + binary shape, with the COPY using the project\'s
// binary name (NOT the Fly app slug — those are deliberately
// decoupled).
func TestGenerateDockerfile_Backend(t *testing.T) {
	out := generateDockerfile(flyTopologyFullstack, "notes-auth", 3000)
	if !contains(out, "FROM debian:bookworm-slim") {
		t.Errorf("backend Dockerfile missing debian base:\n%s", out)
	}
	if !contains(out, "COPY dist/notes-auth /app/notes-auth") {
		t.Errorf("backend Dockerfile missing binary COPY:\n%s", out)
	}
	if !contains(out, `CMD ["/app/notes-auth"]`) {
		t.Errorf("backend Dockerfile missing CMD with binary:\n%s", out)
	}
}

// contains is a one-line wrapper around strings.Contains — keeps the
// test bodies legible without an extra import line at the top.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
