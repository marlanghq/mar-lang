package project

// LoadIntoEnvForRuntime is the lean variant of
// LoadIntoEnvWithModulesAndHook used by the production runtime
// (`mar-runtime`). It performs the same parse-imports / topo-sort /
// evaluate sequence but SKIPS the typecheck pass.
//
// Skipping type-checking is correct here because:
//
//   - The embedded payload was already type-checked at `mar build`
//     time. A build that didn't type-check would have failed before
//     producing a binary, so re-checking on every boot is wasted
//     work.
//
//   - More importantly for binary size: the typecheck-using path
//     pulls `internal/typecheck` into the link graph, which adds
//     ~100 KB of type-signature data + the related crypto/fips140
//     init data to every `mar build` artifact. Removing the
//     transitive reach from the runtime stub lets Go's linker DCE
//     drop it entirely: measurably smaller stubs.
//
// The dev / build paths (mar dev, mar build) still go through
// LoadIntoEnvWithModulesAndHook, which DOES type-check: they care
// about surfacing type errors before runtime.

import (
	"fmt"
	"os"
	"path/filepath"

	"mar/internal/ast"
	"mar/internal/diag"
	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// LoadIntoEnvForRuntime parses `entry` plus its transitive imports
// (BFS), topologically sorts them, then evaluates each into a fresh
// runtime.Env. `installBuiltins` is called after BaseEnv is created
// and before any module is evaluated: used by `App.fullstack` /
// `App.frontend` / `App.backend` to install their capture
// callbacks before user code runs.
//
// Type-checks on the way in. That is not about catching type errors
// , the build pipeline already did, but about ELABORATION: the
// checker writes decisions onto the tree that the evaluator cannot
// re-derive, so a tree that skipped it is quietly wrong rather than
// rejected. See ADR 0017.
func LoadIntoEnvForRuntime(
	entry string,
	installBuiltins func(*runtime.Env, []*ast.Module),
) (*runtime.Env, []*ast.Module, error) {
	mods, err := parseAndOrder(entry)
	if err != nil {
		return nil, nil, err
	}
	// Type-check before evaluating, and not for the type errors: checking is
	// what ELABORATES the tree. A parse-only load hands the evaluator a tree
	// with the checker's decisions missing, which literals became Decimals,
	// which references named an implementation, and those do not fail
	// loudly. `1 + 1.50` passes `mar check`, runs under `mar dev`, and dies
	// here with `+: expected Int`. See ADR 0017.
	byName := indexModules(mods)
	checked, tEnv, err := checkOrdered(mods,
		func(m *ast.Module) string { return joinName(m.Name) },
		func(module string) bool { _, ok := byName[module]; return ok },
		nil,
	)
	if err != nil {
		return nil, nil, err
	}

	// The project-wide passes, here too.
	//
	// The comment above says the build pipeline already caught the errors, and
	// that holds for a bundle THIS binary's own CLI produced. The stub is
	// pre-compiled and shipped, though, so the bundle and the binary running it
	// are two versions that need not match — which is exactly the shape this
	// work keeps finding: a check live on one path and absent on another, with
	// the absent one being the deployed one.
	//
	// Measured on the shipped stub before this: `two pages are declared on` was
	// in it (the App.* builtins carry that one, and the stub runs `main`), and
	// `must be read-only` and `expected \`Cmd ()\`` were not.
	//
	// The shape lint is the one pass still missing here, and for a reason
	// rather than an oversight: it needs the expression types, which
	// `checkOrdered` does not carry back. The other three need only the
	// modules or the value types it already returns.
	if issues := typecheck.RunGetReadOnlyCheck(mods); len(issues) > 0 {
		return nil, nil, fmt.Errorf("%s", issues[0].Message)
	}
	if issues := typecheck.RunSideCheck(mods); len(issues) > 0 {
		return nil, nil, fmt.Errorf("%s", issues[0].Message)
	}
	qualified := map[string]typecheck.Type{}
	for modName, values := range checked {
		for vname, t := range values {
			qualified[modName+"."+vname] = t
		}
	}
	if name, t := mainTypeIn(qualified); t != nil {
		if msg := typecheck.MainSignatureProblem(t); msg != "" {
			return nil, nil, fmt.Errorf("%s: %s", name, msg)
		}
	}

	rEnv := runtime.BaseEnv()
	if installBuiltins != nil {
		installBuiltins(rEnv, mods)
	}
	for _, m := range mods {
		if err := loadIntoEnv(m, joinName(m.Name), rEnv, byName, declaredTypes(checked, m), ctorsFrom(tEnv)); err != nil {
			return nil, nil, err
		}
	}
	return rEnv, mods, nil
}

// parseAndOrder is the parse-only kernel shared by every runtime
// loader. Walks imports breadth-first from `entry`, parses each
// file, then topologically sorts so each module is evaluated after
// every module it depends on. No typecheck calls: that's the
// whole point of the runtime-vs-build split documented above.
func parseAndOrder(entry string) ([]*ast.Module, error) {
	entryAbs, err := filepath.Abs(entry)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(entryAbs)

	loaded := map[string]*ast.Module{}
	paths := map[string]string{}
	sources := map[string]string{}
	queue := []string{entryAbs}
	visited := map[string]bool{entryAbs: true}

	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		src := string(b)
		mod, err := parser.Parse(src)
		if err != nil {
			return nil, diag.Wrap(path, src, err)
		}
		modName := joinName(mod.Name)
		if _, dup := loaded[modName]; dup {
			continue
		}
		loaded[modName] = mod
		paths[modName] = path
		sources[modName] = src
		for _, imp := range mod.Imports {
			if isStdlib(joinName(imp.Module)) {
				continue
			}
			impPath, err := resolveImport(dir, imp.Module)
			if err != nil {
				continue
			}
			if !visited[impPath] {
				visited[impPath] = true
				queue = append(queue, impPath)
			}
		}
	}

	order, err := topoSort(loaded)
	if err != nil {
		return nil, err
	}
	out := make([]*ast.Module, 0, len(order))
	for _, name := range order {
		out = append(out, loaded[name])
	}
	return out, nil
}
