// Package apphost holds the bits of the App.* override pipeline that
// `mar dev` (development server) and `mar-runtime` (production stub)
// both need: the App.frontend / .backend / .fullstack builtins that
// capture routes/services into a LiveProgram, the page-bundle slicing
// helper, and route assembly from low-level Endpoints + typed Services.
//
// Everything here is target-neutral. Dev-only affordances (file watching,
// SSE broadcast, browser-open) live elsewhere.
package apphost

import (
	"fmt"
	"strings"

	"mar/internal/ast"
	"mar/internal/jsserve"
	"mar/internal/runtime"
)

// Install registers project-aware versions of App.frontend / App.backend /
// App.fullstack on the given runtime env. Each captures its arguments
// (routes, services, pages) into the supplied LiveProgram and returns a
// no-op effect — the actual server lifecycle is driven by the host
// (mar dev or mar-runtime), not by the user's `main`.
//
// `mods` is the full project AST (used to slice the page-reachable subset
// for the browser bundle). `port` is the listening port — the App.* effect
// signature takes none, so we wire it in at install time.
func Install(env *runtime.Env, mods []*ast.Module, port int, lp *jsserve.LiveProgram) {
	fs := MakeFullstackBuiltin(mods, port, lp)
	env.Define("appFullstack", fs)
	env.Define("App.fullstack", fs)

	fe := MakeFrontendBuiltin(mods, port, lp)
	env.Define("appFrontend", fe)
	env.Define("App.frontend", fe)

	be := MakeBackendBuiltin(port, lp)
	env.Define("appBackend", be)
	env.Define("App.backend", be)
}

// MakeFrontendBuiltin overrides `App.frontend : List Page -> Effect String ()`.
// Captures the page list, slices the AST modules reachable from those
// pages, hands them to the LiveProgram so the dev server / production
// runtime both ship the same bundle to the browser.
func MakeFrontendBuiltin(mods []*ast.Module, port int, lp *jsserve.LiveProgram) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			pageList, ok := args[0].(runtime.VList)
			if !ok {
				return nil, fmt.Errorf("App.frontend: expected List Page (got %T)", args[0])
			}
			frontMods, err := PickFrontMods(pageList.Elements, mods)
			if err != nil {
				return nil, fmt.Errorf("App.frontend: %v", err)
			}
			lp.SetPort(port)
			if err := lp.Update(nil, frontMods, "__entry"); err != nil {
				return nil, fmt.Errorf("App.frontend: %v", err)
			}
			return noopEffect("appFrontend"), nil
		},
	}
}

// MakeBackendBuiltin overrides `App.backend : { routes, services } -> Effect String ()`.
// Routes are low-level Endpoint mounts; services are typed RPC services
// (Service.expose'd) — both flatten into the same Route slice the
// dispatcher reads.
func MakeBackendBuiltin(port int, lp *jsserve.LiveProgram) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			rec, ok := args[0].(runtime.VRecord)
			if !ok {
				return nil, fmt.Errorf("App.backend: expected { routes, services } record (got %T)", args[0])
			}
			routes, err := ExtractRoutesAndServices(rec, "App.backend")
			if err != nil {
				return nil, err
			}
			lp.SetPort(port)
			if err := lp.Update(routes, nil, ""); err != nil {
				return nil, fmt.Errorf("App.backend: %v", err)
			}
			return noopEffect("appBackend"), nil
		},
	}
}

// MakeFullstackBuiltin overrides `App.fullstack : { api, services, pages } -> Effect String ()`.
// `api` is mounted under /api/*, `services` under /services/*, `pages`
// shipped to the browser via the LiveProgram.
func MakeFullstackBuiltin(mods []*ast.Module, port int, lp *jsserve.LiveProgram) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			rec, ok := args[0].(runtime.VRecord)
			if !ok {
				return nil, fmt.Errorf("App.fullstack: expected { api, services, pages } record (got %T)", args[0])
			}
			pagesV, ok := rec.Fields["pages"]
			if !ok {
				return nil, fmt.Errorf("App.fullstack: missing `pages` field")
			}
			pageList, ok := pagesV.(runtime.VList)
			if !ok {
				return nil, fmt.Errorf("App.fullstack: `pages` is not a list (got %T)", pagesV)
			}
			apiRec := runtime.VRecord{Fields: map[string]runtime.Value{}}
			if v, ok := rec.Fields["api"]; ok {
				apiRec.Fields["routes"] = v
			} else {
				apiRec.Fields["routes"] = runtime.VList{}
			}
			if v, ok := rec.Fields["services"]; ok {
				apiRec.Fields["services"] = v
			} else {
				apiRec.Fields["services"] = runtime.VList{}
			}
			routes, err := ExtractRoutesAndServices(apiRec, "App.fullstack")
			if err != nil {
				return nil, err
			}
			frontMods, err := PickFrontMods(pageList.Elements, mods)
			if err != nil {
				return nil, fmt.Errorf("App.fullstack: %v", err)
			}
			lp.SetPort(port)
			if err := lp.Update(routes, frontMods, "__entry"); err != nil {
				return nil, fmt.Errorf("App.fullstack: %v", err)
			}
			return noopEffect("appFullstack"), nil
		},
	}
}

// PickFrontMods extracts the subset of project modules reachable from the
// pages' origin modules, then appends a synthetic `__entry = appFrontend
// [...pages]` declaration so the JS runtime can boot the bundle by
// looking up `__entry`. Errors when a page lacks provenance (i.e. wasn't
// declared at top level).
func PickFrontMods(pages []runtime.Value, mods []*ast.Module) ([]*ast.Module, error) {
	roots := map[string]bool{}
	pageRefs := make([]ast.Expr, 0, len(pages))
	for i, pv := range pages {
		page, ok := pv.(runtime.VPage)
		if !ok {
			return nil, fmt.Errorf("page %d is not a Page value (got %T)", i, pv)
		}
		if page.OriginName == "" {
			return nil, fmt.Errorf("page %d has no provenance — pages must be top-level bindings (e.g. `myPage = Page.create ...`), not inline expressions", i)
		}
		roots[page.OriginModule] = true
		pageRefs = append(pageRefs, &ast.EQualified{
			Module: parseModulePath(page.OriginModule),
			Name:   page.OriginName,
		})
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
	// Synthetic entry module — Name nil so the page title heuristic
	// doesn't pick "__Entry" over the user's actual module name.
	entryModule := &ast.Module{
		Decls: []ast.Decl{
			&ast.ValueDecl{
				Name: "__entry",
				Body: &ast.EApp{
					Fn:  &ast.EVar{Name: "appFrontend"},
					Arg: &ast.EList{Elements: pageRefs},
				},
			},
		},
	}
	merged = append(merged, entryModule)
	return merged, nil
}

// ExtractRoutesAndServices reads `routes` (List Route) and `services`
// (List ExposedService) fields from a record and produces the unified
// slice the HTTP dispatcher consumes. Each ExposedService becomes a
// POST route at `/services/<module>.<name>`.
func ExtractRoutesAndServices(rec runtime.VRecord, who string) ([]runtime.Value, error) {
	var out []runtime.Value
	if v, ok := rec.Fields["routes"]; ok {
		list, isList := v.(runtime.VList)
		if !isList {
			return nil, fmt.Errorf("%s: `routes` is not a list (got %T)", who, v)
		}
		out = append(out, list.Elements...)
	}
	if v, ok := rec.Fields["services"]; ok {
		list, isList := v.(runtime.VList)
		if !isList {
			return nil, fmt.Errorf("%s: `services` is not a list (got %T)", who, v)
		}
		for i, sv := range list.Elements {
			es, isES := sv.(runtime.VExposedService)
			if !isES {
				return nil, fmt.Errorf("%s: services[%d] is not an ExposedService (got %T)", who, i, sv)
			}
			out = append(out, runtime.ExposedServiceToRoute(es))
		}
	}
	return out, nil
}

// reachableFrom walks the import graph starting at startModule and returns
// the modules in topological order (deps first).
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

func joinModuleName(parts []string) string {
	if len(parts) == 0 {
		return "(unnamed)"
	}
	return strings.Join(parts, ".")
}

func parseModulePath(dotted string) ast.ModuleName {
	out := ast.ModuleName{}
	cur := ""
	for _, c := range dotted {
		if c == '.' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(c)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// noopEffect returns an Effect that does nothing on Run. Used by the
// App.* overrides — the side effect they care about (capturing args
// into the LiveProgram) happens during the function call, not when the
// Effect runs.
func noopEffect(tag string) runtime.VEffect {
	return runtime.VEffect{
		Tag: tag,
		Run: func() (runtime.Value, error) { return runtime.VUnit{}, nil },
	}
}
