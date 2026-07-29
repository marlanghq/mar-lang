package runtime

import (
	"fmt"
	"strings"
)

// A service request is the one value in a Mar program that the type system did
// not produce. It arrives as JSON from a browser, an app, or curl, is decoded
// by shape alone, and is then handed to a handler that was type-checked on the
// assumption that it is a `req`. Nothing in between compares the two.
//
// That is the whole defect. `{"minutes": "soon"}` against `{ minutes : Int }`
// builds a VString, the handler adds it to something, and the failure surfaces
// as `+: expected Int` from inside a builtin — a 500, with a message about
// arithmetic, on a request that should have been a 400 naming the field.
//
// WireShape is the declared input type, reduced to what checking a decoded
// value actually needs. The typechecker knows the real type; the runtime must
// not import the typechecker (types are erased at this boundary, see ADR 0016),
// so the shape is derived at load time by internal/project — which already sits
// on both sides — and stamped onto the service, next to its origin name.
type WireShape struct {
	Kind   WireKind
	Fields []WireField // Record: declared fields, in declaration order
	Elem   *WireShape  // List, Maybe: the element shape
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

// CheckWire reports whether a decoded request matches the declared input type,
// naming the exact field and what was wrong. The message is user-facing: it
// answers a 400 to whoever sent the request, so it says what was expected and
// what arrived, and never leaks anything else.
func CheckWire(v Value, s WireShape) error {
	return checkWireAt(v, s, "")
}

func checkWireAt(v Value, s WireShape, path string) error {
	where := func() string {
		if path == "" {
			return "the request"
		}
		return "`" + path + "`"
	}
	bad := func(want string) error {
		return fmt.Errorf("%s should be %s, got %s", where(), want, wireDescribe(v))
	}

	switch s.Kind {
	case WireAny:
		return nil

	case WireInt:
		if _, ok := v.(VInt); !ok {
			return bad("a whole number")
		}
	case WireString:
		if _, ok := v.(VString); !ok {
			return bad("a string")
		}
	case WireBool:
		if _, ok := v.(VBool); !ok {
			return bad("true or false")
		}
	case WireChar:
		if _, ok := v.(VChar); !ok {
			return bad("a single character")
		}
	case WireDecimal:
		// An Int is a legal Decimal on the wire: JSON has one number type,
		// and a client sending 3 for a Decimal field means 3, not an error.
		switch v.(type) {
		case VDecimal, VInt:
		default:
			return bad("a number")
		}
	case WireTime:
		// Times travel as {"__time": "<ISO-8601>"}; decodeJSON has already
		// turned a well-formed one into a VTime.
		if _, ok := v.(VTime); !ok {
			return bad("a time (an ISO-8601 string under \"__time\")")
		}
	case WireUnit:
		if _, ok := v.(VUnit); !ok {
			return bad("empty")
		}

	case WireMaybe:
		// Absent and null both mean Nothing; the decoder has already made
		// that a VCtor. Anything else has to match the element shape.
		if isWireAbsent(v) {
			return nil
		}
		if c, ok := v.(VCtor); ok {
			if c.Tag == "Nothing" {
				return nil
			}
			if c.Tag == "Just" && len(c.Args) == 1 && s.Elem != nil {
				return checkWireAt(c.Args[0], *s.Elem, path)
			}
			return nil
		}
		if s.Elem != nil {
			return checkWireAt(v, *s.Elem, path)
		}

	case WireList:
		l, ok := v.(VList)
		if !ok {
			return bad("a list")
		}
		if s.Elem == nil {
			return nil
		}
		for i, item := range l.Elements {
			if err := checkWireAt(item, *s.Elem, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}

	case WireRecord:
		rec, ok := v.(VRecord)
		if !ok {
			return bad("an object")
		}
		for _, f := range s.Fields {
			sub, present := rec.Fields[f.Name]
			if !present {
				// A missing Maybe is Nothing; a missing anything else is
				// the single most common wire bug, so it gets its own
				// message rather than "should be a whole number, got
				// nothing".
				if f.Shape.Kind == WireMaybe {
					continue
				}
				return fmt.Errorf("%s is missing the field `%s`", where(), f.Name)
			}
			if err := checkWireAt(sub, f.Shape, joinWirePath(path, f.Name)); err != nil {
				return err
			}
		}
		// Extra fields are allowed on purpose: a newer client sending a
		// field this build does not know about should not be a 400.
	}
	return nil
}

func joinWirePath(path, field string) string {
	if path == "" {
		return field
	}
	return path + "." + field
}

// isWireAbsent covers the two ways "no value" arrives before the decoder has
// had a chance to tag it.
func isWireAbsent(v Value) bool {
	if v == nil {
		return true
	}
	_, isUnit := v.(VUnit)
	return isUnit
}

// wireDescribe names what actually arrived, in the vocabulary of whoever sent
// the JSON — "a string", not "VString".
func wireDescribe(v Value) string {
	switch t := v.(type) {
	case nil:
		return "nothing"
	case VInt:
		return "a whole number"
	case VDecimal:
		return "a number"
	case VString:
		return "a string"
	case VBool:
		return "true or false"
	case VChar:
		return "a character"
	case VList:
		return "a list"
	case VRecord:
		if len(t.Order) == 0 {
			return "an empty object"
		}
		return "an object with " + strings.Join(t.Order, ", ")
	case VUnit:
		return "nothing"
	case VTime:
		return "a time"
	case VCtor:
		return "`" + t.Tag + "`"
	}
	return "something else"
}
