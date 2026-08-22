package typecheck

import (
	"mar/internal/ast"
)

// Reducing a checked type to the shape a decoder can check against is one job
// with two callers: the service dispatcher, which validates a request body
// against the declared request type, and JSON.decode, whose result type is
// fixed at the call site. Written twice they would drift, and the drift would
// be silent — two decoders disagreeing about what a Maybe looks like on the
// wire is not something a test of either one can see.

// CtorLookup answers, for a canonical type name (`Shared.Status`, ADR 0027),
// the constructors it declares — but only when every one of them carries
// nothing. A union with a payload has no single wire form worth asserting
// about, so it keeps the WireAny escape hatch.
type CtorLookup func(canonical string) ([]string, bool)

// CtorsFromEnv builds the lookup out of a type environment's custom types,
// which is where the checker already has them.
func CtorsFromEnv(env *TypeEnv) CtorLookup {
	if env == nil {
		return nil
	}
	return func(canonical string) ([]string, bool) {
		// LookupCustom rather than the map: registrations live on the ROOT
		// frame and the env in hand here is a child, so reading the field
		// directly answered "no custom types exist" for every module.
		ct, ok := env.LookupCustom(canonical)
		if !ok || len(ct.Params) > 0 || len(ct.CtorOrder) == 0 {
			return nil, false
		}
		for _, name := range ct.CtorOrder {
			if len(ct.Constructors[name].Args) > 0 {
				return nil, false
			}
		}
		return append([]string(nil), ct.CtorOrder...), true
	}
}

// StripForall unwraps a scheme down to the type it quantifies.
func StripForall(t Type) Type {
	if f, ok := t.(TForall); ok {
		return StripForall(f.Body)
	}
	return t
}

// wireShapeOf walks a checked type into a shape. depth stops a recursive type
// from expanding forever: a request is a wire format, so anything that deep is
// not something to be asserting about anyway.
func WireShapeOf(t Type, tags CtorLookup) ast.WireShape {
	return wireShapeOf(t, 0, tags)
}

func wireShapeOf(t Type, depth int, tags CtorLookup) ast.WireShape {
	if depth > 6 {
		return ast.WireShape{Kind: ast.WireAny}
	}
	switch n := StripForall(t).(type) {
	case TUnit:
		return ast.WireShape{Kind: ast.WireUnit}

	case TRecord:
		// An OPEN record ({ r | x : Int }) constrains the fields it names and
		// nothing else, which is what checking a named field at a time already
		// does, so it needs no special case.
		fields := make([]ast.WireField, 0, len(n.Order))
		for _, name := range n.Order {
			fields = append(fields, ast.WireField{
				Name:  name,
				Shape: wireShapeOf(n.Fields[name], depth+1, tags),
			})
		}
		return ast.WireShape{Kind: ast.WireRecord, Fields: fields}

	case TCon:
		switch n.Name {
		case "Int":
			return ast.WireShape{Kind: ast.WireInt}
		case "String":
			return ast.WireShape{Kind: ast.WireString}
		case "Bool":
			return ast.WireShape{Kind: ast.WireBool}
		case "Char":
			return ast.WireShape{Kind: ast.WireChar}
		case "Decimal":
			return ast.WireShape{Kind: ast.WireDecimal}
		case "Time":
			return ast.WireShape{Kind: ast.WireTime}
		case "List":
			if len(n.Args) == 1 {
				elem := wireShapeOf(n.Args[0], depth+1, tags)
				return ast.WireShape{Kind: ast.WireList, Elem: &elem}
			}
		case "Maybe":
			if len(n.Args) == 1 {
				elem := wireShapeOf(n.Args[0], depth+1, tags)
				return ast.WireShape{Kind: ast.WireMaybe, Elem: &elem}
			}
		default:
			// A union whose constructors all carry nothing has a finite,
			// known set of legal wire values, so it is checkable — and it
			// needed checking: a tag outside the set reached a `case` the
			// checker had proved total and answered 500.
			if tags != nil && len(n.Args) == 0 {
				if names, ok := tags(n.Name); ok {
					return ast.WireShape{Kind: ast.WireCtor, Tags: names}
				}
			}
		}
	}
	// Type variables, unions carrying a payload, tuples, functions: not this
	// package's business to assert about. See the note at the top.
	return ast.WireShape{Kind: ast.WireAny}
}
