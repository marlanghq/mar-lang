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

// CtorArm is one constructor of a union: its name and what it carries.
type CtorArm struct {
	Name string
	Args []Type
}

// CtorLookup answers, for a canonical type name (`Shared.Status`, ADR 0027),
// the constructors it declares and what each one carries.
//
// It used to refuse any union with a payload, which sounded conservative and
// was the widest hole in the guard: a union is the one shape whose legal
// values are ENUMERABLE, so it is the last thing that should go unchecked. A
// tag the type does not declare, or the right tag with the wrong arguments,
// reached a `case` the checker had proved total.
type CtorLookup func(canonical string, useArgs []Type) ([]CtorArm, bool)

// CtorsFromEnv builds the lookup out of a type environment's custom types,
// which is where the checker already has them.
func CtorsFromEnv(env *TypeEnv) CtorLookup {
	if env == nil {
		return nil
	}
	return func(canonical string, useArgs []Type) ([]CtorArm, bool) {
		// LookupCustom rather than the map: registrations live on the ROOT
		// frame and the env in hand here is a child, so reading the field
		// directly answered "no custom types exist" for every module.
		ct, ok := env.LookupCustom(canonical)
		if !ok || len(ct.CtorOrder) == 0 {
			return nil, false
		}
		// A PARAMETERISED union used to be turned away here, for want of a
		// use site — and the use site is right there in the type being
		// reduced. So `Result String Note` and `Box Int` were WireAny, and
		// measured on a service taking `Box Int`: a body naming a
		// constructor the type does not have answered 500 with
		// "no case branch matched", on a `case` the checker proved total;
		// and `Full "x"` where the type says `Full Int` answered 200 with a
		// string in a field declared Int.
		//
		// The parameters' type variables are recoverable from any
		// constructor's Result, which is `TCon{Name, [TVar(p1), ...]}`, and
		// substituting the use site's arguments into the arms is what makes
		// the payloads concrete enough to check.
		sub := map[int]Type{}
		if len(ct.Params) > 0 {
			ids, ok := paramVarIDs(ct)
			if !ok || len(ids) != len(useArgs) {
				return nil, false
			}
			for i, id := range ids {
				sub[id] = useArgs[i]
			}
		}
		arms := make([]CtorArm, 0, len(ct.CtorOrder))
		for _, name := range ct.CtorOrder {
			args := ct.Constructors[name].Args
			if len(sub) > 0 {
				concrete := make([]Type, len(args))
				for i, a := range args {
					concrete[i] = substituteVars(a, sub)
				}
				args = concrete
			}
			arms = append(arms, CtorArm{Name: name, Args: args})
		}
		return arms, true
	}
}

// StripForall unwraps a scheme down to the type it quantifies.
func StripForall(t Type) Type {
	if f, ok := t.(TForall); ok {
		return StripForall(f.Body)
	}
	return t
}

// WireShapeOf walks a checked type into a shape.
//
// It used to stop at six levels, and the comment here explained that anything
// deeper was "not something to be asserting about anyway". That was a
// rationalisation of a mechanism: the cap existed to stop a RECURSIVE type
// expanding forever, and it paid for that by giving up on ordinary types. A
// plain record seven levels deep sailed through the guard and hit
// `+: unsupported types` from an anonymous request.
//
// So the rule is now the one the cap was standing in for: follow a named type
// until it closes its own cycle, and give up only there. `path` carries the
// canonical names currently being expanded. The depth ceiling stays as a
// BACKSTOP — high enough that no honest type reaches it — because a
// self-recursive type alias is expanded by the checker before this sees it,
// and a compiler that hangs is worse than one that under-checks.
func WireShapeOf(t Type, tags CtorLookup) ast.WireShape {
	return wireShapeOf(t, 0, map[string]bool{}, tags)
}

func wireShapeOf(t Type, depth int, path map[string]bool, tags CtorLookup) ast.WireShape {
	if depth > 64 {
		return ast.WireShape{Kind: ast.WireAny}
	}
	switch n := StripForall(t).(type) {
	case TUnit:
		return ast.WireShape{Kind: ast.WireUnit}

	case TTuple:
		// A tuple's arity is part of its type, so both the arity and the
		// members are checkable. There was no kind for this at all, which
		// made a tuple in a request a guaranteed `tupleFirst: not a 2-tuple`
		// rather than a 422 — and a tuple as the whole request type
		// something no client could ever call successfully.
		members := make([]ast.WireShape, 0, len(n.Members))
		for _, m := range n.Members {
			members = append(members, wireShapeOf(m, depth+1, path, tags))
		}
		return ast.WireShape{Kind: ast.WireTuple, Members: members}

	case TRecord:
		// An OPEN record ({ r | x : Int }) constrains the fields it names and
		// nothing else, which is what checking a named field at a time already
		// does, so it needs no special case.
		fields := make([]ast.WireField, 0, len(n.Order))
		for _, name := range n.Order {
			fields = append(fields, ast.WireField{
				Name:  name,
				Shape: wireShapeOf(n.Fields[name], depth+1, path, tags),
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
				elem := wireShapeOf(n.Args[0], depth+1, path, tags)
				return ast.WireShape{Kind: ast.WireList, Elem: &elem}
			}
		case "Maybe":
			if len(n.Args) == 1 {
				elem := wireShapeOf(n.Args[0], depth+1, path, tags)
				return ast.WireShape{Kind: ast.WireMaybe, Elem: &elem}
			}
		case "Set":
			if len(n.Args) == 1 {
				elem := wireShapeOf(n.Args[0], depth+1, path, tags)
				return ast.WireShape{Kind: ast.WireSet, Elem: &elem}
			}
		case "Dict":
			if len(n.Args) == 2 {
				return ast.WireShape{Kind: ast.WireDict, Members: []ast.WireShape{
					wireShapeOf(n.Args[0], depth+1, path, tags),
					wireShapeOf(n.Args[1], depth+1, path, tags),
				}}
			}
		case "Angle":
			return ast.WireShape{Kind: ast.WireAngle}
		default:
			// A union has a finite, known set of legal wire values: the
			// constructors it declares, each with the arguments it carries.
			// That is the whole reason it is checkable, and it was the
			// widest hole in the guard while only the nullary case was
			// covered.
			//
			// `path` is what stops a recursive type here: a list of
			// comments whose replies are comments closes its own cycle, and
			// the shape gives up at exactly that point rather than six
			// levels into every unrelated record.
			// The `len(n.Args) != 0` that used to be here is gone: a
			// parameterised union is checkable once its arguments are
			// substituted in, which CtorsFromEnv now does.
			if tags == nil {
				break
			}
			// Already inside this type: name the cycle instead of giving
			// up. The checker follows it against the data, which ends.
			if path[n.Name] {
				return ast.WireShape{Kind: ast.WireRef, Name: n.Name}
			}
			arms, ok := tags(n.Name, n.Args)
			if !ok {
				break
			}
			path[n.Name] = true
			names := make([]string, 0, len(arms))
			args := make([][]ast.WireShape, 0, len(arms))
			for _, arm := range arms {
				names = append(names, arm.Name)
				shapes := make([]ast.WireShape, 0, len(arm.Args))
				for _, a := range arm.Args {
					shapes = append(shapes, wireShapeOf(a, depth+1, path, tags))
				}
				args = append(args, shapes)
			}
			delete(path, n.Name)
			return ast.WireShape{Kind: ast.WireCtor, Tags: names, Args: args, Name: n.Name}
		}
	}
	// What is left is what genuinely has no shape to assert: a type
	// variable, a function, a parameterised union, and a named type at the
	// point where it closes its own cycle.
	return ast.WireShape{Kind: ast.WireAny}
}

// paramVarIDs recovers a union's parameter type variables from the result
// type its constructors share, which is `TCon{Name, [TVar(p1), TVar(p2)…]}`.
// CustomType records the parameters by NAME; the identity that matters for
// substitution is the variable, and this is where it survives.
func paramVarIDs(ct CustomType) ([]int, bool) {
	for _, name := range ct.CtorOrder {
		con, ok := StripForall(ct.Constructors[name].Result).(TCon)
		if !ok || len(con.Args) != len(ct.Params) {
			continue
		}
		ids := make([]int, len(con.Args))
		for i, a := range con.Args {
			v, ok := StripForall(a).(TVar)
			if !ok {
				return nil, false
			}
			ids[i] = v.ID
		}
		return ids, true
	}
	return nil, false
}
