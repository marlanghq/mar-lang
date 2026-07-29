// Command refgen writes the generated Mar stdlib reference data module
// (examples/mar-website/Frontend/Reference/Data.mar) from the compiler's type
// schemes plus the authored descriptions. Run it via
//
//	go generate ./internal/refgen
//
// It locates the repo root by walking up to go.mod, so it works from any
// working directory.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"mar/internal/refgen"
)

func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", dir)
		}
		dir = parent
	}
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "refgen:", err)
		os.Exit(1)
	}
	path := filepath.Join(root, filepath.FromSlash(refgen.DataMarRelPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "refgen:", err)
		os.Exit(1)
	}
	want := refgen.MarModule()
	have, _ := os.ReadFile(path)
	if string(have) != want {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "refgen:", err)
			os.Exit(1)
		}
		fmt.Println("refgen: rewrote", path)
	}
}
