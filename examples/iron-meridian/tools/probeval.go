//go:build ignore

// Evaluate one top-level String value from a Mar module in the Go runtime.
//
// Used to run scenario probes against the game's REAL simulation functions
// rather than against a re-implementation of them: `probeval.go Probe.mar
// Probe.report` loads the module and everything it imports, evaluates
// `Probe.report`, and prints it.
//
// Carries //go:build ignore so `go build ./...` and `go vet ./...` never see
// it; naming it on the `go run` line is what builds it. Same arrangement as
// tunedump.go next door.
package main

import (
	"fmt"
	"os"

	"mar/internal/project"
	"mar/internal/runtime"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run tools/probeval.go <entry.mar> <Module.value>")
		os.Exit(2)
	}
	entry, want := os.Args[1], os.Args[2]

	// Type-checks on the way in, which is what ELABORATES the tree the
	// evaluator loads: a parse-only load is quietly wrong rather than
	// rejected. See ADR 0017.
	rEnv, _, err := project.LoadIntoEnvForRuntime(entry, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}

	v, ok := rEnv.Lookup(want)
	if !ok {
		fmt.Fprintf(os.Stderr, "no such value: %s\n", want)
		os.Exit(1)
	}
	s, ok := v.(runtime.VString)
	if !ok {
		fmt.Fprintf(os.Stderr, "%s is %T, want a String\n", want, v)
		os.Exit(1)
	}
	fmt.Println(s.V)
}
