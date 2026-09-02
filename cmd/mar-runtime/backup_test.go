package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// `mar-runtime backup OUT` used to report success on a database that did not
// exist. runtime.OpenDB CREATES a missing SQLite file, so a backup pointed at
// the wrong path made a brand-new empty database, snapshotted that, and
// printed a plausible byte count with exit 0 — to someone taking a backup
// before a migration.
//
// The guard is a stat before the open, which has no interesting failure mode.
// What can regress silently is the line that says what was captured, because
// a byte count alone reads the same either way.

func openProbe(t *testing.T, stmts ...string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return db
}

func TestAnEmptySnapshotSaysItRestoresToNothing(t *testing.T) {
	got := capturedSummary(openProbe(t))
	if !strings.Contains(got, "restores to nothing") {
		t.Fatalf("an empty database must say so, got %q", got)
	}
}

func TestTheSummaryCountsWhatWasCaptured(t *testing.T) {
	db := openProbe(t,
		`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`,
		`CREATE TABLE tags (id INTEGER PRIMARY KEY)`,
		`INSERT INTO notes (body) VALUES ('um'), ('dois'), ('tres')`,
		`INSERT INTO tags DEFAULT VALUES`,
	)
	got := capturedSummary(db)
	if got != "2 tables, 4 rows" {
		t.Fatalf("got %q", got)
	}
}

func TestTheSummaryIsSingularForOneOfEach(t *testing.T) {
	db := openProbe(t,
		`CREATE TABLE notes (id INTEGER PRIMARY KEY)`,
		`INSERT INTO notes DEFAULT VALUES`,
	)
	if got := capturedSummary(db); got != "1 table, 1 row" {
		t.Fatalf("got %q", got)
	}
}

func TestSQLiteInternalTablesAreNotCounted(t *testing.T) {
	// An AUTOINCREMENT table brings sqlite_sequence with it. Counting it
	// would make an empty database look like it holds something.
	db := openProbe(t,
		`CREATE TABLE notes (id INTEGER PRIMARY KEY AUTOINCREMENT)`,
		`INSERT INTO notes DEFAULT VALUES`,
	)
	got := capturedSummary(db)
	if strings.Contains(got, "2 table") {
		t.Fatalf("sqlite_sequence is not the app's data, got %q", got)
	}
}
