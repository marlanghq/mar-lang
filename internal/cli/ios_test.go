package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIOSOutputDir(t *testing.T) {
	got := defaultIOSOutputDir("examples/store.mar", "")
	if got != filepath.Join("dist", "store-ios") {
		t.Fatalf("unexpected default ios output dir: %q", got)
	}
}

func TestRunIOSGenerateCreatesProject(t *testing.T) {
	tempDir := t.TempDir()
	appPath := filepath.Join(tempDir, "todo.mar")
	source := `
app TodoApi

ios {
  bundle_identifier "com.example.todo"
  server_url "https://school.example.com"
}

entity Todo {
  title: String
}
`
	if err := os.WriteFile(appPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write app failed: %v", err)
	}

	outputDir := filepath.Join(tempDir, "generated-ios")
	if err := runIOSGenerate("mar", []string{appPath, outputDir}); err != nil {
		t.Fatalf("runIOSGenerate failed: %v", err)
	}

	projectBytes, err := os.ReadFile(filepath.Join(outputDir, "MarRuntimeIOS.xcodeproj", "project.pbxproj"))
	if err != nil {
		t.Fatalf("read project failed: %v", err)
	}
	project := string(projectBytes)
	if !strings.Contains(project, `PRODUCT_BUNDLE_IDENTIFIER = "com.example.todo";`) {
		t.Fatalf("expected generated bundle identifier, got:\n%s", project)
	}
	if !strings.Contains(project, `INFOPLIST_KEY_CFBundleDisplayName = "TodoApi";`) {
		t.Fatalf("expected display name fallback from app name, got:\n%s", project)
	}
	if !strings.Contains(project, `INFOPLIST_KEY_UISupportedInterfaceOrientations_iPad = "UIInterfaceOrientationPortrait UIInterfaceOrientationPortraitUpsideDown UIInterfaceOrientationLandscapeLeft UIInterfaceOrientationLandscapeRight";`) {
		t.Fatalf("expected iPad orientations in generated project, got:\n%s", project)
	}

	if _, err := os.Stat(filepath.Join(outputDir, "Sources", "Views.swift")); err != nil {
		t.Fatalf("expected generated source files: %v", err)
	}
	viewsBytes, err := os.ReadFile(filepath.Join(outputDir, "Sources", "Views.swift"))
	if err != nil {
		t.Fatalf("read views failed: %v", err)
	}
	if !strings.Contains(string(viewsBytes), `Section("Request Logs")`) {
		t.Fatalf("expected request logs section in generated views, got:\n%s", string(viewsBytes))
	}
	appViewModelBytes, err := os.ReadFile(filepath.Join(outputDir, "Sources", "AppViewModel.swift"))
	if err != nil {
		t.Fatalf("read app view model failed: %v", err)
	}
	if !strings.Contains(string(appViewModelBytes), `private let generatedServerURL = "https://school.example.com"`) {
		t.Fatalf("expected generated server url in app template, got:\n%s", string(appViewModelBytes))
	}
	modelsBytes, err := os.ReadFile(filepath.Join(outputDir, "Sources", "Models.swift"))
	if err != nil {
		t.Fatalf("read models failed: %v", err)
	}
	if !strings.Contains(string(modelsBytes), "var summaryFields: [Field]") {
		t.Fatalf("expected summaryFields helper in generated models, got:\n%s", string(modelsBytes))
	}
	rowPresentationBytes, err := os.ReadFile(filepath.Join(outputDir, "Sources", "RowPresentation.swift"))
	if err != nil {
		t.Fatalf("read row presentation failed: %v", err)
	}
	if !strings.Contains(string(rowPresentationBytes), "return entity.displayName") {
		t.Fatalf("expected friendly row title fallback in generated row presentation helpers, got:\n%s", string(rowPresentationBytes))
	}
	if _, err := os.Stat(filepath.Join(outputDir, "Sources", "MarDateCodec.swift")); err != nil {
		t.Fatalf("expected generated MarDateCodec source file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "Sources", "PayloadEncoder.swift")); err != nil {
		t.Fatalf("expected generated PayloadEncoder source file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "Sources", "SessionStore.swift")); err != nil {
		t.Fatalf("expected generated SessionStore source file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "README.generated.md")); err != nil {
		t.Fatalf("expected generated readme: %v", err)
	}
}

func TestRunIOSGenerateRequiresIOSBlock(t *testing.T) {
	tempDir := t.TempDir()
	appPath := filepath.Join(tempDir, "todo.mar")
	source := `
app TodoApi

entity Todo {
  title: String
}
`
	if err := os.WriteFile(appPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write app failed: %v", err)
	}

	err := runIOSGenerate("mar", []string{appPath, filepath.Join(tempDir, "generated-ios")})
	if err == nil {
		t.Fatal("expected ios block error")
	}
	if !strings.Contains(err.Error(), "iOS generation error") {
		t.Fatalf("expected styled ios generation title, got %v", err)
	}
	if !strings.Contains(err.Error(), "requires an ios block") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), "bundle_identifier") {
		t.Fatalf("expected bundle_identifier to be called out, got %v", err)
	}
	if !strings.Contains(err.Error(), "server_url") {
		t.Fatalf("expected server_url to be called out, got %v", err)
	}
	if !strings.Contains(err.Error(), appPath) {
		t.Fatalf("expected source file path to be called out, got %v", err)
	}
	if !strings.Contains(err.Error(), "bundle_identifier \"com.example.school\"") {
		t.Fatalf("expected ios hint example, got %v", err)
	}
	if !strings.Contains(err.Error(), "server_url \"https://school.example.com\"") {
		t.Fatalf("expected ios hint example, got %v", err)
	}
}

func TestRunIOSGenerateRequiresConfirmationBeforeDeletingExistingProject(t *testing.T) {
	tempDir := t.TempDir()
	appPath := filepath.Join(tempDir, "todo.mar")
	source := `
app TodoApi

ios {
  bundle_identifier "com.example.todo"
  server_url "https://school.example.com"
}

entity Todo {
  title: String
}
`
	if err := os.WriteFile(appPath, []byte(source), 0o644); err != nil {
		t.Fatalf("write app failed: %v", err)
	}

	outputDir := filepath.Join(tempDir, "generated-ios")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		t.Fatalf("mkdir output dir failed: %v", err)
	}
	sentinelPath := filepath.Join(outputDir, "keep.txt")
	if err := os.WriteFile(sentinelPath, []byte("manual change"), 0o644); err != nil {
		t.Fatalf("write sentinel failed: %v", err)
	}

	oldStdin := os.Stdin
	r, w, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatalf("pipe failed: %v", pipeErr)
	}
	_ = w.Close()
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = oldStdin
	})

	err := runIOSGenerate("mar", []string{appPath, outputDir})
	if err == nil {
		t.Fatal("expected confirmation error outside interactive terminal")
	}
	if !strings.Contains(err.Error(), "iOS generate requires confirmation") {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), filepath.ToSlash(outputDir)) {
		t.Fatalf("expected output dir in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "Any manual changes made inside it will be lost.") {
		t.Fatalf("expected manual changes warning, got %v", err)
	}
	if !strings.Contains(err.Error(), "Re-run this command in an interactive terminal to confirm the overwrite.") {
		t.Fatalf("expected interactive hint, got %v", err)
	}
	if _, statErr := os.Stat(sentinelPath); statErr != nil {
		t.Fatalf("expected existing files to remain untouched, got %v", statErr)
	}
}

func TestPrintIOSGenerateSummary(t *testing.T) {
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe failed: %v", err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = original
	})

	t.Setenv("NO_COLOR", "1")
	printIOSGenerateSummary(filepath.Join("dist", "mar-lang-school-ios"))

	_ = writer.Close()
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}

	got := string(output)
	if !strings.Contains(got, "iOS project ready") {
		t.Fatalf("expected summary title, got %q", got)
	}
	if !strings.Contains(got, "dist/mar-lang-school-ios") {
		t.Fatalf("expected output dir, got %q", got)
	}
	if !strings.Contains(got, "1. On macOS, open the Xcode project: open dist/mar-lang-school-ios/MarRuntimeIOS.xcodeproj") {
		t.Fatalf("expected project path in next steps, got %q", got)
	}
	if !strings.Contains(got, "2. Select your signing team.") {
		t.Fatalf("expected signing step, got %q", got)
	}
	if !strings.Contains(got, "3. Build and run on an iPhone, iPad, or Simulator.") {
		t.Fatalf("expected platform guidance, got %q", got)
	}
}
