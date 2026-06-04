package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyPublicDirRejectsReserved(t *testing.T) {
	src := t.TempDir()
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "api", "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := copyPublicDir(src, dist); err == nil {
		t.Fatal("expected error for public/api/x.txt, got nil")
	}
}

func TestCopyPublicDirCopiesNestedSkipsDotfiles(t *testing.T) {
	src := t.TempDir()
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "img"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(src, "logo.png"), "png")
	mustWrite(t, filepath.Join(src, "img", "a.jpg"), "jpg")
	mustWrite(t, filepath.Join(src, ".DS_Store"), "junk")

	n, err := copyPublicDir(src, dist)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("copied %d files, want 2 (dotfile skipped)", n)
	}
	for _, p := range []string{"logo.png", filepath.Join("img", "a.jpg")} {
		if _, err := os.Stat(filepath.Join(dist, p)); err != nil {
			t.Errorf("expected %s in dist: %v", p, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dist, ".DS_Store")); !os.IsNotExist(err) {
		t.Error(".DS_Store should have been skipped")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
