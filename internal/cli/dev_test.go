package cli

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestResolveDevDatabaseOverrideUsesMarDirectory(t *testing.T) {
	t.Parallel()

	projectDir := filepath.Join(t.TempDir(), "todo")
	sourcePath := filepath.Join(projectDir, "todo.mar")

	got := resolveDevDatabaseOverride(sourcePath, "todo.db")
	want := filepath.Join(projectDir, "todo.db")
	if got != want {
		t.Fatalf("unexpected database override path:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestResolveDevDatabaseOverrideKeepsNestedRelativeDatabaseUnderMarDirectory(t *testing.T) {
	t.Parallel()

	projectDir := filepath.Join(t.TempDir(), "todo")
	sourcePath := filepath.Join(projectDir, "todo.mar")

	got := resolveDevDatabaseOverride(sourcePath, filepath.Join("data", "todo.db"))
	want := filepath.Join(projectDir, "data", "todo.db")
	if got != want {
		t.Fatalf("unexpected nested database override path:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestResolveDevDatabaseOverrideSkipsAbsoluteDatabasePath(t *testing.T) {
	t.Parallel()

	sourcePath := filepath.Join(t.TempDir(), "todo.mar")
	absoluteDatabasePath := filepath.Join(t.TempDir(), "todo.db")

	got := resolveDevDatabaseOverride(sourcePath, absoluteDatabasePath)
	if got != "" {
		t.Fatalf("expected no override for absolute database path, got %q", got)
	}
}

func TestWaitForDevServerStopsWhenProcessExits(t *testing.T) {
	t.Parallel()

	done := make(chan error, 1)
	done <- errors.New("startup failed")

	ready, exited, err := waitForDevServer("http://127.0.0.1:1/health", 200*time.Millisecond, done)
	if ready {
		t.Fatal("expected server to be reported as not ready")
	}
	if !exited {
		t.Fatal("expected process exit to be reported")
	}
	if err == nil || err.Error() != "startup failed" {
		t.Fatalf("expected startup error to be returned, got %v", err)
	}
}
