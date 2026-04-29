package typecheck

import (
	"mar/internal/ast"
)

// declInfo records what we need about each top-level value: whether
// it's a function (parametrized) and where it's defined (for error
// reporting).
type declInfo struct {
	isFunction bool
	pos        ast.Pos
}

// checkValueCycles detects illegal dependency cycles between top-level
// value declarations. Functions can recurse freely (their bodies are
// closures, evaluated lazily on call); a non-function value can't.
//
//	a = a + 1                  -- error: self-cycle
//	a = b; b = a + 1            -- error: a -> b -> a
//	a = f 5; f x = a + x        -- error: cycle goes through f, but a
//	                                       can't be evaluated without
//	                                       calling f, which reads a's
//	                                       placeholder
//	even n = ... odd ...        -- ok: only functions in the cycle
//	odd  n = ... even ...
//
// Returns the first cycle found as an InferError pointing at the
// declaration position, or nil if no illegal cycle exists.
func checkValueCycles(mod *ast.Module) error {
	decls := map[string]declInfo{}
	deps := map[string][]string{} // name -> top-level names referenced
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok {
			continue
		}
		decls[v.Name] = declInfo{isFunction: len(v.Params) > 0, pos: v.Pos}
	}
	// Walk each value's body collecting top-level refs. Local variables
	// (let / lambda params / pattern binds) shadow top-level names —
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
		collectTopLevelRefs(v.Body, decls, locals, seen)
		out := make([]string, 0, len(seen))
		for n := range seen {
			out = append(out, n)
		}
		deps[v.Name] = out
	}

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
// Lazy / eager doesn't matter here — see checkValueCycles for why.
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
	// EInt, EFloat, EString, EUnit, EFieldAccessor, EQualified, ECtor:
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
