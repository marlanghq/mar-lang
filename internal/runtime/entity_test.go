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
	got := decodeColumn(f, "Admin")
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
	got := decodeColumn(f, []byte("Member"))
	ctor, ok := got.(VCtor)
	if !ok || ctor.Tag != "Member" {
		t.Fatalf("decode([]byte) = %v, want VCtor{Member}", got)
	}
}

func TestEnumField_DecodeUnknownTagStillReturnsCtor(t *testing.T) {
	// Defensive: a row whose enum column holds a value outside the
	// accepted set — e.g. someone manually edited the DB or the
	// CHECK constraint hasn't been applied yet — should still
	// surface as a VCtor so callers can detect it via pattern-
	// matching, not silently coerce to a string.
	f := enumField()
	got := decodeColumn(f, "Owner")
	ctor, ok := got.(VCtor)
	if !ok || ctor.Tag != "Owner" {
		t.Fatalf("decode unknown = %v, want VCtor{Owner}", got)
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
