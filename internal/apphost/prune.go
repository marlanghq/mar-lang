package apphost

import (
	"mar/internal/ast"
	"mar/internal/typecheck"
)

// The client gets the declarations its pages actually use, and not the module
// they happen to live next to.
//
// Reachability used to be per-module: if a page imported a module, the whole
// module shipped. That is how a schema written the way the examples write it —
//
//	module Backend.Notes exposing (Note, notes)
//	notes = Entity.define { name = "notes", columns = { ... } }
//
// — ended up in the browser in full, table name and columns and constraints,
// because the page imported that module for the `Note` TYPE ALIAS. The alias
// erases; the `notes` value shipped anyway, and `Entity.define` then had to
// evaluate client-side, which is the only reason the JS runtime carries stubs
// for `Entity.*` at all. Forgetting one of those stubs is what made
// `Entity.enum` typecheck and then die in the browser with "unbound name".
//
// The same walk removes service implementations. A page references a service
// CONTRACT (`Service.declare`, which carries the verb and path it fetches);
// the implementation, and every `Repo` call inside it, is reachable only from
// the backend's service list. Pruning to what pages use leaves the handlers on
// the server where they belong.
//
// Over-approximating is safe here and under-approximating is not: a dropped
// declaration is an "unbound name" at runtime. So a bare name that could
// resolve to more than one module keeps all the candidates.

// pruneToPageReachable returns mods with each module's value declarations
// narrowed to those reachable from roots. Type declarations stay: they cost
// nothing at runtime and constructors are reachable through patterns, which
// this walk does not track.
func pruneToPageReachable(mods []*ast.Module, roots []ast.Expr) []*ast.Module {
	idx := newDeclIndex(mods)
	for _, r := range roots {
		if q, ok := r.(*ast.EQualified); ok {
			idx.reach(joinModuleName(q.Module), q.Name)
		}
	}

	out := make([]*ast.Module, 0, len(mods))
	for _, m := range mods {
		name := joinModuleName(m.Name)
		kept := make([]ast.Decl, 0, len(m.Decls))
		for _, d := range m.Decls {
			vd, isValue := d.(*ast.ValueDecl)
			if !isValue || idx.reached[declKey{name, vd.Name}] {
				kept = append(kept, d)
			}
		}
		// A module reduced to nothing but type declarations still has to be
		// present: its constructors are registered from those declarations.
		pruned := *m
		pruned.Decls = kept
		out = append(out, &pruned)
	}
	return out
}

type declKey struct {
	module string
	name   string
}

type declIndex struct {
	byKey   map[declKey]*ast.ValueDecl
	inMod   map[string]*ast.Module
	reached map[declKey]bool
}

func newDeclIndex(mods []*ast.Module) *declIndex {
	idx := &declIndex{
		byKey:   map[declKey]*ast.ValueDecl{},
		inMod:   map[string]*ast.Module{},
		reached: map[declKey]bool{},
	}
	for _, m := range mods {
		name := joinModuleName(m.Name)
		idx.inMod[name] = m
		for _, d := range m.Decls {
			if vd, ok := d.(*ast.ValueDecl); ok {
				idx.byKey[declKey{name, vd.Name}] = vd
			}
		}
	}
	return idx
}

// reach marks one declaration and everything it refers to.
func (idx *declIndex) reach(module, name string) {
	key := declKey{module, name}
	if idx.reached[key] {
		return
	}
	vd, ok := idx.byKey[key]
	if !ok {
		return // builtin, stdlib, or a name from a module we were not given
	}
	idx.reached[key] = true
	idx.walk(module, vd.Body)
}

// candidates resolves a bare name as written inside module `from`: the
// module's own declaration, plus any import that could have exposed it. All of
// them are marked, because picking wrong would drop a declaration the client
// needs and turn a build into a runtime "unbound name".
func (idx *declIndex) candidates(from, name string) []string {
	var out []string
	if _, ok := idx.byKey[declKey{from, name}]; ok {
		out = append(out, from)
	}
	mod, ok := idx.inMod[from]
	if !ok {
		return out
	}
	for _, imp := range mod.Imports {
		impName := joinModuleName(imp.Module)
		if _, exists := idx.byKey[declKey{impName, name}]; !exists {
			continue
		}
		if imp.Exposing.All {
			out = append(out, impName)
			continue
		}
		for _, item := range imp.Exposing.Items {
			if item.Name == name {
				out = append(out, impName)
				break
			}
		}
	}
	return out
}

func (idx *declIndex) walk(from string, e ast.Expr) {
	switch x := e.(type) {
	case nil:
		return
	case *ast.EVar:
		for _, m := range idx.candidates(from, x.Name) {
			idx.reach(m, x.Name)
		}
	case *ast.EQualified:
		// An alias (`import Backend.Notes as N`) resolves to a module the
		// index may not know under that name; candidates() covers the bare
		// case, and an unknown module here simply finds nothing.
		idx.reach(joinModuleName(x.Module), x.Name)
		idx.reach(idx.resolveAlias(from, x.Module), x.Name)
	case *ast.EApp:
		idx.walk(from, x.Fn)
		idx.walk(from, x.Arg)
	case *ast.EBinop:
		idx.walk(from, x.Left)
		idx.walk(from, x.Right)
	case *ast.ELambda:
		idx.walk(from, x.Body)
	case *ast.EIf:
		idx.walk(from, x.Cond)
		idx.walk(from, x.Then)
		idx.walk(from, x.Else)
	case *ast.ECase:
		idx.walk(from, x.Subject)
		for _, b := range x.Branches {
			idx.walk(from, b.Body)
		}
	case *ast.ELet:
		for _, b := range x.Bindings {
			idx.walk(from, b.Body)
		}
		idx.walk(from, x.Body)
	case *ast.ETuple:
		for _, m := range x.Members {
			idx.walk(from, m)
		}
	case *ast.EList:
		for _, el := range x.Elements {
			idx.walk(from, el)
		}
	case *ast.ERecord:
		for _, f := range x.Fields {
			idx.walk(from, f.Value)
		}
	case *ast.ERecordUpdate:
		idx.walk(from, x.Record)
		for _, f := range x.Fields {
			idx.walk(from, f.Value)
		}
	case *ast.EFieldAccess:
		idx.walk(from, x.Record)
	case *ast.ENegate:
		idx.walk(from, x.Inner)
	}
	// Literals, EUnit, ECtor and EFieldAccessor reference no value declaration.
}

// resolveAlias maps `import Backend.Notes as N` so that `N.notes` finds the
// module it was aliased to. Returns the path unchanged when it is not an alias.
func (idx *declIndex) resolveAlias(from string, path ast.ModuleName) string {
	name := joinModuleName(path)
	mod, ok := idx.inMod[from]
	if !ok || len(path) != 1 {
		return name
	}
	for _, imp := range mod.Imports {
		if imp.Alias == path[0] {
			return joinModuleName(imp.Module)
		}
	}
	return name
}

// backendOnlyLeak reports a declaration that survived pruning and still calls
// a builtin that exists only on the server.
//
// This is the backstop that makes the client stubs redundant rather than
// load-bearing. Before pruning, `Entity.*` and `Repo.*` reached the browser
// routinely and the JS runtime defused them one name at a time; the name
// nobody remembered — `Entity.enum` — was a crash in production. After
// pruning, a server-only builtin in client-reachable code means the author
// actually wrote one there, which is a mistake worth a compile error naming
// the file rather than a stub that quietly does nothing.
type backendOnlyLeak struct {
	Module  string
	Decl    string
	Builtin string
	Pos     ast.Pos
}

func findBackendOnlyLeaks(mods []*ast.Module) []backendOnlyLeak {
	var out []backendOnlyLeak
	for _, m := range mods {
		name := joinModuleName(m.Name)
		for _, d := range m.Decls {
			vd, ok := d.(*ast.ValueDecl)
			if !ok {
				continue
			}
			out = append(out, scanBackendOnly(name, vd.Name, vd.Body)...)
		}
	}
	return out
}

func scanBackendOnly(module, decl string, e ast.Expr) []backendOnlyLeak {
	var found []backendOnlyLeak
	var walk func(ast.Expr)
	walk = func(e ast.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *ast.EQualified:
			qualified := joinModuleName(x.Module) + "." + x.Name
			if typecheck.IsBackendOnlyBuiltin(qualified) {
				found = append(found, backendOnlyLeak{module, decl, qualified, x.Pos})
			}
		case *ast.EApp:
			walk(x.Fn)
			walk(x.Arg)
		case *ast.EBinop:
			walk(x.Left)
			walk(x.Right)
		case *ast.ELambda:
			walk(x.Body)
		case *ast.EIf:
			walk(x.Cond)
			walk(x.Then)
			walk(x.Else)
		case *ast.ECase:
			walk(x.Subject)
			for _, b := range x.Branches {
				walk(b.Body)
			}
		case *ast.ELet:
			for _, b := range x.Bindings {
				walk(b.Body)
			}
			walk(x.Body)
		case *ast.ETuple:
			for _, m := range x.Members {
				walk(m)
			}
		case *ast.EList:
			for _, el := range x.Elements {
				walk(el)
			}
		case *ast.ERecord:
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.ERecordUpdate:
			walk(x.Record)
			for _, f := range x.Fields {
				walk(f.Value)
			}
		case *ast.EFieldAccess:
			walk(x.Record)
		case *ast.ENegate:
			walk(x.Inner)
		}
	}
	walk(e)
	return found
}
