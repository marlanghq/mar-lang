package scaffold

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"mar/internal/iosbundle"
	"mar/internal/project"
)

// BuildIOS materializes a generic Swift/SwiftUI iOS scaffold under
// <distDir>/<AppName>/, wired to talk to a mar backend via /_mar/schema.
//
// Unlike Build (native binaries), this doesn't evaluate the user's
// mar program — the iOS app is fully schema-driven and discovers what
// services/routes exist at runtime. We only need:
//
//   - the project's display name (from mar.json `name`, or directory
//     name as a fallback)
//   - the production backend URL (from mar.json `ios.serverUrl`).
//     Required for builds going to TestFlight / App Store. The
//     generated Swift code uses `#if DEBUG` to fall back to Bonjour
//     discovery during local Xcode debug-builds; release builds talk
//     only to ServerURL.
//
// `baseURLOverride` is an optional ad-hoc override (passed via
// `--base-url`) that takes precedence over `ios.serverUrl` for this
// invocation only. Useful for QA / staging without editing mar.json.
//
// The Swift code is regenerated on every build and intended as
// disposable infrastructure: customizing your iOS app means changing
// your mar code, not the iOS scaffolding.
func BuildIOS(entry, distDir, baseURLOverride, marVersion string) error {
	projectDir, err := resolveProjectDir(entry)
	if err != nil {
		return err
	}

	appName := defaultIOSAppName(projectDir)
	// LoadManifest validates mar.json — including the new ios.serverUrl
	// shape rule. Surface validation errors directly so the user sees
	// "ios.serverUrl must be https://..." rather than mysterious
	// downstream behavior.
	manifest, mErr := project.LoadManifest(projectDir)
	if mErr != nil {
		return fmt.Errorf("mar build: %w", mErr)
	}
	if manifest != nil && manifest.Name != "" {
		appName = manifest.Name
	}

	// Resolve the backend URL by precedence:
	//   1. --base-url flag (override for one-off builds)
	//   2. mar.json ios.serverUrl (the documented home)
	//   3. empty — Info.plist falls back to http://localhost:3000
	//      AND the build prints a hint that release builds need a URL.
	baseURL := baseURLOverride
	if baseURL == "" && manifest != nil && manifest.IOS != nil {
		baseURL = manifest.IOS.ServerURL
	}
	if baseURL == "" {
		fmt.Fprintln(os.Stderr,
			"warn: ios.serverUrl is not set in mar.json. The generated app will")
		fmt.Fprintln(os.Stderr,
			"      default to http://localhost:3000 in DEBUG (with Bonjour discovery)")
		fmt.Fprintln(os.Stderr,
			"      and have no production backend in RELEASE.")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr,
			"      Add to mar.json before shipping to TestFlight / App Store:")
		fmt.Fprintln(os.Stderr,
			`        "ios": { "serverUrl": "https://my-app.fly.dev" }`)
		fmt.Fprintln(os.Stderr)
	}

	out, err := iosbundle.Generate(iosbundle.Spec{
		AppName:        appName,
		DefaultBaseURL: baseURL,
		MarVersion:     marVersion,
	}, distDir)
	if err != nil {
		return err
	}

	fmt.Printf("[mar build] iOS scaffold written to %s\n", out)

	// If xcodegen is installed, generate the .xcodeproj for the user
	// — saves the manual step. Falls through to printing instructions
	// when it isn't found, so a fresh checkout still has a workable
	// path forward.
	if xcg, err := exec.LookPath("xcodegen"); err == nil {
		cmd := exec.Command(xcg, "generate")
		cmd.Dir = out
		if cmdOut, err := cmd.CombinedOutput(); err != nil {
			fmt.Printf("            xcodegen failed: %v\n%s\n", err, cmdOut)
			fmt.Printf("            run manually: cd %s && xcodegen generate\n", out)
		} else {
			fmt.Printf("[mar build] xcodegen ✓\n")
			fmt.Printf("            open %s/*.xcodeproj\n", out)
		}
	} else {
		fmt.Printf("            xcodegen not installed — install with `brew install xcodegen`,\n")
		fmt.Printf("            then run: cd %s && xcodegen generate\n", out)
	}
	return nil
}

// resolveProjectDir accepts either a directory or a Main.mar file
// path and returns the project root.
func resolveProjectDir(entry string) (string, error) {
	info, err := os.Stat(entry)
	if err != nil {
		return "", fmt.Errorf("%s: %v", entry, err)
	}
	if info.IsDir() {
		return entry, nil
	}
	return filepath.Dir(entry), nil
}

func defaultIOSAppName(projectDir string) string {
	clean := filepath.Clean(projectDir)
	base := filepath.Base(clean)
	if base == "." || base == "/" || base == "" {
		return "MarApp"
	}
	return base
}
