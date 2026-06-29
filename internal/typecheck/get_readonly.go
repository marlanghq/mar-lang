// GET read-only check — a sound, project-wide pass that holds every
// service declared with the GET verb to a read-only handler.
//
// A GET endpoint is, by HTTP contract, safe: it must not change data.
// Mar enforces that statically. The pass:
//
//  1. Collects every service's verb from `name = Service.declare VERB
//     "path"` across all modules.
//  2. Builds a write-reachability summary over the call graph of all
//     top-level functions: a function "writes" if its body references a
//     database-write builtin (Repo.create / Repo.update /
//     Repo.deleteById) or calls any function that writes. The summary is
//     a fixpoint, so a write hidden behind helper functions is still
//     caught.
//  3. For every `Service.implement svc h` / `Auth.protect svc h` whose
//     service was declared GET, reports an error if the handler `h`
//     reaches a write.
//
// Soundness over precision: the call graph is keyed by simple name and a
// name is treated as writing if ANY top-level function of that name can
// write. That can in theory over-report on a cross-module name collision
// (a false positive), but it can never miss a real write (no false
// negative) — exactly the bias a safety check should have.

package typecheck

import (
	"fmt"
	"strings"

	"mar/internal/ast"
)

// writeBuiltins are the effect builtins that mutate the database. A GET
// handler may not reach any of them.
var writeBuiltins = map[string]bool{
	"Repo.create":     true,
	"Repo.update":     true,
	"Repo.deleteById": true,
}

// RunGetReadOnlyCheck reports every GET service whose handler can perform
// a database write. Returns nil when clean. Reuses ShapeIssue so the CLI
// renders these with the same positioned "Type error" treatment.
func RunGetReadOnlyCheck(mods []*ast.Module) []ShapeIssue {
	verbs := map[string]string{}            // service simple name -> verb
	bodiesByName := map[string][]ast.Expr{} // top-level simple name -> bodies
	for _, m := range mods {
		for _, d := range m.Decls {
			v, ok := d.(*ast.ValueDecl)
			if !ok {
				continue
			}
			bodiesByName[v.Name] = append(bodiesByName[v.Name], v.Body)
			if verb, ok := serviceDeclareVerb(v.Body); ok {
				verbs[v.Name] = verb
			}
		}
	}
	if len(verbs) == 0 {
		return nil
	}

	writes := computeWriteReachability(bodiesByName)

	var issues []ShapeIssue
	for _, m := range mods {
		modName := strings.Join(m.Name, ".")
		for _, d := range m.Decls {
			v, ok := d.(*ast.ValueDecl)
			if !ok {
				continue
			}
			for _, site := range findHandlerSites(v.Body) {
				if verbs[site.serviceName] != "GET" {
					continue
				}
				if exprReachesWrite(collectRefs(site.handler), writes) {
					issues = append(issues, ShapeIssue{
						Module: modName,
						Pos:    site.pos,
						Message: fmt.Sprintf(
							"GET service %q must be read-only, but its handler reaches a database write (Repo.create / Repo.update / Repo.deleteById).\n"+
								"  A GET endpoint must not change data. Declare it with POST / PUT / PATCH / DELETE, or move the write out of the handler.",
							site.serviceName),
					})
				}
			}
		}
	}
	return issues
}

// serviceDeclareVerb returns the HTTP verb of a `Service.declare VERB
// "path"` body. ok=false for anything else.
func serviceDeclareVerb(e ast.Expr) (string, bool) {
	head, args := flattenApp(e)
	if !isQualified(head, "Service", "declare") || len(args) != 2 {
		return "", false
	}
	switch m := args[0].(type) {
	case *ast.ECtor:
		return m.Name, true
	case *ast.EVar:
		return m.Name, true
	}
	return "", false
}

// handlerSite is one `Service.implement` / `Auth.protect` application:
// the service it implements (by simple name) and the handler expression.
type handlerSite struct {
	serviceName string
	handler     ast.Expr
	pos         ast.Pos
}

// findHandlerSites collects every service-implementation application
// inside an expression.
func findHandlerSites(e ast.Expr) []handlerSite {
	var sites []handlerSite
	walkAll(e, func(x ast.Expr) {
		app, ok := x.(*ast.EApp)
		if !ok {
			return
		}
		head, args := flattenApp(app)
		if !isQualified(head, "Service", "implement") && !isQualified(head, "Auth", "protect") {
			return
		}
		if len(args) < 2 {
			return
		}
		name := refSimpleName(args[0])
		if name == "" {
			return
		}
		sites = append(sites, handlerSite{serviceName: name, handler: args[1], pos: app.Pos})
	})
	return sites
}

// refSimpleName returns the bare name of a value reference: `foo` -> foo,
// `Shared.foo` -> foo. Anything else returns "".
func refSimpleName(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.EVar:
		return n.Name
	case *ast.EQualified:
		return n.Name
	}
	return ""
}

// collectRefs returns the set of names an expression references. Qualified
// names are recorded both fully (so `Repo.create` matches a write builtin)
// and by simple name (so a cross-module function call resolves into the
// global call graph).
func collectRefs(e ast.Expr) map[string]bool {
	refs := map[string]bool{}
	walkAll(e, func(x ast.Expr) {
		switch n := x.(type) {
		case *ast.EVar:
			refs[n.Name] = true
		case *ast.EQualified:
			refs[strings.Join(n.Module, ".")+"."+n.Name] = true
			refs[n.Name] = true
		}
	})
	return refs
}

// exprReachesWrite is true when a reference set hits a write builtin
// directly or a function known to write.
func exprReachesWrite(refs map[string]bool, writes map[string]bool) bool {
	for r := range refs {
		if writeBuiltins[r] || writes[r] {
			return true
		}
	}
	return false
}

// computeWriteReachability returns the set of top-level simple names whose
// evaluation can reach a database write, via a fixpoint over the call
// graph. A name writes if any of its bodies references a write builtin or
// any name that writes.
func computeWriteReachability(bodiesByName map[string][]ast.Expr) map[string]bool {
	refsByName := map[string]map[string]bool{}
	for name, bodies := range bodiesByName {
		union := map[string]bool{}
		for _, b := range bodies {
			for r := range collectRefs(b) {
				union[r] = true
			}
		}
		refsByName[name] = union
	}

	writes := map[string]bool{}
	for name, refs := range refsByName {
		for r := range refs {
			if writeBuiltins[r] {
				writes[name] = true
				break
			}
		}
	}

	for changed := true; changed; {
		changed = false
		for name, refs := range refsByName {
			if writes[name] {
				continue
			}
			for r := range refs {
				if writes[r] {
					writes[name] = true
					changed = true
					break
				}
			}
		}
	}
	return writes
}

// walkAll visits e and every sub-expression, calling visit on each. The
// node-type coverage mirrors walkChildren in shape_lint.go.
func walkAll(e ast.Expr, visit func(ast.Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch n := e.(type) {
	case *ast.EApp:
		walkAll(n.Fn, visit)
		walkAll(n.Arg, visit)
	case *ast.EBinop:
		walkAll(n.Left, visit)
		walkAll(n.Right, visit)
	case *ast.ELambda:
		walkAll(n.Body, visit)
	case *ast.EIf:
		walkAll(n.Cond, visit)
		walkAll(n.Then, visit)
		walkAll(n.Else, visit)
	case *ast.ELet:
		for _, b := range n.Bindings {
			walkAll(b.Body, visit)
		}
		walkAll(n.Body, visit)
	case *ast.ETuple:
		for _, m := range n.Members {
			walkAll(m, visit)
		}
	case *ast.EList:
		for _, el := range n.Elements {
			walkAll(el, visit)
		}
	case *ast.ERecord:
		for _, f := range n.Fields {
			walkAll(f.Value, visit)
		}
	case *ast.ERecordUpdate:
		walkAll(n.Record, visit)
		for _, f := range n.Fields {
			walkAll(f.Value, visit)
		}
	case *ast.EFieldAccess:
		walkAll(n.Record, visit)
	case *ast.ECase:
		walkAll(n.Subject, visit)
		for _, b := range n.Branches {
			walkAll(b.Body, visit)
		}
	case *ast.ENegate:
		walkAll(n.Inner, visit)
	}
}
