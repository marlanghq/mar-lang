package typecheck

import (
	"sort"

	"mar/internal/ast"
)

// declInfo records what we need about each top-level value: whether
// it's a function (parametrized) and where it's defined (for error
// reporting).
type declInfo struct {
	isFunction bool
	pos        ast.Pos
}

// valueGraph is the dependency graph over a module's top-level values:
// who each one is, and which other top-level names its body reads.
// Both the cycle check and the evaluation ordering run off it.
type valueGraph struct {
	decls map[string]declInfo
	deps  map[string][]string // name -> top-level names referenced
}

func buildValueGraph(mod *ast.Module) valueGraph {
	g := valueGraph{decls: map[string]declInfo{}, deps: map[string][]string{}}
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		g.decls[v.Name] = declInfo{isFunction: len(v.Params) > 0, pos: v.Pos}
	}
	// Walk each value's body collecting top-level refs. Local variables
	// (let / lambda params / pattern binds) shadow top-level names:
	// pass them as locals so we don't count them as deps.
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		// Function params are locals inside the body.
		locals := map[string]bool{}
		for _, p := range v.Params {
			collectPatternBinds(p, locals)
		}
		seen := map[string]bool{}
		collectTopLevelRefs(v.Body, g.decls, locals, seen)
		out := make([]string, 0, len(seen))
		for n := range seen {
			out = append(out, n)
		}
		// `seen` is a map, so its range order changes run to run. Sort
		// before storing: orderValueDecls walks these to build the
		// evaluation order, and that order has to be reproducible.
		sort.Strings(out)
		g.deps[v.Name] = out
	}
	return g
}

func checkValueCycles(mod *ast.Module) error {
	g := buildValueGraph(mod)
	decls, deps := g.decls, g.deps

	// For each non-function value, DFS to see if it depends back on
	// itself transitively.
	for name, d := range decls {
		if d.isFunction {
			continue
		}
		if path := findCycle(name, deps); path != nil {
			return errorf(d.pos,
				"value '%s' depends on itself (cycle: %s) — non-function values can't be self-referential",
				name, joinCycle(path))
		}
	}
	return nil
}

// findCycle does DFS from start and returns the cycle path (list of
// names ending where it reconnects to start) if start is reachable
// from itself, else nil.
func findCycle(start string, deps map[string][]string) []string {
	stack := []string{start}
	visited := map[string]bool{}
	parent := map[string]string{}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, m := range deps[n] {
			if m == start {
				// Reconstruct path: start -> ... -> n -> start.
				path := []string{start}
				cur := n
				for cur != "" && cur != start {
					path = append(path, cur)
					cur = parent[cur]
				}
				path = append(path, start)
				return path
			}
			if visited[m] {
				continue
			}
			visited[m] = true
			parent[m] = n
			stack = append(stack, m)
		}
	}
	return nil
}

// orderValueDecls rewrites the module's value declarations into an order
// the runtimes can evaluate straight through, so a value may reference a
// value written further down the file.
//
// Every runtime evaluates top-level values eagerly, in the order the
// declarations appear. That made source order load-bearing: `total` reading
// a `nums` declared below it saw the pre-bind placeholder instead of the
// list, and blew up later inside a builtin: on a program that typechecks.
// Fixing it here rather than in each runtime means the Go, JS and Swift
// evaluators need to know nothing about it; they still walk the list in
// order, and the list is now in the right order.
//
// Two groups, in this order:
//
//  1. Functions, keeping source order. A function declaration only builds a
//     closure over the shared module environment, so it reads nothing and
//     can be mutually recursive with any other function. Putting them all
//     first is what lets a value call a function declared below it.
//  2. Non-function values, dependencies first. checkValueCycles has already
//     rejected cycles here, so a topological order always exists. Ties keep
//     source order, so the result is stable and diffs stay readable.
//
// Declarations that are not values (types, aliases) keep their slots.
func orderValueDecls(mod *ast.Module) {
	g := buildValueGraph(mod)

	slots := []int{}
	for i, d := range mod.Decls {
		if _, ok := d.(*ast.ValueDecl); ok {
			slots = append(slots, i)
		}
	}
	if len(slots) < 2 {
		return
	}

	byName := map[string]*ast.ValueDecl{}
	var funcs, values []*ast.ValueDecl
	for _, i := range slots {
		v := mod.Decls[i].(*ast.ValueDecl)
		byName[v.Name] = v
		if len(v.Params) > 0 {
			funcs = append(funcs, v)
		} else {
			values = append(values, v)
		}
	}

	ordered := make([]*ast.ValueDecl, 0, len(slots))
	ordered = append(ordered, funcs...)

	// Post-order DFS: emit a value only after everything it reads.
	done := map[string]bool{}
	var visit func(v *ast.ValueDecl)
	visit = func(v *ast.ValueDecl) {
		if done[v.Name] {
			return
		}
		done[v.Name] = true // set first: a cycle would otherwise spin
		for _, dep := range g.deps[v.Name] {
			if info, ok := g.decls[dep]; ok && !info.isFunction {
				if d := byName[dep]; d != nil {
					visit(d)
				}
			}
		}
		ordered = append(ordered, v)
	}
	for _, v := range values {
		visit(v)
	}

	for n, i := range slots {
		mod.Decls[i] = ordered[n]
	}
}

func joinCycle(path []string) string {
	out := ""
	for i, p := range path {
		if i > 0 {
			out += " -> "
		}
		out += p
	}
	return out
}

// collectTopLevelRefs walks an expression and records every reference
// to a top-level declaration (by looking the name up in `decls`).
// Lazy / eager doesn't matter here: see checkValueCycles for why.
func collectTopLevelRefs(e ast.Expr, decls map[string]declInfo, locals map[string]bool, out map[string]bool) {
	switch n := e.(type) {
	case *ast.EVar:
		if !locals[n.Name] {
			if _, ok := decls[n.Name]; ok {
				out[n.Name] = true
			}
		}
	case *ast.EApp:
		collectTopLevelRefs(n.Fn, decls, locals, out)
		collectTopLevelRefs(n.Arg, decls, locals, out)
	case *ast.EBinop:
		collectTopLevelRefs(n.Left, decls, locals, out)
		collectTopLevelRefs(n.Right, decls, locals, out)
	case *ast.ELambda:
		// Lambda params shadow top-level names inside its body.
		inner := copyLocals(locals)
		for _, p := range n.Params {
			collectPatternBinds(p, inner)
		}
		collectTopLevelRefs(n.Body, decls, inner, out)
	case *ast.EIf:
		collectTopLevelRefs(n.Cond, decls, locals, out)
		collectTopLevelRefs(n.Then, decls, locals, out)
		collectTopLevelRefs(n.Else, decls, locals, out)
	case *ast.ECase:
		collectTopLevelRefs(n.Subject, decls, locals, out)
		for _, b := range n.Branches {
			inner := copyLocals(locals)
			collectPatternBinds(b.Pattern, inner)
			collectTopLevelRefs(b.Body, decls, inner, out)
		}
	case *ast.ELet:
		inner := copyLocals(locals)
		for _, b := range n.Bindings {
			// Bindings see prior bindings, so inner builds incrementally.
			collectTopLevelRefs(b.Body, decls, inner, out)
			collectPatternBinds(b.Pattern, inner)
		}
		collectTopLevelRefs(n.Body, decls, inner, out)
	case *ast.ETuple:
		for _, m := range n.Members {
			collectTopLevelRefs(m, decls, locals, out)
		}
	case *ast.EList:
		for _, m := range n.Elements {
			collectTopLevelRefs(m, decls, locals, out)
		}
	case *ast.ERecord:
		for _, f := range n.Fields {
			collectTopLevelRefs(f.Value, decls, locals, out)
		}
	case *ast.ERecordUpdate:
		collectTopLevelRefs(n.Record, decls, locals, out)
		for _, f := range n.Fields {
			collectTopLevelRefs(f.Value, decls, locals, out)
		}
	case *ast.EFieldAccess:
		collectTopLevelRefs(n.Record, decls, locals, out)
	case *ast.ENegate:
		collectTopLevelRefs(n.Inner, decls, locals, out)
		// EInt, EString, EUnit, EFieldAccessor, EQualified, ECtor:
		// no top-level refs to collect.
	}
}

// collectPatternBinds adds every name that the pattern would bind into
// the locals set (so subsequent ref collection skips them).
func collectPatternBinds(p ast.Pattern, locals map[string]bool) {
	switch x := p.(type) {
	case *ast.PVar:
		locals[x.Name] = true
	case *ast.PCtor:
		for _, a := range x.Args {
			collectPatternBinds(a, locals)
		}
	case *ast.PTuple:
		for _, m := range x.Members {
			collectPatternBinds(m, locals)
		}
	case *ast.PRecord:
		for _, f := range x.Fields {
			locals[f] = true
		}
	case *ast.PCons:
		collectPatternBinds(x.Head, locals)
		collectPatternBinds(x.Tail, locals)
	case *ast.PList:
		for _, e := range x.Elements {
			collectPatternBinds(e, locals)
		}
	}
}

func copyLocals(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
