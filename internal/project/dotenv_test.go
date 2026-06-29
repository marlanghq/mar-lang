package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDotenv_MissingFile(t *testing.T) {
	dir := t.TempDir()
	env, err := LoadDotenv(dir)
	if err != nil {
		t.Fatalf("LoadDotenv: %v", err)
	}
	if env != nil {
		t.Fatalf("expected nil map for missing .env, got %v", env)
	}
}

func TestLoadDotenv_BasicAndComments(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, `
# Database
DATABASE_URL=postgres://localhost/dev

# Mail
SMTP_HOST=smtp.example.com
SMTP_PORT=587

# Trailing comment on an unquoted value
API_BASE=https://api.example.com   # production
`)
	env, err := LoadDotenv(dir)
	if err != nil {
		t.Fatalf("LoadDotenv: %v", err)
	}
	expect := map[string]string{
		"DATABASE_URL": "postgres://localhost/dev",
		"SMTP_HOST":    "smtp.example.com",
		"SMTP_PORT":    "587",
		"API_BASE":     "https://api.example.com",
	}
	assertEnvEquals(t, expect, env)
}

func TestLoadDotenv_QuotedValues(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, `
DOUBLE="value with spaces"
SINGLE='literal $no $interpolation'
HASH_IN_QUOTES="not # a comment"
ESCAPES="line1\nline2\ttab\\backslash\"quote"
EMPTY=
EMPTY_QUOTED=""
`)
	env, err := LoadDotenv(dir)
	if err != nil {
		t.Fatalf("LoadDotenv: %v", err)
	}
	expect := map[string]string{
		"DOUBLE":         "value with spaces",
		"SINGLE":         "literal $no $interpolation",
		"HASH_IN_QUOTES": "not # a comment",
		"ESCAPES":        "line1\nline2\ttab\\backslash\"quote",
		"EMPTY":          "",
		"EMPTY_QUOTED":   "",
	}
	assertEnvEquals(t, expect, env)
}

func TestLoadDotenv_ExportPrefix(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, `export FOO=bar
export   BAZ="qux quux"
`)
	env, err := LoadDotenv(dir)
	if err != nil {
		t.Fatalf("LoadDotenv: %v", err)
	}
	if env["FOO"] != "bar" || env["BAZ"] != "qux quux" {
		t.Fatalf("export prefix not stripped: %v", env)
	}
}

func TestLoadDotenv_MalformedLine(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, "FOO=bar\nthis is not a kv pair\n")
	_, err := LoadDotenv(dir)
	if err == nil {
		t.Fatal("expected error for malformed line")
	}
	if !strings.Contains(err.Error(), "line 2") && !strings.Contains(err.Error(), ":2:") {
		t.Fatalf("error should reference line 2, got: %v", err)
	}
}

func TestLoadDotenv_InvalidKey(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, "1BAD=value\n")
	_, err := LoadDotenv(dir)
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
	if !strings.Contains(err.Error(), "invalid variable name") {
		t.Fatalf("error should mention invalid variable name, got: %v", err)
	}
}

func TestLoadDotenv_UnterminatedQuote(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, `FOO="oops no close`+"\n")
	_, err := LoadDotenv(dir)
	if err == nil {
		t.Fatal("expected error for unterminated quote")
	}
	if !strings.Contains(err.Error(), "unterminated") {
		t.Fatalf("error should mention unterminated quote, got: %v", err)
	}
}

func TestLoadDotenv_GarbageAfterQuote(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, `FOO="ok" garbage`+"\n")
	_, err := LoadDotenv(dir)
	if err == nil {
		t.Fatal("expected error for garbage after quoted value")
	}
}

func TestApplyDotenv_DoesNotOverrideShell(t *testing.T) {
	t.Setenv("MAR_TEST_DOTENV_SHELLWINS", "from-shell")
	t.Setenv("MAR_TEST_DOTENV_FROMFILE", "")
	// Clear FROMFILE so ApplyDotenv populates it.
	os.Unsetenv("MAR_TEST_DOTENV_FROMFILE")

	ApplyDotenv(map[string]string{
		"MAR_TEST_DOTENV_SHELLWINS": "from-file-should-lose",
		"MAR_TEST_DOTENV_FROMFILE":  "from-file",
	})

	if got := os.Getenv("MAR_TEST_DOTENV_SHELLWINS"); got != "from-shell" {
		t.Errorf("shell-set var was overridden: got %q want %q", got, "from-shell")
	}
	if got := os.Getenv("MAR_TEST_DOTENV_FROMFILE"); got != "from-file" {
		t.Errorf("unset var not populated: got %q want %q", got, "from-file")
	}
	// Make sure we clean up the var we set so other tests aren't poisoned.
	os.Unsetenv("MAR_TEST_DOTENV_FROMFILE")
}

func TestLoadAndApplyDotenv_SetsProcessEnv(t *testing.T) {
	dir := t.TempDir()
	writeDotenv(t, dir, "MAR_TEST_DOTENV_LOAD=loaded-value\n")
	defer os.Unsetenv("MAR_TEST_DOTENV_LOAD")

	os.Unsetenv("MAR_TEST_DOTENV_LOAD")
	env, err := LoadAndApplyDotenv(dir)
	if err != nil {
		t.Fatalf("LoadAndApplyDotenv: %v", err)
	}
	if env["MAR_TEST_DOTENV_LOAD"] != "loaded-value" {
		t.Errorf("returned map: %v", env)
	}
	if got := os.Getenv("MAR_TEST_DOTENV_LOAD"); got != "loaded-value" {
		t.Errorf("process env not set: got %q", got)
	}
}

// TestLoadManifest_FoldsDotenv proves the end-to-end wiring: when
// mar.json references env:VAR and .env defines VAR, the manifest
// loads with the resolved value. Shell-set vars must still win over
// .env even when both define the same name.
func TestLoadManifest_FoldsDotenv(t *testing.T) {
	dir := t.TempDir()
	manifest := `{
  "name": "test",
  "entry": "Main.mar",
  "mail": {
    "smtpHost": "env:DOTENV_SMTP_HOST",
    "smtpPassword": "env:DOTENV_SMTP_PASSWORD",
    "from": "noreply@example.com"
  }
}`
	if err := os.WriteFile(filepath.Join(dir, "mar.json"), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write mar.json: %v", err)
	}
	writeDotenv(t, dir, `
DOTENV_SMTP_HOST=smtp.from.dotenv
DOTENV_SMTP_PASSWORD=secret-from-dotenv
DOTENV_SHELL_WINS=from-dotenv
`)
	t.Setenv("DOTENV_SHELL_WINS", "from-shell")
	// Pre-emptively clear vars so a leftover from another test doesn't
	// silently make this one pass for the wrong reason.
	os.Unsetenv("DOTENV_SMTP_HOST")
	os.Unsetenv("DOTENV_SMTP_PASSWORD")
	defer os.Unsetenv("DOTENV_SMTP_HOST")
	defer os.Unsetenv("DOTENV_SMTP_PASSWORD")

	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if m.Mail.SMTPHost != "smtp.from.dotenv" {
		t.Errorf("SMTPHost: got %q, want from-dotenv value", m.Mail.SMTPHost)
	}
	if m.Mail.SMTPPassword != "secret-from-dotenv" {
		t.Errorf("SMTPPassword: got %q, want from-dotenv value", m.Mail.SMTPPassword)
	}
	// The shell-set var should be untouched by .env.
	if got := os.Getenv("DOTENV_SHELL_WINS"); got != "from-shell" {
		t.Errorf("shell-set var was overwritten: got %q", got)
	}
}

// writeDotenv drops a .env file with the given content into dir.
func writeDotenv(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(content), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
}

// assertEnvEquals checks that got contains exactly the entries in want.
func assertEnvEquals(t *testing.T, want, got map[string]string) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("size mismatch: want %d entries, got %d (%v)", len(want), len(got), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: want %q, got %q", k, v, got[k])
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected key %q in result", k)
		}
	}
}
