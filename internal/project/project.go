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
	"sync"

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

	// Boundary shape lint — catches Entity.define / Repo.* /
	// Auth.config issues that BaseEnv's polymorphic types let
	// through. Includes the new Entity.define checks: literal name,
	// portable identifier shape, reserved-prefix rejection, and
	// project-wide duplicate-name detection. Without this call here,
	// `mar check` would silently accept programs that `mar dev`
	// rejects at boot.
	//
	// Note: we pass nil for the expression-type map because Load
	// doesn't keep one. The lint's literal-only mode is enough for
	// the entity-name checks; the record-shape checks that need
	// types are skipped silently when the map is nil (already the
	// documented behavior of RunShapeLint).
	orderedMods := make([]*ast.Module, 0, len(order))
	for _, name := range order {
		orderedMods = append(orderedMods, parsed[name])
	}
	if issues := typecheck.RunShapeLint(orderedMods, nil); len(issues) > 0 {
		issue := issues[0]
		return nil, diag.Wrap(paths[issue.Module], sources[issue.Module], &issue)
	}
	if issues := typecheck.RunGetReadOnlyCheck(orderedMods); len(issues) > 0 {
		issue := issues[0]
		return nil, diag.Wrap(paths[issue.Module], sources[issue.Module], &issue)
	}

	return &Project{
		Root:    root,
		Modules: mods,
		Order:   order,
	}, nil
}

// loadIntoEnv evaluates a module's value declarations into a fresh
// per-module env that chains to the shared `rEnv`. Bare names defined
// in this module live in the local frame; qualified `Module.name`
// aliases are published to `rEnv` so other modules can resolve them
// via `EQualified`. Also processes `import M exposing (...)` clauses
// so the runtime sees the same bare-name bindings the typechecker
// accepted.
//
// `modulesByName` is the project-wide module map. It's only consulted
// here to resolve `import M exposing (Type(..))` — we need the list of
// constructors for `Type`, which lives in `M`'s AST.
//
// Why per-module: with a single shared env, two modules that both
// declare a bare name (e.g. `Backend.Projects.projects = Entity ...`
// and `Frontend.Routes.projects : Path` — yes this happens in real
// code) would clobber each other on load. The flat env's last-write-
// wins would then feed the wrong value into bare references inside
// either module's handlers. Putting each module in its own frame
// keeps bare names module-local; cross-module references must use
// `Module.name`, which the parser/typechecker already enforce except
// for explicit `exposing` imports.
func loadIntoEnv(mod *ast.Module, modName string, rEnv *runtime.Env, modulesByName map[string]*ast.Module) error {
	modEnv := runtime.NewChildEnv(rEnv)

	// Record type aliases double as positional constructors (Elm-style; see
	// the typecheck side in check.go). The checker validated those uses
	// against the alias's field types and reported errors against the alias
	// name; here we give them their runtime meaning by rewriting each
	// `Point x y` (or a bare `Point` passed to a higher-order function) into
	// `\a b -> { x = a, y = b }`. Done before eval — and, because the same
	// module value is what gets serialized for the client, before that too —
	// so no constructor concept ever has to cross the wire.
	desugarRecordAliasCtors(mod, modulesByName)

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
		// `exposing (..)`: bind every export of the module bare —
		// values and ctors registered as `impName.x` in the env chain
		// (for builtin modules like UI, the whole vocabulary). Mirrors
		// the typechecker's wildcard handling in CheckModuleWith.
		if imp.Exposing.All {
			for name, v := range modEnv.ExportsOf(impName) {
				modEnv.Define(name, v)
			}
		}
		for _, item := range imp.Exposing.Items {
			if v, ok := modEnv.Lookup(impName + "." + item.Name); ok {
				modEnv.Define(item.Name, v)
			}
			// `Type(..)`: pull every constructor of the imported type
			// into the bare namespace too. The imported module's AST
			// is the source of truth for the ctor list.
			if item.Open {
				if impMod, ok := modulesByName[impName]; ok {
					for _, d := range impMod.Decls {
						ct, ok := d.(*ast.CustomTypeDecl)
						if !ok || ct.Name != item.Name {
							continue
						}
						for _, c := range ct.Constructors {
							if v, ok := modEnv.Lookup(impName + "." + c.Name); ok {
								modEnv.Define(c.Name, v)
							}
						}
					}
				}
			}
		}
	}

	// Pass 1: register custom-type constructors.
	// Also populate the path-pattern enum registry for any zero-arg
	// ctor type (so `{role:Role}` segments resolve at runtime). The
	// bare ctor name lives in modEnv (module-local); the qualified
	// `Module.Ctor` form is published to the shared `rEnv` so
	// `import M exposing (Type(..))` in Pass 0 of other modules can
	// find it.
	for _, d := range mod.Decls {
		ct, ok := d.(*ast.CustomTypeDecl)
		if !ok {
			continue
		}
		ctorNames := make([]string, 0, len(ct.Constructors))
		ctorArities := map[string]int{}
		for _, c := range ct.Constructors {
			v := makeCtorValueLocal(c.Name, len(c.Args))
			modEnv.Define(c.Name, v)
			rEnv.Define(modName+"."+c.Name, v)
			ctorNames = append(ctorNames, c.Name)
			ctorArities[c.Name] = len(c.Args)
		}
		runtime.RegisterEnumType(ct.Name, ctorNames, ctorArities)
		runtime.RegisterEnumType(modName+"."+ct.Name, ctorNames, ctorArities)
	}

	// Pass 2: pre-bind values to placeholders (for mutual recursion).
	// Local to modEnv — only this module's body needs the placeholder
	// for self-/mutual reference; other modules see the final value
	// via the qualified name set in Pass 3.
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		modEnv.Define(v.Name, runtime.VUnit{})
	}

	// Pass 3: evaluate. The body resolves bare names against modEnv
	// (which shadows but chains to rEnv); closures captured here keep
	// modEnv as their lexical env, so calls from other modules still
	// see this module's own bare bindings.
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		body := ast.Expr(v.Body)
		if len(v.Params) > 0 {
			body = &ast.ELambda{Pos: v.Pos, Params: v.Params, Body: body}
		}
		val, err := runtime.Eval(body, modEnv)
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
		// Same for Services: stamping records the binding's name for
		// diagnostics. The mounted URL comes from the declared path.
		if svc, ok := val.(runtime.VService); ok && svc.OriginName == "" {
			svc.OriginModule = modName
			svc.OriginName = v.Name
			val = svc
		}
		modEnv.Define(v.Name, val)
		rEnv.Define(modName+"."+v.Name, val)
	}
	return nil
}

// LookupMain finds the project's entry `main` value. Tries the
// `Main.main` convention first (multi-file projects with a dedicated
// `module Main` entry point), then falls back to the entry module's
// qualified name. The entry is always last in `mods`
// because the loader's BFS starts from the entry file and the topo
// sort emits dependencies before their dependents — so for any
// project the user can run, mods[len(mods)-1] is the file they
// passed on the CLI.
//
// Returns the value and ok=true when found. Bare-name `main` is
// intentionally not consulted: per-module env scoping (see
// loadIntoEnv) keeps bare names module-local, so a bare `main` in
// the shared `rEnv` is never registered.
func LookupMain(rEnv *runtime.Env, mods []*ast.Module) (runtime.Value, bool) {
	if v, ok := rEnv.Lookup("Main.main"); ok {
		return v, true
	}
	if len(mods) == 0 {
		return nil, false
	}
	entry := joinName(mods[len(mods)-1].Name)
	return rEnv.Lookup(entry + ".main")
}

// desugarRecordAliasCtors rewrites, in place, every use of a record type
// alias as a constructor into the equivalent record-building lambda. It runs
// once per module at load time, before evaluation and serialization, so both
// the Go runtime and the client bundle see only ordinary lambdas.
func desugarRecordAliasCtors(mod *ast.Module, modulesByName map[string]*ast.Module) {
	aliases := collectRecordAliases(mod, modulesByName)
	if len(aliases) == 0 {
		return
	}
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		v.Body = rewriteRecordCtors(v.Body, aliases)
	}
}

// collectRecordAliases maps the name a record-alias constructor is written as
// — bare (`Point`) for the current module's own aliases and for those an
// import exposes, qualified (`Geometry.Point`) for any imported module's
// aliases — to that alias's field names in declaration order. This mirrors
// loadIntoEnv's Pass 0 import-exposing rules, so the set matches exactly what
// the typechecker accepted as a constructor.
func collectRecordAliases(mod *ast.Module, modulesByName map[string]*ast.Module) map[string][]string {
	out := map[string][]string{}
	for name, fields := range moduleRecordAliasMap(mod) {
		out[name] = fields
	}
	for _, imp := range mod.Imports {
		impName := joinName(imp.Module)
		impMod, ok := modulesByName[impName]
		if !ok {
			continue
		}
		impAliases := moduleRecordAliasMap(impMod)
		for name, fields := range impAliases {
			out[impName+"."+name] = fields
		}
		if imp.Exposing.All {
			for name, fields := range impAliases {
				out[name] = fields
			}
			continue
		}
		for _, item := range imp.Exposing.Items {
			if fields, ok := impAliases[item.Name]; ok {
				out[item.Name] = fields
			}
		}
	}
	return out
}

// moduleRecordAliasMap returns the closed-record type aliases declared in m,
// each as its field names in declaration order. Only closed records become
// constructors — `type alias Id = Int` and open rows (`{ r | ... }`) do not.
func moduleRecordAliasMap(m *ast.Module) map[string][]string {
	out := map[string][]string{}
	for _, d := range m.Decls {
		a, ok := d.(*ast.TypeAliasDecl)
		if !ok {
			continue
		}
		rec, ok := a.Body.(*ast.TypeRecord)
		if !ok || rec.Extends != "" {
			continue
		}
		fields := make([]string, len(rec.Fields))
		for i, f := range rec.Fields {
			fields[i] = f.Name
		}
		out[a.Name] = fields
	}
	return out
}

// rewriteRecordCtors walks an expression, replacing each ECtor that names a
// record alias with `\a1 .. an -> { f1 = a1, ... }` and recursing into every
// child. Sub-expression slots are reassigned in place; leaf nodes (literals,
// EVar, EQualified, EFieldAccessor) are returned unchanged.
func rewriteRecordCtors(e ast.Expr, aliases map[string][]string) ast.Expr {
	if e == nil {
		return nil
	}
	switch n := e.(type) {
	case *ast.ECtor:
		key := n.Name
		if len(n.Module) > 0 {
			key = joinName(n.Module) + "." + n.Name
		}
		if fields, ok := aliases[key]; ok {
			return recordCtorLambda(n.Pos, fields)
		}
		return n
	case *ast.EApp:
		n.Fn = rewriteRecordCtors(n.Fn, aliases)
		n.Arg = rewriteRecordCtors(n.Arg, aliases)
		return n
	case *ast.EBinop:
		n.Left = rewriteRecordCtors(n.Left, aliases)
		n.Right = rewriteRecordCtors(n.Right, aliases)
		return n
	case *ast.ELambda:
		n.Body = rewriteRecordCtors(n.Body, aliases)
		return n
	case *ast.EIf:
		n.Cond = rewriteRecordCtors(n.Cond, aliases)
		n.Then = rewriteRecordCtors(n.Then, aliases)
		n.Else = rewriteRecordCtors(n.Else, aliases)
		return n
	case *ast.ELet:
		for i := range n.Bindings {
			n.Bindings[i].Body = rewriteRecordCtors(n.Bindings[i].Body, aliases)
		}
		n.Body = rewriteRecordCtors(n.Body, aliases)
		return n
	case *ast.ETuple:
		for i := range n.Members {
			n.Members[i] = rewriteRecordCtors(n.Members[i], aliases)
		}
		return n
	case *ast.EList:
		for i := range n.Elements {
			n.Elements[i] = rewriteRecordCtors(n.Elements[i], aliases)
		}
		return n
	case *ast.ERecord:
		for i := range n.Fields {
			n.Fields[i].Value = rewriteRecordCtors(n.Fields[i].Value, aliases)
		}
		return n
	case *ast.ERecordUpdate:
		n.Record = rewriteRecordCtors(n.Record, aliases)
		for i := range n.Fields {
			n.Fields[i].Value = rewriteRecordCtors(n.Fields[i].Value, aliases)
		}
		return n
	case *ast.EFieldAccess:
		n.Record = rewriteRecordCtors(n.Record, aliases)
		return n
	case *ast.ECase:
		n.Subject = rewriteRecordCtors(n.Subject, aliases)
		for i := range n.Branches {
			n.Branches[i].Body = rewriteRecordCtors(n.Branches[i].Body, aliases)
		}
		return n
	case *ast.ENegate:
		n.Inner = rewriteRecordCtors(n.Inner, aliases)
		return n
	default:
		return e
	}
}

// recordCtorLambda builds `\$0 $1 .. -> { f0 = $0, f1 = $1, ... }`. The
// synthetic `$n` parameter names cannot collide with anything: the lambda
// body references only its own parameters. A zero-field alias is just the
// empty record value.
func recordCtorLambda(pos ast.Pos, fields []string) ast.Expr {
	if len(fields) == 0 {
		return &ast.ERecord{Pos: pos}
	}
	params := make([]ast.Pattern, len(fields))
	recFields := make([]ast.RecField, len(fields))
	for i, f := range fields {
		name := fmt.Sprintf("$%d", i)
		params[i] = &ast.PVar{Pos: pos, Name: name}
		recFields[i] = ast.RecField{Pos: pos, Name: f, Value: &ast.EVar{Pos: pos, Name: name}}
	}
	return &ast.ELambda{Pos: pos, Params: params, Body: &ast.ERecord{Pos: pos, Fields: recFields}}
}

// makeCtorValueLocal builds the runtime value for a custom-type
// constructor. Duplicates runtime.makeCtorValue (which lives in
// module.go and is reachable only from the runtime package's own
// tests). Keeping the project-side copy avoids widening the runtime
// package's exported surface for a helper that has exactly one
// caller (loadIntoEnv, below).
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

// isStdlib reports whether a module name refers to a built-in (e.g. List,
// String, UI). These don't have to exist as files; the framework provides
// them, so `import X` of one needs no corresponding .mar.
func isStdlib(name string) bool {
	return stdlibModules()[name]
}

var (
	stdlibModulesOnce sync.Once
	stdlibModulesSet  map[string]bool
)

// stdlibModules is the set of built-in module names. It is derived from the
// qualified builtin surface (typecheck.BaseQualifiedSymbols) so it can never
// drift from what the language actually provides — adding a builtin under a
// new module makes that module importable automatically. Computed once.
func stdlibModules() map[string]bool {
	stdlibModulesOnce.Do(func() {
		// View is an ambient type module: UI.* build View values, so View
		// has no qualified functions of its own and isn't in the qualified
		// symbol map — but `import View` is still valid.
		set := map[string]bool{"View": true}
		for qualified := range typecheck.BaseQualifiedSymbols() {
			if i := strings.IndexByte(qualified, '.'); i > 0 {
				set[qualified[:i]] = true
			}
		}
		stdlibModulesSet = set
	})
	return stdlibModulesSet
}

func joinName(parts []string) string {
	return strings.Join(parts, ".")
}
