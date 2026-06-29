// Package admin owns the framework-built-in admin panel — schema,
// boot-time sync of the admins list, and (later phases) the auth
// flow + embedded Mar-source page rendering.
//
// The user's project never imports this package. The runtime
// (cmd/mar / cmd/mar-runtime) calls EnsureSchema once at boot, then
// SyncAdmins to reconcile _mar_admins with mar.json. Everything else
// is internal.
//
// See docs/admin-panel.md for the design.
package admin

import (
	"database/sql"
	"fmt"
	"strings"
)

// EnsureSchema creates the framework-managed admin tables if they
// don't exist yet. Idempotent. Called early on every boot before
// SyncAdmins.
//
// Tables created (all under the reserved _mar_admin_* prefix):
//
//   - _mar_admins          (email, createdAt, lastLoginAt)
//   - _mar_admin_codes     (hashed code, email, expiresAt, attempts)
//   - _mar_admin_sessions  (hashed token, email, expiresAt, createdAt)
//
// Schema choices:
//   - email is the natural key — no surrogate id. Sync inserts/deletes
//     by email; sessions/codes reference admins by email so deletes
//     can be done in one DELETE per email when an admin is removed.
//   - timestamps are stored as Unix epoch ms (INTEGER). Same convention
//     the user-auth tables use; lets us compare without timezone fuss.
//   - The codes/sessions hash columns are TEXT (base64 of HMAC-SHA256)
//     per the existing user-auth convention — auth.Hash returns
//     base64-encoded strings, and storing them as TEXT lets `mar
//     admin list` debug queries use plain SELECT without hex juggling.
func EnsureSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS _mar_admins (
			email        TEXT PRIMARY KEY NOT NULL,
			createdAt    INTEGER NOT NULL,
			lastLoginAt  INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS _mar_admin_codes (
			codeHash     TEXT PRIMARY KEY NOT NULL,
			email        TEXT NOT NULL,
			expiresAt    INTEGER NOT NULL,
			attempts     INTEGER NOT NULL DEFAULT 0,
			createdAt    INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS _mar_admin_codes_email
			ON _mar_admin_codes(email)`,
		`CREATE TABLE IF NOT EXISTS _mar_admin_sessions (
			tokenHash    TEXT PRIMARY KEY NOT NULL,
			email        TEXT NOT NULL,
			expiresAt    INTEGER NOT NULL,
			createdAt    INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS _mar_admin_sessions_email
			ON _mar_admin_sessions(email)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("admin schema: %w", err)
		}
	}
	return nil
}

// SyncAdmins reconciles _mar_admins with the declarative list from
// mar.json. Idempotent. Runs on every boot.
//
// For each email removed from the list (in DB but not in `desired`):
//   - DELETE FROM _mar_admins
//   - DELETE FROM _mar_admin_codes (revoke pending codes)
//   - DELETE FROM _mar_admin_sessions (revoke active sessions)
//
// For each email added (in `desired` but not in DB):
//   - INSERT INTO _mar_admins (email, createdAt = now)
//
// `now` is injected so tests can pin clock; production callers pass
// time.Now().UnixMilli().
//
// Returns the count of additions and removals applied, useful for
// boot-log diagnostics ("synced N admins: +X -Y").
func SyncAdmins(db *sql.DB, desired []string, nowMs int64) (added, removed int, err error) {
	desiredSet := make(map[string]bool, len(desired))
	for _, e := range desired {
		desiredSet[strings.ToLower(strings.TrimSpace(e))] = true
	}

	// Pull current state.
	rows, err := db.Query(`SELECT email FROM _mar_admins`)
	if err != nil {
		return 0, 0, fmt.Errorf("admin sync read: %w", err)
	}
	current := make(map[string]bool)
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("admin sync scan: %w", err)
		}
		current[email] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("admin sync iter: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("admin sync begin: %w", err)
	}
	defer tx.Rollback()

	// Removals.
	for email := range current {
		if !desiredSet[email] {
			if _, err := tx.Exec(`DELETE FROM _mar_admin_codes    WHERE email = ?`, email); err != nil {
				return 0, 0, fmt.Errorf("admin sync delete codes: %w", err)
			}
			if _, err := tx.Exec(`DELETE FROM _mar_admin_sessions WHERE email = ?`, email); err != nil {
				return 0, 0, fmt.Errorf("admin sync delete sessions: %w", err)
			}
			if _, err := tx.Exec(`DELETE FROM _mar_admins         WHERE email = ?`, email); err != nil {
				return 0, 0, fmt.Errorf("admin sync delete admin: %w", err)
			}
			removed++
		}
	}

	// Additions.
	for email := range desiredSet {
		if !current[email] {
			_, err := tx.Exec(
				`INSERT INTO _mar_admins (email, createdAt) VALUES (?, ?)`,
				email, nowMs,
			)
			if err != nil {
				return 0, 0, fmt.Errorf("admin sync insert: %w", err)
			}
			added++
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("admin sync commit: %w", err)
	}
	return added, removed, nil
}

// ListAdmins returns the current rows of _mar_admins (post-sync), in
// alphabetical order by email. Used by `mar-runtime admin list` (over
// SSH for production inspection) and by tests.
func ListAdmins(db *sql.DB) ([]Admin, error) {
	rows, err := db.Query(`
		SELECT email, createdAt, lastLoginAt
		FROM _mar_admins
		ORDER BY email
	`)
	if err != nil {
		return nil, fmt.Errorf("admin list: %w", err)
	}
	defer rows.Close()
	var out []Admin
	for rows.Next() {
		var a Admin
		var lastLogin sql.NullInt64
		if err := rows.Scan(&a.Email, &a.CreatedAtMs, &lastLogin); err != nil {
			return nil, fmt.Errorf("admin list scan: %w", err)
		}
		if lastLogin.Valid {
			a.LastLoginAtMs = lastLogin.Int64
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Admin is a single row from _mar_admins. Returned by ListAdmins.
type Admin struct {
	Email         string
	CreatedAtMs   int64
	LastLoginAtMs int64 // zero when never logged in
}

// Boot ensures the framework admin tables exist and reconciles the
// _mar_admins table with the desired list. Safe to call from
// boot-time wiring in mar dev and mar-runtime.
//
// `desired` should be the canonicalized list (lowercase, trimmed,
// deduped, sorted) — see cmd/mar.LoadAdminsFromManifest.
//
// Returns the (added, removed) counts so callers can log a one-line
// summary. Errors propagate up; ServeLive should refuse to boot if
// schema setup fails.
func Boot(db *sql.DB, desired []string, nowMs int64) (added, removed int, err error) {
	if db == nil {
		return 0, 0, nil
	}
	if err := EnsureSchema(db); err != nil {
		return 0, 0, err
	}
	return SyncAdmins(db, desired, nowMs)
}
