package runtime

import (
	"math/big"
	"strings"
	"testing"
)

// A service request is the one value the type system did not produce. These
// tests are the check that makes it one, so they are mostly about the cases a
// hostile or stale client actually sends.

func rec(pairs ...any) VRecord {
	fields := map[string]Value{}
	order := []string{}
	for i := 0; i < len(pairs); i += 2 {
		name := pairs[i].(string)
		order = append(order, name)
		fields[name] = pairs[i+1].(Value)
	}
	return VRecord{Fields: fields, Order: order}
}

func shape(kind WireKind, fields ...WireField) WireShape {
	return WireShape{Kind: kind, Fields: fields}
}

func TestWireAcceptsAWellFormedRequest(t *testing.T) {
	s := shape(WireRecord,
		WireField{Name: "title", Shape: WireShape{Kind: WireString}},
		WireField{Name: "minutes", Shape: WireShape{Kind: WireInt}},
		WireField{Name: "done", Shape: WireShape{Kind: WireBool}},
	)
	v := rec("title", VString{V: "nap"}, "minutes", VInt{V: 20}, "done", VBool{V: false})
	if err := CheckWire(v, s); err != nil {
		t.Fatalf("a matching request should pass: %v", err)
	}
}

func TestWireRejectsAWrongFieldType(t *testing.T) {
	s := shape(WireRecord, WireField{Name: "minutes", Shape: WireShape{Kind: WireInt}})
	err := CheckWire(rec("minutes", VString{V: "soon"}), s)
	if err == nil {
		t.Fatal("a string where an Int is declared should be rejected")
	}
	// The message is what makes this a 422 instead of a 500: it has to name
	// the field and say what arrived.
	if !strings.Contains(err.Error(), "minutes") {
		t.Errorf("the error should name the field, got: %v", err)
	}
	if !strings.Contains(err.Error(), "whole number") || !strings.Contains(err.Error(), "string") {
		t.Errorf("the error should say expected and actual, got: %v", err)
	}
}

func TestWireRejectsAMissingField(t *testing.T) {
	s := shape(WireRecord,
		WireField{Name: "title", Shape: WireShape{Kind: WireString}},
		WireField{Name: "minutes", Shape: WireShape{Kind: WireInt}},
	)
	err := CheckWire(rec("title", VString{V: "nap"}), s)
	if err == nil {
		t.Fatal("a missing required field should be rejected")
	}
	if !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), "minutes") {
		t.Errorf("the error should say which field is missing, got: %v", err)
	}
}

// A newer client sending a field this build does not know about is not an
// error; refusing it would make every deploy a breaking change.
func TestWireAllowsExtraFields(t *testing.T) {
	s := shape(WireRecord, WireField{Name: "title", Shape: WireShape{Kind: WireString}})
	v := rec("title", VString{V: "nap"}, "colour", VString{V: "blue"})
	if err := CheckWire(v, s); err != nil {
		t.Fatalf("an unknown extra field should be allowed: %v", err)
	}
}

func TestWireMaybeIsOptional(t *testing.T) {
	elem := WireShape{Kind: WireString}
	s := shape(WireRecord,
		WireField{Name: "note", Shape: WireShape{Kind: WireMaybe, Elem: &elem}},
	)
	if err := CheckWire(rec(), s); err != nil {
		t.Errorf("an absent Maybe should be Nothing, not an error: %v", err)
	}
	if err := CheckWire(rec("note", VCtor{Tag: "Nothing"}), s); err != nil {
		t.Errorf("an explicit Nothing should pass: %v", err)
	}
	ok := rec("note", VCtor{Tag: "Just", Args: []Value{VString{V: "hi"}}})
	if err := CheckWire(ok, s); err != nil {
		t.Errorf("a Just of the right type should pass: %v", err)
	}
	bad := rec("note", VCtor{Tag: "Just", Args: []Value{VInt{V: 1}}})
	if err := CheckWire(bad, s); err == nil {
		t.Error("a Just of the wrong type should be rejected")
	}
}

func TestWireChecksInsideLists(t *testing.T) {
	elem := WireShape{Kind: WireInt}
	s := shape(WireRecord,
		WireField{Name: "ids", Shape: WireShape{Kind: WireList, Elem: &elem}},
	)
	good := rec("ids", VList{Elements: []Value{VInt{V: 1}, VInt{V: 2}}})
	if err := CheckWire(good, s); err != nil {
		t.Fatalf("a list of Ints should pass: %v", err)
	}
	bad := rec("ids", VList{Elements: []Value{VInt{V: 1}, VString{V: "two"}}})
	err := CheckWire(bad, s)
	if err == nil {
		t.Fatal("a bad element should be rejected")
	}
	if !strings.Contains(err.Error(), "ids[1]") {
		t.Errorf("the error should point at the element, got: %v", err)
	}
}

// JSON has one number type. A client sending 3 for a Decimal means 3.
func TestWireAcceptsAnIntForADecimal(t *testing.T) {
	s := shape(WireRecord, WireField{Name: "amount", Shape: WireShape{Kind: WireDecimal}})
	if err := CheckWire(rec("amount", VInt{V: 3}), s); err != nil {
		t.Errorf("a whole number is a valid Decimal on the wire: %v", err)
	}
	dec := VDecimal{Coef: big.NewInt(350), Scale: 2}
	if err := CheckWire(rec("amount", dec), s); err != nil {
		t.Errorf("a decimal should pass: %v", err)
	}
	if err := CheckWire(rec("amount", VString{V: "3.50"}), s); err == nil {
		t.Error("a string should not pass as a Decimal")
	}
}

// The shape is deliberately lossy: what it cannot describe it does not assert
// about, because a wrong 422 on a valid request is worse than no check.
func TestWireAnyChecksNothing(t *testing.T) {
	s := shape(WireRecord, WireField{Name: "payload", Shape: WireShape{Kind: WireAny}})
	if err := CheckWire(rec("payload", VCtor{Tag: "Whatever"}), s); err != nil {
		t.Errorf("Any should accept anything: %v", err)
	}
}

func TestWireRejectsANonObjectWhereARecordIsDeclared(t *testing.T) {
	s := shape(WireRecord, WireField{Name: "title", Shape: WireShape{Kind: WireString}})
	if err := CheckWire(VString{V: "not an object"}, s); err == nil {
		t.Error("a bare string where a record is declared should be rejected")
	}
}
