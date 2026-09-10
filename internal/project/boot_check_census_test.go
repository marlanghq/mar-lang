package project

import (
	"go/ast"
	"go/token"

	"mar/internal/gosrc"
	"os"
	"sort"
	"strings"
	"testing"
)

// The goal's middle clause is "an error that cannot be caught at compile time
// should be caught at boot". Answering it needs the list, and the list did not
// exist: load_path_passes_test compares the three loaders against EACH OTHER,
// which catches a pass reaching one and not another, and says nothing about
// whether a pass is at the right stage at all.
//
// The distinction is in the signature, and is not a judgement:
//
//	takes only AST (and types)   nothing has run yet, so it CAN be compile time
//	takes evaluated runtime      the values do not exist until `main` has run,
//	values                       so boot is the earliest it can happen
//
// A pass in the first group that only runs at boot is an error arriving later
// than it has to. A pass in the second group is where it belongs, and this
// records WHY for each one rather than leaving the reader to work it out.
func TestEveryCheckRunsAtTheEarliestStageItCan(t *testing.T) {
	// Why each boot pass cannot be a compile-time pass. All of them come down
	// to the same thing said four ways: the compiler does not know what an app
	// MOUNTS until `main` produces it.
	bootReasons := map[string]string{
		"CheckRouteTable": "the pages and services are whatever `main` returned; " +
			"two pages collide on a path only once both are in the same list",
		"CheckServiceCalls": "a call is only unmounted relative to the mounted set, " +
			"and the mounted set is a value `main` builds",
		"CheckWiring": "the same: a service protected by an Auth nobody configured " +
			"is a fact about the assembled app",
		"CheckLandingPage":     "whether anything answers `/` depends on the page list",
		"CheckProgram":         "the App.* record itself, which only exists once evaluated",
		"CheckFrontendProgram": "the frontend half of the same record",
		"CheckBackendProgram":  "the backend half of the same record",
	}

	fset := token.NewFileSet()
	pkgs := map[string]string{
		"apphost":   "../apphost",
		"typecheck": "../typecheck",
	}
	type pass struct {
		name    string
		astOnly bool
	}
	var found []pass
	for _, dir := range pkgs {
		files, err := gosrc.ParseDir(fset, dir, true)
		if err != nil {
			t.Fatalf("parse %s: %v", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f.Name.Name, "_test") {
				continue
			}
			{
				for _, d := range f.Decls {
					fn, ok := d.(*ast.FuncDecl)
					if !ok || fn.Recv != nil || fn.Type.Params == nil {
						continue
					}
					n := fn.Name.Name
					if !strings.HasPrefix(n, "Check") && !(strings.HasPrefix(n, "Run") && strings.HasSuffix(n, "Check")) &&
						n != "RunShapeLint" {
						continue
					}
					// CheckModule / CheckModuleWith are the type checker itself.
					if n == "CheckModule" || n == "CheckModuleWith" {
						continue
					}
					astOnly := true
					for _, p := range fn.Type.Params.List {
						if strings.Contains(typeString(p.Type), "runtime.") {
							astOnly = false
						}
					}
					found = append(found, pass{n, astOnly})
				}
			}
		}
	}
	if len(found) < 6 {
		t.Fatalf("only %d passes found; the scan is wrong, not the tree", len(found))
	}

	var problems []string
	seen := map[string]bool{}
	for _, p := range found {
		seen[p.name] = true
		if p.astOnly {
			if _, listed := bootReasons[p.name]; listed {
				problems = append(problems, p.name+" is listed as a boot pass and takes only AST.\n"+
					"    Nothing has run yet when it could execute, so it belongs at compile time.")
			}
			continue
		}
		if _, listed := bootReasons[p.name]; !listed {
			problems = append(problems, p.name+" takes evaluated runtime values, so it cannot run before `main`.\n"+
				"    Say why here, or move it earlier if the values are not really needed.")
		}
	}
	for name := range bootReasons {
		if !seen[name] {
			problems = append(problems, "the census lists "+name+", which no longer exists; drop the row")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}

	// And the half that makes the first group mean anything: an AST-only pass
	// has to be REACHED by the command that runs at compile time. Measured
	// before this existed: `mar check` was weaker than `mar dev`, and the
	// deployed loader ran the route table and none of these.
	raw, err := os.ReadFile("project.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, p := range found {
		if !p.astOnly {
			continue
		}
		if !strings.Contains(src, "typecheck."+p.name+"(") && !strings.Contains(src, p.name+"(") {
			t.Errorf("%s takes only AST but project.Load does not call it, so `mar check` "+
				"never runs it and the error waits for a boot that may not happen locally", p.name)
		}
	}

	if !t.Failed() {
		compile := 0
		for _, p := range found {
			if p.astOnly {
				compile++
			}
		}
		t.Logf("%d passes: %d take only AST and are reached by `mar check`; %d need `main` "+
			"evaluated and say why", len(found), compile, len(found)-compile)
	}
}

func typeString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.SelectorExpr:
		return typeString(t.X) + "." + t.Sel.Name
	case *ast.StarExpr:
		return typeString(t.X)
	case *ast.ArrayType:
		return typeString(t.Elt)
	case *ast.Ident:
		return t.Name
	case *ast.MapType:
		return typeString(t.Key) + typeString(t.Value)
	}
	return ""
}
