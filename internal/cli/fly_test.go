package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/model"
)

func TestSlugifyFlyAppName(t *testing.T) {
	got := slugifyFlyAppName("BookStore Admin")
	if got != "book-store-admin" {
		t.Fatalf("unexpected slug: %q", got)
	}
}

func TestResolveFlyDatabasePathsUsesDataMount(t *testing.T) {
	local, fly := resolveFlyDatabasePaths("todo.db", "todo")
	if local != "todo.db" {
		t.Fatalf("unexpected local path: %q", local)
	}
	if fly != "/data/todo.db" {
		t.Fatalf("unexpected fly path: %q", fly)
	}
}

func TestLooksLikePlaceholderEmail(t *testing.T) {
	if !looksLikePlaceholderEmail("no-reply@yourdomain.com") {
		t.Fatal("expected placeholder email to be detected")
	}
	if looksLikePlaceholderEmail("no-reply@segunda.tech") {
		t.Fatal("did not expect real email domain to be treated as placeholder")
	}
}

func TestFindFlyRegion(t *testing.T) {
	region, ok := findFlyRegion("gru")
	if !ok {
		t.Fatal("expected gru to be a valid Fly region")
	}
	if region.Name != "Sao Paulo, Brazil" {
		t.Fatalf("unexpected region name: %q", region.Name)
	}
}

func TestFlyDirHasContents(t *testing.T) {
	tempDir := t.TempDir()
	if flyDirHasContents(tempDir) {
		t.Fatal("expected empty temp dir to be treated as empty")
	}
	if err := os.WriteFile(filepath.Join(tempDir, "fly.toml"), []byte("app = \"todo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !flyDirHasContents(tempDir) {
		t.Fatal("expected dir with files to be treated as non-empty")
	}
}

func TestRequireFlyDeployFilesReturnsStyledErrorWhenMissing(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	err := requireFlyDeployFiles("/tmp/missing-dockerfile", "/tmp/missing-fly-toml")
	if err == nil {
		t.Fatal("expected missing deploy files error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "Fly deploy files are missing") {
		t.Fatalf("expected deploy files title, got %q", msg)
	}
	if !strings.Contains(msg, "Run: mar fly init <app.mar>") {
		t.Fatalf("expected init hint, got %q", msg)
	}
}

func TestFormatFlyInitNoteHighlightsSMTPPasswordEnv(t *testing.T) {
	note := "SMTP password will be read from the RESEND_API_KEY environment variable at runtime."
	result := flyInitResult{SMTPPasswordEnv: "RESEND_API_KEY"}

	got := formatFlyInitNote(true, result, note)

	if !strings.Contains(got, "\033[1;36mRESEND_API_KEY\033[0m") {
		t.Fatalf("expected SMTPPasswordEnv to be cyan, got %q", got)
	}
}

func TestValidateFlyInitPrereqsBlocksPlaceholderEmailBeforePrompts(t *testing.T) {
	err := validateFlyInitPrereqs(&model.App{
		Auth: &model.AuthConfig{
			EmailFrom: "no-reply@mar.local",
		},
	})
	if err == nil {
		t.Fatal("expected placeholder email to block fly init")
	}
	if !strings.Contains(err.Error(), "Fly init blocked") {
		t.Fatalf("expected fly init blocked message, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "auth.email_from is still using a placeholder value") {
		t.Fatalf("expected placeholder email explanation, got %q", err.Error())
	}
}
