package project

import (
	"go/ast"
	"go/token"

	"mar/internal/gosrc"
	"sort"
	"strings"
	"testing"
)

// Three functions load a project, and a project-wide pass added to one of them
// used to reach only that one. That has now happened seven times in this work,
// most recently in the loader the DEPLOYED binary uses: it ran the route table
// (the App.* builtins carry that) and not the GET read-only pass, the side
// check, or the `main` signature.
//
// So the passes each loader runs are compared here, and a difference has to be
// written down rather than merely existing.
func TestEveryLoadPathRunsTheSamePasses(t *testing.T) {
	// The one difference that is a decision, not an omission.
	exempt := map[string]map[string]string{
		"LoadIntoEnvForRuntime": {
			"RunShapeLint": "needs the expression types, which checkOrdered does not carry back",
		},
	}

	loaders := map[string]string{
		"Load": "project.go",
		// The wrapper delegates; the passes live in the overrides form.
		"LoadForServeTypedWithOverrides": "frontend.go",
		"LoadIntoEnvForRuntime":          "runtime_load.go",
	}

	fset := token.NewFileSet()
	files, err := gosrc.ParseDir(fset, ".", true)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// What each loader calls, by walking its body for `typecheck.X(...)`.
	calls := map[string]map[string]bool{}
	{
		for _, file := range files {
			for _, d := range file.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Recv != nil {
					continue
				}
				if _, want := loaders[fn.Name.Name]; !want {
					continue
				}
				found := map[string]bool{}
				ast.Inspect(fn, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					pkgIdent, ok := sel.X.(*ast.Ident)
					if !ok || pkgIdent.Name != "typecheck" {
						return true
					}
					name := sel.Sel.Name
					if strings.HasPrefix(name, "Run") || name == "MainSignatureProblem" {
						found[name] = true
					}
					return true
				})
				calls[fn.Name.Name] = found
			}
		}
	}

	for name := range loaders {
		if len(calls[name]) == 0 {
			t.Fatalf("found no passes in %s; the scan is broken, not the loader", name)
		}
	}

	// The union is what a loader is expected to run.
	all := map[string]bool{}
	for _, found := range calls {
		for pass := range found {
			all[pass] = true
		}
	}
	passes := make([]string, 0, len(all))
	for pass := range all {
		passes = append(passes, pass)
	}
	sort.Strings(passes)

	for loader, file := range loaders {
		for _, pass := range passes {
			if calls[loader][pass] {
				continue
			}
			if why, ok := exempt[loader][pass]; ok {
				t.Logf("%s does not run %s — %s", loader, pass, why)
				continue
			}
			t.Errorf("%s (%s) does not run %s, and the other loaders do.\n"+
				"  A program refused by one entry point and accepted by another is the bug "+
				"this test exists for. Add the pass, or record why it does not belong here.",
				loader, file, pass)
		}
	}

	// An exemption for a pass nobody runs any more is stale.
	for loader, m := range exempt {
		for pass := range m {
			if !all[pass] {
				t.Errorf("%s is exempted from %s, and no loader runs it", loader, pass)
			}
		}
	}
}
