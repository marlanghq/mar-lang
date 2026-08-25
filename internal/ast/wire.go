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
}

type WireKind string

const (
	WireInt     WireKind = "Int"
	WireString  WireKind = "String"
	WireBool    WireKind = "Bool"
	WireDecimal WireKind = "Decimal"
	WireTime    WireKind = "Time"
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

	// WireAny is the escape hatch. It has to exist — a type variable has
	// no shape to assert, and neither does a function — but it had grown
	// to cover things that DO have one: tuples, unions with a payload,
	// and anything nested past an arbitrary depth. Each of those was a
	// place the guard announced itself and then checked nothing.
	//
	// What is left behind it is what genuinely cannot be checked: an
	// unresolved type variable, a function, and a RECURSIVE type at the
	// point where it closes its own cycle.
	WireAny WireKind = "Any"
)

type WireField struct {
	Name  string
	Shape WireShape
}
