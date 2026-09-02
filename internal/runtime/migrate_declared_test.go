package runtime

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// The migrator diffs the SQLite STORAGE type, and INTEGER covers `Int`, `Time`
// and `Decimal` at any scale. Nothing in the database says which one the
// numbers in a column are.
//
// Measured on a running app holding R$ 12,34 as the coefficient 1234:
//
//	Entity.decimal 2 -> Entity.decimal 3
//	boot            said nothing, planned nothing
//	the stored row  was untouched: still 1234
//	the read        answered 1.234
//
// A one-character edit turned R$ 12,34 into R$ 1,234 on every row.

func migrateProbe(t *testing.T, entities ...VEntity) (*Migrator, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe.db")
	SetDBPath(path)
	t.Cleanup(func() { SetDBPath("") })
	db, err := OpenDB()
	if err != nil {
		t.Fatal(err)
	}
	m := NewMigrator(db, entities)
	if _, err := m.Run(); err != nil {
		t.Fatalf("first run: %v", err)
	}
	return m, db
}

func decimalEntity(scale int) VEntity {
	return VEntity{Table: "items", Fields: []EntityField{
		{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
		{Name: "amount", SQLType: "DECIMAL", NotNull: true, DecimalScale: scale},
	}}
}

func rerun(t *testing.T, db *sql.DB, ent VEntity) error {
	t.Helper()
	_, err := NewMigrator(db, []VEntity{ent}).Run()
	return err
}

func amounts(t *testing.T, db *sql.DB) []int64 {
	t.Helper()
	rows, err := db.Query(`SELECT amount FROM items ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

func TestWideningAScaleKeepsEveryAmount(t *testing.T) {
	_, db := migrateProbe(t, decimalEntity(2))
	if _, err := db.Exec(`INSERT INTO items (amount) VALUES (1234)`); err != nil {
		t.Fatal(err) // R$ 12,34
	}
	if err := rerun(t, db, decimalEntity(3)); err != nil {
		t.Fatalf("widening is exact and should apply: %v", err)
	}
	if got := amounts(t, db); len(got) != 1 || got[0] != 12340 {
		t.Fatalf("R$ 12,34 at scale 3 is 12340, got %v", got)
	}
}

func TestNarrowingAScaleAppliesWhenNothingIsLost(t *testing.T) {
	_, db := migrateProbe(t, decimalEntity(3))
	if _, err := db.Exec(`INSERT INTO items (amount) VALUES (12340)`); err != nil {
		t.Fatal(err) // R$ 12,340
	}
	if err := rerun(t, db, decimalEntity(2)); err != nil {
		t.Fatalf("every row divides evenly: %v", err)
	}
	if got := amounts(t, db); len(got) != 1 || got[0] != 1234 {
		t.Fatalf("got %v", got)
	}
}

func TestNarrowingAScaleBlocksWhenADigitWouldBeLost(t *testing.T) {
	// ADR 0004: the language does not round without being asked, and a
	// migration is not the place to start.
	_, db := migrateProbe(t, decimalEntity(3))
	if _, err := db.Exec(`INSERT INTO items (amount) VALUES (12340), (9999)`); err != nil {
		t.Fatal(err)
	}
	err := rerun(t, db, decimalEntity(2))
	if err == nil {
		t.Fatal("9.999 cannot be held at scale 2 without rounding")
	}
	for _, want := range []string{"amount", "scale 3", "scale 2", "UPDATE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
	// And nothing moved: a blocked plan applies none of itself.
	if got := amounts(t, db); len(got) != 2 || got[0] != 12340 || got[1] != 9999 {
		t.Fatalf("a blocked migration changed the data: %v", got)
	}
}

func TestAColumnThatChangesMeaningIsBlocked(t *testing.T) {
	// Int and Time are both INTEGER, so the storage diff sees nothing. The
	// numbers stand for different things and no conversion is right in
	// general.
	asInt := VEntity{Table: "rows", Fields: []EntityField{
		{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
		{Name: "count", SQLType: "INTEGER", NotNull: true},
	}}
	asTime := VEntity{Table: "rows", Fields: []EntityField{
		{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
		{Name: "count", SQLType: "TIMESTAMP", NotNull: true},
	}}
	_, db := migrateProbe(t, asInt)
	if _, err := db.Exec(`INSERT INTO rows (count) VALUES (7)`); err != nil {
		t.Fatal(err)
	}
	err := rerun(t, db, asTime)
	if err == nil {
		t.Fatal("a count is not a millisecond")
	}
	for _, want := range []string{"count", "Int", "Time"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message must mention %q, got: %v", want, err)
		}
	}
}

func TestAnUnchangedDeclarationPlansNothing(t *testing.T) {
	_, db := migrateProbe(t, decimalEntity(2))
	summary, err := NewMigrator(db, []VEntity{decimalEntity(2)}).Run()
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Applied) != 0 {
		t.Fatalf("nothing changed, got %d step(s)", len(summary.Applied))
	}
}

func TestAColumnTheCatalogHasNeverSeenIsAdopted(t *testing.T) {
	// A database that predates this table has been working, so what the
	// source says today is what the data in it means. Inventing an old type
	// for it would be a guess, and the guess would block a working app.
	_, db := migrateProbe(t, decimalEntity(2))
	if _, err := db.Exec(`DELETE FROM ` + declaredTypeCatalog); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO items (amount) VALUES (1234)`); err != nil {
		t.Fatal(err)
	}
	if err := rerun(t, db, decimalEntity(2)); err != nil {
		t.Fatalf("an unseen column is adopted, not guessed about: %v", err)
	}
	if got := amounts(t, db); len(got) != 1 || got[0] != 1234 {
		t.Fatalf("adoption must not touch the data: %v", got)
	}
	// And from here on it is protected.
	if err := rerun(t, db, decimalEntity(3)); err != nil {
		t.Fatal(err)
	}
	if got := amounts(t, db); got[0] != 12340 {
		t.Fatalf("got %v", got)
	}
}
