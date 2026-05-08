// Package project implements multi-file Mar program loading.
//
// A project is a directory containing one or more .mar files. Each file is
// parsed; imports across files are resolved; everything is type-checked and
// optionally evaluated.
//
// Rules:
//   - Module names must match file paths relative to the project root, with
//     '/' replaced by '.'. So src/Posts/Backend.mar is module Posts.Backend.
//   - Imports are by module name. Cycles are not allowed.
//   - Names exposed via `exposing (..)` or listed explicitly become available
//     as `Module.name` in importing modules.
package project

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mar/internal/ast"
	"mar/internal/diag"
	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// Project is a loaded, type-checked multi-file program.
type Project struct {
	Root    string
	Modules map[string]*LoadedModule // keyed by dotted module name
	Order   []string                 // load order (topologically sorted)
}

// LoadedModule is a single parsed + type-checked module.
type LoadedModule struct {
	Name string
	Path string
	AST  *ast.Module
	// Type information from typecheck for this module's values.
	ValueTypes map[string]typecheck.Type
}

// Load reads all .mar files under root, parses, links, and type-checks them.
//
// Returns the loaded project on success, or the first error encountered.
func Load(root string) (*Project, error) {
	files, err := findMarFiles(root)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no .mar files found under %s", root)
	}

	// Parse each file
	parsed := make(map[string]*ast.Module)
	paths := make(map[string]string)
	sources := make(map[string]string)
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", path, err)
		}
		mod, err := parser.Parse(string(src))
		if err != nil {
			return nil, diag.Wrap(path, string(src), err)
		}
		name := joinName(mod.Name)
		if _, dup := parsed[name]; dup {
			return nil, fmt.Errorf("duplicate module %s: %s and %s", name, paths[name], path)
		}
		parsed[name] = mod
		paths[name] = path
		sources[name] = string(src)
	}

	// Topologically sort by imports
	order, err := topoSort(parsed)
	if err != nil {
		return nil, err
	}

	// Type-check each module in order. Types defined in a module are
	// only visible to other modules that import it — without this
	// scoping, two pages could not both define `type Model = ...`.
	tEnv := typecheck.BaseEnv()
	aliasesByModule := map[string]map[string]typecheck.TypeAlias{}
	customsByModule := map[string]map[string]typecheck.CustomType{}
	mods := make(map[string]*LoadedModule)
	for _, name := range order {
		mod := parsed[name]
		// Verify all imports are present
		for _, imp := range mod.Imports {
			impName := joinName(imp.Module)
			if _, ok := parsed[impName]; !ok && !isStdlib(impName) {
				return nil, fmt.Errorf("%s: cannot find imported module %s", name, impName)
			}
		}

		// Build the importable type set for this module: the union of
		// every imported module's exports. (Stdlib types are already
		// in tEnv via BaseEnv.)
		importedAliases := map[string]typecheck.TypeAlias{}
		importedCustoms := map[string]typecheck.CustomType{}
		for _, imp := range mod.Imports {
			impName := joinName(imp.Module)
			if a, ok := aliasesByModule[impName]; ok {
				for k, v := range a {
					importedAliases[k] = v
				}
			}
			if c, ok := customsByModule[impName]; ok {
				for k, v := range c {
					importedCustoms[k] = v
				}
			}
		}

		res, err := typecheck.CheckModuleWith(mod, tEnv, importedAliases, importedCustoms)
		if err != nil {
			return nil, diag.Wrap(paths[name], sources[name], err)
		}
		// Register this module's values into shared tEnv with qualified names.
		for vname, t := range res.ValueTypes {
			tEnv.Define(name+"."+vname, t)
		}
		// Stash this module's types so importing modules pick them up,
		// while siblings that don't import remain unaffected.
		modAliases := map[string]typecheck.TypeAlias{}
		for tname, alias := range res.TypeAliases {
			modAliases[tname] = alias
		}
		aliasesByModule[name] = modAliases

		modCustoms := map[string]typecheck.CustomType{}
		for tname, ct := range res.CustomTypes {
			modCustoms[tname] = ct
			// Also register constructors as qualified values
			for _, cname := range ct.CtorOrder {
				if cval, ok := tEnv.Lookup(cname); ok {
					tEnv.Define(name+"."+cname, cval)
				}
			}
		}
		customsByModule[name] = modCustoms
		mods[name] = &LoadedModule{
			Name:       name,
			Path:       paths[name],
			AST:        mod,
			ValueTypes: res.ValueTypes,
		}
	}

	return &Project{
		Root:    root,
		Modules: mods,
		Order:   order,
	}, nil
}

// loadIntoEnv evaluates a module's value declarations into an existing
// runtime env, registering each value with both its bare name (in the
// module's frame) and its qualified Module.name. Also processes
// `import M exposing (...)` clauses so the runtime sees the same
// bindings the typechecker accepted.
func loadIntoEnv(mod *ast.Module, modName string, rEnv *runtime.Env) error {
	// Pass 0: import exposing — bare-name aliases for runtime values
	// already in env. Mirrors what CheckModuleWith does at the type
	// level; without this, code like `column [...]` after
	// `import View exposing (column)` typechecks but explodes at
	// runtime with "unbound name: column".
	for _, imp := range mod.Imports {
		if len(imp.Exposing.Items) == 0 && !imp.Exposing.All {
			continue
		}
		impName := joinName(imp.Module)
		for _, item := range imp.Exposing.Items {
			if v, ok := rEnv.Lookup(impName + "." + item.Name); ok {
				rEnv.Define(item.Name, v)
			}
		}
	}

	// Pass 1: register custom-type constructors.
	// Also populate the path-pattern enum registry for any zero-arg
	// ctor type (so `{role:Role}` segments resolve at runtime). We
	// register under both the bare name and the fully-qualified one
	// so a path declared in a module that imports `Shared.Role`
	// resolves the same as one declared in `Shared` itself.
	for _, d := range mod.Decls {
		ct, ok := d.(*ast.CustomTypeDecl)
		if !ok {
			continue
		}
		ctorNames := make([]string, 0, len(ct.Constructors))
		ctorArities := map[string]int{}
		for _, c := range ct.Constructors {
			v := makeCtorValueLocal(c.Name, len(c.Args))
			rEnv.Define(c.Name, v)
			rEnv.Define(modName+"."+c.Name, v)
			ctorNames = append(ctorNames, c.Name)
			ctorArities[c.Name] = len(c.Args)
		}
		runtime.RegisterEnumType(ct.Name, ctorNames, ctorArities)
		runtime.RegisterEnumType(modName+"."+ct.Name, ctorNames, ctorArities)
	}

	// Pass 2: pre-bind values to placeholders (for mutual recursion).
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		rEnv.Define(v.Name, runtime.VUnit{})
	}

	// Pass 3: evaluate.
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		body := ast.Expr(v.Body)
		if len(v.Params) > 0 {
			body = &ast.ELambda{Pos: v.Pos, Params: v.Params, Body: body}
		}
		val, err := runtime.Eval(body, rEnv)
		if err != nil {
			return err
		}
		// Record provenance on App values so App.fullstack can name the
		// browser bundle's entry point. Only stamps the first time, so a
		// page re-exported from a wrapper module keeps its original origin.
		if page, ok := val.(runtime.VPage); ok && page.OriginName == "" {
			page.OriginModule = modName
			page.OriginName = v.Name
			val = page
		}
		// Same for Services: stamping powers URL resolution at runtime
		// (the URL is `/services/<module>.<name>`).
		if svc, ok := val.(runtime.VService); ok && svc.OriginName == "" {
			svc.OriginModule = modName
			svc.OriginName = v.Name
			val = svc
		}
		rEnv.Define(v.Name, val)
		rEnv.Define(modName+"."+v.Name, val)
	}
	return nil
}

// makeCtorValueLocal mirrors runtime.makeCtorValue but lives here to avoid
// exposing it. (Could be exported instead later.)
func makeCtorValueLocal(tag string, arity int) runtime.Value {
	if arity == 0 {
		return runtime.VCtor{Tag: tag}
	}
	return runtime.VFn{
		Arity:   arity,
		CtorTag: tag,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			out := make([]runtime.Value, len(args))
			copy(out, args)
			return runtime.VCtor{Tag: tag, Args: out}, nil
		},
	}
}

// --- helpers ---

// findMarFiles returns the .mar files belonging to a project at root.
//
// If root is a single .mar file, returns just that file.
// If root is a directory, walks it recursively — subdirectories that
// match dotted module segments (e.g. `Frontend/Home.mar` for
// `module Frontend.Home`) are picked up. Hidden directories
// (".git", ".cache", etc.) and `node_modules` are skipped.
func findMarFiles(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if strings.HasSuffix(root, ".mar") {
			return []string{root}, nil
		}
		return nil, fmt.Errorf("%s: not a .mar file or directory", root)
	}
	var out []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path == root {
				return nil
			}
			// Skip hidden + dependency directories. Also skip `dist`
			// (compiled output) so re-builds don't loop on themselves.
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "dist" {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".mar") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// topoSort orders modules so each appears after its dependencies.
func topoSort(parsed map[string]*ast.Module) ([]string, error) {
	visited := map[string]bool{}
	visiting := map[string]bool{}
	var order []string

	var visit func(name string) error
	visit = func(name string) error {
		if visited[name] {
			return nil
		}
		if visiting[name] {
			return fmt.Errorf("import cycle involving %s", name)
		}
		mod, ok := parsed[name]
		if !ok {
			return nil // assume stdlib or external
		}
		visiting[name] = true
		for _, imp := range mod.Imports {
			impName := joinName(imp.Module)
			if !isStdlib(impName) {
				if err := visit(impName); err != nil {
					return err
				}
			}
		}
		visiting[name] = false
		visited[name] = true
		order = append(order, name)
		return nil
	}

	// Process in deterministic order.
	names := make([]string, 0, len(parsed))
	for n := range parsed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := visit(n); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// isStdlib reports whether a module name refers to a built-in (e.g.
// List, String). These don't have to exist as files; the runtime and
// typecheck provide them. Keep the list in sync with frontend.go's
// equivalent (today they're the same set).
func isStdlib(name string) bool {
	switch name {
	case "List", "String", "Maybe", "Result", "Effect",
		"JSON", "Response", "Entity", "Repo", "View",
		"UI",
		"App", "Page", "Endpoint", "Http":
		return true
	}
	return false
}

func joinName(parts []string) string {
	return strings.Join(parts, ".")
}
