package runtime

import (
	"strings"
	"testing"
)

// vList builds a VList from elements (helper to keep tests terse).
func vList(xs ...Value) VList { return VList{Elements: xs} }

func TestEntityEnum_AcceptsZeroArgCtors(t *testing.T) {
	out, err := makeEnumColumnConstructor([]Value{
		vList(VCtor{Tag: "Member"}, VCtor{Tag: "Admin"}),
		VConstraint{NotNull: true},
	})
	if err != nil {
		t.Fatalf("Entity.enum: %v", err)
	}
	col, ok := out.(VColumn)
	if !ok {
		t.Fatalf("Entity.enum did not return a VColumn: %T", out)
	}
	if col.SQLType != "TEXT" {
		t.Errorf("SQLType = %q, want TEXT", col.SQLType)
	}
	if !col.NotNull {
		t.Errorf("NotNull = false, want true")
	}
	if len(col.AcceptedCtors) != 2 || col.AcceptedCtors[0] != "Member" || col.AcceptedCtors[1] != "Admin" {
		t.Errorf("AcceptedCtors = %v, want [Member Admin]", col.AcceptedCtors)
	}
}

func TestEntityEnum_RejectsEmptyList(t *testing.T) {
	_, err := makeEnumColumnConstructor([]Value{
		vList(),
		VConstraint{NotNull: true},
	})
	if err == nil {
		t.Fatal("expected error for empty enum list")
	}
	if !strings.Contains(err.Error(), "at least one") {
		t.Errorf("error = %q, want mention of 'at least one'", err)
	}
}

func TestEntityEnum_RejectsCtorWithArgs(t *testing.T) {
	_, err := makeEnumColumnConstructor([]Value{
		vList(VCtor{Tag: "Tagged", Args: []Value{VInt{V: 1}}}),
		VConstraint{NotNull: true},
	})
	if err == nil {
		t.Fatal("expected error for ctor with args")
	}
	if !strings.Contains(err.Error(), "zero-arg") {
		t.Errorf("error = %q, want mention of 'zero-arg'", err)
	}
}

func TestEntityEnum_RejectsDuplicates(t *testing.T) {
	_, err := makeEnumColumnConstructor([]Value{
		vList(VCtor{Tag: "Admin"}, VCtor{Tag: "Admin"}),
		VConstraint{NotNull: true},
	})
	if err == nil {
		t.Fatal("expected error for duplicate ctor")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error = %q, want mention of 'duplicate'", err)
	}
}

func TestEntityEnum_RejectsNonCtorElement(t *testing.T) {
	_, err := makeEnumColumnConstructor([]Value{
		vList(VString{V: "Member"}),
		VConstraint{NotNull: true},
	})
	if err == nil {
		t.Fatal("expected error for VString in enum list")
	}
}

// SQL encode/decode roundtrip --------------------------------------

func enumField() EntityField {
	return EntityField{
		Name:          "role",
		SQLType:       "TEXT",
		NotNull:       true,
		AcceptedCtors: []string{"Member", "Admin"},
	}
}

func TestEnumField_EncodeAcceptsCtor(t *testing.T) {
	f := enumField()
	got, err := marValueToSQLForField(&f, VCtor{Tag: "Admin"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got != "Admin" {
		t.Errorf("encode = %v, want %q", got, "Admin")
	}
}

func TestEnumField_EncodeRejectsUnknownTag(t *testing.T) {
	f := enumField()
	_, err := marValueToSQLForField(&f, VCtor{Tag: "Owner"})
	if err == nil {
		t.Fatal("expected error for tag not in accepted set")
	}
	if !strings.Contains(err.Error(), "accepted set") {
		t.Errorf("error = %q, want 'accepted set'", err)
	}
}

func TestEnumField_EncodeRejectsString(t *testing.T) {
	f := enumField()
	_, err := marValueToSQLForField(&f, VString{V: "Admin"})
	if err == nil {
		t.Fatal("expected error: enum column can't accept VString")
	}
	if !strings.Contains(err.Error(), "constructor") {
		t.Errorf("error = %q, want 'constructor'", err)
	}
}

func TestEnumField_EncodeRejectsCtorWithArgs(t *testing.T) {
	f := enumField()
	_, err := marValueToSQLForField(&f, VCtor{Tag: "Admin", Args: []Value{VInt{V: 1}}})
	if err == nil {
		t.Fatal("expected error: enum column can't accept tagged variant")
	}
}

func TestEnumField_DecodeReturnsCtor(t *testing.T) {
	f := enumField()
	got := mustDecodeColumn(t, f, "Admin")
	ctor, ok := got.(VCtor)
	if !ok {
		t.Fatalf("decode = %T, want VCtor", got)
	}
	if ctor.Tag != "Admin" || len(ctor.Args) != 0 {
		t.Errorf("decode = %v, want VCtor{Admin}", ctor)
	}
}

func TestEnumField_DecodeAcceptsBytes(t *testing.T) {
	// SQLite drivers sometimes return TEXT as []byte.
	f := enumField()
	got := mustDecodeColumn(t, f, []byte("Member"))
	ctor, ok := got.(VCtor)
	if !ok || ctor.Tag != "Member" {
		t.Fatalf("decode([]byte) = %v, want VCtor{Member}", got)
	}
}

func TestEnumField_DecodeUnknownTagIsRefused(t *testing.T) {
	// This test asserted the opposite, and the reason it gave was that a
	// caller could then detect the stray value by pattern-matching. Mar
	// cannot do that: every `case` is total over the constructors the type
	// declares (ADR 0022), so a tag outside the set matches NO branch. The
	// value came back as the declared type, and the failure surfaced
	// somewhere downstream as "no case branch matched" — on a program the
	// checker had proved total.
	//
	// A row can hold one of these: it predates a change to the type, or it
	// was written by something that bypassed the CHECK constraint. Either
	// way it is not a value of the type, and the read says so, naming the
	// column and the set it should have been in.
	f := enumField()
	_, err := decodeColumn("users", f, "Owner")
	if err == nil {
		t.Fatal("a tag the type does not declare is not a value of that type")
	}
	for _, want := range []string{"users", "Owner", "Member", "Admin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

// migration -----------------------------------------------------------

func TestBuildCreateTableSQL_EmitsCheckForEnum(t *testing.T) {
	entity := VEntity{
		Table: "users",
		Fields: []EntityField{
			{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
			{Name: "email", SQLType: "TEXT", NotNull: true},
			{
				Name:          "role",
				SQLType:       "TEXT",
				NotNull:       true,
				AcceptedCtors: []string{"Member", "Admin"},
			},
		},
	}
	sql := buildCreateTableSQL(entity)
	want := "CHECK(role IN ('Member', 'Admin'))"
	if !strings.Contains(sql, want) {
		t.Errorf("CREATE TABLE missing CHECK clause.\nGot:  %s\nWant substring: %s", sql, want)
	}
	// The NOT NULL must come before the CHECK so the column declaration
	// stays valid SQLite syntax.
	if !strings.Contains(sql, "role TEXT NOT NULL CHECK") {
		t.Errorf("CREATE TABLE has malformed enum column.\nGot: %s", sql)
	}
}

// mustDecodeColumn keeps these tests focused on the decoded VALUE. decodeColumn
// gained an error channel when Int became 53 bits (a 64-bit row can hold a
// number Mar cannot represent); the range itself is covered separately, so an
// unexpected failure here should just stop the test.
func mustDecodeColumn(t *testing.T, f EntityField, raw any) Value {
	t.Helper()
	v, err := decodeColumn("t", f, raw)
	if err != nil {
		t.Fatalf("decodeColumn(%v): %v", raw, err)
	}
	return v
}
