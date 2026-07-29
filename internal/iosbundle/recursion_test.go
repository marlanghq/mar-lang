package iosbundle

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mar/internal/jsserve"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// On iOS a runaway recursion ran until the thread's stack was gone, which is a
// crash the app cannot catch or report: the user watches it vanish. The Go
// server got a guard first because there one request could take down every
// other user; this is the same protection for the phone.
//
// The Go runtime carries the depth on the function value, because its requests
// are concurrent and a shared counter would be wrong. Swift's interpreter runs
// on the main actor, so an ambient counter is both correct and simpler — and
// being ambient is what makes it see recursion routed through a higher-order
// builtin without any value having to carry anything.

// buildHeadlessSwift compiles the shipped Swift runtime into a host binary and
// returns its path, skipping the test when the toolchain is not available.
func buildHeadlessSwift(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("compiles the Swift runtime; skipped under -short")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the Swift runtime needs the Apple SDKs")
	}
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Skip("swiftc not installed")
	}
	dir := t.TempDir()
	shimmed := extractHeadlessSwift(t, dir)
	assertShimsHideNoStdlib(t, shimmed)
	write(t, filepath.Join(dir, "_headless_shims.swift"), []byte(swiftDisplayShims))
	write(t, filepath.Join(dir, "main.swift"), []byte(swiftDriver))

	sources, err := filepath.Glob(filepath.Join(dir, "*.swift"))
	if err != nil || len(sources) == 0 {
		t.Fatalf("no Swift sources: %v", err)
	}
	bin := filepath.Join(dir, "run")
	// -O wholemodule matches how the iOS template actually ships (see the
	// Xcode settings the generator writes); an unoptimized build has much
	// fatter frames and would measure a limit the real app never sees.
	build := exec.Command(swiftc, append([]string{"-O", "-wmo", "-o", bin}, sources...)...)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("swiftc: %v\n%s", err, out)
	}
	return bin
}

// runSwift evaluates a Mar program in the compiled runtime and returns stdout
// and stderr. A program that fails at runtime reports on stderr rather than
// trapping, which is what lets this assert on the message.
func runSwift(t *testing.T, bin, src, entry string) (string, string) {
	t.Helper()
	mod, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := typecheck.CheckModule(mod); err != nil {
		t.Fatalf("typecheck: %v", err)
	}
	programJSON, err := json.Marshal(map[string]any{
		"modules": []any{jsserve.SerializeModule(mod)},
		"entry":   entry,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "program.json")
	write(t, path, programJSON)

	cmd := exec.Command(bin, path, entry)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// A refused program exits non-zero on purpose, so the error itself is
		// not a failure — but a crash with no output is, and that looks the
		// same unless the exit status is reported.
		t.Logf("swift exit: %v", err)
	}
	return stdout.String(), stderr.String()
}

func TestSwiftRuntimeRefusesRunawayRecursion(t *testing.T) {
	bin := buildHeadlessSwift(t)

	for _, tc := range []struct{ name, src string }{
		{"self recursion", `module M exposing (boom)


loop : Int -> Int
loop n =
    loop (n + 1)


boom : Int
boom =
    loop 0
`},
		// Routed through a Go-style loop inside a builtin. An ambient counter
		// sees it; a counter threaded only through the evaluator would not.
		{"through List.foldl", `module M exposing (boom)


loop : Int -> Int
loop n =
    List.foldl (\_ _ -> loop (n + 1)) 0 [ 1 ]


boom : Int
boom =
    loop 0
`},
		{"through a pipe", `module M exposing (boom)


loop : Int -> Int
loop n =
    n |> loop


boom : Int
boom =
    loop 0
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr := runSwift(t, bin, tc.src, "M.boom")
			if !strings.Contains(stderr, "too much recursion") {
				t.Fatalf("expected the recursion guard, got stderr:\n%s", stderr)
			}
		})
	}
}

// The guard has to leave honest programs alone, and the ambient counter has to
// unwind: a deep call that RETURNS must leave the depth where it found it, or
// the next evaluation starts partway to the limit.
func TestSwiftRuntimeAllowsDeepTerminatingRecursion(t *testing.T) {
	bin := buildHeadlessSwift(t)
	stdout, stderr := runSwift(t, bin, `module M exposing (answer)


countDown : Int -> Int
countDown n =
    if n <= 0 then
        0

    else
        1 + countDown (n - 1)


-- Runs the deep recursion twice. If the counter did not unwind, the second
-- pass would start at 400 and the two together would trip the 512 limit.
answer : String
answer =
    String.fromInt (countDown 400 + countDown 400)
`, "M.answer")
	if stderr != "" {
		t.Fatalf("legitimate deep recursion was refused: %s", stderr)
	}
	if strings.TrimSpace(stdout) != "800" {
		t.Fatalf("countDown 400 twice = %q, want 800", strings.TrimSpace(stdout))
	}
}
