// Package jsserve hosts the browser-side runtime: an embedded JS interpreter
// for Mar plus an HTTP handler that serves the page, the runtime, and the
// program AST as JSON.
package jsserve

import (
	"fmt"

	"mar/internal/ast"
)

// SerializeModule converts a parsed module to a JSON-friendly map suitable
// for the browser runtime.
func SerializeModule(m *ast.Module) map[string]any {
	decls := make([]any, 0, len(m.Decls))
	for _, d := range m.Decls {
		if s := serializeDecl(d); s != nil {
			decls = append(decls, s)
		}
	}
	// Imports' Exposing lists are needed at JS-runtime load time so
	// `import View exposing (column)` actually binds `column` in the
	// runtime env (not just at the type level).
	imports := make([]any, 0, len(m.Imports))
	for _, imp := range m.Imports {
		items := make([]any, 0, len(imp.Exposing.Items))
		for _, it := range imp.Exposing.Items {
			items = append(items, map[string]any{
				"name": it.Name,
				"open": it.Open,
			})
		}
		imports = append(imports, map[string]any{
			"module":   []string(imp.Module),
			"exposing": items,
			"all":      imp.Exposing.All,
		})
	}
	return map[string]any{
		"name":    m.Name,
		"decls":   decls,
		"imports": imports,
	}
}

func serializeDecl(d ast.Decl) any {
	switch n := d.(type) {
	case *ast.ValueDecl:
		return map[string]any{
			"kind":   "ValueDecl",
			"name":   n.Name,
			"params": serializePatterns(n.Params),
			"body":   serializeExpr(n.Body),
		}
	case *ast.CustomTypeDecl:
		ctors := make([]any, len(n.Constructors))
		for i, c := range n.Constructors {
			ctors[i] = map[string]any{
				"name":     c.Name,
				"argCount": len(c.Args),
			}
		}
		return map[string]any{
			"kind":         "CustomTypeDecl",
			"name":         n.Name,
			"constructors": ctors,
		}
	case *ast.TypeAliasDecl, *ast.AnnotationDecl, *ast.PortDecl:
		// Browser runtime doesn't need types; skip.
		return nil
	}
	return nil
}

func serializeExpr(e ast.Expr) any {
	switch n := e.(type) {
	case *ast.EInt:
		return map[string]any{"kind": "EInt", "value": n.Value}
	case *ast.EFloat:
		return map[string]any{"kind": "EFloat", "value": n.Value}
	case *ast.EString:
		return map[string]any{"kind": "EString", "value": n.Value}
	case *ast.EUnit:
		return map[string]any{"kind": "EUnit"}
	case *ast.EVar:
		return map[string]any{"kind": "EVar", "name": n.Name}
	case *ast.ECtor:
		return map[string]any{"kind": "ECtor", "module": n.Module, "name": n.Name}
	case *ast.EQualified:
		return map[string]any{"kind": "EQualified", "module": n.Module, "name": n.Name}
	case *ast.ENegate:
		return map[string]any{"kind": "ENegate", "inner": serializeExpr(n.Inner)}
	case *ast.EApp:
		return map[string]any{
			"kind": "EApp",
			"fn":   serializeExpr(n.Fn),
			"arg":  serializeExpr(n.Arg),
		}
	case *ast.EBinop:
		return map[string]any{
			"kind":  "EBinop",
			"op":    n.Op,
			"left":  serializeExpr(n.Left),
			"right": serializeExpr(n.Right),
		}
	case *ast.ELambda:
		return map[string]any{
			"kind":   "ELambda",
			"params": serializePatterns(n.Params),
			"body":   serializeExpr(n.Body),
		}
	case *ast.EIf:
		return map[string]any{
			"kind": "EIf",
			"cond": serializeExpr(n.Cond),
			"then": serializeExpr(n.Then),
			"else": serializeExpr(n.Else),
		}
	case *ast.ELet:
		bs := make([]any, len(n.Bindings))
		for i, b := range n.Bindings {
			bs[i] = map[string]any{
				"pattern": serializePattern(b.Pattern),
				"body":    serializeExpr(b.Body),
				"isBind":  b.IsBind,
			}
		}
		return map[string]any{
			"kind":     "ELet",
			"bindings": bs,
			"body":     serializeExpr(n.Body),
		}
	case *ast.ETuple:
		mems := make([]any, len(n.Members))
		for i, m := range n.Members {
			mems[i] = serializeExpr(m)
		}
		return map[string]any{"kind": "ETuple", "members": mems}
	case *ast.EList:
		els := make([]any, len(n.Elements))
		for i, x := range n.Elements {
			els[i] = serializeExpr(x)
		}
		return map[string]any{"kind": "EList", "elements": els}
	case *ast.ERecord:
		fs := make([]any, len(n.Fields))
		for i, f := range n.Fields {
			fs[i] = map[string]any{"name": f.Name, "value": serializeExpr(f.Value)}
		}
		return map[string]any{"kind": "ERecord", "fields": fs}
	case *ast.ERecordUpdate:
		fs := make([]any, len(n.Fields))
		for i, f := range n.Fields {
			fs[i] = map[string]any{"name": f.Name, "value": serializeExpr(f.Value)}
		}
		return map[string]any{
			"kind":   "ERecordUpdate",
			"record": serializeExpr(n.Record),
			"fields": fs,
		}
	case *ast.EFieldAccess:
		return map[string]any{
			"kind":   "EFieldAccess",
			"record": serializeExpr(n.Record),
			"field":  n.Field,
		}
	case *ast.EFieldAccessor:
		return map[string]any{"kind": "EFieldAccessor", "field": n.Field}
	case *ast.ECase:
		brs := make([]any, len(n.Branches))
		for i, b := range n.Branches {
			brs[i] = map[string]any{
				"pattern": serializePattern(b.Pattern),
				"body":    serializeExpr(b.Body),
			}
		}
		return map[string]any{
			"kind":     "ECase",
			"subject":  serializeExpr(n.Subject),
			"branches": brs,
		}
	}
	return map[string]any{"kind": "Unknown", "msg": fmt.Sprintf("%T", e)}
}

func serializePattern(p ast.Pattern) any {
	switch n := p.(type) {
	case *ast.PVar:
		return map[string]any{"kind": "PVar", "name": n.Name}
	case *ast.PWildcard:
		return map[string]any{"kind": "PWildcard"}
	case *ast.PInt:
		return map[string]any{"kind": "PInt", "value": n.Value}
	case *ast.PString:
		return map[string]any{"kind": "PString", "value": n.Value}
	case *ast.PUnit:
		return map[string]any{"kind": "PUnit"}
	case *ast.PCtor:
		args := make([]any, len(n.Args))
		for i, a := range n.Args {
			args[i] = serializePattern(a)
		}
		return map[string]any{
			"kind": "PCtor", "module": n.Module, "name": n.Name, "args": args,
		}
	case *ast.PTuple:
		mems := make([]any, len(n.Members))
		for i, m := range n.Members {
			mems[i] = serializePattern(m)
		}
		return map[string]any{"kind": "PTuple", "members": mems}
	case *ast.PList:
		els := make([]any, len(n.Elements))
		for i, e := range n.Elements {
			els[i] = serializePattern(e)
		}
		return map[string]any{"kind": "PList", "elements": els}
	case *ast.PCons:
		return map[string]any{
			"kind": "PCons",
			"head": serializePattern(n.Head),
			"tail": serializePattern(n.Tail),
		}
	}
	return map[string]any{"kind": "Unknown"}
}

func serializePatterns(ps []ast.Pattern) []any {
	out := make([]any, len(ps))
	for i, p := range ps {
		out[i] = serializePattern(p)
	}
	return out
}
