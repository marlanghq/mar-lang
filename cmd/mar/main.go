// Command mar is the entry point for the Mar compiler and tooling.
//
// Subcommands:
//
//	mar parse <file.mar>   Parse the file and report syntax errors.
//	mar check <file.mar>   Parse and type-check the file, reporting types.
//	mar version            Print the version.
//
// Future: serve, build, repl, fmt, lsp, etc.
package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	"mar/internal/ast"
	"mar/internal/diag"
	"mar/internal/jsserve"
	"mar/internal/lsp"
	"mar/internal/parser"
	"mar/internal/project"
	"mar/internal/runtime"
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

// noopEffect returns an Effect that does nothing on Run. Used by the
// `mar dev` overrides for App.fullstack / App.serve / App.serveScreens —
// the side effect they care about (capturing args into the LiveProgram)
// happens during the function call, not when the Effect runs. The CLI
// drives the actual server lifecycle.
func noopEffect(tag string) runtime.VEffect {
	return runtime.VEffect{
		Tag: tag,
		Run: func() (runtime.Value, error) { return runtime.VUnit{}, nil },
	}
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
	case "parse":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "mar parse: missing file argument")
			os.Exit(2)
		}
		os.Exit(runParse(os.Args[2]))
	case "check":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "mar check: missing file argument")
			os.Exit(2)
		}
		os.Exit(runCheck(os.Args[2]))
	case "run":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "mar run: missing file argument")
			os.Exit(2)
		}
		valueName := "main"
		if len(os.Args) >= 4 {
			valueName = os.Args[3]
		}
		os.Exit(runRun(os.Args[2], valueName))
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
	case "lsp":
		// Language server over stdio. Editors (VSCode, etc.) launch
		// `mar lsp` and pipe LSP JSON-RPC over stdin/stdout.
		if err := lsp.RunStdio(); err != nil {
			fmt.Fprintf(os.Stderr, "mar lsp: %v\n", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Printf("%s (%s)\n", version, commit)
	case "help", "--help", "-h":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "mar: unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: mar <command> [arguments]

Commands:
  parse <file.mar>            Parse the file and report syntax errors.
  check <file.mar>            Parse and type-check the file, reporting types.
  run <file.mar> [valueName]  Type-check, evaluate, and print the named value.
                              Defaults to "main".
  repl                        Start an interactive read-eval-print loop.
  dev [path]                  Run main in dev mode. <path> can be:
                                a .mar file              — single-file app
                                a project directory      — looks for Main.mar
                                (default = current dir)  — same as above
                              What main does decides the runtime:
                                App.fullstack { api, page }   — unified server
                                                                (port from mar.json)
                                App.serve port app            — browser-only app
                                App.serveScreens port screens — multi-screen app
                              Conventional project layout:
                                Main.mar      — entry; main = App.fullstack ...
                                Backend.mar   — routes : List Route
                                Frontend.mar  — page : App
                                Shared.mar    — helpers used by both
                              Names other than Main.mar / main are convention.
  config <dir>                Load and print mar.json from the given project.
  lsp                         Run the Language Server over stdio
                              (used by editor extensions).
  version                     Print the version.

Use "mar help" for this help.`)
}

func runParse(path string) int {
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
	fmt.Printf("module %s\n", joinModuleName(mod.Name))
	fmt.Printf("  imports: %d\n", len(mod.Imports))
	fmt.Printf("  decls: %d\n", len(mod.Decls))
	return 0
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
		fmt.Fprintf(os.Stderr, "mar dev: %v\n", err)
		return 1
	}

	// Resolve port from mar.json (default 3000). Same value used by
	// App.fullstack / App.serve / App.serveScreens — the language no
	// longer takes port as a code argument. Validation errors in the
	// manifest are fatal: surface them now rather than fall back silently.
	port := 3000
	manifest, err := project.LoadManifest(projectDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar dev: %v\n", err)
		return 1
	}
	if manifest != nil && manifest.Server != nil && manifest.Server.Port != 0 {
		port = manifest.Server.Port
	}

	lp := &jsserve.LiveProgram{}
	hub := jsserve.NewReloadHub(lp)

	// compile loads + evaluates the project, capturing the served state
	// into lp. Returns a friendly error message instead of panicking so
	// the watcher can recover from compile errors during development.
	compile := func() error {
		rEnv, _, err := project.LoadIntoEnvWithModulesAndHook(entryFile,
			func(env *runtime.Env, mods []*ast.Module) {
				fs := makeFullstackBuiltin(mods, port, lp)
				env.Define("appFullstack", fs)
				env.Define("App.fullstack", fs)

				fe := makeFrontendBuiltin(mods, port, lp)
				env.Define("appFrontend", fe)
				env.Define("App.frontend", fe)

				be := makeBackendBuiltin(port, lp)
				env.Define("appBackend", be)
				env.Define("App.backend", be)
			})
		if err != nil {
			return err
		}
		mainVal, ok := rEnv.Lookup("Main.main")
		if !ok {
			mainVal, ok = rEnv.Lookup("main")
		}
		if !ok {
			return fmt.Errorf("%s must export `main : Effect String ()`", entryFile)
		}
		eff, ok := mainVal.(runtime.VEffect)
		if !ok {
			return fmt.Errorf("main is not an Effect (got %T)", mainVal)
		}
		// Running the Effect calls one of the overridden builtins, which
		// captures (api, page) and updates lp. The builtin's Effect is a
		// no-op — we drive the server lifecycle from the CLI, not the
		// user's main.
		if _, err := eff.Run(); err != nil {
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

	// Open the browser to the dev URL once the server is ready. Same
	// convenience the lispy `mar dev` had. Honors $MAR_NO_OPEN for
	// headless / CI runs.
	go openBrowserWhenReady(fmt.Sprintf("http://localhost:%d", port))

	// Block on the HTTP server.
	if err := jsserve.ServeLive(port, lp, hub); err != nil {
		fmt.Fprintf(os.Stderr, "mar dev: %v\n", err)
		return 1
	}
	return 0
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
func resolveDevEntry(path string) (entryFile string, projectDir string, err error) {
	info, statErr := os.Stat(path)
	if statErr != nil {
		return "", "", statErr
	}
	if info.IsDir() {
		entry := filepath.Join(path, "Main.mar")
		if _, err := os.Stat(entry); err != nil {
			return "", "", fmt.Errorf("%s: no Main.mar found in directory", path)
		}
		return entry, path, nil
	}
	return path, filepath.Dir(path), nil
}

// reachableFrom walks the import graph starting at startModule and returns
// the subset of `mods` reachable through imports — preserving topological
// order so dependencies come before dependents in the browser bundle.
func reachableFrom(startModule string, mods []*ast.Module) []*ast.Module {
	byName := map[string]*ast.Module{}
	for _, m := range mods {
		byName[joinModuleName(m.Name)] = m
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
			return // stdlib or unknown
		}
		for _, imp := range mod.Imports {
			impName := joinModuleName(imp.Module)
			visit(impName)
		}
		order = append(order, mod)
	}
	visit(startModule)
	return order
}

// pickFrontMods extracts the subset of project modules reachable from
// pages' origin modules. Each page in a frontend / fullstack must come
// from a top-level binding (so it has provenance); we trace from those
// modules so the browser bundle excludes Backend / Main code that would
// fail to evaluate on the JS side.
func pickFrontMods(pages []runtime.Value, mods []*ast.Module) ([]*ast.Module, error) {
	roots := map[string]bool{}
	for i, pv := range pages {
		page, ok := pv.(runtime.VPage)
		if !ok {
			return nil, fmt.Errorf("page %d is not a Page value (got %T)", i, pv)
		}
		if page.OriginName == "" {
			return nil, fmt.Errorf("page %d has no provenance — pages must be top-level bindings (e.g. `myPage = Page.root ...`), not inline expressions", i)
		}
		roots[page.OriginModule] = true
	}
	merged := []*ast.Module{}
	seen := map[string]bool{}
	for root := range roots {
		for _, m := range reachableFrom(root, mods) {
			name := joinModuleName(m.Name)
			if seen[name] {
				continue
			}
			seen[name] = true
			merged = append(merged, m)
		}
	}
	return merged, nil
}

// makeFrontendBuiltin overrides App.frontend in `mar dev`.
//
//	App.frontend : List Page -> Effect String ()
//
// Captures the page list, ships the AST subset reachable from those
// pages to the browser. The JS runtime evaluates the user's `main`
// expression itself — it sees the same App.frontend call and uses the
// JS-side builtin to mount the pages.
func makeFrontendBuiltin(mods []*ast.Module, port int, lp *jsserve.LiveProgram) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			pageList, ok := args[0].(runtime.VList)
			if !ok {
				return nil, fmt.Errorf("App.frontend: expected List Page (got %T)", args[0])
			}
			frontMods, err := pickFrontMods(pageList.Elements, mods)
			if err != nil {
				return nil, fmt.Errorf("App.frontend: %v", err)
			}
			lp.SetPort(port)
			if err := lp.Update(nil, frontMods, "main"); err != nil {
				return nil, fmt.Errorf("App.frontend: %v", err)
			}
			return noopEffect("appFrontend"), nil
		},
	}
}

// makeBackendBuiltin overrides App.backend in `mar dev`.
//
//	App.backend : List Route -> Effect String ()
//
// API server, no frontend bundle. The browser never gets a program.json;
// HTML page renders an empty "this is an API server" notice.
func makeBackendBuiltin(port int, lp *jsserve.LiveProgram) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			routes, ok := args[0].(runtime.VList)
			if !ok {
				return nil, fmt.Errorf("App.backend: expected List Route (got %T)", args[0])
			}
			lp.SetPort(port)
			// Empty mods list disables the frontend bundle on this lp.
			if err := lp.Update(routes.Elements, nil, ""); err != nil {
				return nil, fmt.Errorf("App.backend: %v", err)
			}
			return noopEffect("appBackend"), nil
		},
	}
}

// makeFullstackBuiltin overrides App.fullstack in `mar dev`.
//
//	App.fullstack : { api : List Route, pages : List Page } -> Effect String ()
//
// The unified mode. Backend routes mounted at /api/*, page list shipped
// to the browser as JS. Multi-page apps (more than one Page in `pages`)
// route by URL path.
func makeFullstackBuiltin(mods []*ast.Module, port int, lp *jsserve.LiveProgram) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			rec, ok := args[0].(runtime.VRecord)
			if !ok {
				return nil, fmt.Errorf("App.fullstack: expected { api, pages } record (got %T)", args[0])
			}
			apiV, ok := rec.Fields["api"]
			if !ok {
				return nil, fmt.Errorf("App.fullstack: missing `api` field")
			}
			pagesV, ok := rec.Fields["pages"]
			if !ok {
				return nil, fmt.Errorf("App.fullstack: missing `pages` field")
			}
			apiList, ok := apiV.(runtime.VList)
			if !ok {
				return nil, fmt.Errorf("App.fullstack: `api` is not a list (got %T)", apiV)
			}
			pageList, ok := pagesV.(runtime.VList)
			if !ok {
				return nil, fmt.Errorf("App.fullstack: `pages` is not a list (got %T)", pagesV)
			}
			frontMods, err := pickFrontMods(pageList.Elements, mods)
			if err != nil {
				return nil, fmt.Errorf("App.fullstack: %v", err)
			}
			lp.SetPort(port)
			if err := lp.Update(apiList.Elements, frontMods, "main"); err != nil {
				return nil, fmt.Errorf("App.fullstack: %v", err)
			}
			return noopEffect("appFullstack"), nil
		},
	}
}

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
	fmt.Printf("entry: %s\n", m.Entry)
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
		fmt.Printf("mail.smtpHost:     %s\n", m.Mail.SmtpHost)
		fmt.Printf("mail.smtpPort:     %d\n", m.Mail.SmtpPort)
		fmt.Printf("mail.smtpUsername: %s\n", m.Mail.SmtpUsername)
		// don't print password
		fmt.Printf("mail.smtpPassword: <set: %v>\n", m.Mail.SmtpPassword != "")
	}
	return 0
}

func runRun(path, valueName string) int {
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar: %v\n", err)
		return 1
	}
	var v runtime.Value
	if info.IsDir() {
		v, err = project.Run(path, valueName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			return 1
		}
	} else {
		// Single-file run. If the file lives in a directory with other .mar
		// files (it's part of a project), load the project. Otherwise just
		// the single file.
		dir := filepath.Dir(path)
		siblings, _ := filepath.Glob(filepath.Join(dir, "*.mar"))
		if len(siblings) > 1 {
			// Project mode. Infer the value name from the file's module.
			src, _ := os.ReadFile(path)
			mod, perr := parser.Parse(string(src))
			if perr == nil && len(mod.Name) > 0 {
				modName := mod.Name[0]
				for _, p := range mod.Name[1:] {
					modName += "." + p
				}
				if valueName == "main" {
					valueName = modName + ".main"
				}
			}
			v, err = project.Run(dir, valueName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				return 1
			}
		} else {
			src, err := os.ReadFile(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "mar: %v\n", err)
				return 1
			}
			mod, err := parser.Parse(string(src))
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				return 1
			}
			if _, err := typecheck.CheckModule(mod); err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				return 1
			}
			loaded, err := runtime.LoadModule(mod)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				return 1
			}
			v, err = loaded.Get(valueName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				return 1
			}
		}
	}
	// If the value is an Effect, execute it.
	if _, ok := v.(runtime.VEffect); ok {
		result, err := runtime.RunEffect(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			return 1
		}
		v = result
	}
	// Pretty-print top-level lists with one element per line.
	if list, ok := v.(runtime.VList); ok {
		for _, e := range list.Elements {
			fmt.Println(displayUser(e))
		}
		return 0
	}
	fmt.Println(displayUser(v))
	return 0
}

// displayUser is like Value.Display but unwraps top-level strings (no quotes).
func displayUser(v runtime.Value) string {
	if s, ok := v.(runtime.VString); ok {
		return s.V
	}
	return v.Display()
}

func joinModuleName(parts []string) string {
	if len(parts) == 0 {
		return "(unnamed)"
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += "." + p
	}
	return out
}

func joinCtors(names []string) string {
	if len(names) == 0 {
		return ""
	}
	out := names[0]
	for _, n := range names[1:] {
		out += " | " + n
	}
	return out
}
