package iosbundle

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mar/internal/conformance"
	"mar/internal/jsserve"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// The third runtime. Go and JS run the same corpus from internal/jsserve; until
// this file existed, Swift's stdlib was covered only by drift tests, which see
// that a name is DEFINED and not that it answers the same thing. That is
// exactly the hole `==` fell through: defined in all three, wrong in one.
//
// The evaluator, the loader and the stdlib are Foundation-only, so they compile
// and run headless on the host: no simulator, no Xcode project. Only the two
// display files need UIKit, and they are shimmed out; the guard below keeps
// that from quietly excusing a stdlib function.

// Entry points MarBuiltins calls on the display side. Shimmed rather than
// compiled because their files need UIKit, which does not exist on macOS. If
// MarBuiltins grows a call into another display file, the build fails with
// "cannot find X in scope", which is the right way to find out.
const swiftDisplayShims = `import Foundation

enum MarCanvas { static func register(_ env: Env) {} }
enum MarInput { static func register(_ env: Env) {} }
`

const swiftDriver = `import Foundation

let data = try! Data(contentsOf: URL(fileURLWithPath: CommandLine.arguments[1]))
let program = try! MarJSONCodec.decodeProgram(data)
let env = MarBuiltins.makeEnv()
do {
    for m in program.modules { try MarLoader.load(module: m, into: env) }
} catch {
    // Loading evaluates top-level values, so a program that fails at runtime
    // fails here. Report it instead of trapping, so a test can assert on it.
    FileHandle.standardError.write("mar error: \(error)\n".data(using: .utf8)!)
    exit(2)
}
guard let v = env.lookup(CommandLine.arguments[2]) else {
    FileHandle.standardError.write("unbound: \(CommandLine.arguments[2])\n".data(using: .utf8)!)
    exit(1)
}
guard case .string(let s) = v else {
    FileHandle.standardError.write("not a string: \(v)\n".data(using: .utf8)!)
    exit(1)
}
FileHandle.standardOutput.write(s.data(using: .utf8)!)
`

func TestStdlibSwiftConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the Swift runtime; skipped under -short")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the Swift runtime needs the Apple SDKs (Security, AVFoundation, Network)")
	}
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc not installed; skipping Swift conformance run")
	}

	dir := t.TempDir()
	shimmed := extractHeadlessSwift(t, dir)
	assertShimsHideNoStdlib(t, shimmed)

	mod, err := parser.Parse(conformance.Source)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}
	programJSON, err := json.Marshal(map[string]any{
		"modules": []any{jsserve.SerializeModule(mod)},
		"entry":   "results",
	})
	if err != nil {
		t.Fatalf("marshal program: %v", err)
	}
	write(t, filepath.Join(dir, "program.json"), programJSON)
	write(t, filepath.Join(dir, "_headless_shims.swift"), []byte(swiftDisplayShims))
	write(t, filepath.Join(dir, "main.swift"), []byte(swiftDriver))

	sources, err := filepath.Glob(filepath.Join(dir, "*.swift"))
	if err != nil || len(sources) == 0 {
		t.Fatalf("no Swift sources to compile: %v", err)
	}
	bin := filepath.Join(dir, "conform")
	build := exec.Command(swiftc, append([]string{"-o", bin}, sources...)...)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("swiftc: %v\n%s", err, out)
	}

	run := exec.Command(bin, filepath.Join(dir, "program.json"), conformance.Entry)
	var stderr strings.Builder
	run.Stderr = &stderr
	out, err := run.Output()
	if err != nil {
		t.Fatalf("swift run: %v\n%s", err, stderr.String())
	}

	cases, err := conformance.SplitCases(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("swift output: %v", err)
	}
	problems, err := conformance.Check("Swift", cases)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
	if !t.Failed() {
		t.Logf("%d stdlib answers from the Swift runtime match the hand-written expectations", len(cases))
	}
}

// extractHeadlessSwift writes the shipped Swift sources into dir, leaving out
// the ones that need SwiftUI or UIKit, and returns what it left out. The set is
// detected from the imports rather than listed, so a new display file does not
// need this test edited to keep compiling.
func extractHeadlessSwift(t *testing.T, dir string) map[string]string {
	t.Helper()
	shimmed := map[string]string{}
	err := fs.WalkDir(templateFS, "template", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".swift") {
			return err
		}
		raw, readErr := templateFS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		body := string(raw)
		if strings.Contains(body, "\nimport SwiftUI") || strings.Contains(body, "\nimport UIKit") ||
			strings.HasPrefix(body, "import SwiftUI") || strings.HasPrefix(body, "import UIKit") {
			shimmed[filepath.Base(path)] = body
			return nil
		}
		write(t, filepath.Join(dir, filepath.Base(path)), raw)
		return nil
	})
	if err != nil {
		t.Fatalf("extract Swift sources: %v", err)
	}
	if len(shimmed) == 0 {
		t.Fatal("no display files were shimmed out; the import detection stopped working")
	}
	return shimmed
}

// assertShimsHideNoStdlib is the price of shimming. A display file that
// registered, say, `List.map` would have that name silently missing from the
// headless env, and the run would fail in a way that looks like a stdlib bug:
// or worse, a name the corpus does not reach would go untested while the gate
// reports full coverage. Neither is true today; this keeps it that way.
func assertShimsHideNoStdlib(t *testing.T, shimmed map[string]string) {
	t.Helper()
	for name, body := range shimmed {
		for module := range conformance.Scope {
			if strings.Contains(body, `"`+module+`.`) {
				t.Errorf("%s is shimmed out of the headless build but mentions %s.*, "+
					"so the Swift run would be missing stdlib the corpus expects. "+
					"Compile it instead of shimming it, or move that registration.", name, module)
			}
		}
	}
}

func write(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
}
