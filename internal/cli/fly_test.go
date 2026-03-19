package cli

import (
	"bufio"
	"bytes"
	"errors"
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

func TestResolveFlySecretValueUsesEnvironment(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "secret-123")

	got, err := resolveFlySecretValue("RESEND_API_KEY")
	if err != nil {
		t.Fatalf("resolveFlySecretValue returned error: %v", err)
	}
	if got != "secret-123" {
		t.Fatalf("unexpected secret value: %q", got)
	}
}

func TestReadMaskedSecretInputMasksCharactersAndSupportsBackspace(t *testing.T) {
	var out bytes.Buffer

	got, err := readMaskedSecretInput(strings.NewReader("secx\x7fret\r"), &out)
	if err != nil {
		t.Fatalf("readMaskedSecretInput returned error: %v", err)
	}
	if got != "secret" {
		t.Fatalf("unexpected secret value: %q", got)
	}

	rendered := out.String()
	if strings.Contains(rendered, "secret") {
		t.Fatalf("masked output should not contain the secret, got %q", rendered)
	}
	if !strings.Contains(rendered, "*") {
		t.Fatalf("expected masked output to contain asterisks, got %q", rendered)
	}
	if !strings.HasSuffix(rendered, "\n") {
		t.Fatalf("expected masked output to end with newline, got %q", rendered)
	}
}

func TestMaskFlyCLIArgsMasksSecretValuesInDisplay(t *testing.T) {
	got := maskFlyCLIArgs([]string{
		"secrets",
		"set",
		"RESEND_API_KEY=secret-123",
		"-a",
		"mar-lang-todo",
	})

	if got[2] != "RESEND_API_KEY=********" {
		t.Fatalf("expected secret to be masked, got %q", got[2])
	}
	if got[4] != "mar-lang-todo" {
		t.Fatalf("unexpected non-secret arg change: %q", got[4])
	}
}

func TestFormatFlyCLICommandErrorFriendlyAppNameTaken(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	err := formatFlyCLICommandError(
		false,
		"todo.mar",
		"Create app",
		[]string{"apps", "create", "mar-lang-todo"},
		"Validation failed: Name has already been taken",
		errors.New("exit status 1"),
	)

	if err == nil {
		t.Fatal("expected friendly error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Fly app name is already in use") {
		t.Fatalf("expected friendly title, got %q", msg)
	}
	if !strings.Contains(msg, "Fly.io already has an app named mar-lang-todo.") {
		t.Fatalf("expected app name explanation, got %q", msg)
	}
	if !strings.Contains(msg, "mar fly init todo.mar") {
		t.Fatalf("expected init guidance, got %q", msg)
	}
	if !strings.Contains(msg, "mar fly provision todo.mar") {
		t.Fatalf("expected provision guidance, got %q", msg)
	}
}

func TestConfirmFlyDestroyRequiresInteractiveTerminal(t *testing.T) {
	oldStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	_ = w.Close()
	os.Stdin = r
	defer func() {
		os.Stdin = oldStdin
	}()

	ok, err := confirmFlyDestroy("mar", "mar-lang-todo")
	if ok {
		t.Fatal("expected destroy confirmation to fail outside interactive terminal")
	}
	if err == nil {
		t.Fatal("expected interactive-terminal error")
	}
	if !strings.Contains(err.Error(), "Fly destroy requires confirmation") {
		t.Fatalf("unexpected error: %q", err.Error())
	}
}

func TestPromptYesNoRetriesUntilValidAnswer(t *testing.T) {
	var out bytes.Buffer
	reader := bufio.NewReader(strings.NewReader("kopdsa\nyes\n"))

	confirmed, err := promptYesNo(reader, &out, false, "Continue? [y/N]", false)
	if err != nil {
		t.Fatalf("promptYesNo returned error: %v", err)
	}
	if !confirmed {
		t.Fatal("expected promptYesNo to accept yes after invalid input")
	}

	rendered := out.String()
	if !strings.Contains(rendered, "Please answer yes or no.") {
		t.Fatalf("expected retry hint after invalid input, got %q", rendered)
	}
}

func TestReadMaskedSecretInputTreatsCtrlCAsInterrupt(t *testing.T) {
	var out bytes.Buffer

	_, err := readMaskedSecretInput(strings.NewReader("abc\x03"), &out)
	if !errors.Is(err, errFlySecretPromptInterrupted) {
		t.Fatalf("expected interrupt error, got %v", err)
	}
}

func TestParseFlyDeployArgsSupportsOptionalYes(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantPath  string
		wantYes   bool
		wantValid bool
	}{
		{name: "path only", args: []string{"todo.mar"}, wantPath: "todo.mar", wantYes: false, wantValid: true},
		{name: "yes after path", args: []string{"todo.mar", "--yes"}, wantPath: "todo.mar", wantYes: true, wantValid: true},
		{name: "yes before path", args: []string{"--yes", "todo.mar"}, wantPath: "todo.mar", wantYes: true, wantValid: true},
		{name: "missing path", args: []string{"--yes"}, wantValid: false},
		{name: "too many args", args: []string{"todo.mar", "--yes", "extra"}, wantValid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, gotYes, gotValid := parseFlyDeployArgs(tt.args)
			if gotPath != tt.wantPath || gotYes != tt.wantYes || gotValid != tt.wantValid {
				t.Fatalf("parseFlyDeployArgs(%q) = (%q, %v, %v), want (%q, %v, %v)", tt.args, gotPath, gotYes, gotValid, tt.wantPath, tt.wantYes, tt.wantValid)
			}
		})
	}
}
