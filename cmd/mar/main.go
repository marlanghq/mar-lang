// Command mar is the entry point for the Mar compiler and tooling.
//
// mar is a full-stack web language. The driver reflects that focus:
//
//	mar dev [path]         Run a fullstack/frontend/backend app in dev mode
//	                       (hot reload, dev banner, browser-open).
//	mar build [dir]        Compile a project to a static dist/.
//	mar init <name>        Scaffold a new project.
//	mar check <file>       Type-check (without running).
//	mar repl               Interactive REPL.
//	mar format <file>...   Reformat in place.
//	mar lsp                Language server (used by editor extensions).
//	mar config <dir>       Print mar.json from a project.
//	mar version            Print the version.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"mar/internal/admin"
	"mar/internal/apphost"
	"mar/internal/ast"
	"mar/internal/diag"
	"mar/internal/formatter"
	"mar/internal/jsserve"
	"mar/internal/lsp"
	"mar/internal/parser"
	"mar/internal/project"
	"mar/internal/runtime"
	"mar/internal/scaffold"
	"mar/internal/typecheck"
)

// openBrowserWhenReady polls the dev URL until it answers, then asks the
// OS to open it. Done once per `mar dev` invocation. Set MAR_NO_OPEN=1 in
// the environment to skip (useful for CI or when running headless).
func openBrowserWhenReady(url string) {
	if os.Getenv("MAR_NO_OPEN") != "" {
		return
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", strings.TrimPrefix(url, "http://"), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			openURL(url)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Server never came up — silently give up. The user already sees the
	// startup logs, no need to add noise.
}

// openURL invokes the OS-native "open this URL" command. Errors are
// non-fatal — the dev server keeps running even if the browser launch
// fails (e.g. on a headless box without the helper binary installed).
func openURL(url string) {
	var cmd *exec.Cmd
	switch goruntime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default: // linux, *bsd
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}

// runBuild handles `mar build [--target T] [--out DIR] [path]`.
//
// Behavior depends on the topology the project's main picks:
//   - App.frontend → static dist/ (HTML + JS + program.json).
//   - App.backend / App.fullstack → self-contained executable embedding
//     the cross-compiled mar-runtime stub for `target` plus a ZIP of
//     the project sources. Default target is the host OS/arch.
func runBuild(args []string) int {
	entry := "."
	target := ""
	outDir := ""
	baseURL := ""
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--target" || a == "-t":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "mar build: --target needs a value (e.g. linux-amd64)")
				return 2
			}
			target = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--target="):
			target = strings.TrimPrefix(a, "--target=")
			i++
		case a == "--out" || a == "-o":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "mar build: --out needs a value")
				return 2
			}
			outDir = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--out="):
			outDir = strings.TrimPrefix(a, "--out=")
			i++
		case a == "--base-url":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "mar build: --base-url needs a value")
				return 2
			}
			baseURL = args[i+1]
			i += 2
		case strings.HasPrefix(a, "--base-url="):
			baseURL = strings.TrimPrefix(a, "--base-url=")
			i++
		case a == "-h" || a == "--help":
			fmt.Println(buildUsage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(os.Stderr, "mar build: unknown flag %q\n", a)
			return 2
		default:
			entry = a
			i++
		}
	}

	// distDir defaults to "dist" alongside the entry (whether entry is
	// a file or a directory).
	if outDir == "" {
		baseDir := entry
		if info, err := os.Stat(entry); err == nil && !info.IsDir() {
			baseDir = filepath.Dir(entry)
		}
		outDir = filepath.Join(baseDir, "dist")
	}

	// iOS is its own pipeline: schema-driven scaffold, doesn't run
	// mar code at build time. Everything else (frontend / backend /
	// fullstack) goes through scaffold.Build.
	if target == "ios" {
		if err := scaffold.BuildIOS(entry, outDir, baseURL, version); err != nil {
			fmt.Fprintln(os.Stderr, diag.Format(err))
			return 1
		}
		return 0
	}
	if baseURL != "" {
		fmt.Fprintln(os.Stderr, "mar build: --base-url only applies to --target ios")
		return 2
	}
	if err := scaffold.Build(entry, outDir, target); err != nil {
		fmt.Fprintln(os.Stderr, diag.Format(err))
		return 1
	}
	return 0
}

const buildUsage = `Usage: mar build [--target <T>] [--out <dir>] [--base-url <url>] [path]

Compile a mar project to a deployable artifact.

For App.frontend projects:
  Writes a static dist/ (index.html + runtime.js + program.json) to <dir>.

For App.backend / App.fullstack projects:
  Writes a self-contained executable to <dir>/<projectName> by
  concatenating the cross-compiled mar-runtime stub for <target> with
  a ZIP payload of the project sources + mar.json. The resulting
  binary needs no mar toolchain on the deploy host — just run it.

For --target ios:
  Generates a disposable Xcode project under <dir>/<AppName>/. The
  Swift app discovers your backend via /_mar/schema on cold start, so
  changing your mar code updates the app over the air without
  re-submitting to the App Store. Run xcodegen + open in Xcode.

Flags:
  --target, -t   Build target. Native: darwin-amd64, darwin-arm64,
                 linux-amd64, linux-arm64, windows-amd64. Mobile: ios.
                 Defaults to the host OS/arch.
  --out, -o      Output directory (default: <project>/dist).
  --base-url     iOS only: default backend URL baked into the
                 generated app (overridable at runtime in Settings).
                 Default: http://localhost:3000.

Path defaults to "." (Main.mar in the current directory).`

// runFormat handles `mar format [--check] <files...>`. With files,
// each is rewritten in place. With --check, the command exits 1 if
// any file would change — useful in CI to enforce formatting.
func runFormat(args []string) int {
	check := false
	files := []string{}
	for _, a := range args {
		switch a {
		case "--check":
			check = true
		case "-h", "--help":
			fmt.Println("usage: mar format [--check] <file.mar> [file.mar...]")
			return 0
		default:
			if strings.HasPrefix(a, "-") {
				fmt.Fprintf(os.Stderr, "mar format: unknown flag %q\n", a)
				return 2
			}
			files = append(files, a)
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "mar format: no files given\n\nusage: mar format [--check] <file.mar> [file.mar...]")
		return 2
	}
	dirty := 0
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			fprintError("mar format: %v", err)
			return 1
		}
		formatted := formatter.Format(string(src))
		if formatted == string(src) {
			continue
		}
		dirty++
		if check {
			fmt.Fprintf(os.Stderr, "%s: needs formatting\n", file)
			continue
		}
		if err := os.WriteFile(file, []byte(formatted), 0o644); err != nil {
			fprintError("mar format: %v", err)
			return 1
		}
		fmt.Printf("formatted %s\n", file)
	}
	if check && dirty > 0 {
		return 1
	}
	return 0
}

// lookupMainType finds the type of the entry module's `main`. Module
// names vary (`Main`, `Calculator`, etc.) — keys in valueTypes are
// "Module.value" — so just pick the first ".main" entry. There's only
// ever one `main` in a project (the entry's), so this is unambiguous.
func lookupMainType(valueTypes map[string]typecheck.Type) typecheck.Type {
	if t, ok := valueTypes["Main.main"]; ok {
		return t
	}
	for k, t := range valueTypes {
		if strings.HasSuffix(k, ".main") {
			return t
		}
	}
	return nil
}

// checkMainSignature reports whether t is `Effect String ()`. Returns
// an empty string when the signature is acceptable, else a short
// human-readable message describing the mismatch. Wrapping in a
// `forall` is fine — main can be polymorphic in unused variables.
func checkMainSignature(t typecheck.Type) string {
	if f, ok := t.(typecheck.TForall); ok {
		t = f.Body
	}
	con, ok := t.(typecheck.TCon)
	if !ok || con.Name != "Effect" || len(con.Args) != 2 {
		return fmt.Sprintf("main has type `%s`, expected `Effect String ()`", typecheck.Pretty(t))
	}
	errCon, eOk := con.Args[0].(typecheck.TCon)
	if !eOk || errCon.Name != "String" || len(errCon.Args) != 0 {
		return fmt.Sprintf("main has type `%s`, expected `Effect String ()` (error channel must be String)", typecheck.Pretty(t))
	}
	if _, uOk := con.Args[1].(typecheck.TUnit); !uOk {
		return fmt.Sprintf("main has type `%s`, expected `Effect String ()` (success value must be unit `()`)", typecheck.Pretty(t))
	}
	return ""
}

// version and commit are populated at build time via -ldflags.
// See Makefile.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "check":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "mar check: missing file argument")
			os.Exit(2)
		}
		os.Exit(runCheck(os.Args[2]))
	case "repl":
		os.Exit(runRepl())
	case "config":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "mar config: missing project directory")
			os.Exit(2)
		}
		os.Exit(runConfig(os.Args[2]))
	case "dev":
		path := "."
		if len(os.Args) >= 3 {
			path = os.Args[2]
		}
		os.Exit(runDev(path))
	case "format":
		// `mar format <file>` — rewrite in place. `mar format --check`
		// exits 1 if any file needs reformatting (CI-friendly).
		os.Exit(runFormat(os.Args[2:]))
	case "lsp":
		// Language server over stdio. Editors (VSCode, etc.) launch
		// `mar lsp` and pipe LSP JSON-RPC over stdin/stdout.
		if err := lsp.RunStdio(); err != nil {
			fprintError("mar lsp: %v", err)
			os.Exit(1)
		}
	case "init":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "mar init: missing project name\n\nusage: mar init <name>")
			os.Exit(2)
		}
		name := os.Args[2]
		if err := scaffold.Init(name); err != nil {
			fprintError("mar init: %v", err)
			os.Exit(1)
		}
		fmt.Printf("Created %s/\n  cd %s && mar dev\n", name, name)
	case "build":
		os.Exit(runBuild(os.Args[2:]))
	case "migrate":
		os.Exit(runMigrate(os.Args[2:]))
	case "fly":
		os.Exit(runFly(os.Args[2:]))
	case "admin":
		os.Exit(runAdmin(os.Args[2:]))
	case "completion":
		os.Exit(runCompletion(os.Args[2:]))
	case "version", "--version", "-v":
		fmt.Printf("%s (%s)\n", version, commit)
	case "help", "--help", "-h":
		usage()
	default:
		// `mar foo.mar` or `mar examples/notes-auth` look like the
		// user wanted to run a project but forgot the subcommand.
		// Don't infer — make the intent explicit. Suggest the right
		// command and exit non-zero so scripts notice.
		arg := os.Args[1]
		if looksLikePath(arg) {
			fprintError("mar: %q is not a command.", arg)
			fprintHint("to run a project, type %s.", colorGreen(fmt.Sprintf("mar dev %s", arg)))
			os.Exit(2)
		}
		fprintError("mar: unknown command %q.", arg)
		fprintHint("run %s for the command list.", colorGreen("mar help"))
		os.Exit(2)
	}
}

// looksLikePath reports whether `s` looks like a path attempt —
// contains a separator, ends in .mar, or actually exists on disk.
// Used to give a more specific "did you mean" hint when the user
// almost-but-not-quite typed a real command.
func looksLikePath(s string) bool {
	if strings.HasSuffix(s, ".mar") {
		return true
	}
	if strings.ContainsAny(s, "/\\") {
		return true
	}
	if _, err := os.Stat(s); err == nil {
		return true
	}
	return false
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: mar <command> [args]

Commands:
  dev    [path]              Run with hot reload (path defaults to ".")
  build  [path] [--target T] Compile to dist/ (frontend) or binary (backend)
                             [--out DIR] [--base-url URL]
  init   <name>              Scaffold a new project at <name>/
  check  <path>              Parse + type-check (no run)
  format [--check] <file>... Reformat .mar files in place
  config <dir>               Print mar.json
  migrate <plan|status> [path] Show pending / applied schema migrations (read-only)
  fly <init|provision|deploy|destroy|logs|status|admin> [path]
                             Full Fly.io deployment workflow
  admin <add|remove|list> [args]
                             Manage admin panel access (mar.json admins list)
  repl                       Interactive REPL
  lsp                        Language server over stdio
  completion <shell>         Generate shell completion (zsh, bash, fish)
  version                    Print version

Run 'mar <command> --help' for command-specific help.`)
}

func runCheck(path string) int {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar: %v\n", err)
		return 1
	}
	if info.IsDir() {
		proj, err := project.Load(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, diag.Format(err))
			return 1
		}
		fmt.Printf("project %s — OK (%d modules)\n", path, len(proj.Modules))
		for _, name := range proj.Order {
			m := proj.Modules[name]
			fmt.Printf("  %s\n", m.Name)
			for vname, t := range m.ValueTypes {
				fmt.Printf("    %s : %s\n", vname, typecheck.Pretty(t))
			}
		}
		return 0
	}
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar: %v\n", err)
		return 1
	}
	mod, err := parser.Parse(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, diag.Format(diag.Wrap(path, string(src), err)))
		return 1
	}
	res, err := typecheck.CheckModule(mod)
	if err != nil {
		fmt.Fprintln(os.Stderr, diag.Format(diag.Wrap(path, string(src), err)))
		return 1
	}
	fmt.Printf("module %s — OK\n", joinModuleName(mod.Name))
	if len(res.TypeAliases) > 0 {
		fmt.Println("\nType aliases:")
		for name := range res.TypeAliases {
			fmt.Printf("  %s\n", name)
		}
	}
	if len(res.CustomTypes) > 0 {
		fmt.Println("\nCustom types:")
		for name, ct := range res.CustomTypes {
			fmt.Printf("  %s = %s\n", name, joinCtors(ct.CtorOrder))
		}
	}
	if len(res.ValueTypes) > 0 {
		fmt.Println("\nValues:")
		for name, t := range res.ValueTypes {
			fmt.Printf("  %s : %s\n", name, typecheck.Pretty(t))
		}
	}
	return 0
}

// runDev evaluates `main` in dev mode. Path can be a .mar file (single-file
// app) or a directory containing Main.mar. Defaults to "." when called with
// no arguments.
//
// Whether the runtime serves a unified server, a browser-only app, or
// something else is decided by what `main` returns:
//
//	App.fullstack { api, page }   -> unified server (port from mar.json)
//	App.serve port app            -> browser-only single-screen app
//	App.serveScreens port screens -> browser-only multi-screen app
//	any other Effect              -> just runs the effect
//
// `mar dev` keeps the HTTP server up, watches the project files, and on
// every change re-runs the project to swap in the freshly compiled output
// (via a LiveProgram shared between the watcher and the server). Browsers
// stay connected via SSE on /_mar/reload and rebuild their DOM when a
// reload event fires.
func runDev(path string) int {
	entryFile, projectDir, err := resolveDevEntry(path)
	if err != nil {
		fprintError("mar dev: %v", err)
		return 1
	}

	// Resolve port from mar.json (default 3000). Same value used by
	// App.fullstack / App.serve / App.serveScreens — the language no
	// longer takes port as a code argument. Validation errors in the
	// manifest are fatal: surface them now rather than fall back silently.
	port := 3000
	manifest, err := project.LoadManifest(projectDir)
	if err != nil {
		fprintError("mar dev: %v", err)
		return 1
	}
	if manifest != nil && manifest.Server != nil && manifest.Server.Port != 0 {
		port = manifest.Server.Port
	}

	// Wire the SQLite path into the runtime — Repo.* lazy-opens this on
	// first use. ResolveDatabasePath honors MAR_DATABASE_PATH (override
	// for production deploys) and resolves relative paths against the
	// project directory so `./notes.db` lands next to Main.mar.
	dbPath, _ := project.ResolveDatabasePath(manifest, projectDir)
	if dbPath != "" {
		runtime.SetDBPath(dbPath)
	}

	// Auth: derive (or auto-generate, in dev) the session secret and
	// pass the SMTP credentials to the auth runtime. The handlers stay
	// dormant until the user's program calls `Auth.config` — at that
	// point ServeLive sees a registered VAuth and mounts /_auth/*.
	secret, secretSrc, err := project.ResolveSessionSecret(manifest, projectDir)
	if err != nil {
		fprintError("mar dev: %v", err)
		return 1
	}
	if secret != "" {
		jsserve.SetAuthRuntime(secret, project.ToSMTPConfig(manifest))
		_ = secretSrc // available for diagnostics if we want to log it later
		if manifest != nil && manifest.Mail != nil {
			jsserve.SetAdminMailFrom(manifest.Mail.From)
		}
	}
	jsserve.SetAdminBuildInfo(version)
	jsserve.SetAdminRequestBufferSize(project.ResolvedRecentRequestsSize(manifest))

	// Auto-backup scheduler — periodic VACUUM INTO into a catalog
	// directory alongside mar.db. No-op when the manifest disables
	// it (`database.autoBackup.enabled: false`) or when there's no
	// database configured. Runs in dev too so the developer sees
	// the catalog grow alongside their app — same code path as
	// production, no surprises at deploy time.
	if dbPath != "" {
		db, openErr := runtime.OpenDB()
		if openErr == nil {
			admin.MaybeStartAutoBackup(
				context.Background(),
				db, manifest, projectDir, dbPath, version,
			)
		}
	}

	lp := &jsserve.LiveProgram{}
	lp.SetDevMode(true)
	if manifest != nil && manifest.Name != "" {
		lp.SetAppName(manifest.Name)
	}
	hub := jsserve.NewReloadHub(lp)

	// compile loads + evaluates the project, capturing the served state
	// into lp. Returns a friendly error message instead of panicking so
	// the watcher can recover from compile errors during development.
	compile := func() error {
		// Hot-reload re-runs compile; clear the entity registry +
		// migration cache so stale declarations from the previous
		// version don't participate in the new migration plan, and
		// the per-entity ensureMigrated cache re-validates against
		// the (possibly updated) live schema.
		runtime.ResetRegisteredEntities()
		runtime.ResetMigrationCache()

		rEnv, _, valueTypes, err := project.LoadIntoEnvWithModulesAndHook(entryFile,
			func(env *runtime.Env, mods []*ast.Module) {
				apphost.Install(env, mods, port, lp)
			})
		if err != nil {
			return err
		}

		// Find main and validate its type signature. mar is a web language
		// — every entry point ships through `main : Effect String ()`,
		// where the Effect chooses the topology by calling App.frontend /
		// App.backend / App.fullstack. Reject anything else here so users
		// get a clear up-front error instead of confusing runtime
		// behavior.
		mainVal, ok := rEnv.Lookup("Main.main")
		if !ok {
			mainVal, ok = rEnv.Lookup("main")
		}
		if !ok {
			return fmt.Errorf("%s must export `main : Effect String ()`", entryFile)
		}
		if mainType := lookupMainType(valueTypes); mainType != nil {
			if msg := checkMainSignature(mainType); msg != "" {
				return fmt.Errorf("%s: %s\n\nmar entry points must be `main : Effect String ()` and pick a topology with App.frontend / App.backend / App.fullstack", entryFile, msg)
			}
		}
		eff, ok := mainVal.(runtime.VEffect)
		if !ok {
			return fmt.Errorf("main is not an Effect (got %T) — `mar dev` runs servers and UIs via App.frontend / App.backend / App.fullstack", mainVal)
		}
		// Running the Effect calls one of the overridden builtins, which
		// captures (api, pages) and updates lp. The builtin's Effect is a
		// no-op — we drive the server lifecycle from the CLI, not the
		// user's main.
		if _, err := eff.Run(); err != nil {
			return err
		}
		// Apply any pending schema migrations before the listener
		// accepts traffic. Hot-reloads also pass through here, so
		// editing an entity declaration triggers an immediate diff
		// + apply (no restart needed). Migrator silences the
		// no-change case to keep the dev loop quiet.
		if err := runtime.RunBootMigrations(); err != nil {
			return err
		}
		// Admin panel boot: ensure framework tables + sync from
		// mar.json["admins"]. Reloads pass through here too so
		// editing the admins list triggers an immediate re-sync.
		if err := bootAdminPanel(manifest); err != nil {
			return err
		}
		return nil
	}

	// First compile must succeed — otherwise there's nothing to serve.
	if err := compile(); err != nil {
		fmt.Fprintln(os.Stderr, diag.Format(err))
		return 1
	}
	if lp.Port() == 0 {
		// `main` didn't call any of the App.* overrides — nothing to host.
		// Just exit; this isn't a server.
		fmt.Fprintln(os.Stderr, "mar dev: main returned without invoking App.serve / App.fullstack / App.serveScreens — nothing to host")
		return 0
	}

	// Start the watcher in the background. Compile errors stay visible
	// in the terminal but don't tear the server down: the previous good
	// version stays in lp.
	go watchAndReload(projectDir, compile, hub, lp)

	// Open the browser to the dev URL once the server is ready. Honors
	// $MAR_NO_OPEN for headless / CI runs.
	go openBrowserWhenReady(fmt.Sprintf("http://localhost:%d", port))

	// Block on the HTTP server.
	if err := jsserve.ServeLive(port, lp, hub); err != nil {
		// "address already in use" is the most common mar dev failure
		// (forgot to stop a prior instance; another process holds the
		// port) and the raw Go error is opaque. Special-case it with
		// an actionable hint.
		if isAddrInUseErr(err) {
			fprintError("port %d is already in use.", port)
			fmt.Fprintln(os.Stderr)
			fprintHint("another process (perhaps another %s?) is bound to this port.",
				colorGreen("mar dev"))
			fmt.Fprintf(os.Stderr, "      free it with %s,\n",
				colorGreen(fmt.Sprintf("lsof -ti:%d | xargs kill", port)))
			fmt.Fprintf(os.Stderr, "      or change %s to something else.\n",
				colorMagenta(`mar.json["server"]["port"]`))
			fmt.Fprintln(os.Stderr)
			return 1
		}
		fprintError("mar dev: %v", err)
		return 1
	}
	return 0
}

// isAddrInUseErr matches the various shapes Go's net package uses
// for "port already in use". Robust to errno wrapping; we just
// look for the recognizable substring.
func isAddrInUseErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "Only one usage of each socket address")
}

// fileState is a per-file fingerprint used by the watcher.
type fileState struct {
	mtime time.Time
	size  int64
}

// watchAndReload polls .mar / .json files under root every ~250ms. On any
// change (mtime / size / file added / removed), it runs compile and
// broadcasts the result on the hub. Compile errors don't stop the loop —
// the dev banner shows them in the browser; the previous good version
// keeps running.
func watchAndReload(root string, compile func() error, hub *jsserve.ReloadHub, lp *jsserve.LiveProgram) {
	snapshot := func() map[string]fileState {
		out := map[string]fileState{}
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(p, ".mar") && !strings.HasSuffix(p, ".json") {
				return nil
			}
			out[p] = fileState{mtime: info.ModTime(), size: info.Size()}
			return nil
		})
		return out
	}
	sameSnapshot := func(a, b map[string]fileState) bool {
		if len(a) != len(b) {
			return false
		}
		for k, av := range a {
			bv, ok := b[k]
			if !ok || !av.mtime.Equal(bv.mtime) || av.size != bv.size {
				return false
			}
		}
		return true
	}
	prev := snapshot()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for range tick.C {
		cur := snapshot()
		if sameSnapshot(prev, cur) {
			continue
		}
		prev = cur
		fmt.Println("[mar dev] file change detected, recompiling…")
		if err := compile(); err != nil {
			pretty := diag.Format(err)
			fmt.Fprintf(os.Stderr, "[mar dev] compile error:\n%s\n", pretty)
			lp.SetError(pretty)
			hub.Error(pretty)
			continue
		}
		// Successful compile clears the banner if there was one.
		if lp.LastError() != "" {
			lp.ClearError()
			hub.OK()
		}
		fmt.Println("[mar dev] reloaded")
		hub.Reload()
	}
}

// resolveDevEntry decides which file to load and which dir to read mar.json
// from, given a path that can be either a file or directory.
//
// Convention: when `path` is a directory, the entry file is `Main.mar`
// unless mar.json specifies a different `entry`. Most projects don't
// need to set it — the default is enough.
func resolveDevEntry(path string) (entryFile string, projectDir string, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		return "", "", statErr
	}
	if info.IsDir() {
		// Honor an explicit `entry` field in mar.json; fall back to
		// the conventional Main.mar otherwise. Errors loading
		// mar.json are non-fatal here — runDev will surface them
		// when it loads the manifest for real (port, db, secrets).
		entryName := "Main.mar"
		entryFromManifest := false
		if m, mErr := project.LoadManifest(path); mErr == nil && m != nil && m.Entry != "" {
			entryName = m.Entry
			entryFromManifest = true
		}
		entry := filepath.Join(path, entryName)
		if _, err := os.Stat(entry); err != nil {
			if entryFromManifest {
				return "", "", fmt.Errorf("%s not found\n\n"+
					"Hint: mar.json has \"entry\": %q but that file doesn't exist.\n"+
					"      Create it, fix the typo, or remove \"entry\" to use the default (Main.mar).",
					entry, entryName)
			}
			return "", "", fmt.Errorf("%s not found\n\n"+
				"Hint: by convention the entry file is Main.mar at the project root.\n"+
				"      Create it, or set \"entry\": \"<file>\" in mar.json to point elsewhere.",
				entry)
		}
		return entry, path, nil
	}
	return path, filepath.Dir(path), nil
}

// (App.* override builtins, page-bundle slicing, and route assembly all
// moved to internal/apphost — shared between `mar dev` and `mar-runtime`.)

func runConfig(dir string) int {
	m, err := project.LoadManifest(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", dir, err)
		return 1
	}
	if m == nil {
		fmt.Println("(no mar.json)")
		return 0
	}
	fmt.Printf("name:  %s\n", m.Name)
	if m.Entry != "" {
		fmt.Printf("entry: %s\n", m.Entry)
	} else {
		fmt.Printf("entry: Main.mar (default)\n")
	}
	if m.Server != nil {
		fmt.Printf("server.port:      %d\n", m.Server.Port)
		fmt.Printf("server.host:      %s\n", m.Server.Host)
		fmt.Printf("server.publicUrl: %s\n", m.Server.PublicURL)
	}
	if m.Database != nil {
		fmt.Printf("database.path:    %s\n", m.Database.Path)
	}
	if m.Mail != nil {
		fmt.Printf("mail.from:         %s\n", m.Mail.From)
		fmt.Printf("mail.smtpHost:     %s\n", m.Mail.SMTPHost)
		fmt.Printf("mail.smtpPort:     %d\n", m.Mail.SMTPPort)
		fmt.Printf("mail.smtpUsername: %s\n", m.Mail.SMTPUsername)
		// don't print password
		fmt.Printf("mail.smtpPassword: <set: %v>\n", m.Mail.SMTPPassword != "")
	}
	return 0
}

func joinModuleName(parts []string) string {
	if len(parts) == 0 {
		return "(unnamed)"
	}
	return strings.Join(parts, ".")
}

func joinCtors(names []string) string {
	return strings.Join(names, " | ")
}
