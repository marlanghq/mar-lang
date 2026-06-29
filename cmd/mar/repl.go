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
//   - A `name = expr` style binding (registered into the session)
//   - An expression to evaluate and print
//
// Bindings are immutable: trying to redefine a name fails with "X is
// already defined; use :reset to start a fresh session". This mirrors
// how the language itself rejects mutation.
//
// Commands: :quit / :q, :type EXPR / :t EXPR, :reset.
func runRepl() int {
	fmt.Println("mar repl. :quit, :type EXPR, :reset")
	scanner := bufio.NewScanner(os.Stdin)

	// Persistent envs that grow across lines. Track user-defined names
	// separately from the env so `:reset` can wipe the session without
	// touching builtins, and so we know what came from the user vs the
	// stdlib (used by the rebind-prevention check).
	tEnv := typecheck.BaseEnv()
	rEnv := runtime.BaseEnv()
	subst := typecheck.NewSubst()
	userBindings := map[string]bool{}

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
		if line == ":reset" {
			tEnv = typecheck.BaseEnv()
			rEnv = runtime.BaseEnv()
			subst = typecheck.NewSubst()
			userBindings = map[string]bool{}
			fmt.Println("(session cleared)")
			continue
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
				// Reject rebinding — mar values are immutable, including
				// in the REPL. The session would otherwise silently
				// behave as mutation (closures would see new value).
				if userBindings[name] {
					fmt.Fprintf(os.Stderr, "'%s' is already defined; use :reset to start a fresh session\n", name)
					continue
				}
				if _, builtin := rEnv.Lookup(name); builtin {
					fmt.Fprintf(os.Stderr, "'%s' is a builtin; can't be redefined\n", name)
					continue
				}
				if err := replBind(name, body, tEnv, rEnv, subst); err != nil {
					fmt.Fprintf(os.Stderr, "%v\n", err)
					continue
				}
				userBindings[name] = true
				continue
			}
		}

		// Otherwise: evaluate expression
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
// We just look for `=` not preceded or followed by another `=`.
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
// Note: rEnv is mutated via Define, but tEnv is immutable and main
// holds it as a local. We don't persist type-env updates between lines
// (each line is re-checked against the base env); making tEnv share
// mutability with rEnv would let bindings carry their inferred type
// forward across REPL prompts.
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
