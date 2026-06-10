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
	"mar/internal/apphost"
	"mar/internal/ast"
	"mar/internal/jsserve"
	"mar/internal/project"
	"mar/internal/pwa"
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
//     index.html, runtime.js, program.json
//     Servable as plain files from any HTTP host (CDN, S3, etc.).
//
//   - App.backend or App.fullstack → self-contained executable:
//     <distDir>/<projectName>      (or .exe on windows)
//     The mar-runtime stub for `target` is concatenated with a ZIP
//     payload containing mar.json + every .mar source file. On startup
//     the binary reads its own bytes, extracts the payload, and serves
//     HTTP — no external mar toolchain required on the deploy host.
func Build(entry, distDir, target string) error {
	// Validate mar.json structure up front — before running any user
	// code or writing output: unknown/misplaced keys + shape. Mirrors
	// what `mar dev` enforces at load (same strict path), so a typo'd or
	// misplaced config key fails the build here instead of being silently
	// ignored — then only surfacing under `mar dev`, or worse, in
	// production falling back to a default. Structure-only (no env
	// resolution), so it never trips on unset prod secrets. `entry` may
	// be a file or a directory.
	manifestDir := entry
	if info, err := os.Stat(entry); err == nil && !info.IsDir() {
		manifestDir = filepath.Dir(entry)
	}
	if _, err := project.LoadManifestStructure(manifestDir); err != nil {
		return err
	}

	projectDir, bc, err := loadAndRunForBuild(entry)
	if err != nil {
		return err
	}
	if target == "" {
		target = stubs.HostTarget()
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
		if err := ValidateProductionConfig(projectDir); err != nil {
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
		return buildFrontendDist(projectDir, distDir, bc)
	case kindBackend, kindFullstack:
		return buildServerExecutable(projectDir, distDir, target, bc)
	default:
		return fmt.Errorf("main didn't call any of App.frontend / App.backend / App.fullstack — nothing to build")
	}
}

// isProductionTarget reports whether `target` will be deployed to a
// real host (vs. used as a local debugging artifact). Host target is
// treated as dev; any non-host target (linux-amd64, etc.) is a deploy.
//
// To force prod-shaped output on your local machine without a deploy,
// pick an explicit non-host target: `mar build --target=linux-amd64`
// produces the same artifact `mar fly deploy` would push.
func isProductionTarget(target string) bool {
	if target == "" {
		return false
	}
	if target == stubs.HostTarget() {
		return false
	}
	return true
}

// warnIfNoAdmins prints a stderr block when a production build has
// no admins configured. Doesn't fail the build — some apps legitimately
// don't want an admin panel — but flags the situation prominently so
// the operator doesn't realize too late that prod has no admin access.
//
// `cmd/mar/fly.go` repeats this warning at the END of `mar fly deploy`
// (after the long `fly deploy` output, when the operator's attention
// is back) so the message isn't lost in scrollback.
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
		"Warn: this production build has no admins configured.")
	fmt.Fprintln(os.Stderr,
		"      The admin panel at /_mar/admin will reject every login.")
	fmt.Fprintln(os.Stderr,
		"      Add an admin BEFORE deploying so you can manage prod:")
	fmt.Fprintln(os.Stderr,
		"        mar admin add <email>")
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

Hints:
  - smtpPort is optional and defaults to 587 (Resend, SendGrid,
    Mailgun, AWS SES, Postmark, Brevo, Mailjet all use it).
  - sessionSecret and smtpPassword MUST be env:VAR_NAME — push
    the actual values to Fly with 'mar fly secrets sync'.
  - For Resend: smtpHost is "smtp.resend.com", smtpUsername is
    "resend" (literal), and smtpPassword is your API key from
    https://resend.com/api-keys.`, joinMissingForPaste(e.Missing))
}

// joinMissingForPaste stitches the suggested mar.json fragments
// together with valid commas between them, so the operator can
// paste straight into mar.json without manually adding the comma
// between top-level properties. The fragments themselves don't
// carry a trailing comma (the validator builds them that way to
// stay readable as standalone JSON).
//
// The connecting comma always lands on the closing line of one
// entry, just before the newline-then-next-entry. The last entry
// has no comma — it might be the last property in the user's
// mar.json or might already have a comma in the user's file.
func joinMissingForPaste(missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	parts := make([]string, len(missing))
	for i, m := range missing {
		if i < len(missing)-1 {
			parts[i] = m + ","
		} else {
			parts[i] = m
		}
	}
	return strings.Join(parts, "\n  ")
}

// loadAndRunForBuild does the load + run-main step shared by Build
// and Topology. Returns the project directory + the populated
// buildCtx (which captures App.frontend/backend/fullstack calls)
// so the caller can decide what to do next.
//
// Side effect that matters: running main also registers global
// runtime state (Entity.define hits runtime.RegisteredEntities,
// Auth.config sets runtime.CurrentAuth, etc.). ValidateProductionConfig
// reads CurrentAuth to know whether the app needs mail config, so it
// only runs after main has: Build does so directly; `mar fly deploy`
// validates after Topology has run main.
func loadAndRunForBuild(entry string) (projectDir string, bc *buildCtx, err error) {
	info, err := os.Stat(entry)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %v", entry, err)
	}
	mainFile := entry
	projectDir = entry
	if info.IsDir() {
		mainFile = filepath.Join(entry, "Main.mar")
		if _, err := os.Stat(mainFile); err != nil {
			return "", nil, fmt.Errorf("Main.mar not found in %s", entry)
		}
	} else {
		projectDir = filepath.Dir(entry)
	}

	// Same load + override pattern as `mar dev`, but the overrides
	// capture into a build context instead of a live server.
	bc = &buildCtx{}
	rEnv, mods, _, err := project.LoadIntoEnvWithModulesAndHook(mainFile,
		func(env *runtime.Env, mods []*ast.Module) {
			fe := makeFrontendCapture(mods, bc)
			env.Define("appFrontend", fe)
			env.Define("App.frontend", fe)

			be := makeBackendCapture(bc)
			env.Define("appBackend", be)
			env.Define("App.backend", be)

			fs := makeFullstackCapture(bc)
			env.Define("appFullstack", fs)
			env.Define("App.fullstack", fs)
		})
	if err != nil {
		return "", nil, err
	}
	mainVal, ok := project.LookupMain(rEnv, mods)
	if !ok {
		return "", nil, fmt.Errorf("Main.mar must export `main`")
	}
	eff, ok := mainVal.(runtime.VEffect)
	if !ok {
		return "", nil, fmt.Errorf("main is not an Effect (got %T)", mainVal)
	}
	if _, err := eff.Run(); err != nil {
		return "", nil, err
	}
	return projectDir, bc, nil
}

// Topology reports which App.* the project's main calls — "frontend",
// "backend", or "fullstack". Runs main as a side effect (same path
// Build / Preflight take), so the cost is one full evaluation.
//
// Used by `mar fly deploy` to pick the right Dockerfile shape:
// frontend ships static files via Caddy; backend / fullstack ship
// the self-contained binary. The fly subcommand calls this BEFORE
// any cloud interaction so the operator sees compile errors in
// main / mar.json first.
//
// Returns an error when main doesn\'t call any App.* (the project
// has no executable shape).
func Topology(projectDir string) (string, error) {
	_, bc, err := loadAndRunForBuild(projectDir)
	if err != nil {
		return "", err
	}
	switch bc.kind {
	case kindFrontend:
		return "frontend", nil
	case kindBackend:
		return "backend", nil
	case kindFullstack:
		return "fullstack", nil
	default:
		return "", fmt.Errorf("main didn't call any of App.frontend / App.backend / App.fullstack — nothing to deploy")
	}
}

// ValidateProductionConfig asserts the project's mar.json carries
// the configuration the chosen runtime features need. Today
// validates auth, mail, and the framework admin panel; other
// features (Stripe, Twilio, etc.) would register their own
// validators here.
//
// Returns *ProductionConfigError when mar.json needs additions.
// Returns a plain error for unrelated I/O / parse problems.
//
// Called automatically by Build for production targets, AND by
// `mar fly provision` as a pre-flight (so the operator catches the
// gap before any Fly resources are created — otherwise provision
// would succeed and the deploy would fail late with the same error).
// Public so external callers (the fly provision wrapper) can run it.
func ValidateProductionConfig(projectDir string) error {
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
// the runtime loads. runtime.js stays separate but is revalidated on
// every load (see the `_headers` file below) so a framework upgrade is
// never masked by a stale cached copy.
func buildFrontendDist(projectDir, distDir string, bc *buildCtx) error {
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		return err
	}
	// Entry name "__entry" matches the synthetic decl PickFrontMods
	// appends to bc.frontMods. Using "main" here would crash the
	// browser with "entry not found: main" because the Main module
	// itself isn\'t in frontMods (only modules reachable FROM pages
	// are) — same convention as dev (apphost.go) and iOS (iosbuild.go).
	progJSON, err := makeProgramJSON(bc.frontMods, "__entry", false)
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
		// Cloudflare Pages (and Netlify) read a `_headers` file to set
		// response headers per path. Force revalidation on every asset:
		// index.html carries the inlined program (a stale copy IS the
		// whole old app), and runtime.js keeps a fixed name across
		// deploys (a framework bump would otherwise be masked by a
		// cached copy). `no-cache` still lets the browser STORE the
		// response and send a conditional request — the host's ETag
		// turns the common unchanged case into a cheap 304. This is
		// what stops "I see the old version until I hard-refresh"
		// without re-downloading everything on each load. Hosts that
		// don't understand `_headers` simply ignore the extra file.
		"_headers": []byte("/*\n  Cache-Control: no-cache\n"),
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(distDir, name), content, 0o644); err != nil {
			return err
		}
	}
	// PWA: write the Web App Manifest + icons into dist/_mar/ so the
	// deployed static bundle is installable (the HTML shell references
	// /_mar/manifest.json + /_mar/icon-*.png). Mirrors what `mar dev`
	// serves live from the same config. Always emitted — every Mar
	// frontend is installable by default.
	pwaCount, err := writePWAAssets(projectDir, distDir)
	if err != nil {
		return err
	}

	// Static assets: copy the project's public/ folder verbatim into
	// dist/ (e.g. public/logo.svg → dist/logo.svg, served at /logo.svg).
	// Matches what `mar dev` serves from the same folder, so a path
	// works identically in dev and in the deployed bundle.
	copied, err := copyPublicDir(filepath.Join(projectDir, "public"), distDir)
	if err != nil {
		return err
	}
	fmt.Printf("[mar build] wrote %d files to %s\n", len(files)+pwaCount+copied, distDir)
	return nil
}

// writePWAAssets validates the pwa.icon (if any) and writes the Web App
// Manifest + every icon size into dist/_mar/. Returns the file count.
// A bad icon fails the build (same check `mar dev` runs at boot).
func writePWAAssets(projectDir, distDir string) (int, error) {
	manifest, _ := project.LoadManifestDev(projectDir) // tolerant: nil is fine
	if err := project.ValidatePWAIcon(projectDir, manifest); err != nil {
		return 0, err
	}
	cfg := manifest.ResolvePWA(projectDir)
	dir := filepath.Join(distDir, "_mar")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), pwa.ManifestJSON(cfg), 0o644); err != nil {
		return 0, err
	}
	count := 1
	for _, size := range pwa.IconSizes {
		b, err := pwa.IconPNG(cfg, size)
		if err != nil {
			return count, fmt.Errorf("generate icon-%d: %w", size, err)
		}
		name := fmt.Sprintf("icon-%d.png", size)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return count, err
		}
		count++
	}
	// /favicon.ico at the dist root — the path browsers request
	// implicitly. PNG bytes (browsers accept PNG at .ico).
	fav, err := pwa.IconPNG(cfg, pwa.FaviconSize)
	if err != nil {
		return count, fmt.Errorf("generate favicon: %w", err)
	}
	if err := os.WriteFile(filepath.Join(distDir, "favicon.ico"), fav, 0o644); err != nil {
		return count, err
	}
	count++
	return count, nil
}

// copyPublicDir copies every file under src into distDir, preserving
// the relative tree (src/img/a.png → distDir/img/a.png). A missing src
// is not an error — most projects have no public/ folder. Dotfiles are
// skipped (e.g. .DS_Store). Returns the number of files copied.
func copyPublicDir(src, distDir string) (int, error) {
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		return 0, nil // no public/ folder — nothing to copy
	}
	count := 0
	err = filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		if strings.HasPrefix(fi.Name(), ".") {
			return nil // skip .DS_Store and friends
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		// Refuse files that collide with Mar's reserved namespace —
		// either a generated dist file we'd silently overwrite, or a
		// server route prefix the runtime owns (so the asset would be
		// shadowed in dev/fullstack and never served). Fail loud at
		// build time instead of shipping a broken or invisible asset.
		if reason := jsserve.ReservedPublicPath(rel); reason != "" {
			return fmt.Errorf("public/%s conflicts with Mar's %s; rename it",
				filepath.ToSlash(rel), reason)
		}
		dst := filepath.Join(distDir, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			return err
		}
		count++
		return nil
	})
	return count, err
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
	// pages is the raw list of VPage values captured by App.frontend
	// / App.fullstack. Saved so post-eval callers can build the
	// synthetic `__entry = appFrontend [pages]` module that the
	// browser / iOS runtimes look up at boot. Without it, the
	// generated bundle has no top-level expression to evaluate and
	// the runtime fails to mount any page.
	pages []runtime.Value
	title string
}

func makeFrontendCapture(mods []*ast.Module, bc *buildCtx) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			pageList, ok := args[0].(runtime.VList)
			if !ok {
				return nil, fmt.Errorf("App.frontend: expected List Page (got %T)", args[0])
			}
			// Delegate to apphost.PickFrontMods — the single source of
			// truth for "page-reachable modules + synthetic __entry
			// module". All three build paths (dev/apphost.go,
			// web/here, iOS/iosbuild.go) go through it so the
			// synthetic entry module always lands in the bundle.
			// Skipping it would leave `envLookup("main")` failing at
			// boot with "entry not found: main".
			merged, err := apphost.PickFrontMods(pageList.Elements, mods)
			if err != nil {
				return nil, err
			}
			bc.kind = kindFrontend
			bc.frontMods = merged
			bc.pages = pageList.Elements
			// Title heuristic: pick the last USER-named module. The
			// synthetic __entry module that PickFrontMods appends has
			// Name == nil, so skip it.
			for i := len(merged) - 1; i >= 0; i-- {
				if nm := merged[i].Name; len(nm) > 0 {
					bc.title = nm[len(nm)-1]
					break
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

func makeFullstackCapture(bc *buildCtx) runtime.Value {
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

// makeProgramJSON serializes the merged frontend modules as the
// browser bundle. devMode controls whether the JS runtime sets up
// dev affordances (banner, SSE, time-travel) — false for built dists.
func makeProgramJSON(mods []*ast.Module, entry string, devMode bool) ([]byte, error) {
	// Send modules separately so the browser/iOS runtime can register
	// each decl under both its bare name (for intra-module references
	// during evaluation) and a qualified `Module.name` form (so
	// EQualified lookups from other modules resolve correctly).
	// Merging everything into one module here is tempting but silently
	// overwrites same-named decls across modules — e.g. both
	// `Frontend.SignIn.page` and `Frontend.Home.page` would collapse
	// into whichever was evaluated last, breaking multi-page apps.
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
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<!-- PWA: manifest + icons written into dist/ by mar build (see
     buildFrontendDist). Makes the deployed app installable + fullscreen. -->
<link rel="manifest" href="/_mar/manifest.json">
<link rel="apple-touch-icon" href="/_mar/icon-180.png">
<link rel="icon" type="image/png" href="/_mar/icon-192.png">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-status-bar-style" content="default">
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
<script src="/runtime.js"></script>
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
