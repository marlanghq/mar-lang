package typecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"mar/internal/ast"
	"mar/internal/parser"
)

// The evaluator does not re-derive types; it reads what the checker decided
// and left on the tree (ADR 0016, ADR 0017): which integer literal is really a
// Decimal, which reference means which implementation, what shape a response
// has. Every one of those marks is written while the inferencer VISITS the
// node, so a node the inferencer never reaches carries no mark — and a missing
// mark does not fail, it answers differently. `1 + 1.50` without the Decimal
// mark is integer arithmetic on a program whose type says otherwise.
//
// `ExprTypes` records exactly the nodes the inferencer visited, so comparing
// it against the whole tree measures that reach directly. Over every example
// in the repo: 45,707 expression nodes, all visited but 75, and those 75 are
// one deliberate case.
//
// The exception is a string literal used as a PATH — `signIn : Path {}` with
// `signIn = "/sign-in"`, and the path argument of `Service.declare`. The path
// checker reads those literals straight off the tree to derive the `{name:Type}`
// parameters, and gives the declaration its `Path {...}` type without inferring
// the literal as a String. Nothing is lost: no elaboration mark applies to a
// string literal. It is listed here so the exception stays one line long
// instead of becoming a blanket "strings are exempt".
func TestTheInferencerVisitsEveryExpression(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*", "*.mar"))
	nested, _ := filepath.Glob(filepath.Join("..", "..", "examples", "*", "*", "*.mar"))
	files = append(files, nested...)
	if len(files) < 20 {
		t.Fatalf("found %d example files; the scan is broken, not the checker", len(files))
	}

	unvisited := map[string]int{}
	var where []string
	total, checked := 0, 0

	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		mod, err := parser.Parse(string(src))
		if err != nil {
			continue // a file that does not parse is not this test's subject
		}
		res, err := CheckModule(mod)
		if err != nil || res == nil || res.ExprTypes == nil {
			continue
		}
		checked++
		for _, d := range mod.Decls {
			v, ok := d.(*ast.ValueDecl)
			if !ok {
				continue
			}
			var nodes []exprSite
			collectExprSites(v.Body, len(v.Params) == 0, &nodes)
			for _, site := range nodes {
				total++
				if _, seen := res.ExprTypes[site.expr]; seen || site.pathLiteral {
					continue
				}
				kind := fmt.Sprintf("%T", site.expr)
				unvisited[kind]++
				if len(where) < 10 {
					p := site.expr.Position()
					where = append(where, fmt.Sprintf("%s:%d:%d %s", filepath.Base(f), p.Line, p.Column, kind))
				}
			}
		}
	}

	if checked < 20 {
		t.Fatalf("only %d example modules type-checked; the scan is broken", checked)
	}
	if len(unvisited) > 0 {
		kinds := make([]string, 0, len(unvisited))
		missed := 0
		for k, n := range unvisited {
			kinds = append(kinds, fmt.Sprintf("%s x%d", k, n))
			missed += n
		}
		sort.Strings(kinds)
		t.Fatalf("the inferencer never visited %d node(s) across %d modules:\n  %s\n\nfirst sites:\n  %s\n\n"+
			"A node the inferencer does not reach carries no elaboration mark, and a missing mark "+
			"is a different answer rather than a failure.",
			missed, checked, strings.Join(kinds, "\n  "), strings.Join(where, "\n  "))
	}
	t.Logf("%d expression nodes across %d modules, every one visited", total, checked)
}

type exprSite struct {
	expr        ast.Expr
	pathLiteral bool
}

// collectExprSites walks every expression under e, marking the string literals
// the path checker consumes instead of inferring: the whole body of a
// parameterless declaration (`signIn = "/sign-in"` under a `Path {}`
// annotation) and the second argument of `Service.declare`.
func collectExprSites(e ast.Expr, isDeclBody bool, out *[]exprSite) {
	if e == nil {
		return
	}
	_, isStr := e.(*ast.EString)
	*out = append(*out, exprSite{expr: e, pathLiteral: isStr && isDeclBody})

	switch n := e.(type) {
	case *ast.EApp:
		// `Service.declare METHOD "path"` reaches here as nested EApp; the
		// path literal is the argument of the outer one.
		if head, args := flattenApp(n); isQualified(head, "Service", "declare") && len(args) == 2 {
			if _, ok := args[1].(*ast.EString); ok {
				for i := range *out {
					if (*out)[i].expr == args[1] {
						(*out)[i].pathLiteral = true
					}
				}
			}
		}
		collectExprSites(n.Fn, false, out)
		collectExprSites(n.Arg, false, out)
	case *ast.EBinop:
		collectExprSites(n.Left, false, out)
		collectExprSites(n.Right, false, out)
	case *ast.ENegate:
		collectExprSites(n.Inner, false, out)
	case *ast.EIf:
		collectExprSites(n.Cond, false, out)
		collectExprSites(n.Then, false, out)
		collectExprSites(n.Else, false, out)
	case *ast.ECase:
		collectExprSites(n.Subject, false, out)
		for _, b := range n.Branches {
			collectExprSites(b.Body, false, out)
		}
	case *ast.ELet:
		for _, b := range n.Bindings {
			collectExprSites(b.Body, false, out)
		}
		collectExprSites(n.Body, false, out)
	case *ast.ELambda:
		collectExprSites(n.Body, false, out)
	case *ast.EList:
		for _, x := range n.Elements {
			collectExprSites(x, false, out)
		}
	case *ast.ETuple:
		for _, x := range n.Members {
			collectExprSites(x, false, out)
		}
	case *ast.ERecord:
		for _, f := range n.Fields {
			collectExprSites(f.Value, false, out)
		}
	case *ast.ERecordUpdate:
		collectExprSites(n.Record, false, out)
		for _, f := range n.Fields {
			collectExprSites(f.Value, false, out)
		}
	case *ast.EFieldAccess:
		collectExprSites(n.Record, false, out)
	}
}
