package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/runtime"
)

// TestValidateProductionConfig_NoAuthSkips covers the early-return
// path: when Auth.config wasn't called, validation has nothing to
// check — projects without auth ship without these fields, that's
// fine.
func TestValidateProductionConfig_NoAuthSkips(t *testing.T) {
	runtime.ResetAuthForTesting()
	t.Cleanup(runtime.ResetAuthForTesting)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mar.json"), `{"name":"no-auth-app"}`)

	if err := ValidateProductionConfig(dir); err != nil {
		t.Fatalf("expected no error when auth isn't registered; got %v", err)
	}
}

// TestValidateProductionConfig_AuthRequiresMail confirms the error
// path. When auth is in use but mar.json doesn't declare auth +
// mail, the build fails with copy-pasteable hints.
func TestValidateProductionConfig_AuthRequiresMail(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mar.json"), `{"name":"missing-mail"}`)
	registerFakeAuth()
	t.Cleanup(runtime.ResetAuthForTesting)

	err := ValidateProductionConfig(dir)
	if err == nil {
		t.Fatal("expected error for missing auth+mail config; got nil")
	}
	for _, want := range []string{
		"Auth.config",
		"sessionSecret",
		"smtpHost",
		"smtpPassword",
		"env:",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q\n%v", want, err)
		}
	}
}

// TestBuild_RejectsUnknownManifestField pins that `mar build` validates
// mar.json structure for ANY target (not just production builds): a
// typo'd or misplaced top-level key fails the build instead of being
// silently ignored. Regression guard for the dev≠build gap — `mar dev`
// always rejected this, but build + check used to wave it through.
func TestBuild_RejectsUnknownManifestField(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Main.mar"),
		"module Main exposing (main)\n\nmain : Effect String ()\nmain = App.frontend []\n")
	// "port" belongs under "server", not at the top level — the exact
	// misplacement that used to build clean and silently fall back to
	// the default port.
	writeFile(t, filepath.Join(dir, "mar.json"), `{"name":"x","port":3011}`)

	err := Build(dir, filepath.Join(dir, "dist"), "")
	if err == nil {
		t.Fatal(`expected Build to reject a misplaced top-level "port" key; got nil`)
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("Build error should flag the unknown field; got: %v", err)
	}
}

// TestValidateProductionConfig_PartialMail catches the case where
// the user added a mail block but forgot fields. Error should
// enumerate exactly what's missing rather than telling them to
// rewrite the whole block.
func TestValidateProductionConfig_PartialMail(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mar.json"), `{
  "name": "partial",
  "auth": { "sessionSecret": "env:S" },
  "mail": { "from": "noreply@x.com" }
}`)
	registerFakeAuth()
	t.Cleanup(runtime.ResetAuthForTesting)

	err := ValidateProductionConfig(dir)
	if err == nil {
		t.Fatal("expected error for partial mail config; got nil")
	}
	// Should mention the specific missing fields, not the whole
	// block-replacement template.
	for _, want := range []string{
		`"smtpHost"`,
		`"smtpUsername"`,
		`"smtpPassword"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q\n%v", want, err)
		}
	}
}

// TestValidateProductionConfig_HappyPath confirms a fully-configured
// mar.json passes validation. smtpPort omitted on purpose — the
// default makes it optional.
func TestValidateProductionConfig_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mar.json"), `{
  "name": "happy",
  "auth": { "sessionSecret": "env:SESSION" },
  "mail": {
    "from": "noreply@x.com",
    "smtpHost": "smtp.resend.com",
    "smtpUsername": "resend",
    "smtpPassword": "env:RESEND_API_KEY"
  }
}`)
	registerFakeAuth()
	t.Cleanup(runtime.ResetAuthForTesting)

	if err := ValidateProductionConfig(dir); err != nil {
		t.Errorf("expected nil for fully configured project; got %v", err)
	}
}

// TestIsProductionTarget pins the rule: empty target = host-mode
// (dev), non-host targets = production. Matters because
// `mar build` against the host without --target is sometimes used
// for local debugging, where the missing fields are fine.
func TestIsProductionTarget(t *testing.T) {
	cases := []struct {
		target string
		want   bool
	}{
		{"", false},
		{"linux-amd64", true},
		{"linux-arm64", true},
		{"darwin-amd64", true},
		{"windows-amd64", true},
	}
	for _, tc := range cases {
		got := isProductionTarget(tc.target)
		if got != tc.want {
			t.Errorf("isProductionTarget(%q): got %v, want %v",
				tc.target, got, tc.want)
		}
	}
}

// ---- helpers ----

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// registerFakeAuth simulates the side effect Auth.config has at
// runtime so ValidateProductionConfig sees a registered Auth.
// Calling RegisterAuth with a zero VAuth is enough — the validator
// only checks `CurrentAuth() != nil`.
func registerFakeAuth() {
	runtime.RegisterAuth(runtime.VAuth{})
}
