// Schema fingerprint: a deterministic hash of a SQLite database's
// structure. Used by the backup/restore flow to reject restores
// whose schema doesn't match the running app's schema.
//
// The fingerprint covers CREATE TABLE / CREATE INDEX statements as
// stored in sqlite_master. It deliberately excludes:
//
//   - sqlite_* internal tables (vary per SQLite version)
//   - rowid / page count / sequence values (data-dependent, not schema)
//
// Two databases with the same fingerprint are guaranteed to have
// the same column types, constraints, indexes: anything the app
// code might rely on. Different fingerprints mean a migration ran
// (or the file is corrupt / from a different app); restore against
// such a target should refuse rather than silently invite "data
// violates new constraint" failures at first write.

package admin

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"hash"
	"sort"
	"strings"
)

// SchemaFingerprint returns a stable hex-encoded SHA-256 of the
// database's schema. Returns an empty string + error if the schema
// can't be read.
//
// Determinism: rows are sorted by (type, name) so insertion order
// doesn't matter. Whitespace inside CREATE statements is collapsed
// : SQLite preserves user-provided formatting in sqlite_master.sql,
// and we don't want a re-formatting roundtrip to flip the hash.
func SchemaFingerprint(db *sql.DB) (string, error) {
	rows, err := db.Query(
		`SELECT type, name, sql FROM sqlite_master
		 WHERE name NOT LIKE 'sqlite_%'
		 AND sql IS NOT NULL`,
	)
	if err != nil {
		return "", fmt.Errorf("admin.SchemaFingerprint: %w", err)
	}
	defer rows.Close()

	type entry struct{ kind, name, sql string }
	var entries []entry
	for rows.Next() {
		var e entry
		if err := rows.Scan(&e.kind, &e.name, &e.sql); err != nil {
			return "", fmt.Errorf("admin.SchemaFingerprint scan: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("admin.SchemaFingerprint iter: %w", err)
	}

	// Sort for determinism: sqlite_master row order isn't guaranteed
	// across SQLite versions or after VACUUM.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].kind != entries[j].kind {
			return entries[i].kind < entries[j].kind
		}
		return entries[i].name < entries[j].name
	})

	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%s\x00%s\x00%s\x00", e.kind, e.name, normalizeSQL(e.sql))
	}
	if err := foldDeclaredTypes(h, db); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// declaredTypeCatalog is the table the migrator writes each column's DECLARED
// Mar type into. The name is duplicated from internal/runtime rather than
// imported, because that package imports this one's neighbours; the two are
// held together by a test in cmd/mar-runtime.
const declaredTypeCatalog = "_mar_column_types"

// DeclaredTypeCatalogName exposes the name the duplication test checks.
func DeclaredTypeCatalogName() string { return declaredTypeCatalog }

// foldDeclaredTypes folds the declared type of every column into the hash.
//
// sqlite_master is the STORAGE view, and the storage type erases the one thing
// a restore most needs to compare. Measured: two databases differing only in
// `Entity.decimal 2` against `Entity.decimal 3` fingerprinted IDENTICALLY,
// because `amount INTEGER` is the SQL either way. So a bundle taken at scale 2
// restored into a binary running scale 3 while restore-db printed
// "Schema: matches the bundle", and every stored coefficient came to mean a
// different amount — R$ 12,34 read back as R$ 1,234.
//
// The catalog records exactly what the SQL text erases, and its CREATE TABLE
// is already in the hash; what was missing is its CONTENT.
//
// A database with no catalog is one written before it existed. It contributes
// nothing rather than failing, which keeps the fingerprint of an old bundle
// what it always was.
func foldDeclaredTypes(h hash.Hash, db *sql.DB) error {
	rows, err := db.Query(
		`SELECT table_name, column_name, declared, scale FROM ` + declaredTypeCatalog +
			` ORDER BY table_name, column_name`)
	if err != nil {
		return nil // no catalog: a database older than it
	}
	defer rows.Close()
	for rows.Next() {
		var table, column, declared string
		var scale int
		if err := rows.Scan(&table, &column, &declared, &scale); err != nil {
			return fmt.Errorf("admin.SchemaFingerprint declared types: %w", err)
		}
		fmt.Fprintf(h, "declared\x00%s\x00%s\x00%s\x00%d\x00", table, column, declared, scale)
	}
	return rows.Err()
}

// normalizeSQL collapses whitespace so two semantically-identical
// CREATE statements with different formatting hash the same. It does
// NOT lowercase or otherwise touch the SQL: case is significant
// inside string literals and could change semantics.
func normalizeSQL(s string) string {
	// Replace any run of whitespace (space, tab, newline) with a
	// single space; trim leading/trailing.
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := true
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}
