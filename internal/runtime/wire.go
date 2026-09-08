package runtime

import (
	"fmt"
	"math/big"
	"strings"

	"mar/internal/ast"
)

// A service request is the one value in a Mar program that the type system did
// not produce. It arrives as JSON from a browser, an app, or curl, is decoded
// by shape alone, and is then handed to a handler that was type-checked on the
// assumption that it is a `req`. Nothing in between compares the two.
//
// That is the whole defect. `{"minutes": "soon"}` against `{ minutes : Int }`
// builds a VString, the handler adds it to something, and the failure surfaces
// as `+: expected Int` from inside a builtin, a 500, with a message about
// arithmetic, on a request that should have been a 400 naming the field.
//
// WireShape is the declared input type, reduced to what checking a decoded
// value actually needs. The typechecker knows the real type; the runtime must
// not import the typechecker (types are erased at this boundary, see ADR 0016),
// so the shape is derived at load time by internal/project, which already sits
// on both sides, and stamped onto the service, next to its origin name.
// The shape vocabulary lives on the AST (internal/ast/wire.go) because the
// typechecker writes it and the runtime reads it. These aliases keep it
// spelled `runtime.WireShape` at every call site here, where the reader is
// already thinking about values rather than trees.
type (
	WireShape = ast.WireShape
	WireKind  = ast.WireKind
	WireField = ast.WireField
)

const (
	WireInt     = ast.WireInt
	WireString  = ast.WireString
	WireBool    = ast.WireBool
	WireDecimal = ast.WireDecimal
	WireTime    = ast.WireTime
	WireChar    = ast.WireChar
	WireUnit    = ast.WireUnit
	WireList    = ast.WireList
	WireMaybe   = ast.WireMaybe
	WireRecord  = ast.WireRecord
	WireTuple   = ast.WireTuple
	WireSet     = ast.WireSet
	WireDict    = ast.WireDict
	WireAngle   = ast.WireAngle
	WireRef     = ast.WireRef
	WireCtor    = ast.WireCtor
	WireAny     = ast.WireAny
)

// CheckWire reports whether a decoded request matches the declared input type,
// naming the exact field and what was wrong. The message is user-facing: it
// answers a 400 to whoever sent the request, so it says what was expected and
// what arrived, and never leaks anything else.
func CheckWire(v Value, s WireShape) error {
	// The named shapes seen on the way down, so a Ref can point back at the
	// type it is inside. A shape is a TREE and a recursive type is not: the
	// point where one re-entered itself used to become WireAny, checking
	// nothing from there down. Following the cycle against the DATA
	// terminates, because the data is finite.
	return checkWireAt(v, s, "")
}

func checkWireAt(v Value, s WireShape, path string) error {
	return checkWireIn(v, s, path, map[string]WireShape{})
}

func checkWireIn(v Value, s WireShape, path string, seen map[string]WireShape) error {
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
		// Accepting it was only half the job, though — the handler then got
		// a VInt where it was checked against a Decimal, and died with
		// `Decimal.toString: expected Decimal`. Accepting a value means
		// making it into the value the handler was promised, so this one
		// coerces rather than merely permits. See coerceWire.
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
		// Anything that carries no declared field satisfies a `()` request,
		// which means an object with unknown keys does too. Records already
		// ignore extras a few lines down, deliberately and for a stated
		// reason; `()` rejecting them was an accident of writing the simple
		// case first, and it made the two halves of one rule disagree.
		switch v.(type) {
		case VUnit, VRecord:
		default:
			return bad("empty")
		}

	case WireMaybe:
		// Absent means Nothing. Everything else has to be a Maybe the
		// decoder recognised, and its contents have to match.
		//
		// A raw `null` and a raw unwrapped value used to be waved through
		// here, on the theory that leniency was the safe direction. It was
		// not: both reach the handler as something that is not a Maybe, and
		// `{"a":null}` and `{"a":7}` against a `Maybe Int` field were 500s,
		// which is the outcome this whole file exists to remove.
		if isWireAbsent(v) {
			return nil
		}
		c, ok := v.(VCtor)
		if !ok {
			return bad("a Maybe (leave the field out for Nothing)")
		}
		switch {
		case c.Tag == "Nothing":
			return nil
		case c.Tag == "Just" && len(c.Args) == 1:
			if s.Elem == nil {
				return nil
			}
			return checkWireIn(c.Args[0], *s.Elem, path, seen)
		}
		return bad("a Maybe (leave the field out for Nothing)")

	case WireCtor:
		// Remember this shape under its type name so a Ref below can find
		// it. Copying the map keeps sibling branches independent.
		if s.Name != "" {
			next := make(map[string]WireShape, len(seen)+1)
			for k, v := range seen {
				next[k] = v
			}
			next[s.Name] = s
			seen = next
		}
		c, ok := v.(VCtor)
		if !ok {
			return bad("one of " + strings.Join(quoteEach(s.Tags), ", "))
		}
		for i, tag := range s.Tags {
			if c.Tag != tag {
				continue
			}
			// The right tag still has to carry the right things. A
			// constructor's arguments are part of its type, and letting
			// them through unchecked was how a value that violates its own
			// declared type reached a handler — and, through one, the
			// database.
			var want []WireShape
			if i < len(s.Args) {
				want = s.Args[i]
			}
			if len(c.Args) != len(want) {
				return fmt.Errorf("%s: `%s` takes %s, got %d",
					where(), tag, plural(len(want), "value"), len(c.Args))
			}
			for j, arg := range c.Args {
				if err := checkWireIn(arg, want[j], fmt.Sprintf("%s.%s[%d]", path, tag, j), seen); err != nil {
					return err
				}
			}
			return nil
		}
		return fmt.Errorf("%s should be one of %s, got `%s`",
			where(), strings.Join(quoteEach(s.Tags), ", "), c.Tag)

	case WireRef:
		// The type this position re-enters, remembered on the way down. A
		// Ref with nothing to point at is a shape from a bundle older than
		// this kind; it asserts nothing rather than refusing, which is what
		// keeps an old bundle from becoming a new outage.
		target, ok := seen[s.Name]
		if !ok {
			return nil
		}
		return checkWireIn(v, target, path, seen)

	case WireSet:
		// The decoder rebuilds a `{"__set":[...]}` into a real Set, so
		// anything else here is a body that did not carry one — measured, a
		// plain `[1,2,3]` reached the handler and answered 500 with
		// `Set.toList: not a Set`.
		set, ok := v.(VSet)
		if !ok {
			return bad("a set")
		}
		for i, item := range set.Items {
			if err := checkWireIn(item, *s.Elem, fmt.Sprintf("%s[%d]", path, i), seen); err != nil {
				return err
			}
		}
		return nil

	case WireDict:
		d, ok := v.(VDict)
		if !ok {
			return bad("a dictionary")
		}
		for i, pair := range d.Pairs {
			if err := checkWireIn(pair.Key, s.Members[0], fmt.Sprintf("%s[%d].key", path, i), seen); err != nil {
				return err
			}
			if err := checkWireIn(pair.Value, s.Members[1], fmt.Sprintf("%s[%d].value", path, i), seen); err != nil {
				return err
			}
		}
		return nil

	case WireAngle:
		if _, ok := v.(VAngle); !ok {
			return bad("an angle")
		}
		return nil

	case WireTuple:
		// A tuple travels as a JSON array, and comes back from the decoder
		// as a LIST, because an array is all the wire has and nothing tells
		// the decoder which one was meant. So both arrive here, and the
		// arity — which is part of a tuple's type — is what separates a
		// well-formed one from anything else.
		//
		// There was no arm here at all: a tuple sailed through the guard
		// and met `tupleFirst: not a 2-tuple`, which is why a tuple as a
		// request type could not be called successfully by anyone.
		members := tupleMembers(v)
		if members == nil {
			return bad(fmt.Sprintf("a list of %s", plural(len(s.Members), "value")))
		}
		if len(members) != len(s.Members) {
			return fmt.Errorf("%s should be a list of %s, got %d",
				where(), plural(len(s.Members), "value"), len(members))
		}
		for i, m := range members {
			if err := checkWireIn(m, s.Members[i], fmt.Sprintf("%s[%d]", path, i), seen); err != nil {
				return err
			}
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
			if err := checkWireIn(item, *s.Elem, fmt.Sprintf("%s[%d]", path, i), seen); err != nil {
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
			if err := checkWireIn(sub, f.Shape, joinWirePath(path, f.Name), seen); err != nil {
				return err
			}
		}
		// Extra fields are allowed on purpose: a newer client sending a
		// field this build does not know about should not be a 400.
	}
	return nil
}

// quoteEach renders a tag set the way the sender writes it on the wire, so
// the message can be copied into a request rather than translated.
func quoteEach(tags []string) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = "`" + t + "`"
	}
	return out
}

// tupleMembers reads the positions out of whichever of the two forms arrived:
// a VTuple built inside the program, or the VList a JSON array decodes to.
// Returns nil for anything else.
func tupleMembers(v Value) []Value {
	switch n := v.(type) {
	case VTuple:
		return n.Members
	case VList:
		return n.Elements
	}
	return nil
}

// plural keeps the counts in these messages reading like English rather than
// like a format string: "1 value", "2 values".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
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
// the JSON: "a string", not "VString".
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

// CoerceWire makes a checked request into the value the handler was promised.
//
// Checking and coercing are two different jobs, and conflating them is what
// made the Decimal case a 500: the check accepted `{"amount": 3}` for a
// `Decimal` field, correctly, because JSON has one number type — and then
// handed the handler a VInt, which died inside `Decimal.toString`. Accepting
// a value has to mean producing the value that was accepted.
//
// It runs only after CheckWire has passed, so it never has to decide what a
// mismatch means: anything it does not recognise it returns untouched.
func CoerceWire(v Value, s WireShape) Value {
	return coerceWireIn(v, s, map[string]WireShape{})
}

// coerceWireIn threads the named shapes the same way checkWireIn does, so a
// coercion below a cycle closure — an Int that has to become a Decimal, say —
// happens at every depth rather than only above the first recursion.
func coerceWireIn(v Value, s WireShape, seen map[string]WireShape) Value {
	switch s.Kind {
	case WireRef:
		if target, ok := seen[s.Name]; ok {
			return coerceWireIn(v, target, seen)
		}
		return v

	case WireDecimal:
		if i, ok := v.(VInt); ok {
			return VDecimal{Coef: big.NewInt(i.V), Scale: 0}
		}

	case WireMaybe:
		// `null` decodes to VUnit, and an absent field arrives as nothing at
		// all. Both mean Nothing, and both used to be waved through the
		// check and then handed to a handler that had been checked against
		// a `Maybe` — a 500 on the most ordinary optional field there is.
		// Permitting absence without producing the Nothing was only half a
		// rule.
		if isWireAbsent(v) {
			return VCtor{Tag: "Nothing"}
		}
		if c, ok := v.(VCtor); ok && c.Tag == "Just" && len(c.Args) == 1 && s.Elem != nil {
			return VCtor{Tag: "Just", Args: []Value{coerceWireIn(c.Args[0], *s.Elem, seen)}}
		}

	case WireList:
		l, ok := v.(VList)
		if !ok || s.Elem == nil {
			return v
		}
		out := make([]Value, len(l.Elements))
		for i, item := range l.Elements {
			out[i] = coerceWireIn(item, *s.Elem, seen)
		}
		return VList{Elements: out}

	case WireTuple:
		// The decoder handed back a list; the handler was checked against a
		// tuple. Accepting the array means producing the tuple.
		members := tupleMembers(v)
		if members == nil || len(members) != len(s.Members) {
			return v
		}
		out := make([]Value, len(members))
		for i, m := range members {
			out[i] = coerceWireIn(m, s.Members[i], seen)
		}
		return VTuple{Members: out}

	case WireCtor:
		// Remembered under its type name so a Ref below can find it, the
		// same way checkWireIn does.
		if s.Name != "" {
			next := make(map[string]WireShape, len(seen)+1)
			for k, sh := range seen {
				next[k] = sh
			}
			next[s.Name] = s
			seen = next
		}
		c, ok := v.(VCtor)
		if !ok {
			return v
		}
		for i, tag := range s.Tags {
			if c.Tag != tag || i >= len(s.Args) || len(c.Args) != len(s.Args[i]) {
				continue
			}
			args := make([]Value, len(c.Args))
			for j, a := range c.Args {
				args[j] = coerceWireIn(a, s.Args[i][j], seen)
			}
			return VCtor{Tag: c.Tag, Args: args}
		}
		return v

	case WireRecord:
		rec, ok := v.(VRecord)
		if !ok {
			return v
		}
		fields := make(map[string]Value, len(rec.Fields))
		for k, val := range rec.Fields {
			fields[k] = val
		}
		order := append([]string(nil), rec.Order...)
		for _, f := range s.Fields {
			sub, present := fields[f.Name]
			if !present {
				// A declared optional field that nobody sent still has to
				// BE there: the handler reads `req.note` whether or not the
				// client wrote it, and a record without the key is not a
				// record of the declared type.
				if f.Shape.Kind == WireMaybe {
					fields[f.Name] = VCtor{Tag: "Nothing"}
					order = append(order, f.Name)
				}
				continue
			}
			fields[f.Name] = coerceWireIn(sub, f.Shape, seen)
		}
		return VRecord{Fields: fields, Order: order}
	}
	return v
}

// EmptyBodyAsRecord resolves an ambiguity that belongs to the service
// assembler rather than to the check.
//
// A request with no fields to assemble comes out as VUnit, because the
// assembler counts fields and cannot know whether the declared type is a
// record or `()`. Against a record shape that VUnit is the `{}` it came from,
// and a record whose fields are ALL optional is satisfied by `{}` — that was
// a live false rejection, "the request should be an object, got nothing", on
// a body that was correct.
//
// It is done here, at the one call site that has the ambiguity, rather than
// inside CheckWire, so that the check can go on saying the true thing
// everywhere else: `null` decoded at a record type is NOT an empty record,
// and JSON.decode has to be told so.
func EmptyBodyAsRecord(v Value, s WireShape) Value {
	if s.Kind != WireRecord {
		return v
	}
	if _, isRecord := v.(VRecord); isRecord {
		return v
	}
	if !isWireAbsent(v) {
		return v
	}
	return VRecord{Fields: map[string]Value{}}
}
