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

// The lenient half of this file was where the reachable 500s lived. Each of
// these was measured against a real server before it was a test: the check
// said yes, and the handler then received something it had not been checked
// against.

func TestMaybeAbsentBecomesNothing(t *testing.T) {
	s := WireShape{Kind: WireRecord, Fields: []WireField{
		{Name: "note", Shape: WireShape{Kind: WireMaybe, Elem: &WireShape{Kind: WireString}}},
	}}
	// Absent is legal...
	if err := CheckWire(rec(), s); err != nil {
		t.Fatalf("a missing optional field is Nothing: %v", err)
	}
	// ...and permitting it is only half the rule: the field has to BE there
	// afterwards, because the handler reads it whether or not it was sent.
	out, ok := CoerceWire(rec(), s).(VRecord)
	if !ok {
		t.Fatalf("expected a record, got %T", CoerceWire(rec(), s))
	}
	got, present := out.Fields["note"]
	if !present {
		t.Fatal("the optional field was accepted and then not supplied")
	}
	if c, ok := got.(VCtor); !ok || c.Tag != "Nothing" {
		t.Fatalf("note = %v, want Nothing", got)
	}
}

func TestMaybeNullBecomesNothing(t *testing.T) {
	// `null` decodes to VUnit. It means Nothing, and has to arrive as one.
	s := WireShape{Kind: WireMaybe, Elem: &WireShape{Kind: WireString}}
	if err := CheckWire(VUnit{}, s); err != nil {
		t.Fatalf("null is Nothing: %v", err)
	}
	if c, ok := CoerceWire(VUnit{}, s).(VCtor); !ok || c.Tag != "Nothing" {
		t.Fatalf("null coerced to %v, want Nothing", CoerceWire(VUnit{}, s))
	}
}

func TestMaybeRejectsRawValue(t *testing.T) {
	// An unwrapped value used to be waved through and then failed inside the
	// handler, which is the outcome this file exists to remove.
	s := WireShape{Kind: WireMaybe, Elem: &WireShape{Kind: WireString}}
	if err := CheckWire(VString{V: "raw"}, s); err == nil {
		t.Fatal("a raw value is not a Maybe")
	}
}

func TestDecimalAcceptsAnIntAndProducesOne(t *testing.T) {
	// JSON has one number type, so 3 for a Decimal field means 3. Accepting
	// it without producing a Decimal was a 500 inside Decimal.toString.
	s := WireShape{Kind: WireDecimal}
	if err := CheckWire(VInt{V: 3}, s); err != nil {
		t.Fatalf("an Int is a legal Decimal on the wire: %v", err)
	}
	if _, ok := CoerceWire(VInt{V: 3}, s).(VDecimal); !ok {
		t.Fatalf("coerced to %T, want VDecimal", CoerceWire(VInt{V: 3}, s))
	}
}

func TestAllOptionalRecordAcceptsAnEmptyBody(t *testing.T) {
	// An empty body assembles to VUnit before the check sees it. A record
	// whose fields are all optional is satisfied by `{}` — this was a live
	// false rejection: "the request should be an object, got nothing".
	s := WireShape{Kind: WireRecord, Fields: []WireField{
		{Name: "note", Shape: WireShape{Kind: WireMaybe, Elem: &WireShape{Kind: WireString}}},
	}}
	body := EmptyBodyAsRecord(VUnit{}, s)
	if err := CheckWire(body, s); err != nil {
		t.Fatalf("an all-optional record accepts an empty body: %v", err)
	}
	out, ok := CoerceWire(body, s).(VRecord)
	if !ok {
		t.Fatalf("expected a record, got %T", CoerceWire(body, s))
	}
	if c, ok := out.Fields["note"].(VCtor); !ok || c.Tag != "Nothing" {
		t.Fatalf("note = %v, want Nothing", out.Fields["note"])
	}
}

func TestRequiredFieldStillMissing(t *testing.T) {
	// The other half: reading VUnit as an empty object must not turn a
	// missing REQUIRED field into a pass, and the message names the field.
	s := WireShape{Kind: WireRecord, Fields: []WireField{
		{Name: "n", Shape: WireShape{Kind: WireInt}},
	}}
	err := CheckWire(EmptyBodyAsRecord(VUnit{}, s), s)
	if err == nil {
		t.Fatal("a required field is still required")
	}
	if !strings.Contains(err.Error(), "`n`") {
		t.Fatalf("the message must name the field, got: %v", err)
	}
}

func TestUnitIgnoresUnknownFields(t *testing.T) {
	// Records ignore extras deliberately, so `()` has to as well: it is one
	// rule, and the two halves disagreed.
	if err := CheckWire(rec("anything", VInt{V: 1}), WireShape{Kind: WireUnit}); err != nil {
		t.Fatalf("a `()` request ignores what it does not declare: %v", err)
	}
}

func TestCtorRejectsATagTheTypeDoesNotDeclare(t *testing.T) {
	s := WireShape{Kind: WireCtor, Tags: []string{"Draft", "Sent"}}
	if err := CheckWire(VCtor{Tag: "Sent"}, s); err != nil {
		t.Fatalf("a declared tag passes: %v", err)
	}
	err := CheckWire(VCtor{Tag: "Purple"}, s)
	if err == nil {
		t.Fatal("an undeclared tag reached a `case` the checker had proved total")
	}
	for _, want := range []string{"Draft", "Sent", "Purple"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the message must name the legal set and what arrived, got: %v", err)
		}
	}
	if err := CheckWire(VString{V: "Sent"}, s); err == nil {
		t.Fatal("a bare string is not a constructor on the wire")
	}
}
