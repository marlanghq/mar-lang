// Package types — termination.go implements a conservative heuristic check
// for obvious non-terminating recursion.
//
// What's rejected (compile-time error):
//   - Direct same-argument recursion: (define (loop n) (loop n))
//     The recursive call passes the parameters verbatim, so no progress.
//
// What's accepted (potentially unsound, but heuristic by design):
//   - Recursion with structurally-different args (e.g., (loop (- n 1)))
//   - Recursion via builtins (map/fold over data)
//   - Mutual recursion (could be implemented later via SCC analysis)
//
// The fuel/budget runtime guard catches anything this heuristic misses, so
// the worst case is "trapped at runtime as 'execution budget exceeded'"
// rather than process crash.
package types

import (
	"fmt"

	"mar/internal/expr"
	"mar/internal/model"
)

// CheckTermination scans every user function for the trivial diverging
// pattern: a recursive call with arguments identical to the parameters.
// Returns the first offending function, or nil.
func CheckTermination(app *model.App) error {
	for _, fn := range app.Functions {
		parsed, err := parseFunctionBody(app, &fn)
		if err != nil {
			// Parse errors are surfaced by other passes; skip here.
			continue
		}
		if reason := findDivergingRecursiveCall(parsed, fn.Name, fn.Parameters); reason != "" {
			loc := ""
			if fn.LineNo > 0 {
				loc = fmt.Sprintf(" (line %d)", fn.LineNo)
			}
			return fmt.Errorf("function %s%s does not appear to terminate: %s\n  hint: each recursive call should pass at least one argument that gets smaller (a sub-list, a smaller number)", fn.Name, loc, reason)
		}
	}
	return nil
}

func parseFunctionBody(app *model.App, fn *model.Function) (expr.Expr, error) {
	allowedVars := map[string]struct{}{}
	for _, p := range fn.Parameters {
		allowedVars[p] = struct{}{}
	}
	allowedVars = expr.AllowedVariablesWithBuiltins(allowedVars)
	functionArities := map[string]int{}
	for _, f := range app.Functions {
		functionArities[f.Name] = len(f.Parameters)
	}
	recordFields := map[string][]string{}
	for _, r := range app.Records {
		fs := make([]string, 0, len(r.Fields))
		for _, f := range r.Fields {
			fs = append(fs, f.Name)
		}
		recordFields[r.Name] = fs
	}
	variantArities := map[string]int{}
	for _, typ := range app.Types {
		for _, v := range typ.Variants {
			variantArities[v.Name] = len(v.Fields)
		}
	}
	return expr.Parse(fn.Expression, expr.ParserOptions{
		AllowedVariables: allowedVars,
		AllowedFunctions: functionArities,
		AllowedRecords:   recordFields,
		AllowedVariants:  variantArities,
	})
}

// findDivergingRecursiveCall walks e looking for a recursive call to fnName
// whose arguments match params verbatim (no rearrangement, no operation).
// Returns a human-readable reason on the first offender; empty string if OK.
func findDivergingRecursiveCall(e expr.Expr, fnName string, params []string) string {
	switch n := e.(type) {

	case expr.Call:
		if n.Name == fnName && argsExactlyMatchParams(n.Args, params) {
			return fmt.Sprintf("recursive call (%s %s) passes the parameters unchanged", fnName, joinSpace(params))
		}
		for _, a := range n.Args {
			if r := findDivergingRecursiveCall(a, fnName, params); r != "" {
				return r
			}
		}

	case expr.If:
		if r := findDivergingRecursiveCall(n.Condition, fnName, params); r != "" {
			return r
		}
		// Diverging recursion ONLY counts if both branches diverge — a
		// recursive call inside a conditional is fine if one branch is a
		// base case. Be conservative: only flag if both then and else
		// diverge to fnName(params verbatim).
		thenR := findDivergingRecursiveCall(n.Then, fnName, params)
		elseR := findDivergingRecursiveCall(n.Else, fnName, params)
		if thenR != "" && elseR != "" {
			return thenR
		}

	case expr.Cond:
		// All clause bodies (including else) must diverge to flag.
		divergeAll := len(n.Clauses) > 0
		var reason string
		for _, c := range n.Clauses {
			if !c.Else {
				if r := findDivergingRecursiveCall(c.Test, fnName, params); r != "" {
					return r
				}
			}
			r := findDivergingRecursiveCall(c.Body, fnName, params)
			if r == "" {
				divergeAll = false
			} else if reason == "" {
				reason = r
			}
		}
		if divergeAll && reason != "" {
			return reason
		}

	case expr.Match:
		if r := findDivergingRecursiveCall(n.Subject, fnName, params); r != "" {
			return r
		}
		divergeAll := len(n.Clauses) > 0
		var reason string
		for _, c := range n.Clauses {
			r := findDivergingRecursiveCall(c.Body, fnName, params)
			if r == "" {
				divergeAll = false
			} else if reason == "" {
				reason = r
			}
		}
		if divergeAll && reason != "" {
			return reason
		}

	case expr.Let:
		for _, b := range n.Bindings {
			if r := findDivergingRecursiveCall(b.Value, fnName, params); r != "" {
				return r
			}
		}
		return findDivergingRecursiveCall(n.Body, fnName, params)

	case expr.Begin:
		for _, child := range n.Expressions {
			if r := findDivergingRecursiveCall(child, fnName, params); r != "" {
				return r
			}
		}

	case expr.Lambda:
		// Inside a lambda, recursion to enclosing fn would be free-variable
		// capture — could still loop. Walk the body too.
		return findDivergingRecursiveCall(n.Body, fnName, params)

	case expr.Binary:
		if r := findDivergingRecursiveCall(n.Left, fnName, params); r != "" {
			return r
		}
		return findDivergingRecursiveCall(n.Right, fnName, params)

	case expr.Unary:
		return findDivergingRecursiveCall(n.Right, fnName, params)

	case expr.Get:
		return findDivergingRecursiveCall(n.Target, fnName, params)

	case expr.Assoc:
		if r := findDivergingRecursiveCall(n.Target, fnName, params); r != "" {
			return r
		}
		for _, u := range n.Updates {
			if r := findDivergingRecursiveCall(u.Value, fnName, params); r != "" {
				return r
			}
		}

	case expr.RecordConstructor:
		for _, a := range n.Args {
			if r := findDivergingRecursiveCall(a, fnName, params); r != "" {
				return r
			}
		}

	case expr.TaggedConstructor:
		for _, a := range n.Args {
			if r := findDivergingRecursiveCall(a, fnName, params); r != "" {
				return r
			}
		}

	case expr.ListLiteral:
		for _, item := range n.Items {
			if r := findDivergingRecursiveCall(item, fnName, params); r != "" {
				return r
			}
		}

	case expr.RegexMatch:
		return findDivergingRecursiveCall(n.Text, fnName, params)
	}
	return ""
}

// argsExactlyMatchParams reports whether the call args are positionally
// identical to the parameter names — i.e., (f x y) calling (f x y) verbatim.
func argsExactlyMatchParams(args []expr.Expr, params []string) bool {
	if len(args) != len(params) {
		return false
	}
	for i, a := range args {
		v, ok := a.(expr.Variable)
		if !ok {
			return false
		}
		if v.Name != params[i] {
			return false
		}
	}
	return true
}

func joinSpace(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out
}
