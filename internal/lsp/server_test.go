package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFindProjectRoot covers the walk-up-the-tree logic that
// projectAnalyze uses to find a Main.mar / mar.json no matter how
// deep the active file is in the project tree.
func TestFindProjectRoot(t *testing.T) {
	tmp := t.TempDir()

	// Layout:
	//   tmp/
	//     mar.json
	//     Main.mar
	//     Backend/
	//       Notes.mar         ← editing this
	//     Frontend/
	//       Pages/
	//         Home.mar        ← or this (deeper)
	mustWriteFile(t, filepath.Join(tmp, "mar.json"), `{"name":"x"}`)
	mustWriteFile(t, filepath.Join(tmp, "Main.mar"), "module Main")
	mustWriteFile(t, filepath.Join(tmp, "Backend", "Notes.mar"), "module Backend.Notes")
	mustWriteFile(t, filepath.Join(tmp, "Frontend", "Pages", "Home.mar"), "module Frontend.Pages.Home")

	cases := []struct {
		name string
		from string
		want string
	}{
		{"from project root", tmp, tmp},
		{"from Backend/", filepath.Join(tmp, "Backend"), tmp},
		{"from Frontend/Pages/", filepath.Join(tmp, "Frontend", "Pages"), tmp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := findProjectRoot(tc.from)
			if !ok {
				t.Fatalf("expected to find root from %s; got false", tc.from)
			}
			if got != tc.want {
				t.Errorf("from %s: got %s, want %s", tc.from, got, tc.want)
			}
		})
	}
}

// TestFindProjectRoot_NoMarker — random tmp dir without mar.json /
// Main.mar returns ("", false) so the LSP falls back to single-file
// analysis instead of running project mode against an unrelated tree.
func TestFindProjectRoot_NoMarker(t *testing.T) {
	tmp := t.TempDir()
	mustWriteFile(t, filepath.Join(tmp, "random.mar"), "module Random")

	if _, ok := findProjectRoot(tmp); ok {
		t.Errorf("expected no project root in unmarked dir; got ok=true")
	}
}

// TestFindProjectRoot_MainMarOnly — projects without mar.json (just
// Main.mar at the root) are also detected. Useful for `mar dev path`
// invocations on simple example projects.
func TestFindProjectRoot_MainMarOnly(t *testing.T) {
	tmp := t.TempDir()
	mustWriteFile(t, filepath.Join(tmp, "Main.mar"), "module Main")
	mustWriteFile(t, filepath.Join(tmp, "Helpers", "X.mar"), "module Helpers.X")

	got, ok := findProjectRoot(filepath.Join(tmp, "Helpers"))
	if !ok || got != tmp {
		t.Errorf("expected %s; got %s ok=%v", tmp, got, ok)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAnalyze_MultiModuleProject — end-to-end smoke against the
// daily-checklist example. Mirrors what the LSP server does when the
// editor opens Main.mar: read the file, hand it to `analyze` with
// the path, expect zero diagnostics.
//
// A user-visible regression — "unknown qualified name:
// Backend.Users.users" on a working project — would surface here as
// a non-empty diags slice. Earlier this came back as a false positive
// when the LSP fell through to single-file analysis instead of
// running through projectAnalyze; the test ensures we stay in the
// project-mode path for any example with a mar.json next to the
// active file.
func TestAnalyze_MultiModuleProject(t *testing.T) {
	wd, _ := os.Getwd()
	mainFile := filepath.Join(wd, "..", "..", "examples/daily-checklist/Main.mar")
	abs, err := filepath.Abs(mainFile)
	if err != nil {
		t.Fatalf("filepath.Abs: %v", err)
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read %s: %v", abs, err)
	}
	s := &Server{}
	diags := s.analyze(abs, string(content))
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics for daily-checklist/Main.mar; got %d:\n%+v",
			len(diags), diags)
	}
}
