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
// no-op effect: the actual server lifecycle is driven by the host
// (mar dev or mar-runtime), not by the user's `main`.
//
// `mods` is the full project AST (used to slice the page-reachable subset
// for the browser bundle). `port` is the listening port: the App.* effect
// signature takes none, so we wire it in at install time.
func Install(env *runtime.Env, mods []*ast.Module, port int, lp *jsserve.LiveProgram) {
	fs := MakeFullstackBuiltin(env, mods, port, lp)
	env.Define("appFullstack", fs)
	env.Define("App.fullstack", fs)

	fe := MakeFrontendBuiltin(mods, port, lp)
	env.Define("appFrontend", fe)
	env.Define("App.frontend", fe)

	be := MakeBackendBuiltin(port, lp)
	env.Define("appBackend", be)
	env.Define("App.backend", be)
}

// MakeFrontendBuiltin overrides `App.frontend : List Page -> Cmd ()`.
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
			frontMods, err := CheckFrontendProgram("App.frontend", pageList.Elements, mods)
			if err != nil {
				return nil, err
			}
			lp.SetPort(port)
			if err := lp.Update(nil, frontMods, "__entry"); err != nil {
				return nil, fmt.Errorf("App.frontend: %v", err)
			}
			return noopEffect("appFrontend"), nil
		},
	}
}

// MakeBackendBuiltin overrides `App.backend : { services } -> Task ()`.
// Services are typed RPC services, each mounted at the verb and path it
// was declared with.
func MakeBackendBuiltin(port int, lp *jsserve.LiveProgram) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			rec, ok := args[0].(runtime.VRecord)
			if !ok {
				return nil, fmt.Errorf("App.backend: expected { services } record (got %T)", args[0])
			}
			routes, err := CheckBackendProgram(rec)
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

// MakeFullstackBuiltin overrides `App.fullstack : { services, pages } -> Cmd ()`.
// `services` mount at the verb + path each was declared with; `pages`
// ship to the browser via the LiveProgram.
func MakeFullstackBuiltin(env *runtime.Env, mods []*ast.Module, port int, lp *jsserve.LiveProgram) runtime.Value {
	return runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			rec, ok := args[0].(runtime.VRecord)
			if !ok {
				return nil, fmt.Errorf("App.fullstack: expected { services, pages } record (got %T)", args[0])
			}
			pagesV, ok := rec.Fields["pages"]
			if !ok {
				return nil, fmt.Errorf("App.fullstack: missing `pages` field")
			}
			pageList, ok := pagesV.(runtime.VList)
			if !ok {
				return nil, fmt.Errorf("App.fullstack: `pages` is not a list (got %T)", pagesV)
			}
			checked, err := CheckProgram("App.fullstack", "" /*serves /_mar/admin*/, rec, pageList.Elements, mods, env)
			if err != nil {
				return nil, err
			}
			lp.SetPort(port)
			if err := lp.Update(checked.Routes, checked.FrontMods, "__entry"); err != nil {
				return nil, fmt.Errorf("App.fullstack: %v", err)
			}
			return noopEffect("appFullstack"), nil
		},
	}
}

// PickFrontMods extracts the subset of project modules reachable from the
// pages' origin modules, narrows each one to the declarations the pages
// actually use, then appends a synthetic `__entry = appFrontend [...pages]`
// declaration so the JS runtime can boot the bundle by looking up `__entry`.
// Errors when a page lacks provenance (i.e. wasn't declared at top level), or
// when client-reachable code calls a builtin that exists only on the server.
//
// The narrowing is what keeps the server's half of a fullstack app out of the
// browser: importing a module for one type alias used to ship everything else
// in it, schema and service handlers included. See prune.go.
func PickFrontMods(pages []runtime.Value, mods []*ast.Module) ([]*ast.Module, error) {
	// Roots are kept in the order the page list first names them, not in a
	// map: the order decides the module order of the bundle, and through it
	// the program hash every response advertises and the page title. Ranging
	// over a map here made two boots of one unchanged program serve two
	// different programs.
	var roots []string
	rootSeen := map[string]bool{}
	pageRefs := make([]ast.Expr, 0, len(pages))
	for i, pv := range pages {
		page, ok := pv.(runtime.VPage)
		if !ok {
			return nil, fmt.Errorf("page %d is not a Page value (got %T)", i, pv)
		}
		if page.OriginName == "" {
			return nil, fmt.Errorf("page %d has no provenance — pages must be top-level bindings (e.g. `myPage = Page.create ...`), not inline expressions", i)
		}
		if !rootSeen[page.OriginModule] {
			rootSeen[page.OriginModule] = true
			roots = append(roots, page.OriginModule)
		}
		pageRefs = append(pageRefs, &ast.EQualified{
			Module: parseModulePath(page.OriginModule),
			Name:   page.OriginName,
		})
	}
	merged := []*ast.Module{}
	seen := map[string]bool{}
	for _, root := range roots {
		for _, m := range reachableFrom(root, mods) {
			name := joinModuleName(m.Name)
			if seen[name] {
				continue
			}
			seen[name] = true
			merged = append(merged, m)
		}
	}
	// Synthetic entry module: Name nil so the page title heuristic
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
	// Built here, not parsed, so the typechecker never sees it. Nothing in
	// it needs elaborating: no numeric literals, no polymorphic references,
	// just a call over the page references collected above. The claim is
	// explicit because the evaluating and serializing sides refuse a tree
	// that has not been through the checker.
	entryModule.MarkElaborated()

	merged = pruneToPageReachable(merged, pageRefs)
	if leaks := findBackendOnlyLeaks(merged); len(leaks) > 0 {
		l := leaks[0]
		return nil, fmt.Errorf(
			"%s.%s runs in the browser and calls `%s`, which only exists on the server.\n"+
				"  Move the call into a service handler and reach it from the page with Service.call.",
			l.Module, l.Decl, l.Builtin)
	}

	merged = append(merged, entryModule)
	return merged, nil
}

// ExtractServices reads the `services` field (List ExposedService) and
// turns each entry into the internal route record the HTTP dispatcher
// consumes: mounted at the verb + path the service was declared with.
func ExtractServices(rec runtime.VRecord, who string) ([]runtime.Value, error) {
	routes, _, err := extractServices(rec, who)
	return routes, err
}

// extractServices also hands back the contracts themselves, which the route
// table check needs and ExposedServiceToRoute erases.
func extractServices(rec runtime.VRecord, who string) ([]runtime.Value, []runtime.VExposedService, error) {
	var out []runtime.Value
	var exposed []runtime.VExposedService
	if v, ok := rec.Fields["services"]; ok {
		list, isList := v.(runtime.VList)
		if !isList {
			return nil, nil, fmt.Errorf("%s: `services` is not a list (got %T)", who, v)
		}
		for i, sv := range list.Elements {
			es, isES := sv.(runtime.VExposedService)
			if !isES {
				return nil, nil, fmt.Errorf("%s: services[%d] is not an ExposedService (got %T)", who, i, sv)
			}
			// A contract that is not a top-level binding never had its
			// declared request type reduced to a shape, because the
			// stamping happens where the loader can see a value AND its
			// declaration together — at a named declaration. An inline
			// `Service.declare` mounts a route whose body is therefore
			// never validated against anything, and the handler is handed
			// a value its own type forbids. Worse, an entity-backed
			// handler then WRITES it: one unauthenticated POST can put a
			// string in an INTEGER column and make every later read of
			// that table fail.
			//
			// Pages have refused this since they were written, ten lines
			// up in this same file. Services went through the sibling
			// function and got no such check.
			if es.Service.InputShape == nil {
				return nil, nil, fmt.Errorf(
					"%s: services[%d] (%s %s) was declared inline, so its request body would never be checked.\n"+
						"  A contract has to be a top-level binding — `myService = Service.declare POST \"/path\"` —\n"+
						"  because that is where the compiler can attach the declared request type to it.",
					who, i, es.Service.Verb, es.Service.Path)
			}
			// The checker refuses this where it can read the verb off the
			// declaration; this is the same rule for every other spelling
			// of it (a verb passed through a variable or a pipe).
			if ast.BodilessVerb(es.Service.Verb) {
				switch es.Service.InputShape.Kind {
				case runtime.WireRecord, runtime.WireUnit:
				default:
					return nil, nil, fmt.Errorf(
						"%s: services[%d] (%s %s) takes a request that is not a record, so no client can send it.\n"+
							"  A %s request has no body: path parameters travel in their slots and the other fields of\n"+
							"  a record in the query string. Make the request a record, for example `{ value : ... }`.",
						who, i, es.Service.Verb, es.Service.Path, es.Service.Verb)
				}
			}
			exposed = append(exposed, es)
			out = append(out, runtime.ExposedServiceToRoute(es))
		}
	}
	if err := checkPathEnumsAgree(exposed); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", who, err)
	}
	return out, exposed, nil
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
// App.* overrides: the side effect they care about (capturing args
// into the LiveProgram) happens during the function call, not when the
// Effect runs.
func noopEffect(tag string) runtime.VEffect {
	return runtime.VEffect{
		Tag: tag,
		Run: func() (runtime.Value, error) { return runtime.VUnit{}, nil },
	}
}

// checkPathEnumsAgree refuses a program whose route patterns would decode a
// path segment into constructors of the wrong type.
//
// The runtime matches a `{name:Type}` segment through a registry keyed by the
// BARE type name, and two modules that each declare a `Status` write to the
// same key — last one loaded wins. The checker is not confused: a type's
// identity is `Module.Name` (ADR 0027), and it resolves the segment to the
// right one. So a booted process holds both answers at once, and the wrong one
// is the one the URL goes through: a `case` the checker PROVED total falls
// through from an ordinary link, and `linkTo` throws on a constructor written
// as a literal in the source.
//
// The comparison is a TAUTOLOGY-GUARD, and that is the whole design. It asks
// only whether the checker's answer for THIS param and the runtime's answer
// for that bare name are the same list; where they are, the program behaves
// exactly as it does today, so the check provably cannot refuse a working
// program.
//
// The tempting simpler rule — "this type name is declared by more than one
// module" — over-approximates and WOULD refuse working programs, because
// whether the collision bites depends on module load order: two modules can
// both declare `Status` while the route still resolves to the one that
// happens to win. That program works, and has to keep working.
//
// The checker's answer is already on the service: stampServiceShape asked the
// type environment for the request type, so the field named by the path param
// carries the constructors the checker resolved.
func checkPathEnumsAgree(services []runtime.VExposedService) error {
	for _, es := range services {
		if es.Service.InputShape == nil {
			continue // refused separately, with its own message
		}
		pattern, err := runtime.ParsePathPattern(es.Service.Path)
		if err != nil {
			continue // an unparseable pattern is someone else's error to report
		}
		for _, seg := range pattern.Segments {
			if !seg.IsParam || runtime.ValidPathTypes[seg.Type] {
				continue
			}
			live, registered := runtime.EnumTypes[seg.Type]
			if !registered {
				continue
			}
			checker, found := ctorsOfField(*es.Service.InputShape, seg.Name)
			if !found {
				continue // the param is not a constructor field; nothing to compare
			}
			if sameCtorList(checker, live) {
				continue
			}
			return fmt.Errorf(
				"the route `%s` decodes `{%s:%s}` into `%s`, but the `%s` this route means is `%s`.\n"+
					"  Path types are matched by their bare name at run time, so when two modules "+
					"declare a `%s` only one of them can win, and this route got the other.\n"+
					"  Rename one of the two types.",
				es.Service.Path, seg.Name, seg.Type,
				strings.Join(live, " | "), seg.Type, strings.Join(checker, " | "), seg.Type)
		}
	}
	return nil
}

// ctorsOfField finds the constructor set the checker resolved for one field of
// a request type.
func ctorsOfField(shape runtime.WireShape, field string) ([]string, bool) {
	if shape.Kind != runtime.WireRecord {
		return nil, false
	}
	for _, f := range shape.Fields {
		if f.Name != field {
			continue
		}
		if f.Shape.Kind != runtime.WireCtor {
			return nil, false
		}
		return f.Shape.Tags, true
	}
	return nil, false
}

func sameCtorList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CheckFrontendProgram is the whole boot check for `App.frontend`, in one
// place both the dev server and `mar build` call.
//
// It exists for the reason CheckProgram exists: the checks used to live inside
// the builtin the dev server installs, and `mar build` installs a DIFFERENT
// builtin — one that captures the page list and returns. So a program `mar dev`
// refused to run built and shipped without a word: two pages on one path, for
// instance, which is the case whose own message says the browser keeps the last
// and the phone keeps the first.
// `who` names the App.* builtin in the message: an iOS build of a fullstack
// app reaches this too, and telling its author about `App.frontend` would send
// them looking for a builtin their program does not call.
func CheckFrontendProgram(who string, pages []runtime.Value, mods []*ast.Module) ([]*ast.Module, error) {
	// The page-side checks. They used to live only on the fullstack path, so a
	// frontend-only app got none of them: two pages on one path, a protected
	// page with no way to authenticate, an app with nowhere for a stray URL to
	// land.
	if err := reportRouteTable(who, nil, pages); err != nil {
		return nil, err
	}
	frontMods, err := PickFrontMods(pages, mods)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", who, err)
	}
	return frontMods, nil
}

// CheckBackendProgram is the same for `App.backend`: the service list turned
// into routes, with the route table checked.
func CheckBackendProgram(rec runtime.VRecord) ([]runtime.Value, error) {
	routes, exposed, err := extractServices(rec, "App.backend")
	if err != nil {
		return nil, err
	}
	if err := reportRouteTable("App.backend", exposed, nil); err != nil {
		return nil, err
	}
	return routes, nil
}

// CheckedProgram is what a registered fullstack program amounts to once every
// boot check has passed: the routes to mount, the contracts behind them, and
// the modules the pages reach.
type CheckedProgram struct {
	Routes    []runtime.Value
	Exposed   []runtime.VExposedService
	FrontMods []*ast.Module
}

// CheckProgram is the whole boot check for a fullstack program, in one place
// both build paths call.
//
// It exists because they did not. `mar dev` installed these builtins and ran
// every check; `mar build --target ios` replaced App.fullstack with its own
// capture, which reads `pages` and ignores `services` entirely — so the same
// program was refused by one and shipped by the other. Measured: a page whose
// only fetch has no handler stops `mar dev` with the contract named, and
// builds an iOS app that prints "Next steps".
//
// One program must not have two answers, and the way to keep it that way is
// one function rather than two lists of calls that drift.
// `adminPanel` says whether THIS deployment serves `/_mar/admin`. It is not
// something `who` can answer: `App.fullstack` serves it in a browser and does
// not on a phone, where the admin panel has no app at all and the Swift
// runtime throws "Page.adminProtected is web-only" when the page is reached.
func CheckProgram(who, noAdminPanel string, rec runtime.VRecord, pages []runtime.Value, mods []*ast.Module, env *runtime.Env) (CheckedProgram, error) {
	routes, exposed, err := extractServices(rec, who)
	if err != nil {
		return CheckedProgram{}, err
	}
	if err := reportRouteTableOn(who, noAdminPanel, exposed, pages); err != nil {
		return CheckedProgram{}, err
	}
	frontMods, err := PickFrontMods(pages, mods)
	if err != nil {
		return CheckedProgram{}, fmt.Errorf("%s: %v", who, err)
	}
	// After PickFrontMods on purpose: it narrows the modules to what the
	// pages actually reach, so a contract called from code no page can run
	// is not something to refuse a boot over.
	if err := CheckServiceCalls(frontMods, env, exposed); err != nil {
		return CheckedProgram{}, fmt.Errorf("%s: %w", who, err)
	}
	return CheckedProgram{Routes: routes, Exposed: exposed, FrontMods: frontMods}, nil
}
