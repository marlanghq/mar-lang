package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// TestDiscoverManifestEnvRefs covers the small regex-based scanner
// that finds env:VAR references in mar.json. The scanner is what
// `mar fly provision` uses to know which secrets to prompt for —
// missing a ref means the secret never reaches fly.
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

// TestReadFlyToml exercises the regex-based parser for the three
// fields the wrappers need from a generated fly.toml.
func TestReadFlyToml(t *testing.T) {
	src := `app = "my-app"
primary_region = "gru"

[build]
  dockerfile = "Dockerfile"

[mounts]
  source = "my_app_data"
  destination = "/data"

[http_service]
  internal_port = 3000
`
	path := filepath.Join(t.TempDir(), "fly.toml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write fly.toml: %v", err)
	}
	cfg, err := readFlyToml(path)
	if err != nil {
		t.Fatalf("readFlyToml: %v", err)
	}
	if cfg.AppName != "my-app" {
		t.Errorf("AppName: got %q, want %q", cfg.AppName, "my-app")
	}
	if cfg.Region != "gru" {
		t.Errorf("Region: got %q, want %q", cfg.Region, "gru")
	}
	if cfg.VolumeName != "my_app_data" {
		t.Errorf("VolumeName: got %q, want %q", cfg.VolumeName, "my_app_data")
	}
}

// TestReadFlyToml_MissingFields surfaces a clear error instead of
// returning empty strings — the rest of the wrapper would otherwise
// fail with a confusing fly CLI error.
func TestReadFlyToml_MissingApp(t *testing.T) {
	src := `primary_region = "gru"
[mounts]
  source = "data"
`
	path := filepath.Join(t.TempDir(), "fly.toml")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("write fly.toml: %v", err)
	}
	_, err := readFlyToml(path)
	if err == nil {
		t.Fatal("expected error for missing app name; got nil")
	}
}

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

// TestSlugifyFlyAppName covers the Fly-app-slug normalization. Same
// cases the lispy version handled — re-deploying an app from old
// mar.json's should produce identical slugs.
func TestSlugifyFlyAppName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"notes-auth-multipage", "notes-auth-multipage"},
		{"My Cool App", "my-cool-app"},
		{"camelCase", "camel-case"},
		{"PascalCase", "pascal-case"},
		{"with_underscore", "with-underscore"},
		{"with.dots", "with-dots"},
		{"--leading-trailing-hyphens--", "leading-trailing-hyphens"},
		{"  whitespace  ", "whitespace"},
		{"a---b", "a-b"},
		{"", ""},
	}
	for _, tc := range cases {
		got := slugifyFlyAppName(tc.in)
		if got != tc.want {
			t.Errorf("slugifyFlyAppName(%q): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFindFlyRegion confirms valid lookups + error path for unknown
// codes. Whitespace and case-insensitive intentionally accepted —
// users typing the picker output should not have to be precise.
func TestFindFlyRegion(t *testing.T) {
	if r, ok := findFlyRegion("gru"); !ok || r.Code != "gru" {
		t.Errorf("findFlyRegion(gru): got %v ok=%v", r, ok)
	}
	if r, ok := findFlyRegion("  GRU  "); !ok || r.Code != "gru" {
		t.Errorf("case-insensitive + whitespace tolerance broke: got %v ok=%v", r, ok)
	}
	if _, ok := findFlyRegion("nonsense"); ok {
		t.Error("findFlyRegion(nonsense): expected ok=false")
	}
}

// TestNormalizeFlyAppMemory exercises the memory option matcher.
// Same case-insensitive + whitespace-tolerant rules as the region
// matcher.
func TestNormalizeFlyAppMemory(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"256mb", "256mb", true},
		{"  256MB  ", "256mb", true},
		{"1gb", "1gb", true},
		{"1GB", "1gb", true},
		{"3gb", "", false},   // not in the curated set
		{"forty", "", false}, // bogus
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := normalizeFlyAppMemory(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("normalizeFlyAppMemory(%q): got (%q, %v), want (%q, %v)",
				tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestPromptFlyAppMemory_FromEnv confirms the FLY_MEMORY env var
// overrides the prompt entirely (used in CI).
func TestPromptFlyAppMemory_FromEnv(t *testing.T) {
	t.Setenv("FLY_MEMORY", "1gb")
	got, err := promptFlyAppMemory()
	if err != nil {
		t.Fatalf("promptFlyAppMemory: %v", err)
	}
	if got != "1gb" {
		t.Errorf("got %q, want %q", got, "1gb")
	}
}

func TestPromptFlyAppMemory_InvalidEnv(t *testing.T) {
	t.Setenv("FLY_MEMORY", "9001gb")
	_, err := promptFlyAppMemory()
	if err == nil {
		t.Fatal("expected error for invalid FLY_MEMORY; got nil")
	}
}
