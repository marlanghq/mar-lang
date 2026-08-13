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
// check: projects without auth ship without these fields, that's
// fine.
func TestValidateProductionConfig_NoAuthSkips(t *testing.T) {
	runtime.ResetAuthForTesting()
	t.Cleanup(runtime.ResetAuthForTesting)

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mar.json"), `{"name":"no-auth-app","locale":"en"}`)

	if err := ValidateProductionConfig(dir); err != nil {
		t.Fatalf("expected no error when auth isn't registered; got %v", err)
	}
}

// TestValidateProductionConfig_AuthRequiresMail confirms the error
// path. When auth is in use but mar.json doesn't declare auth +
// mail, the build fails with copy-pasteable hints.
func TestValidateProductionConfig_AuthRequiresMail(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mar.json"), `{"name":"missing-mail","locale":"en"}`)
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
// silently ignored. Regression guard for the dev≠build gap: `mar dev`
// always rejected this, but build + check used to wave it through.
func TestBuild_RejectsUnknownManifestField(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Main.mar"),
		"module Main exposing (main)\n\nmain : Cmd ()\nmain = App.frontend []\n")
	// "port" belongs under "server", not at the top level: the exact
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
  "locale": "en",
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
// mar.json passes validation. smtpPort omitted on purpose: the
// default makes it optional.
func TestValidateProductionConfig_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "mar.json"), `{
  "name": "happy",
  "locale": "en",
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

// TestFlyDeployEvalSequence_NoDuplicateEntity guards the `mar deploy`
// (Fly path) double-evaluation bug. The deploy runs main twice in one process:
// Topology() to pick the Dockerfile shape, then Build() to compile, and
// both go through loadAndRunForBuild. That helper must reset the global
// entity registry before evaluating; without it, the second eval
// re-registers every Entity.define and the build dies with
// "Entity.define: name ... declared more than once in this build".
// guestbook is a minimal App.fullstack with one entity ("entries") and
// no production config, so it exercises the registry without secrets.
func TestFlyDeployEvalSequence_NoDuplicateEntity(t *testing.T) {
	t.Cleanup(runtime.ResetForReload)

	dir, err := filepath.Abs(filepath.Join("..", "..", "examples", "guestbook"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Main.mar")); err != nil {
		t.Skipf("guestbook fixture missing: %v", err)
	}

	// Eval #1: the same call mar deploy makes to detect topology.
	if _, err := Topology(dir); err != nil {
		t.Fatalf("Topology (eval #1) failed: %v", err)
	}
	// Eval #2: the compile step. Pre-fix this tripped the duplicate-
	// entity guard because the registry still held eval #1's entities.
	if err := Build(dir, t.TempDir(), ""); err != nil {
		t.Fatalf("Build after Topology must not trip the duplicate-entity guard: %v", err)
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
// Calling RegisterAuth with a zero VAuth is enough: the validator
// only checks `CurrentAuth() != nil`.
func registerFakeAuth() {
	runtime.RegisterAuth(runtime.VAuth{})
}

// TestProductionShellDeclaresColorScheme guards a dev≠prod gap that
// shipped once: the `mar dev` shell grew the color-scheme and
// theme-color metas, the `mar build` shell did not, and deployed apps
// quietly behaved differently from the ones they were developed as.
//
// theme-color is the one with teeth. The strip above the page on iOS
// Safari is browser chrome: env(safe-area-inset-top) reads 0 there,
// so the frosted nav bar cannot reach it and its color is only ever
// what this meta says. Undeclared, Safari falls back to the PWA
// manifest's theme_color and paints a flat band over a light page.
func TestProductionShellDeclaresColorScheme(t *testing.T) {
	for _, want := range []string{
		`<meta name="color-scheme" content="light dark">`,
		`<meta name="theme-color" content="#f7f7f9" media="(prefers-color-scheme: light)">`,
		`<meta name="theme-color" content="#232326" media="(prefers-color-scheme: dark)">`,
		// The pre-runtime dark baseline, so a dark-mode reload does not
		// flash white before the bundle parses.
		`@media (prefers-color-scheme: dark)`,
		`html { background-color: #161618; }`,
	} {
		if !strings.Contains(productionPageHTML, want) {
			t.Errorf("production shell is missing %s", want)
		}
	}
}

// TestProductionShellCarriesTheLocale guards the same dev≠prod gap for
// the app's language: both shells have to write the manifest's tag as
// <html lang>, because that attribute is what a screen reader picks
// its voice from. Hardcoding "en" here (which is what both shells did
// before) makes an app in Portuguese get read aloud in English.
func TestProductionShellCarriesTheLocale(t *testing.T) {
	if !strings.Contains(productionPageHTML, `<html lang="%s">`) {
		t.Fatal("production shell does not take a locale for <html lang>")
	}
	html := buildIndexHTML("pt-BR", "Diario", []byte("{}"))
	if !strings.Contains(html, `<html lang="pt-BR">`) {
		t.Errorf("rendered shell missing the locale:\n%s", html[:200])
	}
	if !strings.Contains(html, "<title>Diario</title>") {
		t.Error("locale and title are swapped: the title did not land")
	}
}
