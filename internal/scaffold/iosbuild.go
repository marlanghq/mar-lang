package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mar/internal/apphost"
	"mar/internal/ast"
	"mar/internal/iosbundle"
	"mar/internal/project"
	"mar/internal/runtime"
)

// IOSBuildResult is the structured outcome of BuildIOS — what the
// caller needs to render a properly-formatted summary at the
// presentation layer. Returning a value (instead of printing inline)
// lets cmd/mar own the color palette and spacing rules without
// scaffold having to import them.
type IOSBuildResult struct {
	// OutputDir is the absolute path to the materialized scaffold
	// (e.g. <distDir>/<AppName>/).
	OutputDir string

	// ProjectDir is the project root (the directory containing
	// mar.json). Exposed so cmd/mar can peek at sibling files like
	// deploy/fly/fly.toml to enrich the "missing serverUrl"
	// suggestion with the operator's actual deploy slug.
	ProjectDir string

	// AppName is the SwiftIdentifier-mangled name baked into the
	// .pbxproj as the target / scheme name. May differ from
	// manifest.Name when the manifest contains spaces or
	// punctuation (e.g. "notes-fullstack" → "NotesFullstack").
	AppName string

	// BundleID, DisplayName, MarketingVersion, BuildNumber are the
	// values from mar.json's `ios` block (all required for the
	// build to succeed). Exposed so the build summary can echo
	// what's about to be signed / archived.
	BundleID         string
	DisplayName      string
	MarketingVersion string
	BuildNumber      string

	// BaseURL is the production backend URL baked into Info.plist
	// as MarBaseURL — what RELEASE builds talk to. When empty,
	// the bundle falls back to http://localhost:3000 and
	// MissingServerURL is true.
	BaseURL string

	// MissingServerURL is true when ios.serverUrl in mar.json was
	// not set. Caller should warn the operator before shipping to
	// TestFlight / App Store.
	MissingServerURL bool
}

// IOSConfigError reports that mar.json's `ios` block is missing
// required fields. cmd/mar prints it as a typed error with a
// paste-ready JSON snippet that combines the missing fields with
// reasonable starter values — the operator can copy the block into
// mar.json and have a working build on the next attempt.
//
// Captured separately from a plain `error` so cmd/mar can render it
// with the production-config-style formatting (multi-line magenta
// JSON, dimmed placeholders) without scaffold having to import the
// color helpers.
type IOSConfigError struct {
	// Missing lists the field names that aren't set in mar.json,
	// in stable order. Always at least one entry — empty Missing
	// means the validation passed and IOSConfigError shouldn't
	// have been constructed.
	Missing []string

	// Suggestions maps each missing field to the example value
	// shown in the paste-ready JSON. Computed at validation time
	// because some suggestions depend on context (bundleId
	// includes the lowercased project name, displayName echoes
	// manifest.name).
	Suggestions map[string]string
}

func (e *IOSConfigError) Error() string {
	return fmt.Sprintf("mar.json: ios block missing required fields: %s",
		strings.Join(e.Missing, ", "))
}

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
// The Swift code is regenerated on every build and intended as
// disposable infrastructure: customizing your iOS app means changing
// your mar code, not the iOS scaffolding.
func BuildIOS(entry, distDir, marVersion string) (IOSBuildResult, error) {
	projectDir, err := resolveProjectDir(entry)
	if err != nil {
		return IOSBuildResult{}, err
	}

	appName := defaultIOSAppName(projectDir)
	// Structure-only load (no env:VAR resolution). The iOS build
	// reads only structural fields from the manifest; going through
	// the env-resolving LoadManifest would fail on projects that
	// reference SMTP_PASSWORD / SESSION_SECRET as Fly secrets
	// (correctly absent from the developer's shell) and block the
	// iOS build for no reason.
	manifest, mErr := project.LoadManifestStructure(projectDir)
	if mErr != nil {
		return IOSBuildResult{}, fmt.Errorf("mar build: %w", mErr)
	}
	if manifest != nil && manifest.Name != "" {
		appName = manifest.Name
	}

	var ios *project.IOSConfig
	if manifest != nil {
		ios = manifest.IOS
	}
	if cfgErr := validateIOSConfig(ios, appName); cfgErr != nil {
		return IOSBuildResult{}, cfgErr
	}

	// Compile the user's mar code into program.json so the iOS app
	// can render its first screen instantly from the embedded bundle
	// (instant cold start). Failure here means the user's mar source
	// has an error; surface it the same way other build paths do.
	programJSON, err := compileIOSProgram(entry)
	if err != nil {
		return IOSBuildResult{}, fmt.Errorf("mar build: %w", err)
	}

	spec := iosbundle.Spec{
		AppName:          appName,
		BundleID:         ios.BundleID,
		DisplayName:      ios.DisplayName,
		MarketingVersion: ios.MarketingVersion,
		BuildNumber:      ios.BuildNumber,
		DefaultBaseURL:   ios.ServerURL,
		MarVersion:       marVersion,
		EmbeddedProgram:  programJSON,
	}
	out, err := iosbundle.Generate(spec, distDir)
	if err != nil {
		return IOSBuildResult{}, err
	}

	// ServerURL is the only optional field. When empty the bundle
	// falls back to http://localhost:3000 (handled inside
	// iosbundle.Generate); the result echoes that fallback so the
	// summary line is honest, and MissingServerURL flags the warn.
	resolvedBaseURL := ios.ServerURL
	if resolvedBaseURL == "" {
		resolvedBaseURL = "http://localhost:3000"
	}
	return IOSBuildResult{
		OutputDir:        out,
		ProjectDir:       projectDir,
		AppName:          iosbundle.SwiftIdentifier(appName),
		BundleID:         ios.BundleID,
		DisplayName:      ios.DisplayName,
		MarketingVersion: ios.MarketingVersion,
		BuildNumber:      ios.BuildNumber,
		BaseURL:          resolvedBaseURL,
		MissingServerURL: ios.ServerURL == "",
	}, nil
}

// validateIOSConfig checks mar.json's `ios` block has every field
// required to produce a signable bundle. Returns nil when all
// required fields are present (ServerURL is optional and handled
// via Warn, not Error).
//
// Suggestions for missing fields are computed here (rather than at
// print-time) because some depend on context the printer doesn't
// have: bundleId uses the lowercased app name, displayName echoes
// the manifest.name literal.
func validateIOSConfig(ios *project.IOSConfig, appName string) *IOSConfigError {
	type field struct {
		name       string
		value      string
		suggestion string
	}
	swiftName := iosbundle.SwiftIdentifier(appName)
	fields := []field{
		{"bundleId", ifConfig(ios, func(c *project.IOSConfig) string { return c.BundleID }),
			"com.yourcompany." + strings.ToLower(swiftName)},
		{"displayName", ifConfig(ios, func(c *project.IOSConfig) string { return c.DisplayName }),
			appName},
		{"marketingVersion", ifConfig(ios, func(c *project.IOSConfig) string { return c.MarketingVersion }),
			"0.0.1"},
		{"buildNumber", ifConfig(ios, func(c *project.IOSConfig) string { return c.BuildNumber }),
			"1"},
	}
	var missing []string
	suggestions := map[string]string{}
	for _, f := range fields {
		if f.value == "" {
			missing = append(missing, f.name)
			suggestions[f.name] = f.suggestion
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &IOSConfigError{Missing: missing, Suggestions: suggestions}
}

// ifConfig is a small nil-safe getter so validateIOSConfig can treat
// a missing `ios` block and a present-but-empty field identically.
func ifConfig(ios *project.IOSConfig, get func(*project.IOSConfig) string) string {
	if ios == nil {
		return ""
	}
	return get(ios)
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

	// Clear per-load global runtime state before evaluating, mirroring
	// Build / Topology and `mar dev`. Keeps a second in-process
	// evaluation from tripping the "Entity.define declared more than
	// once" guard.
	runtime.ResetForReload()

	bc := &buildCtx{}
	rEnv, allMods, _, err := project.LoadIntoEnvWithModulesAndHook(mainFile,
		func(env *runtime.Env, mods []*ast.Module) {
			fe := makeIOSPagesCapture(bc, false /*fromRecord*/)
			env.Define("appFrontend", fe)
			env.Define("App.frontend", fe)

			fs := makeIOSPagesCapture(bc, true /*fromRecord*/)
			env.Define("appFullstack", fs)
			env.Define("App.fullstack", fs)

			be := makeBackendCapture(bc)
			env.Define("appBackend", be)
			env.Define("App.backend", be)
		})
	if err != nil {
		return nil, err
	}
	mainVal, ok := project.LookupMain(rEnv, allMods)
	if !ok {
		return nil, fmt.Errorf("Main.mar must export `main`")
	}
	eff, ok := mainVal.(runtime.VEffect)
	if !ok {
		return nil, fmt.Errorf("main is not a Cmd (got %T)", mainVal)
	}
	if _, err := eff.Run(); err != nil {
		return nil, err
	}
	if len(bc.pages) == 0 {
		// App.backend project (no pages). iOS still needs a stub
		// program; we ship an empty pages list so the shell can
		// render a sensible "No pages defined" placeholder without
		// crashing. No __entry needed when there's nothing to mount.
		return makeProgramJSON(nil, "main", false)
	}
	// apphost.PickFrontMods does the page-reachable module walk AND
	// appends a synthetic `__entry = appFrontend [pages]` module that
	// the iOS runtime looks up at boot. Without that synthetic entry,
	// the embedded program.json has nothing for env.lookup("main")
	// or env.lookup("__entry") to find, runProgramSync throws,
	// AppViewModel falls back to .loading, and the user sees the
	// spinner instead of the instant cold-start the embedded
	// snapshot was meant to provide.
	mods, err := apphost.PickFrontMods(bc.pages, allMods)
	if err != nil {
		return nil, err
	}
	return makeProgramJSON(mods, "__entry", false)
}

// makeIOSPagesCapture captures the page list from App.frontend
// (`fromRecord=false`, args[0] is the page list) or App.fullstack
// (`fromRecord=true`, args[0] is a record with a "pages" field).
// Both flow into the same buildCtx.pages slot; downstream callers
// (compileIOSProgram) feed that into apphost.PickFrontMods which
// does the page-reachable module walk + synthetic __entry append.
//
// Compare with makeFrontendCapture (the web build path), which
// inlines the module walk locally instead of via apphost. Both
// should converge on apphost once the web build is also migrated.
func makeIOSPagesCapture(bc *buildCtx, fromRecord bool) runtime.Value {
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

			// Validate provenance up-front so the error fires here,
			// in user-source context, rather than deep inside
			// apphost.PickFrontMods later.
			for i, pv := range pageList.Elements {
				page, ok := pv.(runtime.VPage)
				if !ok {
					return nil, fmt.Errorf("page %d is not a Page (got %T)", i, pv)
				}
				if page.OriginName == "" {
					return nil, fmt.Errorf("page %d has no provenance — pages must be top-level bindings", i)
				}
			}
			bc.kind = kindFrontend
			bc.pages = pageList.Elements
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
