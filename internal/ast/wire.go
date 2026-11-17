package ast

// A value that arrives from outside a Mar program — a service request body, a
// string handed to JSON.decode — was not produced by the type system. It is
// decoded by shape alone and then handed to code that was checked on the
// assumption that it has the declared type. Nothing in between compares the
// two, and that gap is where a checked program meets a 500.
//
// WireShape is the declared type, reduced to what checking a decoded value
// actually needs. It lives here, on the AST, because it is written by the
// typechecker (which knows the real type) and read by the runtimes (which must
// not import the typechecker: types are erased at that boundary, see ADR 0016).
// The tree is the channel those two already share — the same one that carries
// which integer literals became Decimals and which reference resolved to which
// native implementation.
//
// The reduction is deliberately lossy. It answers one question: "could this
// decoded value have come from a value of the declared type?", and anything it
// cannot answer becomes WireAny rather than a guess, because a wrong rejection
// of a valid request is worse than the failure it replaces.

type WireShape struct {
	Kind    WireKind
	Fields  []WireField   // Record: declared fields, in declaration order
	Elem    *WireShape    // List, Maybe: the element shape
	Members []WireShape   // Tuple: one shape per position, in order
	Tags    []string      // Ctor: the constructor names the type declares
	Args    [][]WireShape // Ctor: the argument shapes of each tag, same order as Tags

	// Name is the instantiation a Ctor shape came from (the canonical type
	// name with its arguments, `Main.Box Int`; just the name for a type
	// without parameters), and the one a Ref points back at. A shape is a
	// TREE and a recursive type is not, so the point where a type re-enters
	// itself names the cycle, and a checker follows it against the DATA,
	// which is finite. The arguments are part of the name because a union
	// inside another instantiation of itself (a Result of a Result) is a
	// different type, not a cycle.
	Name string
}

type WireKind string

const (
	WireInt     WireKind = "Int"
	WireString  WireKind = "String"
	WireBool    WireKind = "Bool"
	WireDecimal WireKind = "Decimal"
	WireTime    WireKind = "Time"
	WireDate    WireKind = "Date"
	WireChar    WireKind = "Char"
	WireUnit    WireKind = "()"
	WireList    WireKind = "List"
	WireMaybe   WireKind = "Maybe"
	WireRecord  WireKind = "Record"

	// WireTuple had no kind at all, which meant a tuple anywhere in a
	// request was waved through and then hit `tupleFirst: not a 2-tuple`
	// — and a tuple as the whole request type could never be called
	// successfully by anyone. A tuple's arity is part of its type, so
	// both the arity and the member shapes are checkable.
	WireTuple WireKind = "Tuple"

	// Set, Dict and Angle had no kind either, and the consequence was the
	// same shape as the tuple's, one step worse. Measured, on a service
	// whose request type says `Set Int`: a body of `{"tags":[1,2,3]}` — a
	// plain list where the wire form is `{"__set":[...]}` — passed the
	// guard, reached the handler, and answered 500 with
	// `Set.toList: not a Set`. The guard exists to answer 422 naming the
	// field instead of letting the handler fail downstream, and for these
	// three it could not.
	//
	// Elem carries the element shape for Set; for Dict, Members carries the
	// key shape then the value shape.
	// WireRef is the point where a type re-enters itself: `Name` is the
	// instantiation already being checked, higher up, spelled as that
	// Ctor's Name. In `type Comment = Comment String (List Comment)`, a
	// wrong value one level DEEPER is a 422 naming the position, exactly
	// like the same mistake at the first level.
	WireRef WireKind = "Ref"

	WireSet   WireKind = "Set"
	WireDict  WireKind = "Dict"
	WireAngle WireKind = "Angle"
	// WireDuration is a Duration, `{"__duration": ms}` on the wire, which
	// the decoders turn into one before the check.
	WireDuration WireKind = "Duration"

	// WireCtor is a union: the finite set of constructors the type
	// declares, and what each one carries.
	//
	// It started as nullary-only, which left a union with a PAYLOAD
	// falling through to WireAny — and that was the widest hole in the
	// guard. A tag the type does not declare, or the right tag with the
	// wrong arguments, reached a `case` the checker had proved total:
	// "no case branch matched", a 500, from an anonymous request. Worse,
	// a payload that violates its own declared type got written to the
	// database through it, which bricks an endpoint permanently and is
	// invisible to the checker.
	//
	// A union is the one shape where the legal values are enumerable, so
	// it is the last place that should have been unchecked.
	WireCtor WireKind = "Ctor"

	// WireAny is the escape hatch, for what genuinely cannot be checked:
	// an unresolved type variable, a function, and a union whose recursion
	// changes its own arguments, which no finite shape describes
	// (maxSameUnion in internal/typecheck/wireshape.go). A recursive type
	// that closes its cycle is a WireRef. Everything with a shape has a
	// kind of its own, because a position behind WireAny is one the guard
	// waves through.
	WireAny WireKind = "Any"
)

type WireField struct {
	Name  string
	Shape WireShape
}

// BodilessVerb reports whether a request with this HTTP verb carries no body.
// Its request value travels in the URL: path parameters in their slots, and
// the record's other fields as one JSON object in the `q` query parameter.
// The checker, the boot and the server's request assembly all ask here.
func BodilessVerb(verb string) bool {
	return verb == "GET" || verb == "DELETE"
}
