package project

import (
	"strings"

	"mar/internal/ast"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// ctorsFrom builds the union lookup the shape reduction needs.
//
// This used to be built by walking the module ASTs, because that was the
// cheapest place constructor names and arities both existed. It cannot stay
// there: checking a union with a PAYLOAD needs the argument TYPES, and an AST
// carries type expressions that only the checker can resolve. The checker's
// own environment has them, so the lookup comes from there and there is one
// implementation of it rather than two that can drift.
func ctorsFrom(env *typecheck.TypeEnv) typecheck.CtorLookup {
	return typecheck.CtorsFromEnv(env)
}

// This package is the one place that can see both a checked type and a runtime
// value, so it is where the declared request type gets attached to the service
// the dispatcher will check bodies against. The reduction itself lives in
// internal/typecheck, next to the types it reads, because JSON.decode needs
// exactly the same reduction and two copies of it would drift apart quietly.
//
// This only works because EVERY load path type-checks, including the deployed
// one: see ADR 0017. Deriving it here while some path skipped checking would
// give a guard that exists under `mar dev` and vanishes on deploy — or, as it
// turned out, the reverse: the dev loader passed a literal nil where the
// checked types belonged, so the guard was live in production and absent on
// every developer's machine for a month.

// stampServiceShape records the declared request shape on a service value, so
// the dispatcher can reject a malformed body with a 422 that names the field
// instead of letting the handler fail somewhere downstream.
func stampServiceShape(svc runtime.VService, t typecheck.Type, tags typecheck.CtorLookup) runtime.VService {
	if svc.InputShape != nil || t == nil {
		return svc
	}
	con, ok := typecheck.StripForall(t).(typecheck.TCon)
	if !ok || con.Name != "Service" || len(con.Args) != 2 {
		return svc
	}
	s := typecheck.WireShapeOf(con.Args[0], tags)
	svc.InputShape = &s
	return svc
}

// declaredTypes indexes a module's checked value types by bare name, which is
// how loadIntoEnv sees them.
func declaredTypes(all map[string]map[string]typecheck.Type, mod *ast.Module) map[string]typecheck.Type {
	if all == nil {
		return nil
	}
	return all[joinName(mod.Name)]
}

// declaredTypesFlat is the same thing for the loader that carries its checked
// types in one flat `Module.value` map rather than grouped by module. It exists
// because the dev/build path had the types in hand and passed a literal nil
// instead, so every shape was stamped as "check nothing" and the guard this
// file exists for was present on deploy and absent under `mar dev` — exactly
// the split ADR 0017 set out to avoid, pointing the other way.
func declaredTypesFlat(all map[string]typecheck.Type, mod *ast.Module) map[string]typecheck.Type {
	if all == nil {
		return nil
	}
	prefix := joinName(mod.Name) + "."
	out := map[string]typecheck.Type{}
	for qualified, t := range all {
		if !strings.HasPrefix(qualified, prefix) {
			continue
		}
		bare := qualified[len(prefix):]
		if strings.Contains(bare, ".") {
			continue
		}
		out[bare] = t
	}
	return out
}
