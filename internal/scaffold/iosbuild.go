package scaffold

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"mar/internal/ast"
	"mar/internal/iosbundle"
	"mar/internal/project"
	"mar/internal/runtime"
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
			"Warn: ios.serverUrl is not set in mar.json. The generated app will")
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

	// Compile the user's mar code into program.json so the iOS app
	// can render its first screen instantly from the embedded bundle
	// (Approach C — instant cold start). Failure here means the
	// user's mar source has an error; surface it the same way other
	// build paths do.
	programJSON, err := compileIOSProgram(entry)
	if err != nil {
		return fmt.Errorf("mar build: %w", err)
	}

	out, err := iosbundle.Generate(iosbundle.Spec{
		AppName:           appName,
		DefaultBaseURL:    baseURL,
		MarVersion:        marVersion,
		EmbeddedProgram:   programJSON,
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

// compileIOSProgram evaluates the user's main, captures the pages
// list (works with both App.frontend and App.fullstack), and
// returns the program.json bytes. The returned bundle is what the
// iOS shell loads at boot (Resources/program.json) for instant
// cold-start; the same bytes are also fetched fresh from the
// server when the network is available so the embedded copy is a
// fallback, not the source of truth.
func compileIOSProgram(entry string) ([]byte, error) {
	// Resolve entry → Main.mar path. Caller passes either a directory
	// (look for Main.mar inside) or a file path (use directly). Same
	// shape as scaffold.Build.
	info, err := os.Stat(entry)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", entry, err)
	}
	mainFile := entry
	if info.IsDir() {
		mainFile = filepath.Join(entry, "Main.mar")
		if _, err := os.Stat(mainFile); err != nil {
			return nil, fmt.Errorf("Main.mar not found in %s", entry)
		}
	}

	bc := &buildCtx{}
	rEnv, _, _, err := project.LoadIntoEnvWithModulesAndHook(mainFile,
		func(env *runtime.Env, mods []*ast.Module) {
			fe := makeIOSPagesCapture(mods, bc, false /*fromRecord*/)
			env.Define("appFrontend", fe)
			env.Define("App.frontend", fe)

			fs := makeIOSPagesCapture(mods, bc, true /*fromRecord*/)
			env.Define("appFullstack", fs)
			env.Define("App.fullstack", fs)

			be := makeBackendCapture(bc)
			env.Define("appBackend", be)
			env.Define("App.backend", be)
		})
	if err != nil {
		return nil, err
	}
	mainVal, ok := rEnv.Lookup("Main.main")
	if !ok {
		mainVal, ok = rEnv.Lookup("main")
	}
	if !ok {
		return nil, fmt.Errorf("Main.mar must export `main`")
	}
	eff, ok := mainVal.(runtime.VEffect)
	if !ok {
		return nil, fmt.Errorf("main is not an Effect (got %T)", mainVal)
	}
	if _, err := eff.Run(); err != nil {
		return nil, err
	}
	if len(bc.frontMods) == 0 {
		// App.backend project (no pages). iOS still needs a stub
		// program; we ship an empty pages list so the shell can
		// render a sensible placeholder ("No pages defined") and
		// the operator can navigate to the Settings tab regardless.
		return makeProgramJSON(nil, "main", false)
	}
	return makeProgramJSON(bc.frontMods, "main", false)
}

// makeIOSPagesCapture mirrors makeFrontendCapture but is shared
// between App.frontend (`fromRecord=false`, args[0] is the page
// list) and App.fullstack (`fromRecord=true`, args[0] is a record
// with a "pages" field). Both flow into the same buildCtx fields
// so downstream extraction is identical.
func makeIOSPagesCapture(mods []*ast.Module, bc *buildCtx, fromRecord bool) runtime.Value {
	tag := "appFrontend"
	if fromRecord {
		tag = "appFullstack"
	}
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			var pageList runtime.VList
			if fromRecord {
				rec, ok := args[0].(runtime.VRecord)
				if !ok {
					return nil, fmt.Errorf("App.fullstack: expected record argument (got %T)", args[0])
				}
				pagesV, ok := rec.Fields["pages"]
				if !ok {
					return nil, fmt.Errorf("App.fullstack: missing `pages` field")
				}
				pageList, ok = pagesV.(runtime.VList)
				if !ok {
					return nil, fmt.Errorf("App.fullstack: `pages` is not a list (got %T)", pagesV)
				}
			} else {
				var ok bool
				pageList, ok = args[0].(runtime.VList)
				if !ok {
					return nil, fmt.Errorf("App.frontend: expected List Page (got %T)", args[0])
				}
			}

			roots := map[string]bool{}
			for i, pv := range pageList.Elements {
				page, ok := pv.(runtime.VPage)
				if !ok {
					return nil, fmt.Errorf("page %d is not a Page (got %T)", i, pv)
				}
				if page.OriginName == "" {
					return nil, fmt.Errorf("page %d has no provenance — pages must be top-level bindings", i)
				}
				roots[page.OriginModule] = true
			}
			merged := []*ast.Module{}
			seen := map[string]bool{}
			for root := range roots {
				for _, m := range reachableFrom(root, mods) {
					name := joinName(m.Name)
					if seen[name] {
						continue
					}
					seen[name] = true
					merged = append(merged, m)
				}
			}
			bc.kind = kindFrontend
			bc.frontMods = merged
			if len(merged) > 0 {
				nm := merged[len(merged)-1].Name
				if len(nm) > 0 {
					bc.title = nm[len(nm)-1]
				}
			}
			return runtime.VEffect{Tag: tag, Run: func() (runtime.Value, error) {
				return runtime.VUnit{}, nil
			}}, nil
		},
	}
}

func defaultIOSAppName(projectDir string) string {
	clean := filepath.Clean(projectDir)
	base := filepath.Base(clean)
	if base == "." || base == "/" || base == "" {
		return "MarApp"
	}
	return base
}
