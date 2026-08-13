package project

import (
	"mar/internal/ast"
	"mar/internal/runtime"
	"mar/internal/typecheck"
)

// This package is the one place that can see both a checked type and a runtime
// value, so it is where the declared request type is reduced to the shape the
// dispatcher checks incoming JSON against. The runtime cannot do it (it does
// not import the typechecker: ADR 0016) and the typechecker will not (it does
// not know what a service dispatcher is).
//
// This only works because EVERY load path type-checks, including the deployed
// one: see ADR 0017. Deriving it here while some path skipped checking would
// give a guard that exists under `mar dev` and vanishes on deploy, which is
// worse than no guard at all.
//
// The reduction is deliberately lossy. It answers one question: "could this
// decoded JSON have come from a value of the declared type?", and anything it
// cannot answer becomes WireAny rather than a guess, because a wrong 422 on a
// valid request is worse than the 500 this replaces.

// stampServiceShape records the declared request shape on a service value, so
// the dispatcher can reject a malformed body with a 422 that names the field
// instead of letting the handler fail somewhere downstream.
func stampServiceShape(svc runtime.VService, t typecheck.Type) runtime.VService {
	if svc.InputShape != nil || t == nil {
		return svc
	}
	con, ok := stripForall(t).(typecheck.TCon)
	if !ok || con.Name != "Service" || len(con.Args) != 2 {
		return svc
	}
	s := wireShapeOf(con.Args[0], 0)
	svc.InputShape = &s
	return svc
}

func stripForall(t typecheck.Type) typecheck.Type {
	if f, ok := t.(typecheck.TForall); ok {
		return stripForall(f.Body)
	}
	return t
}

// wireShapeOf walks a checked type into a shape. depth stops a recursive type
// from expanding forever: a request is a wire format, so anything that deep is
// not something to be asserting about anyway.
func wireShapeOf(t typecheck.Type, depth int) runtime.WireShape {
	if depth > 6 {
		return runtime.WireShape{Kind: runtime.WireAny}
	}
	switch n := stripForall(t).(type) {
	case typecheck.TUnit:
		return runtime.WireShape{Kind: runtime.WireUnit}

	case typecheck.TRecord:
		// An OPEN record ({ r | x : Int }) constrains the fields it names and
		// nothing else, which is what checking a named field at a time already
		// does, so it needs no special case.
		fields := make([]runtime.WireField, 0, len(n.Order))
		for _, name := range n.Order {
			fields = append(fields, runtime.WireField{
				Name:  name,
				Shape: wireShapeOf(n.Fields[name], depth+1),
			})
		}
		return runtime.WireShape{Kind: runtime.WireRecord, Fields: fields}

	case typecheck.TCon:
		switch n.Name {
		case "Int":
			return runtime.WireShape{Kind: runtime.WireInt}
		case "String":
			return runtime.WireShape{Kind: runtime.WireString}
		case "Bool":
			return runtime.WireShape{Kind: runtime.WireBool}
		case "Char":
			return runtime.WireShape{Kind: runtime.WireChar}
		case "Decimal":
			return runtime.WireShape{Kind: runtime.WireDecimal}
		case "Time":
			return runtime.WireShape{Kind: runtime.WireTime}
		case "List":
			if len(n.Args) == 1 {
				elem := wireShapeOf(n.Args[0], depth+1)
				return runtime.WireShape{Kind: runtime.WireList, Elem: &elem}
			}
		case "Maybe":
			if len(n.Args) == 1 {
				elem := wireShapeOf(n.Args[0], depth+1)
				return runtime.WireShape{Kind: runtime.WireMaybe, Elem: &elem}
			}
		}
	}
	// Type variables, custom unions, tuples, functions: not this package's
	// business to assert about. See the note at the top.
	return runtime.WireShape{Kind: runtime.WireAny}
}

// declaredTypes indexes a module's checked value types by bare name, which is
// how loadIntoEnv sees them.
func declaredTypes(all map[string]map[string]typecheck.Type, mod *ast.Module) map[string]typecheck.Type {
	if all == nil {
		return nil
	}
	return all[joinName(mod.Name)]
}
