// Schema fingerprint — a deterministic hash of a SQLite database's
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
// the same column types, constraints, indexes — anything the app
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
	"sort"
	"strings"
)

// SchemaFingerprint returns a stable hex-encoded SHA-256 of the
// database's schema. Returns an empty string + error if the schema
// can't be read.
//
// Determinism: rows are sorted by (type, name) so insertion order
// doesn't matter. Whitespace inside CREATE statements is collapsed
// — SQLite preserves user-provided formatting in sqlite_master.sql,
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

	// Sort for determinism — sqlite_master row order isn't guaranteed
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
	return hex.EncodeToString(h.Sum(nil)), nil
}

// normalizeSQL collapses whitespace so two semantically-identical
// CREATE statements with different formatting hash the same. It does
// NOT lowercase or otherwise touch the SQL — case is significant
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
