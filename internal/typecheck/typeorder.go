package typecheck

import (
	"sort"

	"mar/internal/ast"
)

// typeDeclsInDependencyOrder returns the module's type declarations ordered so
// that a declaration comes after every declaration whose name it mentions.
//
// The checker resolves a type alias by SUBSTITUTING its body at each use, and
// the substitution table is filled in as the declarations are walked. Walking
// them in source order therefore made the order load-bearing in the type
// namespace exactly the way it used to be in the value namespace (ADR 0015):
//
//	type alias Outer = { inner : Inner }   -- Inner is not registered yet,
//	type alias Inner = { n : Int }         -- so it becomes an opaque TCon
//
// and `{ inner = { n = 1 } }` then fails with "cannot unify { n : number }
// with Inner" — a confusing error on a program that is fine. Unlike the value
// case this is caught at compile time, so it costs an afternoon rather than a
// production incident, but it is the same defect and the same fix.
//
// Only the ITERATION is reordered; mod.Decls is left alone, so this composes
// with orderValueDecls without either having to know about the other.
//
// Cycles keep source order rather than erroring. A cycle among custom types is
// legal and common (a tree node holding a list of itself), and it works
// because a custom type resolves to a TCon carrying only its name. A cycle
// through an alias is a genuinely infinite type, and reporting it belongs with
// the other alias diagnostics, not here.
func typeDeclsInDependencyOrder(mod *ast.Module) []ast.Decl {
	var decls []ast.Decl
	index := map[string]int{}
	for _, d := range mod.Decls {
		switch n := d.(type) {
		case *ast.TypeAliasDecl:
			index[n.Name] = len(decls)
			decls = append(decls, d)
		case *ast.CustomTypeDecl:
			index[n.Name] = len(decls)
			decls = append(decls, d)
		}
	}
	if len(decls) < 2 {
		return decls
	}

	deps := make([][]int, len(decls))
	for i, d := range decls {
		seen := map[int]bool{}
		for _, name := range typeNamesUsedBy(d) {
			if j, ok := index[name]; ok && j != i {
				seen[j] = true
			}
		}
		for j := range seen {
			deps[i] = append(deps[i], j)
		}
		sort.Ints(deps[i]) // the map above has no order; the output must
	}

	ordered := make([]ast.Decl, 0, len(decls))
	done := make([]bool, len(decls))
	var visit func(i int)
	visit = func(i int) {
		if done[i] {
			return
		}
		done[i] = true // set first, so a cycle unwinds instead of spinning
		for _, j := range deps[i] {
			visit(j)
		}
		ordered = append(ordered, decls[i])
	}
	for i := range decls {
		visit(i)
	}
	return ordered
}

// typeNamesUsedBy lists the type names a declaration's body mentions. Only
// unqualified names matter: a qualified name belongs to another module and is
// resolved through the import table, not through this module's declarations.
func typeNamesUsedBy(d ast.Decl) []string {
	var out []string
	var walk func(te ast.TypeExpr)
	walk = func(te ast.TypeExpr) {
		switch t := te.(type) {
		case *ast.TypeCon:
			if len(t.Module) == 0 {
				out = append(out, t.Name)
			}
			for _, a := range t.Args {
				walk(a)
			}
		case *ast.TypeArrow:
			walk(t.From)
			walk(t.To)
		case *ast.TypeRecord:
			for _, f := range t.Fields {
				walk(f.Type)
			}
		case *ast.TypeTuple:
			for _, m := range t.Members {
				walk(m)
			}
		}
	}
	switch n := d.(type) {
	case *ast.TypeAliasDecl:
		walk(n.Body)
	case *ast.CustomTypeDecl:
		for _, c := range n.Constructors {
			for _, a := range c.Args {
				walk(a)
			}
		}
	}
	return out
}
