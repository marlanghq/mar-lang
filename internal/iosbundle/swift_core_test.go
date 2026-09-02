package iosbundle

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/jsserve"
	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// The iOS interpreter, driven from the command line.
//
// Everything Swift-side used to need a simulator: build the app with
// xcodebuild, install it, drive it. That is minutes per run, which is why the
// Swift half of a comparison was always the expensive half — and why the web
// half got tests the phone half did not.
//
// It turns out not to be necessary for the INTERPRETER. Of the template's
// Swift files, the ones that make up the interpreter — values, expressions,
// eval, env, the JSON codec, Decimal, the wire shapes, the loader, the
// builtins — import nothing but Foundation. Six symbols from the app around
// them are missing, and `tools/swiftcore/HostStubs.swift` supplies those.
//
// The stubs TRAP rather than answer, on purpose: a stub that returns something
// plausible is the lie the fake browser told for months. Reaching one means
// the probe left the interpreter, and it says so instead of passing.
//
// Assembling takes a copy and about six seconds of swiftc. That is cheap
// enough to compare the phone against the server on any pure value.
func TestTheSwiftInterpreterAgreesWithTheServer(t *testing.T) {
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc is not installed")
	}
	if testing.Short() {
		t.Skip("assembles and compiles the Swift core; not for -short")
	}

	dir := t.TempDir()
	if err := assembleSwiftCore(t, dir); err != nil {
		t.Fatalf("assembling the core: %v", err)
	}

	bin := filepath.Join(dir, "driver")
	build := exec.Command(swiftc, "-o", bin)
	sources, _ := filepath.Glob(filepath.Join(dir, "*.swift"))
	build.Args = append(build.Args, sources...)
	build.Dir = dir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("swiftc: %v\n%s", err, out)
	}

	// The program is compiled and serialized by THIS repo, so both sides read
	// the same bytes — the comparison is about the two evaluators, not about
	// two ways of writing a fixture.
	programJSON, values := swiftProbeProgram(t)
	progPath := filepath.Join(dir, "program.json")
	if err := os.WriteFile(progPath, programJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	for name, want := range values {
		t.Run(name, func(t *testing.T) {
			out, err := exec.Command(bin, progPath, "Main."+name).CombinedOutput()
			if err != nil {
				t.Fatalf("driver: %v\n%s", err, out)
			}
			got := strings.TrimSpace(string(out))
			if got != want {
				t.Errorf("Main.%s\n  server: %s\n  phone:  %s", name, want, got)
			}
		})
	}
}

// assembleSwiftCore copies the Foundation-only template sources plus the host
// stubs and the driver into dir.
func assembleSwiftCore(t *testing.T, dir string) error {
	t.Helper()
	// The imports that mean "this file is the app, not the interpreter".
	appOnly := []string{
		"import SwiftUI", "import UIKit", "import AVFoundation", "import Combine",
		"import Network", "import Observation", "import GameController",
		"import QuartzCore", "import Security",
	}
	srcDir := filepath.Join("template", "Sources")
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	copied := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".swift") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			return err
		}
		text := string(body)
		isApp := false
		for _, imp := range appOnly {
			if strings.Contains(text, imp+"\n") {
				isApp = true
				break
			}
		}
		if isApp {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), body, 0o644); err != nil {
			return err
		}
		copied++
	}
	if copied < 15 {
		t.Fatalf("only %d interpreter sources found; the import filter is wrong, not the template", copied)
	}
	for _, name := range []string{"HostStubs.swift", "main.swift"} {
		body, err := os.ReadFile(filepath.Join("..", "..", "tools", "swiftcore", name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// swiftProbeProgram builds the fixture and answers what the SERVER computes
// for each value, so the Swift side is compared against the other runtime
// rather than against a string written here by hand.
func swiftProbeProgram(t *testing.T) ([]byte, map[string]string) {
	t.Helper()
	const src = `module Main exposing (..)


note : Path { slug : String }
note =
    "/notes/{slug:String}"


-- The path encoder: three runtimes once had three answers for this.
link : String
link =
    linkTo note { slug = "a b/c" }


-- Decimal is exact on both sides or money is wrong on one of them.
money : String
money =
    Decimal.toString (1.50 + 2.25)


-- A document that is not JSON. This one carried Foundation's message until
-- an hour ago, and Foundation's message changes with the phone's language.
notJSON : String
notJSON =
    case JSON.decode "<html>" of
        Ok r -> r.x
        Err e -> e


-- A shape mismatch keeps ITS message, which is Mar's and not the host's.
wrongShape : String
wrongShape =
    case JSON.decode "{\"id\":\"1\"}" of
        Ok r -> String.fromInt r.id
        Err e -> e


-- Int is 53 bits on every runtime, not 64 on one of them.
bigInt : String
bigInt =
    String.fromInt (9007199254740991 - 1)


sorted : String
sorted =
    String.join "," (List.sort [ "b", "a", "C" ])
`
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("check: %v", err)
	}
	program, err := json.Marshal(map[string]any{"modules": []any{jsserve.SerializeModule(mod)}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := runtime.LoadModule(mod)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := map[string]string{}
	for _, name := range []string{"link", "money", "notJSON", "wrongShape", "bigInt", "sorted"} {
		v, ok := loaded.Env.Lookup(name)
		if !ok {
			t.Fatalf("%s is not bound", name)
		}
		s, ok := v.(runtime.VString)
		if !ok {
			t.Fatalf("%s is a %T, not a String", name, v)
		}
		want[name] = s.V
	}
	return program, want
}
