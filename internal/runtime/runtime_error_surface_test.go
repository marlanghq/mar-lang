package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// The evaluator has two ways to report a failure, and which one it picks is a
// claim about whose bug it is. `internalErrorf` says the checker already ruled
// this state out, so reaching it is a bug in Mar; `errorf` says a checked
// program did something the language allows and the runtime could not finish.
//
// Only the second kind is a runtime error of the language, and there are
// exactly two of them:
//
//   - the recursion budget, which is a budget and cannot be anything else;
//   - `no case branch matched`, which every `case` a program can WRITE is now
//     total against (ADR 0022), but which the runtimes still reach by feeding
//     a case a value its types never sanctioned: an async result from a page
//     the user already navigated away from. Both client runtimes match on this
//     message to drop the stale message instead of crashing the app, so it
//     must stay a plain runtime error and keep its wording.
//
// Everything else in the evaluator — an unbound name, an operand of `&&` that
// is not a Bool, a field missing from a record, an AST node with no arm — is
// something `mar check` rules out, and was reported as though the reader had
// written it wrong. Each was measured against a real program first (they are
// refused at parse or at check), then relabelled.
//
// This test reads the source rather than the behaviour because unreachability
// cannot be tested by example: what can be tested is that nobody adds a third
// runtime error without saying why here.
func TestTheEvaluatorHasTwoRuntimeErrors(t *testing.T) {
	sanctioned := map[string]string{
		"too much recursion": "a budget: the call stack is finite and the program asked for more",
		"no case branch matched": "a value the types never sanctioned, reaching a case from a torn-down " +
			"page; the client runtimes match on this text to drop it",
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "eval.go", nil, 0)
	if err != nil {
		t.Fatalf("parse eval.go: %v", err)
	}

	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "errorf" || len(call.Args) < 2 {
			return true
		}
		// A message built by concatenation (`"a" + "b"`) is a `+` tree whose
		// leftmost leaf is the part that identifies it.
		arg := call.Args[1]
		for {
			bin, ok := arg.(*ast.BinaryExpr)
			if !ok || bin.Op != token.ADD {
				break
			}
			arg = bin.X
		}
		lit, ok := arg.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			found = append(found, "a non-literal message at "+fset.Position(call.Pos()).String())
			return true
		}
		found = append(found, strings.Trim(lit.Value, `"`))
		return true
	})

	var unexpected []string
	seen := map[string]bool{}
	for _, msg := range found {
		matched := ""
		for prefix := range sanctioned {
			if strings.HasPrefix(msg, prefix) {
				matched = prefix
				break
			}
		}
		if matched == "" {
			unexpected = append(unexpected, msg)
			continue
		}
		seen[matched] = true
	}

	if len(unexpected) > 0 {
		sort.Strings(unexpected)
		t.Errorf("the evaluator reports %d failure(s) as the reader's fault that this test does not sanction:\n  %s\n\n"+
			"A checked program can only fail on a budget or on a value its types never sanctioned. "+
			"If this really is one of those, add it above with the reason; otherwise use internalErrorf, "+
			"which says the checker let something through.",
			len(unexpected), strings.Join(unexpected, "\n  "))
	}
	for msg, why := range sanctioned {
		if !seen[msg] {
			t.Errorf("%q is sanctioned as a runtime error (%s) but the evaluator no longer reports it; "+
				"if it moved, move this entry with it", msg, why)
		}
	}
}
