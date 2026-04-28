package project

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"mar/internal/ast"
	"mar/internal/parser"
	"mar/internal/typecheck"
)

// LoadForServe loads the entry .mar file plus any modules it (transitively)
// imports from the same directory. Returns the modules in dependency order,
// suitable for the JS runtime to execute.
//
// Built-in stdlib modules (List, String, Maybe, Result, Effect, IO, JSON,
// Server, Response, Db, Entity, View, App, Screen, Endpoint, Http) are
// considered runtime-provided and are not loaded as files.
func LoadForServe(entry string) ([]*ast.Module, error) {
	entryAbs, err := filepath.Abs(entry)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(entryAbs)

	// BFS over imports.
	loaded := map[string]*ast.Module{} // module name -> AST
	paths := map[string]string{}
	queue := []string{entryAbs}
	visited := map[string]bool{entryAbs: true}

	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]

		src, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		mod, err := parser.Parse(string(src))
		if err != nil {
			return nil, fmt.Errorf("%s: %v", path, err)
		}
		modName := joinName(mod.Name)
		if _, dup := loaded[modName]; dup {
			continue
		}
		loaded[modName] = mod
		paths[modName] = path

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
		return nil, err
	}

	// Type-check in order, accumulating shared aliases/customs/values.
	tEnv := typecheck.BaseEnv()
	allAliases := map[string]typecheck.TypeAlias{}
	allCustoms := map[string]typecheck.CustomType{}
	for _, name := range order {
		mod := loaded[name]
		res, err := typecheck.CheckModuleWith(mod, tEnv, allAliases, allCustoms)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", name, err)
		}
		for vname, t := range res.ValueTypes {
			tEnv.Define(name+"."+vname, t)
		}
		for tname, alias := range res.TypeAliases {
			allAliases[tname] = alias
		}
		for tname, ct := range res.CustomTypes {
			allCustoms[tname] = ct
		}
	}

	out := make([]*ast.Module, 0, len(order))
	for _, name := range order {
		out = append(out, loaded[name])
	}
	return out, nil
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

// (joinName, isStdlib, topoSort live in project.go.)
var _ = sort.Strings
