package runtime

import (
	"errors"
	"strings"
	"testing"
)

// Removing a field from an entity emitted nothing at all: planEntity walked
// the DECLARED fields and never looked at what was left over. That is fine
// when the leftover column can hold a row on its own, and fatal when it
// cannot — Repo.create names only declared columns, so the database has to
// supply a value for an undeclared NOT NULL column with no default, and
// cannot. Every create then failed inside a request with a constraint error
// naming a column the source no longer mentions, on an app that booted clean.
//
// This is the goal's middle tier: the information is entirely in the live
// schema and the declaration, so the answer belongs at boot.

func TestOrphanNullableColumnIsKept(t *testing.T) {
	db := openTempDB(t)
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL, legacy TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO notes (body, legacy) VALUES ('keep me', 'old')`); err != nil {
		t.Fatal(err)
	}
	ent := VEntity{Table: "notes", Fields: []EntityField{
		{Name: "id", SQLType: "INTEGER", Serial: true},
		{Name: "body", SQLType: "TEXT", NotNull: true},
	}}
	summary, err := NewMigrator(db, []VEntity{ent}).Run()
	if err != nil {
		t.Fatalf("a leftover column that can hold a row is not a problem: %v", err)
	}
	if len(summary.Notes) == 0 || !strings.Contains(strings.Join(summary.Notes, " "), "legacy") {
		t.Fatalf("the note has to name the column, got: %v", summary.Notes)
	}
	// And the data is still there, which is the whole point of a note.
	assertRow(t, db, `SELECT legacy FROM notes WHERE id = 1`, "old")
}

func TestOrphanColumnWithDefaultIsKept(t *testing.T) {
	// NOT NULL is not the deciding fact on its own: a default means the
	// database can still supply the value, so creates keep working.
	db := openTempDB(t)
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL, legacy TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	ent := VEntity{Table: "notes", Fields: []EntityField{
		{Name: "id", SQLType: "INTEGER", Serial: true},
		{Name: "body", SQLType: "TEXT", NotNull: true},
	}}
	if _, err := NewMigrator(db, []VEntity{ent}).Run(); err != nil {
		t.Fatalf("a leftover column with a default is not a problem: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes (body) VALUES ('x')`); err != nil {
		t.Fatalf("creates have to keep working: %v", err)
	}
}

func TestOrphanRequiredColumnBlocksAtBoot(t *testing.T) {
	db := openTempDB(t)
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL, legacy TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO notes (body, legacy) VALUES ('keep me', 'old')`); err != nil {
		t.Fatal(err)
	}
	ent := VEntity{Table: "notes", Fields: []EntityField{
		{Name: "id", SQLType: "INTEGER", Serial: true},
		{Name: "body", SQLType: "TEXT", NotNull: true},
	}}

	// Today's behaviour, for the record: the app boots and the WRITE fails.
	if _, err := db.Exec(`INSERT INTO notes (body) VALUES ('x')`); err == nil {
		t.Fatal("this insert is supposed to be impossible: that is the bug")
	}

	_, err := NewMigrator(db, []VEntity{ent}).Run()
	var be *BlockedMigrationError
	if !errors.As(err, &be) {
		t.Fatalf("expected a blocked migration, got %T (%v)", err, err)
	}
	for _, want := range []string{"notes", "legacy"} {
		if !strings.Contains(be.Error(), want) {
			t.Errorf("the message must name %q, got:\n%s", want, be.Error())
		}
	}

	// The hint has to run verbatim, keep the data, and unblock the boot.
	script := indentedBlock(be.Hint)
	if script == "" {
		t.Fatalf("hint carries no script:\n%s", be.Hint)
	}
	if strings.Contains(strings.ToUpper(script), "DROP TABLE") {
		t.Errorf("a hint must never drop a table:\n%s", script)
	}
	if _, err := db.Exec(script); err != nil {
		t.Fatalf("hint SQL does not run: %v\n\n%s", err, script)
	}
	if _, err := NewMigrator(db, []VEntity{ent}).Run(); err != nil {
		t.Fatalf("still blocked after following the hint: %v", err)
	}
	// The column's data survived, in the side table the hint named.
	assertRow(t, db, `SELECT legacy FROM notes_legacy_kept LIMIT 1`, "old")
	// And a create works again, which is what was broken.
	if _, err := db.Exec(`INSERT INTO notes (body) VALUES ('x')`); err != nil {
		t.Fatalf("creates have to work after the fix: %v", err)
	}
}

func TestOrphanRequiredColumnInAnIndexIsDroppable(t *testing.T) {
	// DROP COLUMN refuses while an index still mentions the column, so the
	// hint has to lead with the DROP INDEX or it does not run.
	db := openTempDB(t)
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL, legacy TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX notes_uq_legacy ON notes(legacy)`); err != nil {
		t.Fatal(err)
	}
	ent := VEntity{Table: "notes", Fields: []EntityField{
		{Name: "id", SQLType: "INTEGER", Serial: true},
		{Name: "body", SQLType: "TEXT", NotNull: true},
	}}
	_, err := NewMigrator(db, []VEntity{ent}).Run()
	var be *BlockedMigrationError
	if !errors.As(err, &be) {
		t.Fatalf("expected a blocked migration, got %T (%v)", err, err)
	}
	script := indentedBlock(be.Hint)
	if !strings.Contains(strings.ToUpper(script), "DROP INDEX") {
		t.Fatalf("the script has to drop the index first:\n%s", script)
	}
	if _, err := db.Exec(script); err != nil {
		t.Fatalf("hint SQL does not run: %v\n\n%s", err, script)
	}
	if _, err := NewMigrator(db, []VEntity{ent}).Run(); err != nil {
		t.Fatalf("still blocked after following the hint: %v", err)
	}
}
