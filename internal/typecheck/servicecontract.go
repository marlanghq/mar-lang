package typecheck

import (
	"fmt"

	"mar/internal/ast"
)

// A `Service` contract exists to make two sides of a wire agree. Written
// without an annotation it stops doing that, and nothing said so.
//
//	createNote = Service.declare POST "/notes"
//
// infers `Service a b`, which is generalized — so `Service.implement` and
// `Service.call` instantiate the variables SEPARATELY. The one value whose
// job is to force the client and the server to the same request type agrees
// with each of them privately instead.
//
// The consequence is measurable. On notes-fullstack, with the annotation:
//
//	POST /notes  {}    422, "the request is missing the field `body`"
//
// and with the annotation removed, on the same request:
//
//	POST /notes  {}    500 Internal Server Error
//
// The request guard reduces a type variable to "check nothing", so a body
// with no fields reaches a handler that expects them and fails halfway. The
// typed path parameters go the same way: there is no declared request type to
// compare a path segment against.
//
// This is a fact about a type, so it does not wait for boot.

// checkServiceContracts refuses a top-level `Service` whose request or
// response type is still a variable.
//
// It reads the FINAL type of each top-level value, so an annotated contract
// is judged on its annotation and an inferred one on what was inferred. A
// function that merely mentions `Service a b` in its signature is untouched:
// taking any contract as an argument is a reasonable thing to write, and only
// a value that IS a contract has a wire to be wrong about.
func checkServiceContracts(mod *ast.Module, types map[string]Type, tags CtorLookup) error {
	for _, d := range mod.Decls {
		v, ok := d.(*ast.ValueDecl)
		if !ok || len(v.Params) > 0 {
			continue // a function, not a contract
		}
		t, ok := types[v.Name]
		if !ok {
			continue
		}
		con, ok := StripForall(t).(TCon)
		if !ok || con.Name != "Service" || len(con.Args) != 2 {
			continue
		}
		for i, side := range []string{"request", "response"} {
			if where := functionInside(con.Args[i], tags, map[string]bool{}); where != "" {
				return errorf(v.Pos, "the contract `%s` puts a function in its %s type (%s).\n"+
					"  A request and a response are JSON: a function has no form there, so the "+
					"field can never carry anything. Measured, the guard asks for it and takes "+
					"`null`: sending `{ \"fn\": null }` is accepted and leaving `fn` out is a 422 "+
					"naming a field no caller can supply.\n"+
					"  Send the data the function would be applied to, and keep the function on the "+
					"side that runs it",
					v.Name, side, where)
			}
			if !hasTypeVar(con.Args[i]) {
				continue
			}
			return errorf(v.Pos, "the contract `%s` leaves its %s type open (`%s`), so it is not a contract.\n"+
				"  `Service.implement` and `Service.call` would each pick their own, which is the one thing "+
				"a contract exists to prevent: the server would answer a shape the caller never agreed to, "+
				"and a malformed body would reach the handler unchecked.\n"+
				"  Give it a type annotation, for example `%s : Service %s`",
				v.Name, side, Pretty(t), v.Name, exampleServiceArgs(con))
		}
	}
	return nil
}

// exampleServiceArgs spells the annotation to write, keeping whichever side
// is already concrete.
func exampleServiceArgs(con TCon) string {
	name := func(t Type, fallback string) string {
		if hasTypeVar(t) {
			return fallback
		}
		return Pretty(t)
	}
	return fmt.Sprintf("%s %s", name(con.Args[0], "YourRequest"), name(con.Args[1], "YourResponse"))
}

// hasTypeVar reports whether a type still contains a variable anywhere.
func hasTypeVar(t Type) bool {
	switch n := StripForall(t).(type) {
	case TVar:
		return true
	case TCon:
		for _, a := range n.Args {
			if hasTypeVar(a) {
				return true
			}
		}
	case TArrow:
		return hasTypeVar(n.From) || hasTypeVar(n.To)
	case TRecord:
		for _, f := range n.Fields {
			if hasTypeVar(f) {
				return true
			}
		}
		return n.Tail != nil
	case TTuple:
		for _, m := range n.Members {
			if hasTypeVar(m) {
				return true
			}
		}
	}
	return false
}

// functionInside names the first place a function appears inside a type, and
// is empty when there is none. It reads through unions to their constructor
// payloads, because `type Job = Run (Int -> Int)` hides one just as well as a
// record field does.
//
// `seen` closes a recursive type's cycle: a comment whose replies are comments
// would otherwise be walked forever.
func functionInside(t Type, tags CtorLookup, seen map[string]bool) string {
	switch n := StripForall(t).(type) {
	case TArrow:
		return "`" + Pretty(n) + "`"
	case TRecord:
		for _, name := range n.Order {
			if where := functionInside(n.Fields[name], tags, seen); where != "" {
				return "the field `" + name + "` holds " + where
			}
		}
	case TTuple:
		for _, m := range n.Members {
			if where := functionInside(m, tags, seen); where != "" {
				return where
			}
		}
	case TCon:
		for _, a := range n.Args {
			if where := functionInside(a, tags, seen); where != "" {
				return where
			}
		}
		if tags == nil || seen[n.Name] {
			return ""
		}
		arms, ok := tags(n.Name)
		if !ok {
			return ""
		}
		seen[n.Name] = true
		defer delete(seen, n.Name)
		for _, arm := range arms {
			for _, a := range arm.Args {
				if where := functionInside(a, tags, seen); where != "" {
					return "`" + arm.Name + "` carries " + where
				}
			}
		}
	}
	return ""
}
