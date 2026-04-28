package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"mar/internal/ast"
	"mar/internal/parser"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// runRepl starts an interactive REPL.
//
// Each line is treated as either:
//   - A `let name = expr` style binding (registered into the session)
//   - An expression to evaluate and print
//
// Lines ending with backslash continue on the next line. ":quit" exits.
func runRepl() int {
	fmt.Println("mar repl — type :quit to exit, :type EXPR to inspect type")
	scanner := bufio.NewScanner(os.Stdin)

	// Persistent envs that grow across lines.
	tEnv := typecheck.BaseEnv()
	rEnv := runtime.BaseEnv()
	subst := typecheck.NewSubst()
	bindingCount := 0

	prompt := "> "
	for {
		fmt.Print(prompt)
		if !scanner.Scan() {
			fmt.Println()
			return 0
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if line == ":quit" || line == ":q" {
			return 0
		}
		if strings.HasPrefix(line, ":type ") || strings.HasPrefix(line, ":t ") {
			expr := strings.TrimSpace(line[strings.IndexByte(line, ' ')+1:])
			t, err := replInferExpr(expr, tEnv, subst)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				continue
			}
			fmt.Printf(": %s\n", typecheck.Pretty(t))
			continue
		}

		// `name = expr` form?
		if eq := topLevelEquals(line); eq > 0 {
			name := strings.TrimSpace(line[:eq])
			body := strings.TrimSpace(line[eq+1:])
			if isLowerIdent(name) {
				if err := replBind(name, body, tEnv, rEnv, subst); err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
					continue
				}
				// Update env shadows: replBind mutates rEnv via Define and
				// returns updated tEnv. We need to pick that up... refactor.
				continue
			}
		}

		// Otherwise: evaluate expression
		bindingCount++
		v, t, err := replEvalExpr(line, tEnv, rEnv, subst)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			continue
		}
		fmt.Printf("%s : %s\n", v.Display(), typecheck.Pretty(t))
	}
}

// topLevelEquals returns the index of the first `=` that's a top-level binding
// (not part of `==` or inside a record/lambda). Returns -1 if not found.
//
// MVP: just look for `=` not preceded/followed by another `=`.
func topLevelEquals(s string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '=':
			if depth == 0 {
				if i+1 < len(s) && s[i+1] == '=' {
					i++ // skip == and continue
					continue
				}
				if i > 0 && s[i-1] == '=' {
					continue
				}
				if i > 0 && (s[i-1] == '<' || s[i-1] == '>' || s[i-1] == '/' || s[i-1] == '!') {
					continue
				}
				return i
			}
		}
	}
	return -1
}

func isLowerIdent(s string) bool {
	if s == "" || !(s[0] >= 'a' && s[0] <= 'z') {
		return false
	}
	for _, r := range s[1:] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '\'') {
			return false
		}
	}
	return true
}

// replInferExpr parses a single expression and infers its type.
func replInferExpr(exprSrc string, env *typecheck.TypeEnv, subst *typecheck.Subst) (typecheck.Type, error) {
	mod, err := parser.Parse("module Repl exposing (..)\n__repl = " + exprSrc + "\n")
	if err != nil {
		return nil, err
	}
	if len(mod.Decls) != 1 {
		return nil, fmt.Errorf("repl: expected single expression")
	}
	vd, ok := mod.Decls[0].(*ast.ValueDecl)
	if !ok {
		return nil, fmt.Errorf("repl: parse failed to produce value decl")
	}
	t, err := typecheck.Infer(vd.Body, env, subst)
	if err != nil {
		return nil, err
	}
	return subst.Apply(t), nil
}

// replEvalExpr parses a single expression, infers, evaluates, returns value+type.
func replEvalExpr(exprSrc string, tEnv *typecheck.TypeEnv, rEnv *runtime.Env, subst *typecheck.Subst) (runtime.Value, typecheck.Type, error) {
	mod, err := parser.Parse("module Repl exposing (..)\n__repl = " + exprSrc + "\n")
	if err != nil {
		return nil, nil, err
	}
	vd, _ := mod.Decls[0].(*ast.ValueDecl)
	t, err := typecheck.Infer(vd.Body, tEnv, subst)
	if err != nil {
		return nil, nil, err
	}
	v, err := runtime.Eval(vd.Body, rEnv)
	if err != nil {
		return nil, nil, err
	}
	return v, subst.Apply(t), nil
}

// replBind handles `name = expr`: type-check, evaluate, register in both envs.
//
// Note: we mutate rEnv via Define, but tEnv is immutable so we have to
// return... but main holds it as a local. For MVP, we use a workaround:
// rEnv is mutated; tEnv updates would need refactoring. Skip permanent
// type-env updates for now (each line gets re-checked against base).
func replBind(name, exprSrc string, tEnv *typecheck.TypeEnv, rEnv *runtime.Env, subst *typecheck.Subst) error {
	mod, err := parser.Parse("module Repl exposing (..)\n" + name + " = " + exprSrc + "\n")
	if err != nil {
		return err
	}
	vd, _ := mod.Decls[0].(*ast.ValueDecl)
	t, err := typecheck.Infer(vd.Body, tEnv, subst)
	if err != nil {
		return err
	}
	v, err := runtime.Eval(vd.Body, rEnv)
	if err != nil {
		return err
	}
	rEnv.Define(name, v)
	tEnv.Define(name, subst.Apply(t))
	fmt.Printf("%s : %s\n", name, typecheck.Pretty(subst.Apply(t)))
	return nil
}
