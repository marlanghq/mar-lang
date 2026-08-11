// Command mathgen rewrites the generated sine table: the whole of
// internal/runtime/math_table_gen.go, the marked region inside
// internal/jsserve/runtime.js, and the whole of
// internal/iosbundle/template/Sources/MarMathTable.swift. Run it via
//
//	go generate ./internal/mathgen
//
// It locates the repo root by walking up to go.mod, so it works from any
// working directory.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"mar/internal/mathgen"
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

func write(path, want string) {
	have, _ := os.ReadFile(path)
	if string(have) == want {
		return
	}
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "mathgen:", err)
		os.Exit(1)
	}
	fmt.Println("mathgen: rewrote", path)
}

func main() {
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mathgen:", err)
		os.Exit(1)
	}

	jsPath := filepath.Join(root, "internal", "jsserve", "runtime.js")
	js, err := os.ReadFile(jsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mathgen:", err)
		os.Exit(1)
	}
	spliced, err := mathgen.SpliceJS(string(js))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	write(jsPath, spliced)

	write(filepath.Join(root, "internal", "runtime", "math_table_gen.go"), mathgen.GoFile())
	write(filepath.Join(root, "internal", "iosbundle", "template", "Sources", "MarMathTable.swift"), mathgen.SwiftFile())
}
