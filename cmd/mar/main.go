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
	"os"
	"path/filepath"

	"mar/internal/ast"
	"mar/internal/jsserve"
	"mar/internal/parser"
	"mar/internal/project"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

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
	case "serve":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "mar serve: missing file argument")
			os.Exit(2)
		}
		port := 4000
		entry := "main"
		os.Exit(runServe(os.Args[2], port, entry))
	case "app":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "mar app: missing project directory")
			os.Exit(2)
		}
		os.Exit(runApp(os.Args[2]))
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
  app <projectDir>            Run a full-stack project. Looks for Main.mar,
                              evaluates Main.main (typically built with
                              App.fullstack). Conventional layout:
                                Main.mar      — entry; main = App.fullstack ...
                                Backend.mar   — routes : List Route
                                Frontend.mar  — page : App
                                Shared.mar    — helpers used by both
                              Names other than Main.mar / main are convention.
  serve <file.mar>            Serve the file as a browser app on :4000.
  config <dir>                Load and print mar.json from the given project.
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
		fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
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
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
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
		fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
		return 1
	}
	res, err := typecheck.CheckModule(mod)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
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

// runApp runs a full-stack mar project. Convention: the project directory
// contains a Main.mar module exporting `main : Effect String ()` — typically
// `App.fullstack { api = Backend.routes, page = Frontend.page }`.
//
// Backend.mar / Frontend.mar / Shared.mar are conventional names but not
// required: Main.mar can import whatever it likes. The CLI itself doesn't
// look up Backend or Frontend by name — only Main.main matters.
//
// The HTTP port comes from <dir>/mar.json (server.port). Default 3000 if
// the manifest is absent or doesn't specify a port.
func runApp(dir string) int {
	mainFile := filepath.Join(dir, "Main.mar")
	if _, err := os.Stat(mainFile); err != nil {
		fmt.Fprintf(os.Stderr, "mar app: %s not found\n", mainFile)
		return 1
	}

	// Resolve port from mar.json (default 3000). Validation errors in the
	// manifest are fatal — better to surface them now than silently fall
	// back to defaults.
	port := 3000
	manifest, err := project.LoadManifest(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mar app: %v\n", err)
		return 1
	}
	if manifest != nil && manifest.Server != nil && manifest.Server.Port != 0 {
		port = manifest.Server.Port
	}

	// Load the full project (parse + type-check + evaluate all modules into
	// a shared runtime env). The hook installs a project-aware version of
	// App.fullstack BEFORE any module is evaluated — Main.main calls it
	// during evaluation, so the override has to be in place by then.
	rEnv, _, err := project.LoadIntoEnvWithModulesAndHook(mainFile,
		func(env *runtime.Env, mods []*ast.Module) {
			impl := makeFullstackBuiltin(mods, port)
			env.Define("appFullstack", impl)
			env.Define("App.fullstack", impl) // qualified alias is a separate binding
		})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", mainFile, err)
		return 1
	}

	// Look up Main.main and run it as an Effect.
	mainVal, ok := rEnv.Lookup("Main.main")
	if !ok {
		mainVal, ok = rEnv.Lookup("main")
	}
	if !ok {
		fmt.Fprintf(os.Stderr, "mar app: Main.mar must export `main : Effect String ()`\n")
		return 1
	}
	eff, ok := mainVal.(runtime.VEffect)
	if !ok {
		fmt.Fprintf(os.Stderr, "mar app: Main.main is not an Effect (got %T)\n", mainVal)
		return 1
	}
	if _, err := eff.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "mar app: %v\n", err)
		return 1
	}
	return 0
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

// makeFullstackBuiltin returns a 1-arg native function that mirrors the
// signature of App.fullstack:
//
//	{ api : List Route, page : App } -> Effect String ()
//
// It captures the project's module ASTs (so the browser bundle entry can
// be resolved from the page's provenance) and the HTTP port resolved by
// the CLI from mar.json.
func makeFullstackBuiltin(mods []*ast.Module, port int) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			rec, ok := args[0].(runtime.VRecord)
			if !ok {
				return nil, fmt.Errorf("App.fullstack: expected { api, page } record (got %T)", args[0])
			}
			apiV, ok := rec.Fields["api"]
			if !ok {
				return nil, fmt.Errorf("App.fullstack: missing `api` field")
			}
			pageV, ok := rec.Fields["page"]
			if !ok {
				return nil, fmt.Errorf("App.fullstack: missing `page` field")
			}
			apiList, ok := apiV.(runtime.VList)
			if !ok {
				return nil, fmt.Errorf("App.fullstack: `api` is not a list (got %T)", apiV)
			}
			page, ok := pageV.(runtime.VApp)
			if !ok {
				return nil, fmt.Errorf("App.fullstack: `page` is not an App value (got %T)", pageV)
			}
			if page.OriginName == "" {
				return nil, fmt.Errorf("App.fullstack: `page` has no provenance — must be a top-level binding (e.g. `page = App.create ...` in some module), not an inline expression")
			}
			// Ship only the modules reachable from the frontend page —
			// otherwise Backend.mar / Main.mar would land in the browser
			// bundle, where Db / Entity / App.fullstack don't exist and
			// pre-eval of top-level decls would crash.
			frontMods := reachableFrom(page.OriginModule, mods)
			return runtime.VEffect{
				Tag: "appFullstack",
				Run: func() (runtime.Value, error) {
					if err := jsserve.ServeUnified(port, apiList.Elements, frontMods, page.OriginName); err != nil {
						return nil, err
					}
					return runtime.VUnit{}, nil
				},
			}, nil
		},
	}
}


func runServe(path string, port int, entry string) int {
	// `mar serve` follows imports from the entry file (within its directory).
	// Backend-only modules (those NOT reached via the entry's import graph)
	// are excluded; only what the frontend uses is shipped to the browser.
	mods, err := project.LoadForServe(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
		return 1
	}
	if err := jsserve.ServeModules(port, mods, entry); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	return 0
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
