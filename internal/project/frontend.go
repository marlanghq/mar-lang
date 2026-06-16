package project

import (
	"fmt"
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

// LoadForServeTyped does the same load as LoadForServe but also returns
// the global type environment built during checking, keyed by qualified
// name ("Module.value"). `mar dev` uses this to enforce that
// `Main.main : Effect String ()` before kicking off the server.
//
// Built-in stdlib modules (List, String, UI, Dict, Time, etc.) are
// runtime-provided and are not loaded as files; see isStdlib in project.go,
// which derives the full set from the qualified builtin surface.
func LoadForServeTyped(entry string) ([]*ast.Module, map[string]typecheck.Type, error) {
	return LoadForServeTypedWithOverrides(entry, nil)
}

// LoadForServeTypedWithOverrides loads + type-checks the project, but if
// the BFS would read a file whose absolute path matches a key in
// `overrides`, it uses the override's string content instead of touching
// disk. The LSP uses this for the actively-edited file so cross-module
// type-checking sees in-memory content (otherwise saved-file lag produces
// spurious "unknown qualified name" errors on every cross-module
// reference in the buffer being edited).
//
// `overrides` keys must be absolute file paths (use filepath.Abs); empty
// or nil map means "no overrides", behaviour identical to LoadForServeTyped.
func LoadForServeTypedWithOverrides(entry string, overrides map[string]string) ([]*ast.Module, map[string]typecheck.Type, error) {
	entryAbs, err := filepath.Abs(entry)
	if err != nil {
		return nil, nil, err
	}
	dir := filepath.Dir(entryAbs)

	// BFS over imports.
	loaded := map[string]*ast.Module{} // module name -> AST
	paths := map[string]string{}       // module name -> file path
	sources := map[string]string{}     // module name -> file source (for error context)
	queue := []string{entryAbs}
	visited := map[string]bool{entryAbs: true}

	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]

		var src string
		if override, ok := overrides[path]; ok {
			src = override
		} else {
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, nil, err
			}
			src = string(b)
		}
		mod, err := parser.Parse(src)
		if err != nil {
			return nil, nil, diag.Wrap(path, src, err)
		}
		modName := joinName(mod.Name)
		if _, dup := loaded[modName]; dup {
			continue
		}
		loaded[modName] = mod
		paths[modName] = path
		sources[modName] = src

		// Resolve imports against the same directory.
		for _, imp := range mod.Imports {
			impName := joinName(imp.Module)
			if isStdlib(impName) {
				continue
			}
			impPath, err := resolveImport(dir, imp.Module)
			if err != nil {
				continue // import not found locally — assume external/stdlib-ish
			}
			if !visited[impPath] {
				visited[impPath] = true
				queue = append(queue, impPath)
			}
		}
	}

	// Topological sort.
	order, err := topoSort(loaded)
	if err != nil {
		return nil, nil, err
	}

	// Type-check in order. Types defined in a module are only visible
	// to other modules that import it — without this scoping, two
	// pages could not both define `type Model = ...`.
	tEnv := typecheck.BaseEnv()
	aliasesByModule := map[string]map[string]typecheck.TypeAlias{}
	customsByModule := map[string]map[string]typecheck.CustomType{}
	valueTypes := map[string]typecheck.Type{}
	// Per-expression types from every module merged here. The shape
	// lint consults this map to validate non-literal record values
	// (e.g. `body = input.body`) against entity column types.
	allExprTypes := map[ast.Expr]typecheck.Type{}
	for _, name := range order {
		mod := loaded[name]
		// Build the importable type set for this module: union of
		// every imported module's exports.
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
			return nil, nil, diag.Wrap(paths[name], sources[name], err)
		}
		for vname, t := range res.ValueTypes {
			tEnv.Define(name+"."+vname, t)
			valueTypes[name+"."+vname] = t
		}
		for e, t := range res.ExprTypes {
			allExprTypes[e] = t
		}
		modAliases := map[string]typecheck.TypeAlias{}
		for tname, alias := range res.TypeAliases {
			modAliases[tname] = alias
		}
		aliasesByModule[name] = modAliases

		modCustoms := map[string]typecheck.CustomType{}
		for tname, ct := range res.CustomTypes {
			modCustoms[tname] = ct
		}
		customsByModule[name] = modCustoms
	}

	out := make([]*ast.Module, 0, len(order))
	for _, name := range order {
		out = append(out, loaded[name])
	}

	// Boundary shape lint. Catches record-shape mismatches at
	// Repo.* / Auth.config callsites that BaseEnv's polymorphic
	// types let through. Surfaces the FIRST issue as a regular
	// source error so the CLI's snippet renderer kicks in — same
	// shape as a typecheck.InferError. (Reporting them all at once
	// would need a multi-error wrapper; one-at-a-time fits the
	// existing pipeline and matches how typecheck.CheckModule
	// surfaces only the first inference failure.)
	if issues := typecheck.RunShapeLint(out, allExprTypes); len(issues) > 0 {
		issue := issues[0]
		modPath := paths[issue.Module]
		modSrc := sources[issue.Module]
		return nil, nil, diag.Wrap(modPath, modSrc, &issue)
	}

	// GET services must be read-only. Project-wide pass: a GET handler
	// that reaches a database write (even through helpers) is rejected.
	if issues := typecheck.RunGetReadOnlyCheck(out); len(issues) > 0 {
		issue := issues[0]
		modPath := paths[issue.Module]
		modSrc := sources[issue.Module]
		return nil, nil, diag.Wrap(modPath, modSrc, &issue)
	}

	return out, valueTypes, nil
}

// resolveImport looks for a .mar file matching the dotted module name in the
// given directory (and its subdirectories that match the path).
//
//	Foo       -> dir/Foo.mar
//	Foo.Bar   -> dir/Foo/Bar.mar
func resolveImport(dir string, moduleName ast.ModuleName) (string, error) {
	if len(moduleName) == 0 {
		return "", fmt.Errorf("empty module name")
	}
	parts := append([]string(nil), moduleName...)
	parts[len(parts)-1] = parts[len(parts)-1] + ".mar"
	candidate := filepath.Join(append([]string{dir}, parts...)...)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// Try alternate: dir + "Module/Name.mar"
	flat := strings.Join(moduleName, string(filepath.Separator)) + ".mar"
	alt := filepath.Join(dir, flat)
	if _, err := os.Stat(alt); err == nil {
		return alt, nil
	}
	// Single-segment fallback at top level.
	if len(moduleName) == 1 {
		single := filepath.Join(dir, moduleName[0]+".mar")
		if _, err := os.Stat(single); err == nil {
			return single, nil
		}
	}
	return "", fmt.Errorf("import %s not found in %s", joinName(moduleName), dir)
}

// LoadIntoEnvWithModulesAndHook is the same as LoadIntoEnvWithModules but
// runs `installBuiltins` after BaseEnv is created and before any module is
// evaluated. Used by `mar dev` to override App.fullstack with a version
// that captures the project's module ASTs (the default builtin can't see
// them and errors out).
//
// Returns the env, modules, and a "Module.value" -> Type map so callers
// can enforce signatures (e.g. `Main.main : Effect String ()`) before
// running anything.
func LoadIntoEnvWithModulesAndHook(
	entry string,
	installBuiltins func(*runtime.Env, []*ast.Module),
) (*runtime.Env, []*ast.Module, map[string]typecheck.Type, error) {
	mods, valueTypes, err := LoadForServeTyped(entry)
	if err != nil {
		return nil, nil, nil, err
	}
	rEnv := runtime.BaseEnv()
	if installBuiltins != nil {
		installBuiltins(rEnv, mods)
	}
	byName := indexModules(mods)
	for _, m := range mods {
		if err := loadIntoEnv(m, joinName(m.Name), rEnv, byName); err != nil {
			return nil, nil, nil, err
		}
	}
	return rEnv, mods, valueTypes, nil
}

// indexModules keys modules by their dotted name. Used by loadIntoEnv to
// resolve `import M exposing (Type(..))` against the imported module's AST.
func indexModules(mods []*ast.Module) map[string]*ast.Module {
	out := make(map[string]*ast.Module, len(mods))
	for _, m := range mods {
		out[joinName(m.Name)] = m
	}
	return out
}

// (joinName, isStdlib, topoSort live in project.go.)
var _ = sort.Strings
