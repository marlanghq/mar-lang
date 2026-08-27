package runtime

import (
	"errors"
	"strings"
	"testing"
)

// An Entity.enum column's constructor set was frozen at CREATE TABLE time
// forever: the migrator compares the STORAGE type, and a TEXT column with any
// set is just TEXT. Changing the type in Mar source emitted nothing, and the
// CHECK went on enforcing a set the program no longer declares.
//
// This needs no schema catalog. The old set is recorded, exactly, in the CHECK
// text the migrator itself wrote — `PRAGMA table_info` simply does not return
// it, and sqlite_master does.

func enumEntity(tags ...string) VEntity {
	return VEntity{Table: "tasks", Fields: []EntityField{
		{Name: "id", SQLType: "INTEGER", Serial: true},
		{Name: "title", SQLType: "TEXT", NotNull: true},
		{Name: "status", SQLType: "TEXT", NotNull: true, AcceptedCtors: tags},
	}}
}

func TestEnumTagAddedWidensTheConstraint(t *testing.T) {
	db := openTempDB(t)
	if _, err := NewMigrator(db, []VEntity{enumEntity("Todo", "Done")}).Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (title, status) VALUES ('a', 'Todo')`); err != nil {
		t.Fatal(err)
	}
	// Today's bug: the declaration lists Blocked and the database refuses it.
	if _, err := db.Exec(`INSERT INTO tasks (title, status) VALUES ('b', 'Blocked')`); err == nil {
		t.Fatal("the old CHECK is supposed to refuse this: that is the bug")
	}

	if _, err := NewMigrator(db, []VEntity{enumEntity("Todo", "Done", "Blocked")}).Run(); err != nil {
		t.Fatalf("widening a set accepts everything already stored: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (title, status) VALUES ('b', 'Blocked')`); err != nil {
		t.Fatalf("a declared constructor has to be writable: %v", err)
	}
	// And the row that was already there survived the rewrite.
	assertRow(t, db, `SELECT title || '/' || status FROM tasks WHERE id = 1`, "a/Todo")
}

func TestEnumTagRemovedWithNoRowsNarrows(t *testing.T) {
	db := openTempDB(t)
	if _, err := NewMigrator(db, []VEntity{enumEntity("Todo", "Done", "WontFix")}).Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (title, status) VALUES ('a', 'Todo')`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewMigrator(db, []VEntity{enumEntity("Todo", "Done")}).Run(); err != nil {
		t.Fatalf("nothing holds the departing tag, so narrowing is safe: %v", err)
	}
	// The constraint really did narrow.
	if _, err := db.Exec(`INSERT INTO tasks (title, status) VALUES ('b', 'WontFix')`); err == nil {
		t.Fatal("a constructor the type no longer declares must be refused by the database")
	}
	assertRow(t, db, `SELECT title FROM tasks WHERE id = 1`, "a")
}

func TestEnumTagRemovedWithRowsBlocksAtBoot(t *testing.T) {
	db := openTempDB(t)
	if _, err := NewMigrator(db, []VEntity{enumEntity("Todo", "Done", "WontFix")}).Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (title, status) VALUES ('a', 'WontFix')`); err != nil {
		t.Fatal(err)
	}
	_, err := NewMigrator(db, []VEntity{enumEntity("Todo", "Done")}).Run()
	var be *BlockedMigrationError
	if !errors.As(err, &be) {
		t.Fatalf("a row holding a departed constructor cannot be read, so the boot must stop: got %T (%v)", err, err)
	}
	for _, want := range []string{"tasks", "status", "WontFix", "1 row"} {
		if !strings.Contains(be.Error(), want) {
			t.Errorf("the message must mention %q, got:\n%s", want, be.Error())
		}
	}
	script := indentedBlock(be.Hint)
	if script == "" {
		t.Fatalf("hint carries no script:\n%s", be.Hint)
	}
	if strings.Contains(strings.ToUpper(script), "DROP TABLE") {
		t.Errorf("a hint must never drop a table:\n%s", script)
	}
	script = strings.ReplaceAll(script, "<value>", "Done")
	if _, err := db.Exec(script); err != nil {
		t.Fatalf("hint SQL does not run: %v\n\n%s", err, script)
	}
	if _, err := NewMigrator(db, []VEntity{enumEntity("Todo", "Done")}).Run(); err != nil {
		t.Fatalf("still blocked after following the hint: %v", err)
	}
	assertRow(t, db, `SELECT status FROM tasks WHERE id = 1`, "Done")
}

func TestEnumRenameWidensBeforeItBlocks(t *testing.T) {
	// A rename arrives as one addition and one removal. The widening has to
	// go out FIRST, or the operator's own UPDATE is rejected by a constraint
	// quoting a set their source no longer contains.
	db := openTempDB(t)
	if _, err := NewMigrator(db, []VEntity{enumEntity("Todo", "Doing")}).Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO tasks (title, status) VALUES ('a', 'Doing')`); err != nil {
		t.Fatal(err)
	}
	_, err := NewMigrator(db, []VEntity{enumEntity("Todo", "InProgress")}).Run()
	var be *BlockedMigrationError
	if !errors.As(err, &be) {
		t.Fatalf("expected a block, got %T (%v)", err, err)
	}
	// The widening applied on the way to the block, so the transitional
	// CHECK accepts both spellings and the one-line rename runs.
	if _, err := db.Exec(`UPDATE tasks SET status = 'InProgress' WHERE status = 'Doing'`); err != nil {
		t.Fatalf("the rename has to be possible after the widening: %v", err)
	}
	if _, err := NewMigrator(db, []VEntity{enumEntity("Todo", "InProgress")}).Run(); err != nil {
		t.Fatalf("the next boot narrows to the declared set: %v", err)
	}
	assertRow(t, db, `SELECT status FROM tasks WHERE id = 1`, "InProgress")
}

func TestEnumUnchangedEmitsNothing(t *testing.T) {
	// The floor: a set that did not change must not rewrite the table on
	// every boot.
	db := openTempDB(t)
	ent := enumEntity("Todo", "Done")
	if _, err := NewMigrator(db, []VEntity{ent}).Run(); err != nil {
		t.Fatal(err)
	}
	summary, err := NewMigrator(db, []VEntity{ent}).Run()
	if err != nil {
		t.Fatal(err)
	}
	if summary.HasChanges() {
		t.Fatalf("an unchanged entity must be a no-op, got: %+v", summary.Applied)
	}
}
