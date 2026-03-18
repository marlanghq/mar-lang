package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestComputeEditorGitSigns(t *testing.T) {
	base := []string{
		"app Todo",
		"port 4100",
		"database \"todo.db\"",
		"entity Todo {",
		"  title: String",
		"}",
	}
	current := []string{
		"app Todo",
		"port 4100",
		"database \"todo.db\"",
		"entity Todo {",
		"  title: String",
		"  done: Bool",
		"}",
	}

	signs := computeEditorGitSigns(base, current)
	if got := signs[5]; got != '+' {
		t.Fatalf("expected line 6 to be marked as addition, got %q", got)
	}
}

func TestComputeEditorGitSignsModification(t *testing.T) {
	base := []string{
		"title: String",
	}
	current := []string{
		"title: String optional",
	}

	signs := computeEditorGitSigns(base, current)
	if got := signs[0]; got != '~' {
		t.Fatalf("expected modified line to be marked as ~, got %q", got)
	}
}

func TestComputeEditorGitSignsDeletion(t *testing.T) {
	base := []string{
		"one",
		"two",
		"three",
	}
	current := []string{
		"one",
		"three",
	}

	signs := computeEditorGitSigns(base, current)
	if got := signs[1]; got != '-' {
		t.Fatalf("expected deletion marker on line 2, got %q", got)
	}
}

func TestEditorSaveFormatsOnSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todo.mar")

	editor := &marEditor{
		filePath: path,
		lines: []string{
			"app TodoApi",
			"entity Todo{",
			"title:String",
			"}",
		},
		savedLines: []string{""},
	}

	if err := editor.save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file failed: %v", err)
	}

	expected := "" +
		"app TodoApi\n" +
		"entity Todo {\n" +
		"  title: String\n" +
		"}\n"

	if string(content) != expected {
		t.Fatalf("unexpected saved content\n--- expected ---\n%s\n--- got ---\n%s", expected, string(content))
	}
	if editor.lines[1] != "entity Todo {" {
		t.Fatalf("expected editor buffer to be updated with formatted content, got %#v", editor.lines)
	}
	if !strings.Contains(editor.status, "Saved todo.mar") {
		t.Fatalf("expected save status message, got %q", editor.status)
	}
}

func TestEditorSaveFallsBackWhenFormatFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "todo.mar")

	editor := &marEditor{
		filePath: path,
		lines: []string{
			"app TodoApi",
			"entity Todo {",
			"  title String",
			"}",
		},
		savedLines: []string{""},
	}

	if err := editor.save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file failed: %v", err)
	}

	expected := "" +
		"app TodoApi\n" +
		"entity Todo {\n" +
		"  title String\n" +
		"}\n"

	if string(content) != expected {
		t.Fatalf("unexpected saved fallback content\n--- expected ---\n%s\n--- got ---\n%s", expected, string(content))
	}
	if !strings.Contains(editor.status, "format failed:") {
		t.Fatalf("expected format failure status message, got %q", editor.status)
	}
}

func TestEditorStatusBarLeftTextPrefersRecentStatus(t *testing.T) {
	editor := &marEditor{
		filePath:   "todo.mar",
		status:     "Saved todo.mar (format failed: line 3: unknown statement)",
		statusTime: time.Now(),
	}

	if got := editor.statusBarLeftText(); !strings.Contains(got, "format failed:") {
		t.Fatalf("expected recent status to be shown in status bar, got %q", got)
	}
}

func TestEditorStatusBarLeftTextFallsBackToFilename(t *testing.T) {
	editor := &marEditor{
		filePath:   "todo.mar",
		dirty:      true,
		status:     "Old message",
		statusTime: time.Now().Add(-11 * time.Second),
	}

	if got := editor.statusBarLeftText(); got != "todo.mar (modified)" {
		t.Fatalf("expected filename fallback in status bar, got %q", got)
	}
}
