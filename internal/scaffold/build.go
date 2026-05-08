package scaffold

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"mar/internal/appbundle"
	"mar/internal/appbundle/stubs"
	"mar/internal/ast"
	"mar/internal/jsserve"
	"mar/internal/project"
	"mar/internal/runtime"
)

// Build compiles a mar project to a deployable artifact.
//
// `entry` is either a directory (looks for Main.mar inside) or the path
// to a .mar file to use as the entry point. `distDir` is where the
// output is written. `target` selects the OS/arch for the embedded
// runtime stub when the project needs a server (backend / fullstack);
// pass "" to default to the host target.
//
// Output depends on the topology the project's `main` picks:
//
//   - App.frontend → static dist/:
//       index.html, runtime.js, program.json
//     Servable as plain files from any HTTP host (CDN, S3, etc.).
//
//   - App.backend or App.fullstack → self-contained executable:
//       <distDir>/<projectName>      (or .exe on windows)
//     The mar-runtime stub for `target` is concatenated with a ZIP
//     payload containing mar.json + every .mar source file. On startup
//     the binary reads its own bytes, extracts the payload, and serves
//     HTTP — no external mar toolchain required on the deploy host.
func Build(entry, distDir, target string) error {
	info, err := os.Stat(entry)
	if err != nil {
		return fmt.Errorf("%s: %v", entry, err)
	}
	mainFile := entry
	projectDir := entry
	if info.IsDir() {
		mainFile = filepath.Join(entry, "Main.mar")
		if _, err := os.Stat(mainFile); err != nil {
			return fmt.Errorf("Main.mar not found in %s", entry)
		}
	} else {
		projectDir = filepath.Dir(entry)
	}

	if target == "" {
		target = stubs.HostTarget()
	}

	// Same load + override pattern as `mar dev`, but the overrides
	// capture into a build context instead of a live server.
	bc := &buildCtx{}
	rEnv, _, _, err := project.LoadIntoEnvWithModulesAndHook(mainFile,
		func(env *runtime.Env, mods []*ast.Module) {
			fe := makeFrontendCapture(mods, bc)
			env.Define("appFrontend", fe)
			env.Define("App.frontend", fe)

			be := makeBackendCapture(bc)
			env.Define("appBackend", be)
			env.Define("App.backend", be)

			fs := makeFullstackCapture(mods, bc)
			env.Define("appFullstack", fs)
			env.Define("App.fullstack", fs)
		})
	if err != nil {
		return err
	}
	mainVal, ok := rEnv.Lookup("Main.main")
	if !ok {
		mainVal, ok = rEnv.Lookup("main")
	}
	if !ok {
		return fmt.Errorf("Main.mar must export `main`")
	}
	eff, ok := mainVal.(runtime.VEffect)
	if !ok {
		return fmt.Errorf("main is not an Effect (got %T)", mainVal)
	}
	if _, err := eff.Run(); err != nil {
		return err
	}

	// Production validation. When the build target is a deploy
	// binary (linux-*, windows-*, darwin-*) and the user invoked
	// Auth.config, mar.json must declare every secret the runtime
	// will need at boot. Catches the misconfiguration class where
	// `mar dev` works (auto-generated session secret + stdout sink)
	// but a production deploy would 503 on every sign-in.
	//
	// Skipped for the host (dev) target, since `mar build` against
	// the host is sometimes used as a quick smoke test where the
	// missing fields are intentional.
	if isProductionTarget(target) {
		if err := validateProductionConfig(projectDir); err != nil {
			return err
		}
		// Discovery warning — admin panel is opt-in and many projects
		// won't bother. But silent absence means devs who don't know
		// about the panel never benefit. Print once per build, not an
		// error. Suppressed via --no-admin-warning in case CI noise
		// gets annoying (deferred wiring; for now just print).
		warnIfNoAdmins(projectDir)
	}

	switch bc.kind {
	case kindFrontend:
		return buildFrontendDist(distDir, bc)
	case kindBackend, kindFullstack:
		return buildServerExecutable(projectDir, distDir, target, bc)
	default:
		return fmt.Errorf("main didn't call any of App.frontend / App.backend / App.fullstack — nothing to build")
	}
}

// isProductionTarget reports whether `target` will be deployed to a
// real host (vs. used as a local debugging artifact). All non-host
// targets are deploys; host target is treated as dev unless
// MAR_BUILD_PROD is set explicitly.
func isProductionTarget(target string) bool {
	if target == "" {
		return false
	}
	if target == stubs.HostTarget() && os.Getenv("MAR_BUILD_PROD") == "" {
		return false
	}
	return true
}

// warnIfNoAdmins prints a one-line stderr hint when a production
// build has no admins configured. Doesn't fail the build — admin
// is opt-in. The warning ensures devs encountering the framework
// for the first time learn the panel exists.
func warnIfNoAdmins(projectDir string) {
	manifest, err := project.LoadManifestStructure(projectDir)
	if err != nil {
		return // structural error already surfaces upstream
	}
	if manifest != nil && len(manifest.Admins) > 0 {
		return
	}
	// Multi-line block. Blank line BEFORE only (docs/cli-style.md §1
	// "adjacent blocks rule") — next caller adds their own leading
	// blank.
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr,
		"warn: building for production with no admins configured.")
	fmt.Fprintln(os.Stderr,
		"      the admin panel at /_mar/admin will be inaccessible.")
	fmt.Fprintln(os.Stderr,
		"      run `mar admin add YOUR_EMAIL` if you want admin access in production.")
}

// ProductionConfigError is the structured error returned by
// validateProductionConfig when mar.json is missing required
// production fields. The CLI catches this specifically so it can
// format the message with colors + blank lines per cli-style.md;
// other callers (tests, library use) can call .Error() to get a
// plain-text rendering without ANSI escapes.
type ProductionConfigError struct {
	// Missing is the list of mar.json fragments the user needs to
	// add. Each entry is a JSON-shaped suggestion ready to paste.
	Missing []string
}

func (e *ProductionConfigError) Error() string {
	return fmt.Sprintf(`production build requires auth and mail config in mar.json.

Your project uses Auth.config which sends sign-in emails. The runtime
needs persistent secrets and a real SMTP provider in production —
without them, every sign-in attempt would fail.

Add to mar.json:

  %s

Hint: smtpPort is optional and defaults to 587 (Resend, SendGrid,
      Mailgun, AWS SES, Postmark, Brevo, Mailjet all use it).

Hint: sessionSecret and smtpPassword MUST be env:VAR_NAME — set
      the values via 'mar fly provision' (or 'fly secrets set'
      for bare fly deploys).`, strings.Join(e.Missing, "\n  "))
}

// validateProductionConfig asserts the project's mar.json carries
// the configuration the chosen runtime features need. Today
// validates auth, mail, and the framework admin panel; other
// features (Stripe, Twilio, etc.) would register their own
// validators here.
//
// Returns *ProductionConfigError when mar.json needs additions.
// Returns a plain error for unrelated I/O / parse problems.
func validateProductionConfig(projectDir string) error {
	// LoadManifestStructure reads without env-resolving — fly
	// secrets aren't visible at build time, but we don't need
	// their values, only that the env:VAR placeholder is wired.
	manifest, err := project.LoadManifestStructure(projectDir)
	if err != nil {
		return err
	}

	// Two triggers for sessionSecret + mail config:
	//   - User auth (Auth.config registered) — needs both.
	//   - Admin panel (mar.json admins non-empty) — needs sessionSecret
	//     (shared HMAC) but mail is best-effort (admin login degrades
	//     to "doesn't work" rather than blocking the whole app).
	authInUse := runtime.CurrentAuth() != nil
	adminInUse := manifest != nil && len(manifest.Admins) > 0
	if !authInUse && !adminInUse {
		return nil
	}

	var missing []string
	if manifest == nil || manifest.Auth == nil || manifest.Auth.SessionSecret == "" {
		missing = append(missing, `"auth": { "sessionSecret": "env:SESSION_SECRET" }`)
	}
	// Mail required only when user-auth is in use. Admin-only projects
	// can still ship — the panel just won't be able to send codes
	// until SMTP is configured (logged at boot).
	if authInUse {
		if manifest == nil || manifest.Mail == nil {
			// Multi-line JSON snippet so the operator can paste
			// directly into mar.json. The cmd/mar formatter adds
			// 2 spaces of left padding on each line, so the JSON
			// here uses 2-space inner indent (printed: 4) and
			// no indent for the closing brace (printed: 2) —
			// matches typical mar.json formatting.
			missing = append(missing, `"mail": {
  "from": "...",
  "smtpHost": "...",
  "smtpUsername": "...",
  "smtpPassword": "env:SMTP_PASSWORD"
}`)
		} else {
			var partial []string
			if manifest.Mail.From == "" {
				partial = append(partial, `"from"`)
			}
			if manifest.Mail.SMTPHost == "" {
				partial = append(partial, `"smtpHost"`)
			}
			if manifest.Mail.SMTPUsername == "" {
				partial = append(partial, `"smtpUsername"`)
			}
			if manifest.Mail.SMTPPassword == "" {
				partial = append(partial, `"smtpPassword"`)
			}
			if len(partial) > 0 {
				missing = append(missing, fmt.Sprintf(`"mail" needs: %s`, strings.Join(partial, ", ")))
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &ProductionConfigError{Missing: missing}
}

// buildFrontendDist writes the static asset bundle for App.frontend
// projects. Output is plain files servable by any HTTP host.
//
// program.json is embedded directly into the index.html so the browser
// boots in a single round-trip — no waterfall fetch for the AST after
// the runtime loads. runtime.js stays separate so it can be cached
// independently across deploys (its content rarely changes).
func buildFrontendDist(distDir string, bc *buildCtx) error {
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return err
	}
	progJSON, err := makeProgramJSON(bc.frontMods, "main", false)
	if err != nil {
		return err
	}
	html := buildIndexHTML(bc.title, progJSON)
	runtimeJS, err := jsserve.RuntimeJSProduction()
	if err != nil {
		return err
	}
	files := map[string][]byte{
		"index.html": []byte(html),
		"runtime.js": []byte(runtimeJS),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(distDir, name), content, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("[mar build] wrote %d files to %s\n", len(files), distDir)
	return nil
}

// buildServerExecutable produces a self-contained executable: a
// pre-built mar-runtime stub for `target` concatenated with a ZIP
// payload of the project sources + mar.json. The resulting binary
// extracts its own payload at startup, so no mar toolchain is needed
// on the deploy host.
func buildServerExecutable(projectDir, distDir, target string, bc *buildCtx) error {
	stubBytes, err := stubs.Get(target)
	if err != nil {
		return err
	}

	manifestPath := filepath.Join(projectDir, "mar.json")
	manifestJSON, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", manifestPath, err)
	}

	sources, err := appbundle.CollectSources(projectDir)
	if err != nil {
		return err
	}

	payload, err := appbundle.BuildPayload(appbundle.BuildInput{
		ManifestJSON: manifestJSON,
		Sources:      sources,
	})
	if err != nil {
		return err
	}

	name := outputName(projectDir, target)
	outputPath := filepath.Join(distDir, name)
	if err := appbundle.WriteExecutable(stubBytes, payload, outputPath); err != nil {
		return err
	}

	totalSize := len(stubBytes) + len(payload)
	fmt.Printf("[mar build] %s\n", outputPath)
	fmt.Printf("            target: %s   size: %s   sources: %d files\n",
		target, humanSize(totalSize), len(sources))
	return nil
}

// outputName picks a filename for the produced executable. Uses the
// `name` field from mar.json if present, otherwise the project
// directory name. Adds `.exe` for Windows targets so the file is
// runnable as-is on Windows hosts.
func outputName(projectDir, target string) string {
	base := ""
	if data, err := os.ReadFile(filepath.Join(projectDir, "mar.json")); err == nil {
		var m struct{ Name string }
		if json.Unmarshal(data, &m) == nil {
			base = m.Name
		}
	}
	if base == "" {
		base = filepath.Base(filepath.Clean(projectDir))
		if base == "." || base == "/" {
			base = "app"
		}
	}
	if strings.HasPrefix(target, "windows-") {
		base += ".exe"
	}
	return base
}

func humanSize(n int) string {
	const KB, MB = 1024, 1024 * 1024
	switch {
	case n >= MB:
		return fmt.Sprintf("%.1f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.1f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// --- internals: capture overrides ---

type buildKind int

const (
	kindUnset buildKind = iota
	kindFrontend
	kindBackend
	kindFullstack
)

// buildCtx is the build-time analog of jsserve.LiveProgram. The override
// builtins write into it; Build reads it after evaluating Main.main.
type buildCtx struct {
	kind      buildKind
	frontMods []*ast.Module
	title     string
}

func makeFrontendCapture(mods []*ast.Module, bc *buildCtx) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			pageList, ok := args[0].(runtime.VList)
			if !ok {
				return nil, fmt.Errorf("App.frontend: expected List Page (got %T)", args[0])
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
			return runtime.VEffect{Tag: "appFrontend", Run: func() (runtime.Value, error) {
				return runtime.VUnit{}, nil
			}}, nil
		},
	}
}

func makeBackendCapture(bc *buildCtx) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			bc.kind = kindBackend
			return runtime.VEffect{Tag: "appBackend", Run: func() (runtime.Value, error) {
				return runtime.VUnit{}, nil
			}}, nil
		},
	}
}

func makeFullstackCapture(mods []*ast.Module, bc *buildCtx) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			bc.kind = kindFullstack
			return runtime.VEffect{Tag: "appFullstack", Run: func() (runtime.Value, error) {
				return runtime.VUnit{}, nil
			}}, nil
		},
	}
}

// reachableFrom + joinName are tiny duplicates of helpers in cmd/mar
// and project — kept here to avoid circular imports.
func reachableFrom(startModule string, mods []*ast.Module) []*ast.Module {
	byName := map[string]*ast.Module{}
	for _, m := range mods {
		byName[joinName(m.Name)] = m
	}
	visited := map[string]bool{}
	var order []*ast.Module
	var visit func(name string)
	visit = func(name string) {
		if visited[name] {
			return
		}
		visited[name] = true
		mod, ok := byName[name]
		if !ok {
			return
		}
		for _, imp := range mod.Imports {
			visit(joinName(imp.Module))
		}
		order = append(order, mod)
	}
	visit(startModule)
	return order
}

func joinName(parts []string) string {
	return strings.Join(parts, ".")
}

// makeProgramJSON serializes the merged frontend modules as the
// browser bundle. devMode controls whether the JS runtime sets up
// dev affordances (banner, SSE, time-travel) — false for built dists.
func makeProgramJSON(mods []*ast.Module, entry string, devMode bool) ([]byte, error) {
	// Send modules separately so the browser/iOS runtime can register
	// each decl under both its bare name (for intra-module references
	// during evaluation) and a qualified `Module.name` form (so
	// EQualified lookups from other modules resolve correctly). A
	// previous version merged everything into one module here, which
	// silently overwrote same-named decls across modules — two
	// `Frontend.SignIn.page` and `Frontend.Home.page` collapsed into
	// whichever was evaluated last, breaking multi-page apps.
	serializedModules := make([]any, 0, len(mods))
	for _, m := range mods {
		serializedModules = append(serializedModules, jsserve.SerializeModule(m))
	}
	return json.Marshal(map[string]any{
		"modules": serializedModules,
		"entry":   entry,
		"devMode": devMode,
	})
}

// buildIndexHTML produces the production HTML page with `program.json`
// embedded inline as a JSON script element. Differences from the dev
// version: no SSE reload connection, no dev banner, no waterfall fetch
// of the AST — boot is one round-trip total (HTML + runtime.js).
func buildIndexHTML(title string, programJSON []byte) string {
	if title == "" {
		title = "mar app"
	}
	// </script> inside JSON would prematurely close the script tag.
	// Escape the < as < — JSON.parse ignores it, no other char
	// classes need escaping in <script type="application/json">.
	safeProgram := strings.ReplaceAll(string(programJSON), "</", `</`)
	return fmt.Sprintf(productionPageHTML, title, safeProgram)
}

const productionPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<style>
  *, *::before, *::after { box-sizing: border-box; }
  :root {
    --fg: #1a1a1a; --bg: #fafafa; --surface: #fff; --border: #e2e2e2;
    --accent: #2563eb; --radius: 6px;
  }
  html, body { margin: 0; padding: 0; }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
    font-size: 15px; line-height: 1.4;
    color: var(--fg); background: var(--bg);
    padding: 1.5rem;
  }
  h1 { font-size: 1.75rem; font-weight: 700; margin: 0 0 0.5rem; }
  h2 { font-size: 1.1rem; font-weight: 600; margin: 1rem 0 0.4rem; }
  button {
    appearance: none; border: 1px solid var(--border);
    background: var(--surface); color: var(--fg);
    padding: 0.4rem 0.9rem; border-radius: var(--radius);
    font: inherit; cursor: pointer;
  }
  button:hover { background: #f0f0f0; }
  input:not([type="checkbox"]):not([type="radio"]):not([type="submit"]):not([type="button"]):not([type="file"]),
  textarea {
    border: 1px solid var(--border); background: var(--surface);
    padding: 0.45rem 0.6rem; border-radius: var(--radius);
    font: inherit; width: 100%%; max-width: 24rem;
  }
  textarea { min-height: 4.5rem; resize: vertical; }
  a { color: var(--accent); text-decoration: none; }
  a:hover { text-decoration: underline; }
  ul { list-style: none; padding: 0; margin: 0; }
  li { padding: 0.35rem 0; }
  li + li { border-top: 1px solid var(--border); }
  section { padding: 1rem 0; }
  #mar-root {
    display: flex; flex-direction: column;
    min-height: calc(100vh - 3rem);
  }
</style>
</head>
<body>
<div id="mar-root"></div>
<script type="application/json" id="mar-program">%s</script>
<script src="./runtime.js"></script>
<script>
window.addEventListener('DOMContentLoaded', function () {
  try {
    var raw = document.getElementById('mar-program').textContent;
    marRun(JSON.parse(raw));
  } catch (e) {
    var root = document.getElementById('mar-root');
    var pre = document.createElement('pre');
    pre.style.color = '#b00';
    pre.textContent = String(e && e.message || e);
    root.appendChild(pre);
  }
});
</script>
</body>
</html>`
