package runtime

import (
	"strings"
	"testing"
)

// A migration step used to be one string handed to one db.Exec, and a step
// that needed several statements spelled them "BEGIN; ...; COMMIT" inside it.
// That does not roll back on the driver we ship: when a later statement
// failed, the earlier ones stayed committed. The rebuild step is a DROP
// followed by a CREATE, so a failing CREATE left the table simply gone, and
// every write after it went through as though nothing had happened.
//
// A migration is the one place where half of something is worse than none of
// it, so a step now runs as a real transaction, with its audit row inside it.

func TestMultiStatementStepRollsBackWholly(t *testing.T) {
	db := openTempDB(t)
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO notes (body) VALUES ('keep me')`); err != nil {
		t.Fatal(err)
	}
	m := NewMigrator(db, nil)
	if err := m.ensureMigrationMetaTable(); err != nil {
		t.Fatal(err)
	}

	// Second statement is invalid on purpose: this is the shape of a rebuild
	// whose CREATE does not compile.
	err := m.applyStep(MigrationStep{
		Kind:        StepRecreateEmpty,
		Table:       "notes",
		Description: "rebuild notes",
		SQLs: []string{
			`DROP TABLE notes`,
			`CREATE TABLE notes (this is not sql)`,
		},
	})
	if err == nil {
		t.Fatal("a step whose second statement is invalid must fail")
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM notes`).Scan(&n); err != nil {
		t.Fatalf("the table has to still be here: %v", err)
	}
	if n != 1 {
		t.Fatalf("the row has to still be here, got %d rows", n)
	}

	// And the ledger must not claim a migration that did not happen.
	var audits int
	if err := db.QueryRow(`SELECT count(*) FROM _mar_schema_migrations WHERE table_name = 'notes'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 0 {
		t.Fatalf("a failed step recorded %d audit rows; the ledger would lie to the next boot", audits)
	}
}

func TestMultiStatementStepCommitsWholly(t *testing.T) {
	db := openTempDB(t)
	if _, err := db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	m := NewMigrator(db, nil)
	if err := m.ensureMigrationMetaTable(); err != nil {
		t.Fatal(err)
	}
	step := MigrationStep{
		Kind:        StepRecreateEmpty,
		Table:       "notes",
		Description: "rebuild notes",
		SQLs: []string{
			`DROP TABLE notes`,
			`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL, tag TEXT NOT NULL)`,
		},
	}
	if err := m.applyStep(step); err != nil {
		t.Fatalf("a valid step must apply: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO notes (body, tag) VALUES ('a', 'b')`); err != nil {
		t.Fatalf("the rebuilt table has to be usable: %v", err)
	}
	// The audit row shows both statements the way an operator would paste
	// them back, not just the first.
	var sqlText string
	if err := db.QueryRow(`SELECT sql_text FROM _mar_schema_migrations WHERE table_name = 'notes'`).Scan(&sqlText); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DROP TABLE", "CREATE TABLE"} {
		if !strings.Contains(sqlText, want) {
			t.Errorf("the audit row must carry %q, got: %s", want, sqlText)
		}
	}
}
