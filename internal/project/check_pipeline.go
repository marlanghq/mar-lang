package project

import (
	"mar/internal/ast"
	"mar/internal/typecheck"
)

// checkOrdered type-checks modules in dependency order, threading each
// module's exports into a shared type environment so the next one can import
// them, and returns each module's value types keyed by dotted module name.
//
// Every load path has to call this, including the one that runs a built
// artifact, and not for the type errors: **checking is what elaborates the
// tree**. The checker writes decisions onto the AST that the runtimes cannot
// re-derive — which integer literals became Decimals (ADR 0013), which
// reference resolved to which implementation (ADR 0014), and the request shape
// a service dispatcher validates against (ADR 0017). A parse-only load is a
// tree with those decisions missing, which does not fail loudly; it fails
// later, somewhere else, on a program that `mar check` accepted.
//
// `wrap` decorates an error with source context when the caller has it.
func checkOrdered(
	ordered []*ast.Module,
	nameOf func(*ast.Module) string,
	present func(module string) bool,
	wrap func(mod *ast.Module, err error) error,
) (map[string]map[string]typecheck.Type, error) {
	tEnv := typecheck.BaseEnv()
	aliasesByModule := map[string]map[string]typecheck.TypeAlias{}
	customsByModule := map[string]map[string]typecheck.CustomType{}
	out := map[string]map[string]typecheck.Type{}

	for _, mod := range ordered {
		name := nameOf(mod)

		importedAliases := map[string]typecheck.TypeAlias{}
		importedCustoms := map[string]typecheck.CustomType{}
		for _, imp := range mod.Imports {
			impName := joinName(imp.Module)
			if present != nil && !present(impName) && !isStdlib(impName) {
				continue
			}
			for k, v := range aliasesByModule[impName] {
				importedAliases[impName+"."+k] = v
			}
			for k, v := range customsByModule[impName] {
				importedCustoms[impName+"."+k] = v
			}
		}

		res, err := typecheck.CheckModuleWith(mod, tEnv, importedAliases, importedCustoms)
		if err != nil {
			if wrap != nil {
				return nil, wrap(mod, err)
			}
			return nil, err
		}

		for vname, t := range res.ValueTypes {
			tEnv.Define(name+"."+vname, t)
		}
		modAliases := map[string]typecheck.TypeAlias{}
		for tname, alias := range res.TypeAliases {
			modAliases[tname] = alias
		}
		aliasesByModule[name] = modAliases

		modCustoms := map[string]typecheck.CustomType{}
		for tname, ct := range res.CustomTypes {
			modCustoms[tname] = ct
			for _, cname := range ct.CtorOrder {
				if cval, ok := tEnv.Lookup(cname); ok {
					tEnv.Define(name+"."+cname, cval)
				}
			}
		}
		customsByModule[name] = modCustoms
		out[name] = res.ValueTypes
	}
	return out, nil
}
