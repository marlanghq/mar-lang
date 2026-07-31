package iosbundle

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mar/internal/jsserve"
	"mar/internal/parity"
)

// The two runtimes are handed the same program and asked the same question.
//
// Until this existed the answer was a report: I ran 33 examples through both
// runtimes once, compared the screens, and 82 of 83 matched. That number was
// true and unreproducible — half the harness lived in a scratch directory. So
// this is the durable half: two fixtures that exercise the primitives, run on
// both runtimes every time the suite runs.
//
// It trades breadth for repeatability, and the trade is worth naming. A sweep
// over the real examples covers layouts nobody would think to write; this
// covers the primitives, forever, with no dev server and no network.
//
// Two surfaces, because the first one cannot see the second:
//
//   - text, which is what a person reads. It caught nothing across 33
//     examples, which is the good outcome for a regression net.
//   - the draw list, which is what a canvas produces INSTEAD of text. Ten
//     canvas screens "matched" in that sweep by both reporting an empty
//     string. That is agreement by vacuity, and it covered the apps with the
//     most code in them.
func TestSurfaceParity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds an iOS app and drives a simulator")
	}

	cases := []struct {
		name   string
		source string
		// wantShapes says a draw list must be present. Without it, a canvas
		// that stopped reaching the painter on BOTH platforms would still
		// compare equal — the exact failure this test exists to end.
		wantShapes bool
	}{
		{name: "Widgets", source: parity.UISource},
		{name: "Canvas", source: parity.CanvasSource, wantShapes: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			program, err := parity.Compile(tc.source, jsserve.SerializeModule)
			if err != nil {
				t.Fatalf("compiling the fixture: %v", err)
			}

			web, err := parity.RunWeb(jsserve.RuntimeJS(), program, parity.SurfaceDriver)
			if errors.Is(err, parity.ErrNoNode) {
				t.Skip("node not installed")
			}
			if err != nil {
				t.Fatalf("the web half failed to run: %v", err)
			}
			webSurface := parseWebSurface(web)
			ios := parseIOSSurface(runRouteSmoke(t, program, tc.name))

			if tc.wantShapes && webSurface["SHAPES"] == "" {
				t.Fatalf("the web runtime drew no shapes at all.\n%s", web)
			}
			for _, key := range []string{"TEXT", "SHAPES", "TEXT+1", "SHAPES+1"} {
				if webSurface[key] == "" && ios[key] == "" {
					continue
				}
				if webSurface[key] != ios[key] {
					t.Errorf("%s differs between the runtimes.\n web: %s\n iOS: %s",
						key, webSurface[key], ios[key])
				}
			}
		})
	}
}

// parseWebSurface reads the WEBTEXT / WEBSHAPES lines the surface driver
// prints; parseIOSSurface reads the TEXT / SHAPES lines routeSmoke prints.
// Two readers because the two halves label their output differently — the
// content they carry is what has to match.
func parseWebSurface(out string) map[string]string {
	found := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		for _, p := range []struct{ prefix, key string }{
			{"WEBSHAPES+1 ", "SHAPES+1"},
			{"WEBSHAPES ", "SHAPES"},
			{"WEBTEXT+1 ", "TEXT+1"},
			{"WEBTEXT ", "TEXT"},
		} {
			if after, ok := strings.CutPrefix(strings.TrimSpace(line), p.prefix); ok {
				found[p.key] = after
				break
			}
		}
	}
	return found
}

func parseIOSSurface(out string) map[string]string {
	found := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "[mar] ")
		if !ok {
			continue
		}
		for _, key := range []string{"SHAPES+1", "SHAPES", "TEXT+1", "TEXT"} {
			// "TEXT /path words…" — the route sits between the label and the
			// content, and the fixtures are single-page, so it is dropped.
			if after, ok := strings.CutPrefix(rest, key+" "); ok {
				_, content, _ := strings.Cut(after, " ")
				found[key] = content
				break
			}
		}
	}
	return found
}

// runRouteSmoke builds an app around `program`, launches it with the route
// smoke on, and returns everything it printed.
func runRouteSmoke(t *testing.T, program []byte, name string) string {
	t.Helper()
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("xcrun not found (needs Xcode)")
	}
	udid, err := bootedSimulator()
	if err != nil {
		t.Skip("no iOS simulator available: " + err.Error())
	}

	const bundleID = "dev.marlang.surfacecheck"
	dir := t.TempDir()
	projectDir, err := Generate(Spec{
		AppName:          "SurfaceCheck",
		BundleID:         bundleID,
		DisplayName:      "SurfaceCheck",
		MarketingVersion: "1.0.0",
		BuildNumber:      "1",
		DefaultBaseURL:   "http://127.0.0.1:1",
		EmbeddedProgram:  program,
	}, dir)
	if err != nil {
		t.Fatalf("scaffolding the iOS project: %v", err)
	}

	build := exec.Command("xcodebuild",
		"-project", filepath.Join(projectDir, "SurfaceCheck.xcodeproj"),
		"-scheme", "SurfaceCheck",
		"-sdk", "iphonesimulator",
		"-destination", "platform=iOS Simulator,id="+udid,
		"-derivedDataPath", filepath.Join(dir, "dd"),
		"build")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("the iOS template does not compile:\n%s", swiftErrors(string(out)))
	}
	app := filepath.Join(dir, "dd", "Build", "Products", "Debug-iphonesimulator", "SurfaceCheck.app")
	if _, err := os.Stat(app); err != nil {
		t.Fatalf("the build produced no SurfaceCheck.app")
	}

	_ = exec.Command("xcrun", "simctl", "terminate", udid, bundleID).Run()
	if out, err := exec.Command("xcrun", "simctl", "install", udid, app).CombinedOutput(); err != nil {
		t.Fatalf("simctl install: %v\n%s", err, out)
	}

	launch := exec.Command("xcrun", "simctl", "launch", "--console-pty", udid, bundleID)
	launch.Env = append(os.Environ(), "SIMCTL_CHILD_MAR_ROUTE_SMOKE=1")
	stdout, err := launch.StdoutPipe()
	if err != nil {
		t.Fatalf("wiring the app's stdout: %v", err)
	}
	launch.Stderr = launch.Stdout
	if err := launch.Start(); err != nil {
		t.Fatalf("simctl launch: %v", err)
	}
	defer func() {
		_ = exec.Command("xcrun", "simctl", "terminate", udid, bundleID).Run()
		_ = launch.Process.Kill()
		_, _ = launch.Process.Wait()
	}()

	// The app does not exit on its own; the smoke says when it is finished.
	done := make(chan string, 1)
	go func() {
		var log strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				log.Write(buf[:n])
				if strings.Contains(log.String(), "ROUTE SMOKE DONE") {
					done <- log.String()
					return
				}
			}
			if err != nil {
				done <- log.String()
				return
			}
		}
	}()

	select {
	case out := <-done:
		return out
	case <-time.After(60 * time.Second):
		t.Fatalf("%s: the route smoke never finished", name)
		return ""
	}
}
