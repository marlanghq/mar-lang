package apphost

import (
	"fmt"
	"sort"
	"strings"

	"mar/internal/ast"
	"mar/internal/runtime"
)

// A `Service.call` needs two things to work, and each is a fact about the
// program: the contract, which the source declares, and a handler mounted on
// the contract's verb and path, which `main` registers. `mar check` sees the
// first half and cannot see the second — a route table only exists once `main`
// has run. So the comparison belongs at boot.
//
// Measured, with a contract left out of the `services` list:
//
//	mar check                 passes
//	GET /notes                200 OK, text/html — the SPA shell
//	the page                  ServerError "decode failed: Unexpected token '<'"
//
// The app answers its own request with its own HTML and hands it to a JSON
// decoder. Nothing on the way says the sentence that would end it: this
// contract has no handler.

// calledContract is one `Service.call` site, kept with enough to name it.
type calledContract struct {
	Module string // the module the contract is named in
	Name   string
	In     string // the module the call was found in
	Pos    ast.Pos
}

func (c calledContract) qualified() string {
	if c.Module == "" {
		return c.Name
	}
	return c.Module + "." + c.Name
}

// CheckServiceCalls refuses a call on a contract nothing mounts.
//
// It only speaks when it is sure of both halves: the contract has to resolve
// to a real `Service` value, and its verb and path have to be absent from the
// mounted table. A call whose argument is built by a function rather than
// named resolves to nothing and is passed over — this refuses what it can
// prove, and stays quiet otherwise.
func CheckServiceCalls(mods []*ast.Module, env *runtime.Env, mounted []runtime.VExposedService) error {
	if env == nil {
		return nil
	}
	live := map[string]bool{}
	for _, es := range mounted {
		live[es.Service.Verb+" "+es.Service.Path] = true
	}

	var unmounted []string
	seen := map[string]bool{}
	for _, call := range serviceCallSites(mods) {
		name := call.qualified()
		if seen[name] {
			continue
		}
		v, ok := env.Lookup(name)
		if !ok {
			// A contract imported unqualified is bound under its bare name.
			if v, ok = env.Lookup(call.Name); !ok {
				continue
			}
		}
		contract, ok := v.(runtime.VService)
		if !ok {
			continue
		}
		if live[contract.Verb+" "+contract.Path] {
			continue
		}
		seen[name] = true
		unmounted = append(unmounted, fmt.Sprintf(
			"`%s` (%s %s), called in %s at line %d", name, contract.Verb, contract.Path, call.In, call.Pos.Line))
	}
	if len(unmounted) == 0 {
		return nil
	}
	sort.Strings(unmounted)
	return fmt.Errorf(
		"a page calls a service contract that nothing mounts:\n    %s\n"+
			"  The request reaches no handler, so the server answers with the app's own "+
			"HTML and the page reports a decode failure on a `<`.\n"+
			"  Add it to the `services` list with `Service.implement`",
		strings.Join(unmounted, "\n    "))
}

func moduleName(m ast.ModuleName) string { return strings.Join(m, ".") }

// serviceCallSites finds every `Service.call <contract>` whose contract is
// named rather than computed.
func serviceCallSites(mods []*ast.Module) []calledContract {
	var found []calledContract
	for _, m := range mods {
		for _, d := range m.Decls {
			vd, ok := d.(*ast.ValueDecl)
			if !ok {
				continue
			}
			var here []calledContract
			walkCalls(vd.Body, &here)
			for _, c := range here {
				c.In = moduleName(m.Name)
				found = append(found, c)
			}
		}
	}
	return found
}

func walkCalls(e ast.Expr, found *[]calledContract) {
	if e == nil {
		return
	}
	if app, ok := e.(*ast.EApp); ok {
		if head, args := flattenApp(app); len(args) > 0 && isServiceCall(head) {
			if c, ok := namedContract(args[0]); ok {
				*found = append(*found, c)
			}
		}
	}
	ast.EachChild(e, func(child ast.Expr) { walkCalls(child, found) })
}

func isServiceCall(head ast.Expr) bool {
	q, ok := head.(*ast.EQualified)
	return ok && moduleName(q.Module) == "Service" && q.Name == "call"
}

// namedContract reads the contract out of the first argument when it is a
// name. Anything else — a call, a field access — is not something this check
// can resolve, and it says nothing rather than guessing.
func namedContract(e ast.Expr) (calledContract, bool) {
	switch n := e.(type) {
	case *ast.EQualified:
		return calledContract{Module: moduleName(n.Module), Name: n.Name, Pos: n.Pos}, true
	case *ast.EVar:
		return calledContract{Name: n.Name, Pos: n.Pos}, true
	}
	return calledContract{}, false
}

func flattenApp(e ast.Expr) (ast.Expr, []ast.Expr) {
	if app, ok := e.(*ast.EApp); ok {
		h, a := flattenApp(app.Fn)
		return h, append(a, app.Arg)
	}
	return e, nil
}
