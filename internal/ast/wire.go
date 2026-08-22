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
	Kind   WireKind
	Fields []WireField // Record: declared fields, in declaration order
	Elem   *WireShape  // List, Maybe: the element shape
	Tags   []string    // Ctor: the constructor names the type declares
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

	// WireCtor is a union whose constructors all carry nothing — the
	// shape a status, a role or a category has. It used to fall through
	// to WireAny, and a tag the type does not declare then reached a
	// `case` the checker had proved total: "no case branch matched", a
	// 500, from a request. The set of legal tags is finite and known at
	// load time, so this is the one place a union CAN be checked.
	WireCtor WireKind = "Ctor"

	// WireAny is the escape hatch, and it has to exist: a request type can
	// legitimately be a custom union, a type variable, or something whose
	// wire form this package should not be asserting. Checking nothing is
	// the honest answer there, and it is strictly better than the previous
	// behaviour, which checked nothing everywhere.
	WireAny WireKind = "Any"
)

type WireField struct {
	Name  string
	Shape WireShape
}
