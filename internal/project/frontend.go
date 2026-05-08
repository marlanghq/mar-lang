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
// Built-in stdlib modules (List, String, Maybe, Result, Effect, IO, JSON,
// Server, Response, Db, Entity, View, App, Screen, Endpoint, Http) are
// considered runtime-provided and are not loaded as files.
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
	return out, valueTypes, nil
}

// resolveImport looks for a .mar file matching the dotted module name in the
// given directory (and its subdirectories that match the path).
//
//   Foo       -> dir/Foo.mar
//   Foo.Bar   -> dir/Foo/Bar.mar
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
	for _, m := range mods {
		if err := loadIntoEnv(m, joinName(m.Name), rEnv); err != nil {
			return nil, nil, nil, err
		}
	}
	return rEnv, mods, valueTypes, nil
}

// (joinName, isStdlib, topoSort live in project.go.)
var _ = sort.Strings
