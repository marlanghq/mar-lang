// Command ctorgen rewrites the generated builtin-constructor registrations:
// the marked region inside internal/jsserve/runtime.js and the whole of
// internal/iosbundle/template/Sources/MarBuiltinCtors.swift. Run it via
//
//	go generate ./internal/ctorgen
//
// It locates the repo root by walking up to go.mod, so it works from any
// working directory.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"mar/internal/ctorgen"
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
		fmt.Fprintln(os.Stderr, "ctorgen:", err)
		os.Exit(1)
	}

	jsPath := filepath.Join(root, "internal", "jsserve", "runtime.js")
	js, err := os.ReadFile(jsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ctorgen:", err)
		os.Exit(1)
	}
	spliced, err := ctorgen.SpliceJS(string(js))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if spliced != string(js) {
		if err := os.WriteFile(jsPath, []byte(spliced), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ctorgen:", err)
			os.Exit(1)
		}
		fmt.Println("ctorgen: rewrote", jsPath)
	}

	goPath := filepath.Join(root, "internal", "runtime", "builtin_ctors_gen.go")
	wantGo := ctorgen.GoFile()
	haveGo, _ := os.ReadFile(goPath)
	if string(haveGo) != wantGo {
		if err := os.WriteFile(goPath, []byte(wantGo), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ctorgen:", err)
			os.Exit(1)
		}
		fmt.Println("ctorgen: rewrote", goPath)
	}

	swiftPath := filepath.Join(root, "internal", "iosbundle", "template", "Sources", "MarBuiltinCtors.swift")
	want := ctorgen.SwiftFile()
	have, _ := os.ReadFile(swiftPath)
	if string(have) != want {
		if err := os.WriteFile(swiftPath, []byte(want), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "ctorgen:", err)
			os.Exit(1)
		}
		fmt.Println("ctorgen: rewrote", swiftPath)
	}
}
