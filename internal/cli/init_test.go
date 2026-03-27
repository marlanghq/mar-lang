package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectNameToAppName(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"todo":       "Todo",
		"my-app":     "MyApp",
		"my_app":     "MyApp",
		"PocketBase": "PocketBase",
	}

	for input, want := range cases {
		if got := projectNameToAppName(input); got != want {
			t.Fatalf("projectNameToAppName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCreateInitProjectCreatesStarterFiles(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	result, err := createInitProject(tmpDir, "todo-app")
	if err != nil {
		t.Fatalf("createInitProject returned error: %v", err)
	}

	if got, want := result.AppName, "TodoApp"; got != want {
		t.Fatalf("unexpected app name: got %q want %q", got, want)
	}

	marSource, err := os.ReadFile(filepath.Join(tmpDir, "todo-app", "todo-app.mar"))
	if err != nil {
		t.Fatalf("read .mar file: %v", err)
	}
	marText := string(marSource)
	if !strings.Contains(marText, "app TodoApp") {
		t.Fatalf("expected app declaration in starter .mar, got %q", marText)
	}
	if strings.Contains(marText, "\nport ") {
		t.Fatalf("did not expect explicit port in starter .mar, got %q", marText)
	}
	if strings.Contains(marText, "\ndatabase ") {
		t.Fatalf("did not expect explicit database in starter .mar, got %q", marText)
	}
	if strings.Contains(marText, "\n  id: ") {
		t.Fatalf("did not expect explicit id field in starter .mar, got %q", marText)
	}
	if !strings.Contains(marText, `authorize all when user_authenticated`) {
		t.Fatalf("expected starter authorize rule in .mar, got %q", marText)
	}
	if _, err := parseMarFile(filepath.Join(tmpDir, "todo-app", "todo-app.mar")); err != nil {
		t.Fatalf("expected starter .mar to parse, got error: %v", err)
	}

	gitIgnore, err := os.ReadFile(filepath.Join(tmpDir, "todo-app", ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	if !strings.Contains(string(gitIgnore), "*.db-wal") {
		t.Fatalf("expected SQLite ignore rules, got %q", string(gitIgnore))
	}

	readme, err := os.ReadFile(filepath.Join(tmpDir, "todo-app", "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	readmeText := string(readme)
	if !strings.Contains(readmeText, "mar dev todo-app.mar") {
		t.Fatalf("expected dev command in README, got %q", readmeText)
	}
	if !strings.Contains(readmeText, "mar compile todo-app.mar") {
		t.Fatalf("expected compile command in README, got %q", readmeText)
	}
}

func TestCreateInitProjectFailsWhenDirectoryExists(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	projectDir := filepath.Join(tmpDir, "todo-app")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := createInitProject(tmpDir, "todo-app")
	if err == nil {
		t.Fatal("expected error when project directory already exists")
	}
	styledErr, ok := err.(interface{ StyledCLI() string })
	if !ok {
		t.Fatalf("expected styled CLI error, got %T", err)
	}
	output := styledErr.StyledCLI()
	if !strings.Contains(output, "Project directory already exists") {
		t.Fatalf("expected styled title in output, got %q", output)
	}
	if !strings.Contains(output, "Hint:") {
		t.Fatalf("expected hint in output, got %q", output)
	}
	if !strings.HasSuffix(output, "\n") {
		t.Fatalf("expected trailing newline in output, got %q", output)
	}
}
