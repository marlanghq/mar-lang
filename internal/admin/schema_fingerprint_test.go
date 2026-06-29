package admin

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openMemDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestSchemaFingerprint_Deterministic — same schema in different
// table-creation orders hashes the same. (The fingerprint sorts by
// (type, name) before hashing.)
//
// Note: SQLite stores sqlite_master.sql VERBATIM as the operator
// typed it. Two schemas with different formatting produce different
// fingerprints — this is fine for our use case (Mar's migrator is
// deterministic; same migrator version = byte-identical CREATE
// statements). We only need to be robust to insertion order, which
// is what this test verifies.
func TestSchemaFingerprint_Deterministic(t *testing.T) {
	a := openMemDB(t)
	b := openMemDB(t)

	mustExec(t, a, `CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL)`)
	mustExec(t, a, `CREATE TABLE posts (id INTEGER PRIMARY KEY, body TEXT)`)
	mustExec(t, a, `CREATE INDEX users_email ON users(email)`)

	// Same statements, different creation order.
	mustExec(t, b, `CREATE TABLE posts (id INTEGER PRIMARY KEY, body TEXT)`)
	mustExec(t, b, `CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT NOT NULL)`)
	mustExec(t, b, `CREATE INDEX users_email ON users(email)`)

	fa, err := SchemaFingerprint(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := SchemaFingerprint(b)
	if err != nil {
		t.Fatal(err)
	}
	if fa != fb {
		t.Errorf("expected identical fingerprints despite table-creation order\n  a=%s\n  b=%s",
			fa, fb)
	}
}

// TestSchemaFingerprint_NormalizeWhitespace — the formatting-tolerance
// part of the contract. Even though Mar's migrator is deterministic,
// we want minor whitespace variations (single space vs double, leading
// spaces) to hash the same so a stray edit doesn't break restore.
func TestSchemaFingerprint_NormalizeWhitespace(t *testing.T) {
	a := openMemDB(t)
	b := openMemDB(t)
	mustExec(t, a, `CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)`)
	// Same tokens, runs of whitespace collapsed.
	mustExec(t, b, `CREATE  TABLE   users   (id    INTEGER  PRIMARY  KEY,  email  TEXT)`)

	fa, _ := SchemaFingerprint(a)
	fb, _ := SchemaFingerprint(b)
	if fa != fb {
		t.Errorf("whitespace runs should collapse to same hash\n  a=%s\n  b=%s", fa, fb)
	}
}

// TestSchemaFingerprint_DifferentSchemas — adding a column changes
// the hash.
func TestSchemaFingerprint_DifferentSchemas(t *testing.T) {
	a := openMemDB(t)
	b := openMemDB(t)
	mustExec(t, a, `CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT)`)
	mustExec(t, b, `CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT, name TEXT)`)

	fa, _ := SchemaFingerprint(a)
	fb, _ := SchemaFingerprint(b)
	if fa == fb {
		t.Errorf("expected different fingerprints; both = %s", fa)
	}
}

// TestSchemaFingerprint_IgnoresData — inserting rows doesn't change
// the schema fingerprint.
func TestSchemaFingerprint_IgnoresData(t *testing.T) {
	db := openMemDB(t)
	mustExec(t, db, `CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`)
	before, _ := SchemaFingerprint(db)
	mustExec(t, db, `INSERT INTO notes (body) VALUES ('hello'), ('world')`)
	after, _ := SchemaFingerprint(db)
	if before != after {
		t.Errorf("data changes shouldn't affect schema fingerprint\n  before=%s\n  after=%s",
			before, after)
	}
}

// TestSchemaFingerprint_NoSchema — a fresh DB with no tables still
// produces a stable empty hash.
func TestSchemaFingerprint_NoSchema(t *testing.T) {
	db := openMemDB(t)
	got, err := SchemaFingerprint(db)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Error("empty schema should still produce a hex hash, not empty string")
	}
}

func mustExec(t *testing.T, db *sql.DB, sqlText string) {
	t.Helper()
	if _, err := db.Exec(sqlText); err != nil {
		t.Fatalf("exec %q: %v", sqlText, err)
	}
}
