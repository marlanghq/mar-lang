package typecheck

import (
	"fmt"
	"sort"
	"strings"

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
				// A side that reduces to WireAny is a side the guard cannot
				// check: it asserts nothing, so a malformed body reaches the
				// handler unchecked — the same failure the open-type case
				// below describes, arrived at by a different route.
				//
				// What lands here is a RUNTIME-INTERNAL type: `View`, `Shape`,
				// `Sound`, `Attr`, `Page`. They are values the renderer and the
				// synthesiser build for themselves, they hold closures, and they
				// have no JSON form — so no list of them is needed here, and
				// none can go stale: the wire reduction already answers "can
				// this be checked", and this asks it.
				//
				// Measured before this existed: `Service () Shape` type-checked,
				// and the three runtimes do not even agree on the tag for a
				// canvas text shape (`text` on the server and the web,
				// `canvasText` on the phone), so what crossed would have drawn
				// nothing on one of them.
				if where := uncheckableInside(WireShapeOf(con.Args[i], tags), ""); where != "" {
					return errorf(v.Pos, "the contract `%s` puts a value with no wire form in its %s type (%s: %s).\n"+
						"  A request and a response are JSON, and this type reduces to \"check nothing\": the guard "+
						"would accept any shape at all and hand it to the handler. `View`, `Shape`, `Sound`, `Attr` "+
						"and `Page` are built by a runtime for itself and hold closures.\n"+
						"  Send the data the value is built FROM, and build it on the side that shows it",
						v.Name, side, Pretty(con.Args[i]), where)
				}
				continue
			}
			return errorf(v.Pos, "the contract `%s` leaves its %s type open (`%s`), so it is not a contract.\n"+
				"  `Service.implement` and `Service.call` would each pick their own, which is the one thing "+
				"a contract exists to prevent: the server would answer a shape the caller never agreed to, "+
				"and a malformed body would reach the handler unchecked.\n"+
				"  Give it a type annotation, for example `%s : Service %s`",
				v.Name, side, Pretty(t), v.Name, exampleServiceArgs(con))
		}
		if err := checkBodilessRequest(v, con); err != nil {
			return err
		}
	}
	return nil
}

// checkBodilessRequest refuses a GET or DELETE contract whose request is not a
// record (or `()`).
//
// A request without a body travels in the URL: path parameters in their
// slots, and every other field of the record in the query string. A value
// that is not a record has no fields to put there, so no client could send
// it. The verb is read from the declaration's syntax here, where the error can
// point at the line; apphost refuses the same contract at boot whatever way
// the verb was written.
func checkBodilessRequest(v *ast.ValueDecl, con TCon) error {
	verb, ok := serviceDeclareVerb(v.Body)
	if !ok || !ast.BodilessVerb(verb) {
		return nil
	}
	switch StripForall(con.Args[0]).(type) {
	case TRecord, TUnit:
		return nil
	}
	example := TCon{Name: "Service", Args: []Type{
		TRecord{Fields: map[string]Type{"value": con.Args[0]}, Order: []string{"value"}},
		con.Args[1],
	}}
	return errorf(v.Pos, "the contract `%s` is declared %s, and its request type `%s` is not a record.\n"+
		"  A %s request has no body: path parameters travel in their slots and the other fields of "+
		"a record in the query string. A value that is not a record has no fields to put there.\n"+
		"  Make the request a record, for example `%s : %s`",
		v.Name, verb, Pretty(con.Args[0]), verb, v.Name, Pretty(example))
}

// exampleServiceArgs spells the annotation to write, keeping whichever side
// is already concrete.
func exampleServiceArgs(con TCon) string {
	side := func(t Type, placeholder string) Type {
		if hasTypeVar(t) {
			return TCon{Name: placeholder}
		}
		return t
	}
	full := Pretty(TCon{Name: "Service", Args: []Type{side(con.Args[0], "YourRequest"), side(con.Args[1], "YourResponse")}})
	return strings.TrimPrefix(full, "Service ")
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

// checkDecodeTargets refuses a `JSON.decode` whose result type is concrete and
// still reduces to "check nothing".
//
// An UNPINNED decode — passed around as a value, or a result nobody looked at —
// also reduces to WireAny, and that one is fine: nothing was promised, so the
// runtimes behave as they did before shapes existed. What is not fine is a
// decode pinned to a type the guard cannot check, because there the program
// DID promise something and the guard will wave any document through.
func checkDecodeTargets(exprTypes map[ast.Expr]Type, tags CtorLookup) error {
	// Sorted so the error is the same one on every run: the map's order is not.
	type site struct {
		pos ast.Pos
		typ Type
	}
	var sites []site
	for e, t := range exprTypes {
		q, ok := e.(*ast.EQualified)
		if !ok || joinName(q.Module, q.Name) != "JSON.decode" {
			continue
		}
		res := StripForall(t)
		arrow, ok := res.(TArrow)
		if !ok {
			continue
		}
		sites = append(sites, site{pos: q.Pos, typ: arrow.To})
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].pos.Line != sites[j].pos.Line {
			return sites[i].pos.Line < sites[j].pos.Line
		}
		return sites[i].pos.Column < sites[j].pos.Column
	})

	for _, s := range sites {
		// `Result String a` with `a` unpinned: nothing was promised.
		if hasTypeVar(s.typ) {
			continue
		}
		decoded := s.typ
		if con, ok := StripForall(decoded).(TCon); ok && con.Name == "Result" && len(con.Args) == 2 {
			decoded = con.Args[1]
		}
		if where := uncheckableInside(WireShapeOf(decoded, tags), ""); where != "" {
			return errorf(s.pos, "this `JSON.decode` is typed to produce a value with no wire form (%s: %s).\n"+
				"  The decoder checks a document against the type it was asked for, and this type reduces to "+
				"\"check nothing\": every document would decode, including one that has nothing to do with it. "+
				"`View`, `Shape`, `Sound`, `Attr` and `Page` are built by a runtime for itself and hold closures.\n"+
				"  Decode the data the value is built FROM, and build it after",
				Pretty(decoded), where)
		}
	}
	return nil
}

// uncheckableInside finds a position in a reduced wire shape that asserts
// nothing, and describes where it is. Empty means every position is checked.
//
// It walks the SHAPE rather than the type because the shape is what the guard
// runs on: whatever reduces to WireAny is exactly what the guard will wave
// through, whether that is a renderer's `View` or something added later that
// the reduction does not know either.
func uncheckableInside(s ast.WireShape, path string) string {
	where := path
	if where == "" {
		where = "the whole type"
	}
	switch s.Kind {
	case ast.WireAny:
		return where
	case ast.WireRecord:
		for _, f := range s.Fields {
			if got := uncheckableInside(f.Shape, "the field `"+f.Name+"`"); got != "" {
				return got
			}
		}
	case ast.WireList, ast.WireMaybe, ast.WireSet:
		if s.Elem != nil {
			if got := uncheckableInside(*s.Elem, "the element of "+where); got != "" {
				return got
			}
		}
	case ast.WireTuple, ast.WireDict:
		for i, m := range s.Members {
			if got := uncheckableInside(m, fmt.Sprintf("position %d of %s", i+1, where)); got != "" {
				return got
			}
		}
	case ast.WireCtor:
		for i, args := range s.Args {
			tag := ""
			if i < len(s.Tags) {
				tag = s.Tags[i]
			}
			for _, a := range args {
				if got := uncheckableInside(a, "an argument of `"+tag+"`"); got != "" {
					return got
				}
			}
		}
	}
	return ""
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
		arms, ok := tags(n.Name, n.Args)
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
