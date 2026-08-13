package typecheck

import (
	"fmt"
	"sort"
	"strings"

	"mar/internal/ast"
)

// RunSideCheck rejects code that reaches across the client/server line, in
// EITHER direction.
//
// The unit is the top-level declaration, not the module. Roots are the
// declarations that define a page, a body applying `Page.create` and friends:
// and from each root the check follows the call graph. A root that reaches a
// backend builtin is an error: the code goes into the client bundle, and the
// name it calls exists only on the server.
//
// Declaration granularity is load-bearing rather than a refinement. The module
// version of this rule rejected `examples/guestbook`, a legitimate single-file
// fullstack app whose `Main.mar` holds the page next to the entity and the
// handlers. The build already splits that correctly by pruning to what the
// pages reach, so a module-level rule was measuring the wrong thing.
//
// It also needs no evaluation, which is what lets it live in the typechecker
// instead of in the build. The older apphost pass took evaluated page VALUES to
// find its roots, so only `mar dev` and `mar build` could call it; `mar check`
// and the LSP never evaluate and stayed silent. `Page.create` is visible in the
// source, so the roots are too, and now every command reports the same thing.
//
// The call graph is keyed by MODULE-QUALIFIED name. Keying by simple name: as
// the GET read-only pass does, deliberately, for a case where over-reporting is
// harmless, would be wrong here: a `load` in a page module and a `load` in a
// service module is ordinary in a fullstack app, and conflating them would
// reject working code.
//
// Both directions run on the same graph, because the failure is the same
// shape both ways: a name that does not exist where the code will run. The
// first version only walked client -> server, so a service handler could reach
// `Canvas.rect` and compile clean, and the program died on the first request
// instead of in the browser. The side table already carried `SideFrontend`;
// only the second walk was missing.
//
// See docs/proposals/side-checking.md.
func RunSideCheck(mods []*ast.Module) []ShapeIssue {
	g := buildSideGraph(mods)
	if len(g.roots) == 0 {
		return nil // nothing defines a page: nothing can reach the server wrongly
	}

	// reaches[decl] = the out-of-place builtin this declaration can arrive at,
	// computed once per direction over the same call graph.
	toServer := spread(g, func(d *sideDecl) *sideRef { return d.backend })
	toClient := spread(g, func(d *sideDecl) *sideRef { return d.frontend })

	var issues []ShapeIssue
	for _, root := range g.roots {
		reaches := toServer
		if root.server {
			reaches = toClient
		}
		hit := reaches[root.decl]
		if hit == nil {
			continue
		}
		issues = append(issues, ShapeIssue{
			Module:  root.module,
			Pos:     hit.pos,
			Message: leakMessage(root, hit),
		})
	}
	sortIssues(issues)
	return issues
}

// spread runs the reachability fixed point for one direction. `seed` picks the
// hit a declaration carries directly; everything else inherits it through the
// call graph, remembering which hop delivered it so a leak three helpers deep
// can name the one to look at.
func spread(g *sideGraph, seed func(*sideDecl) *sideRef) map[string]*sideRef {
	reaches := map[string]*sideRef{}
	for name, d := range g.decls {
		if hit := seed(d); hit != nil {
			reaches[name] = hit
		}
	}
	for changed := true; changed; {
		changed = false
		for name, d := range g.decls {
			if reaches[name] != nil {
				continue
			}
			for _, callee := range d.calls {
				if hit := reaches[callee]; hit != nil {
					reaches[name] = &sideRef{name: hit.name, pos: hit.pos, via: callee}
					changed = true
					break
				}
			}
		}
	}
	return reaches
}

// sideRef is one backend name and where it was found. `via` names the callee
// that led here, so a leak three helpers deep can say which hop to look at.
type sideRef struct {
	name string
	pos  ast.Pos
	via  string
}

type sideDecl struct {
	module   string
	name     string // module-qualified
	backend  *sideRef
	frontend *sideRef
	calls    []string // module-qualified callees
}

type sideRoot struct {
	module string
	decl   string // module-qualified
	simple string
	pos    ast.Pos
	server bool // a service handler rather than a page
}

type sideGraph struct {
	decls map[string]*sideDecl
	roots []sideRoot
}

// pageBuilders are the names whose application makes a declaration a page, and
// therefore a root of the client bundle.
var pageBuilders = map[string]bool{
	"Page.create":           true,
	"Page.dynamic":          true,
	"Page.protected":        true,
	"Page.dynamicProtected": true,
	"Page.adminProtected":   true,
	"Page.sheet":            true,
}

// serviceBuilders are the names whose application makes a declaration a
// service handler, and therefore a root of the server bundle.
//
// `App.backend` and `App.fullstack` are deliberately NOT here. A fullstack
// `main` names the pages as well as the services, so rooting the server walk
// there would follow it straight into `UI` and report every page in the app.
var serviceBuilders = map[string]bool{
	"Service.implement": true,
	"Auth.protect":      true,
}

func buildSideGraph(mods []*ast.Module) *sideGraph {
	g := &sideGraph{decls: map[string]*sideDecl{}}

	// Resolving a reference needs to know, per module, which bare names came
	// from which import, because `import UI exposing (text)` makes `text` a
	// sided builtin with no qualifier at the call site.
	type modInfo struct {
		name  string
		bare  map[string]string // bare name -> qualifying module
		local map[string]bool   // top-level names defined here
	}
	infos := map[string]*modInfo{}
	locals := map[string]map[string]bool{}
	for _, m := range mods {
		name := strings.Join(m.Name, ".")
		local := map[string]bool{}
		for _, d := range m.Decls {
			if v, ok := d.(*ast.ValueDecl); ok {
				local[v.Name] = true
			}
		}
		locals[name] = local
		infos[name] = &modInfo{name: name, bare: map[string]string{}, local: local}
	}

	// Resolve bare names exactly, never by guessing. An earlier draft fell
	// back to "any module that defines this name", and it produced false
	// positives across eight examples: a page's `update` was linked to a
	// same-named backend declaration and inherited its `Entity.define`. That
	// is the simple-name keying trap this check was supposed to avoid,
	// reintroduced through the resolver. A bare name resolves through the
	// import that brought it or not at all.
	for _, m := range mods {
		info := infos[strings.Join(m.Name, ".")]
		for _, imp := range m.Imports {
			dep := strings.Join(imp.Module, ".")
			for _, item := range imp.Exposing.Items {
				info.bare[item.Name] = dep
			}
			if imp.Exposing.All {
				// `exposing (..)` binds everything that module exports. For
				// a user module that is its top-level names, which we have;
				// for a builtin module it is the whole vocabulary, and those
				// names arrive qualified in SideOf anyway.
				for n := range locals[dep] {
					info.bare[n] = dep
				}
			}
		}
	}

	for _, m := range mods {
		info := infos[strings.Join(m.Name, ".")]
		for _, d := range m.Decls {
			v, ok := d.(*ast.ValueDecl)
			if !ok {
				continue
			}
			qual := info.name + "." + v.Name
			sd := &sideDecl{module: info.name, name: qual}
			isPage := false
			isService := false

			walkAll(v.Body, func(x ast.Expr) {
				var ref, modOf string
				var pos ast.Pos
				switch n := x.(type) {
				case *ast.EQualified:
					modOf = strings.Join(n.Module, ".")
					ref, pos = modOf+"."+n.Name, n.Pos
				case *ast.EVar:
					pos = n.Pos
					switch {
					case info.local[n.Name]:
						sd.calls = append(sd.calls, info.name+"."+n.Name)
						return
					case info.bare[n.Name] != "":
						modOf = info.bare[n.Name]
						ref = modOf + "." + n.Name
					default:
						// A bare global, a local binding, or a parameter.
						// None of them carry a side.
						return
					}
				default:
					return
				}

				if pageBuilders[ref] {
					isPage = true
				}
				if serviceBuilders[ref] {
					isService = true
				}
				if s, ok := SideOf(ref); ok {
					if s == SideBackend && sd.backend == nil {
						sd.backend = &sideRef{name: ref, pos: pos}
					}
					if s == SideFrontend && sd.frontend == nil {
						sd.frontend = &sideRef{name: ref, pos: pos}
					}
					return
				}
				// Not a builtin: a value of another module.
				if _, isMod := infos[modOf]; isMod {
					sd.calls = append(sd.calls, ref)
				}
			})

			g.decls[qual] = sd
			if isPage {
				g.roots = append(g.roots, sideRoot{
					module: info.name, decl: qual, simple: v.Name, pos: v.Pos,
				})
			}
			// A declaration can bind services without being a page; it cannot
			// sensibly be both, and if it were, both walks would report it.
			if isService {
				g.roots = append(g.roots, sideRoot{
					module: info.name, decl: qual, simple: v.Name, pos: v.Pos,
					server: true,
				})
			}
		}
	}
	return g
}

func leakMessage(root sideRoot, hit *sideRef) string {
	where, absent, fix := "runs in the browser", "only exists on the server",
		"  Move the call into a service handler and reach it from the page with Service.call."
	kind := "page"
	if root.server {
		where, absent, fix = "runs on the server", "only exists in the browser",
			"  Return the data from the handler and let the page do the drawing."
		kind = "service handler"
	}
	if hit.via == "" {
		return fmt.Sprintf("the %s `%s` %s and calls `%s`, which %s.\n%s",
			kind, root.simple, where, hit.name, absent, fix)
	}
	return fmt.Sprintf("the %s `%s` %s and reaches `%s` through `%s`, and `%s` %s.\n%s",
		kind, root.simple, where, hit.name, hit.via, hit.name, absent, fix)
}

// sortIssues makes the output stable: map iteration order otherwise reshuffles
// the diagnostics between runs, which turns a passing test into a flaky one.
func sortIssues(issues []ShapeIssue) {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Module != issues[j].Module {
			return issues[i].Module < issues[j].Module
		}
		return issues[i].Pos.Line < issues[j].Pos.Line
	})
}
