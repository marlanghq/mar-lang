package runtime

import (
	"math"
	"strings"
	"testing"
)

// A row is the third thing a Mar program receives that the type system did
// not produce, after a request body and a service response. Those two are
// checked against their declared types; a row was not.
//
// SQLite is dynamically typed, so what a column HOLDS and what it was
// DECLARED as are two different facts, and decodeColumn used the declaration
// only to pick a branch — anything the branch did not recognise fell through
// to whatever the database handed back. These are the three ways that showed,
// each one a value that violates its own declared type, arriving in a program
// the checker had already proved.

func rowField(name, sqlType string, ctors []string) EntityField {
	return EntityField{Name: name, SQLType: sqlType, NotNull: true, AcceptedCtors: ctors}
}

func TestRowEnumOutsideTheDeclaredSetIsRefused(t *testing.T) {
	// The one that reached `no case branch matched`: the value came back AS
	// the declared type, and no branch of a total `case` could match it.
	f := rowField("status", "TEXT", []string{"Draft", "Sent"})
	if _, err := decodeColumn("notes", f, "Draft"); err != nil {
		t.Fatalf("a declared tag reads back: %v", err)
	}
	err := mustRowError(t, "notes", f, "Archived")
	for _, want := range []string{"notes", "status", "Archived", "Draft", "Sent"} {
		if !strings.Contains(err, want) {
			t.Errorf("the message must mention %q, got: %s", want, err)
		}
	}
}

func TestRowTextInANumericColumnIsRefused(t *testing.T) {
	// The fallback that let the database overrule the declaration: a TEXT
	// value in an INTEGER column used to arrive as a String, on a field the
	// type says is an Int.
	f := rowField("qty", "INTEGER", nil)
	err := mustRowError(t, "items", f, "seven")
	for _, want := range []string{"items", "qty", "whole number", "seven"} {
		if !strings.Contains(err, want) {
			t.Errorf("the message must mention %q, got: %s", want, err)
		}
	}
}

func TestRowNumberInATextColumnIsRefused(t *testing.T) {
	// And the same thing pointing the other way.
	f := rowField("name", "TEXT", nil)
	err := mustRowError(t, "items", f, int64(42))
	for _, want := range []string{"items", "name", "text", "42"} {
		if !strings.Contains(err, want) {
			t.Errorf("the message must mention %q, got: %s", want, err)
		}
	}
}

func TestRowFractionalNumberInAnIntColumnIsRefused(t *testing.T) {
	// 3.7 became 3, silently, on a column declared Int. An INTEGER column
	// can hold a REAL because SQLite lets it.
	f := rowField("qty", "INTEGER", nil)
	err := mustRowError(t, "items", f, 3.7)
	for _, want := range []string{"items", "qty", "3.7", "whole number"} {
		if !strings.Contains(err, want) {
			t.Errorf("the message must mention %q, got: %s", want, err)
		}
	}
	// An integral float IS that number, and still reads back.
	v, decErr := decodeColumn("items", f, 3.0)
	if decErr != nil {
		t.Fatalf("3.0 is the number 3: %v", decErr)
	}
	if n, ok := v.(VInt); !ok || n.V != 3 {
		t.Fatalf("3.0 read back as %v, want VInt 3", v)
	}
}

func TestRowBooleanColumnRefusesText(t *testing.T) {
	f := rowField("done", "BOOLEAN", nil)
	if _, err := decodeColumn("items", f, int64(1)); err != nil {
		t.Fatalf("SQLite stores a bool as 0/1: %v", err)
	}
	mustRowError(t, "items", f, "yes")
}

func mustRowError(t *testing.T, table string, f EntityField, raw any) string {
	t.Helper()
	_, err := decodeColumn(table, f, raw)
	if err == nil {
		t.Fatalf("%s.%s holding %v violates its declared type and must be refused", table, f.Name, raw)
	}
	return err.Error()
}

func TestRowHugeFloatIsRefusedRatherThanSaturated(t *testing.T) {
	// Found by attacking this session's own change. `storedInt` accepted any
	// integral float, and 1e300 is integral: converting it to int64 is
	// implementation-defined in Go, so it came out as some arbitrary number
	// and kept going. A saturated value reads as real data, which is exactly
	// the failure this function exists to remove.
	//
	// TIMESTAMP and DECIMAL go through the same helper and are not bounded
	// to Mar's 53-bit Int (a Time has its own range, and a Decimal's
	// coefficient is not an Int), so the bound that belongs here is int64's
	// own, and it has to hold for all three column types.
	for _, sqlType := range []string{"INTEGER", "TIMESTAMP", "DECIMAL"} {
		f := rowField("v", sqlType, nil)
		if _, err := decodeColumn("t", f, 1e300); err == nil {
			t.Errorf("%s: a float outside int64 must be refused, not saturated", sqlType)
		}
		if _, err := decodeColumn("t", f, math.Inf(1)); err == nil {
			t.Errorf("%s: infinity is not a whole number", sqlType)
		}
		if _, err := decodeColumn("t", f, math.NaN()); err == nil {
			t.Errorf("%s: NaN is not a whole number", sqlType)
		}
		// And the values that ARE representable still read back.
		if _, err := decodeColumn("t", f, 42.0); err != nil {
			t.Errorf("%s: 42.0 is the number 42: %v", sqlType, err)
		}
	}
}

// A TIMESTAMP holds a Time, so a stored millisecond outside the range of a
// Time is refused like an INTEGER outside Int: the program never holds a
// Time that no runtime can write or read back.
func TestRowTimestampOutsideTheRangeOfATimeIsRefused(t *testing.T) {
	f := rowField("at", "TIMESTAMP", nil)
	for _, ms := range []int64{MaxTimeMillis + 1, MinTimeMillis - 1, 1 << 62} {
		_, err := decodeColumn("events", f, ms)
		if err == nil || !strings.Contains(err.Error(), "Time out of range: ") ||
			!strings.Contains(err.Error(), "milliseconds from the database") {
			t.Errorf("%d: want the range refusal, got %v", ms, err)
		}
	}
	for _, ms := range []int64{MinTimeMillis, MaxTimeMillis, 1700000000123} {
		v, err := decodeColumn("events", f, ms)
		if err != nil || v.(VTime).Millis != ms {
			t.Errorf("%d: got %v, %v", ms, v, err)
		}
	}
}
