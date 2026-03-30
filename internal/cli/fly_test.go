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

func TestFlyVolumeNameFromOutputNameReplacesHyphens(t *testing.T) {
	got := flyVolumeNameFromOutputName("sample-school")
	if got != "sample_school_data" {
		t.Fatalf("unexpected volume name: %q", got)
	}
}

func TestFlyVolumeNameFromOutputNameTruncatesToFlyLimit(t *testing.T) {
	got := flyVolumeNameFromOutputName("very-long-project-name-that-exceeds-thirty-characters")
	if got != "very_long_project_name_th_data" {
		t.Fatalf("unexpected truncated volume name: %q", got)
	}
	if !isValidFlyVolumeName(got) {
		t.Fatalf("expected valid fly volume name, got %q", got)
	}
}

func TestFlyVolumeNameFromOutputNameFallsBackWhenEmpty(t *testing.T) {
	got := flyVolumeNameFromOutputName("!!!")
	if got != "mar_app_data" {
		t.Fatalf("unexpected fallback volume name: %q", got)
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
	if !strings.Contains(msg, "Before you can deploy to Fly.io, you need to generate the deployment configuration files.") {
		t.Fatalf("expected detailed deploy hint, got %q", msg)
	}
	if !strings.Contains(msg, "Run: mar fly init <app.mar>") {
		t.Fatalf("expected init hint, got %q", msg)
	}
}

func TestRunFlyLogsRequiresFlyConfigFile(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "todo.mar")
	source := strings.Join([]string{
		"app Todo",
		"",
		"entity Todo {",
		"  title: String",
		"}",
		"",
	}, "\n")
	if err := os.WriteFile(inputPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write .mar file: %v", err)
	}

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(oldWD)
	}()

	err = runFlyLogs(inputPath)
	if err == nil {
		t.Fatal("expected missing fly config to fail")
	}

	msg := err.Error()
	if !strings.Contains(msg, "Fly deploy config is missing") {
		t.Fatalf("expected missing config title, got %q", msg)
	}
	if !strings.Contains(msg, filepath.Join("deploy", "fly", "fly.toml")) {
		t.Fatalf("expected fly.toml path in error, got %q", msg)
	}
	if !strings.Contains(msg, "Run: mar fly init <app.mar>") {
		t.Fatalf("expected init hint, got %q", msg)
	}
}

func TestFormatFlyInitNoteIncludesSMTPPasswordEnv(t *testing.T) {
	note := "SMTP password will be read from the RESEND_API_KEY environment variable at runtime."
	result := flyInitResult{SMTPPasswordEnv: "RESEND_API_KEY"}

	got := formatFlyInitNote(true, result, note)

	if !strings.Contains(got, "RESEND_API_KEY") {
		t.Fatalf("expected SMTPPasswordEnv to appear in note, got %q", got)
	}
}

func TestValidateFlyInitPrereqsBlocksPlaceholderEmailBeforePrompts(t *testing.T) {
	err := validateFlyInitPrereqs(&model.App{
		Auth: &model.AuthConfig{
			EmailFrom: "no-reply@mar.local",
			EmailTransport: "smtp",
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

func TestValidateFlyInitPrereqsBlocksConsoleEmailTransport(t *testing.T) {
	err := validateFlyInitPrereqs(&model.App{
		Auth: &model.AuthConfig{
			EmailTransport: "console",
			EmailFrom:      "no-reply@segunda.tech",
		},
	})
	if err == nil {
		t.Fatal("expected console email transport to block fly init")
	}
	if !strings.Contains(err.Error(), "Fly init blocked") {
		t.Fatalf("expected fly init blocked message, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "auth.email_transport is set to console") {
		t.Fatalf("expected console transport explanation, got %q", err.Error())
	}
}

func TestValidateFlyAuthForDeployBlocksConsoleTransport(t *testing.T) {
	err := validateFlyAuthForDeploy(&model.App{
		Auth: &model.AuthConfig{
			EmailTransport: "console",
		},
	}, "Fly deploy blocked")
	if err == nil {
		t.Fatal("expected console email transport to block fly deploy")
	}
	if !strings.Contains(err.Error(), "Fly deploy blocked") {
		t.Fatalf("expected fly deploy blocked title, got %q", err.Error())
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

func TestPromptFlyAppMemoryUsesDefaultOnBlank(t *testing.T) {
	got, err := promptFlyAppMemory(bufio.NewReader(strings.NewReader("\n")), &bytes.Buffer{}, false)
	if err != nil {
		t.Fatalf("promptFlyAppMemory returned error: %v", err)
	}
	if got != "256mb" {
		t.Fatalf("expected default memory, got %q", got)
	}
}

func TestPromptFlyAppMemoryAcceptsNumberedChoice(t *testing.T) {
	got, err := promptFlyAppMemory(bufio.NewReader(strings.NewReader("3\n")), &bytes.Buffer{}, false)
	if err != nil {
		t.Fatalf("promptFlyAppMemory returned error: %v", err)
	}
	if got != "1gb" {
		t.Fatalf("expected numbered selection to resolve to 1gb, got %q", got)
	}
}

func TestPromptFlyAppMemoryRetriesAfterInvalidInput(t *testing.T) {
	var out bytes.Buffer

	got, err := promptFlyAppMemory(bufio.NewReader(strings.NewReader("99\n512mb\n")), &out, false)
	if err != nil {
		t.Fatalf("promptFlyAppMemory returned error: %v", err)
	}
	if got != "512mb" {
		t.Fatalf("expected retry selection to resolve to 512mb, got %q", got)
	}
	if !strings.Contains(out.String(), "Choose one of the listed options") {
		t.Fatalf("expected retry hint after invalid input, got %q", out.String())
	}
}

func TestRunFlyDestroyRejectsYesFlagInPlaceOfMarPath(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	err := runFly("mar", []string{"destroy", "--yes"})
	if err == nil {
		t.Fatal("expected usage error when --yes is passed to fly destroy")
	}

	msg := err.Error()
	if !strings.Contains(msg, "Fly usage") {
		t.Fatalf("expected fly usage message, got %q", msg)
	}
	if !strings.Contains(msg, "mar fly destroy <app.mar>") {
		t.Fatalf("expected destroy usage hint, got %q", msg)
	}
}

func TestRunFlyDestroyRequiresExistingMarFile(t *testing.T) {
	missingPath := filepath.Join(t.TempDir(), "missing.mar")

	err := runFlyDestroy("mar", missingPath)
	if err == nil {
		t.Fatal("expected missing .mar file to fail")
	}
	if !os.IsNotExist(err) {
		t.Fatalf("expected missing file error, got %v", err)
	}
}

func TestRenderFlyTomlUsesLeanDefaultVMConfig(t *testing.T) {
	got := renderFlyToml(flyInitResult{
		FlyAppName:  "mar-lang-todo",
		RegionCode:  "gru",
		Port:        4200,
		DatabaseFly: "/data/personal-todo.db",
		AppMemory:   "256mb",
		VolumeName:  "personal-todo_data",
	})

	if !strings.Contains(got, "  size = \"shared-cpu-1x\"\n") {
		t.Fatalf("expected shared cpu vm size, got:\n%s", got)
	}
	if !strings.Contains(got, "  memory = \"256mb\"\n") {
		t.Fatalf("expected lean default memory, got:\n%s", got)
	}
	if strings.Contains(got, "cpus = ") {
		t.Fatalf("did not expect explicit cpus line in lean template, got:\n%s", got)
	}
	if strings.Contains(got, "memory_mb = ") {
		t.Fatalf("did not expect memory_mb line in lean template, got:\n%s", got)
	}
}
