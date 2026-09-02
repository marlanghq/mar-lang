package apphost

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/jsserve"
	"mar/internal/project"
	"mar/internal/runtime"
)

// WireAny is the shape that asserts nothing. Every position that reduces to it
// is a position a malformed request can pass through, so the census of where
// it still appears IS the state of the request guard — and it is worth a test
// rather than a memory, because the number only ever moves when somebody
// changes the reduction.
//
// The kinds that used to produce it, and where they went:
//
//	a type variable in a contract      refused by `mar check`
//	a function in a contract           refused by `mar check`
//	Set, Dict, Angle                   kinds of their own
//	a parameterised union              substituted at the use site
//	a recursive type's cycle closure   named, and followed against the data
func TestNoExampleHasAnUncheckedPositionInAServiceRequest(t *testing.T) {
	if testing.Short() {
		t.Skip("boots every example; skipped under -short")
	}
	dirs, err := filepath.Glob("../../examples/*")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no examples found: %v", err)
	}

	var anyFound []string
	var contracts int
	for _, dir := range dirs {
		entry := filepath.Join(dir, "Main.mar")
		if _, err := os.Stat(entry); err != nil {
			continue
		}
		name := filepath.Base(dir)
		func() {
			defer func() { _ = recover() }()
			runtime.ResetForReload()
			runtime.SetDBPath(filepath.Join(t.TempDir(), "census.db"))
			lp := &jsserve.LiveProgram{}
			rEnv, mods, _, err := project.LoadIntoEnvWithModulesAndHook(entry,
				func(env *runtime.Env, ms []*ast.Module) { Install(env, ms, 0, lp) })
			if err != nil {
				return
			}
			for _, m := range mods {
				prefix := strings.Join(m.Name, ".")
				for _, d := range m.Decls {
					vd, ok := d.(*ast.ValueDecl)
					if !ok {
						continue
					}
					v, ok := rEnv.Lookup(prefix + "." + vd.Name)
					if !ok {
						continue
					}
					svc, ok := v.(runtime.VService)
					if !ok || svc.InputShape == nil {
						continue
					}
					contracts++
					if where := findWireAny(*svc.InputShape, ""); where != "" {
						anyFound = append(anyFound,
							fmt.Sprintf("%s: %s %s at %s", name, svc.Verb, svc.Path, where))
					}
				}
			}
		}()
	}

	if contracts < 20 {
		t.Fatalf("only %d contracts were reached; the census is not exercising them", contracts)
	}
	sort.Strings(anyFound)
	if len(anyFound) > 0 {
		t.Fatalf("%d request position(s) assert nothing:\n  %s",
			len(anyFound), strings.Join(anyFound, "\n  "))
	}
	t.Logf("%d service contracts, and no position in any of their request types "+
		"reduces to a shape that asserts nothing", contracts)
}

// findWireAny names the first position that asserts nothing, or "".
func findWireAny(s ast.WireShape, path string) string {
	at := path
	if at == "" {
		at = "the request"
	}
	switch s.Kind {
	case ast.WireAny:
		return at
	case ast.WireList, ast.WireMaybe, ast.WireSet:
		if s.Elem != nil {
			return findWireAny(*s.Elem, path+"[]")
		}
	case ast.WireTuple, ast.WireDict:
		for i, m := range s.Members {
			if w := findWireAny(m, fmt.Sprintf("%s[%d]", path, i)); w != "" {
				return w
			}
		}
	case ast.WireRecord:
		for _, f := range s.Fields {
			if w := findWireAny(f.Shape, path+"."+f.Name); w != "" {
				return w
			}
		}
	case ast.WireCtor:
		for i, per := range s.Args {
			tag := ""
			if i < len(s.Tags) {
				tag = s.Tags[i]
			}
			for j, a := range per {
				if w := findWireAny(a, fmt.Sprintf("%s.%s[%d]", path, tag, j)); w != "" {
					return w
				}
			}
		}
	}
	return ""
}

// The other half. A response shape is written onto the `Service.call` site
// rather than onto the contract, because that is where the result type is
// known — so the census walks the elaborated tree instead of the registry.
//
// This is the shape the BROWSER and the PHONE check what the server sent
// against, and its stated job is version skew: an open tab, or a phone still
// on an embedded snapshot, against a server whose response type has moved. A
// position that asserts nothing there is skew that arrives silently.
//
// `JSON.decode` rides along, because it is stamped the same way and is the
// other place a value the type system did not produce enters a program.
func TestNoExampleHasAnUncheckedPositionInAServiceResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("boots every example; skipped under -short")
	}
	dirs, _ := filepath.Glob("../../examples/*")
	var anyFound []string
	byKind := map[string]int{}
	for _, dir := range dirs {
		entry := filepath.Join(dir, "Main.mar")
		if _, err := os.Stat(entry); err != nil {
			continue
		}
		name := filepath.Base(dir)
		func() {
			defer func() { _ = recover() }()
			runtime.ResetForReload()
			runtime.SetDBPath(filepath.Join(t.TempDir(), "census.db"))
			lp := &jsserve.LiveProgram{}
			_, mods, _, err := project.LoadIntoEnvWithModulesAndHook(entry,
				func(env *runtime.Env, ms []*ast.Module) { Install(env, ms, 0, lp) })
			if err != nil {
				return
			}
			for _, m := range mods {
				for _, d := range m.Decls {
					vd, ok := d.(*ast.ValueDecl)
					if !ok {
						continue
					}
					eachServiceCallShape(vd.Body, func(kind string, s ast.WireShape, line int) {
						byKind[kind]++
						if where := findWireAny(s, ""); where != "" {
							anyFound = append(anyFound, fmt.Sprintf(
								"%s: %s line %d, at %s", name, strings.Join(m.Name, "."), line, where))
						}
					})
				}
			}
		}()
	}

	// Counted per kind, so an arm that never fires is visible rather than
	// hidden inside a total. No example uses `JSON.decode` today: that arm
	// is a guard for the next one that does, and this says so out loud
	// rather than letting a zero pass for coverage.
	if byKind["Service.call"] < 10 {
		t.Fatalf("only %d Service.call sites carried a shape; the census is not exercising them",
			byKind["Service.call"])
	}
	sort.Strings(anyFound)
	if len(anyFound) > 0 {
		t.Fatalf("%d response position(s) assert nothing:\n  %s",
			len(anyFound), strings.Join(anyFound, "\n  "))
	}
	t.Logf("no position asserts nothing in: %d Service.call site(s), %d JSON.decode site(s)",
		byKind["Service.call"], byKind["JSON.decode"])
}

// wireCarrier is the set of call sites the checker stamps a shape onto: the
// response of a `Service.call`, and the result of a `JSON.decode`. They are
// the two places a value the type system did not produce enters a program.
func wireCarrier(q *ast.EQualified) bool {
	m := moduleName(q.Module)
	return (m == "Service" && q.Name == "call") || (m == "JSON" && q.Name == "decode")
}

// eachServiceCallShape visits the shape the checker stamped on every such
// call site in an expression.
func eachServiceCallShape(e ast.Expr, visit func(string, ast.WireShape, int)) {
	if e == nil {
		return
	}
	if q, ok := e.(*ast.EQualified); ok && q.Wire != nil && wireCarrier(q) {
		visit(moduleName(q.Module)+"."+q.Name, *q.Wire, q.Pos.Line)
	}
	switch n := e.(type) {
	case *ast.EApp:
		eachServiceCallShape(n.Fn, visit)
		eachServiceCallShape(n.Arg, visit)
	case *ast.EBinop:
		eachServiceCallShape(n.Left, visit)
		eachServiceCallShape(n.Right, visit)
	case *ast.ELambda:
		eachServiceCallShape(n.Body, visit)
	case *ast.EIf:
		eachServiceCallShape(n.Cond, visit)
		eachServiceCallShape(n.Then, visit)
		eachServiceCallShape(n.Else, visit)
	case *ast.ELet:
		for _, b := range n.Bindings {
			eachServiceCallShape(b.Body, visit)
		}
		eachServiceCallShape(n.Body, visit)
	case *ast.ETuple:
		for _, m := range n.Members {
			eachServiceCallShape(m, visit)
		}
	case *ast.EList:
		for _, el := range n.Elements {
			eachServiceCallShape(el, visit)
		}
	case *ast.ERecord:
		for _, f := range n.Fields {
			eachServiceCallShape(f.Value, visit)
		}
	case *ast.ERecordUpdate:
		eachServiceCallShape(n.Record, visit)
		for _, f := range n.Fields {
			eachServiceCallShape(f.Value, visit)
		}
	case *ast.EFieldAccess:
		eachServiceCallShape(n.Record, visit)
	case *ast.ECase:
		eachServiceCallShape(n.Subject, visit)
		for _, b := range n.Branches {
			eachServiceCallShape(b.Body, visit)
		}
	case *ast.ENegate:
		eachServiceCallShape(n.Inner, visit)
	}
}
