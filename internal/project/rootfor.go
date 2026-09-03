package project

import (
	"os"
	"path/filepath"
)

// RootFor answers "which project does this file belong to", and is the only
// place that answers it.
//
// It exists because there were two answers. The LSP had walked up looking for
// a project marker since early on -- its comment describes the exact bug,
// every cross-module name surfacing as unknown -- and the CLI's `mar check`
// did not, so the same file produced a type error the project did not have.
// Fixing the CLI a moment ago created the second implementation rather than
// removing the difference, and the two did not agree:
//
//	the editor   mar.json OR Main.mar marks a project; stops after 20 levels
//	the CLI      mar.json only; stops at the filesystem root
//
// A directory holding Main.mar and no mar.json is a project to `mar check .`
// and to the editor, and was not one to `mar check Main.mar`. Measured: two
// pages on `/` were refused by the first two and passed by the third.
//
// Main.mar is a marker because a project without a manifest is a real shape --
// `mar check .` loads one -- and mar.json is preferred because it is the
// project's own declaration of where its root is.
//
// The 20-level bound the LSP carried is gone. The loop already stops when it
// reaches the filesystem root, which is what the bound was guarding against,
// and a file nested deeper than twenty directories was silently getting no
// project at all.
func RootFor(path string) (string, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	dir := abs
	if info, err := os.Stat(abs); err != nil || !info.IsDir() {
		dir = filepath.Dir(abs)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "mar.json")); err == nil {
			return dir, true
		}
		if _, err := os.Stat(filepath.Join(dir, "Main.mar")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}
