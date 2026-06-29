package auth

import (
	"database/sql"
	"fmt"
)

// Migrate creates the framework-managed auth tables if they're missing.
// Idempotent — safe to call on every dispatcher boot. Both tables use
// the `_mar_` prefix (reserved by the manifest checker) so they don't
// collide with user-defined entities.
func Migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS _mar_auth_codes (
            id          INTEGER PRIMARY KEY AUTOINCREMENT,
            email       TEXT    NOT NULL,
            code_hash   TEXT    NOT NULL,
            attempts    INTEGER NOT NULL DEFAULT 0,
            expires_at  INTEGER NOT NULL,
            created_at  INTEGER NOT NULL,
            locked_at   INTEGER
        )`,
		`CREATE INDEX IF NOT EXISTS _mar_auth_codes_email_idx
            ON _mar_auth_codes(email)`,
		`CREATE INDEX IF NOT EXISTS _mar_auth_codes_expires_idx
            ON _mar_auth_codes(expires_at)`,
		`CREATE TABLE IF NOT EXISTS _mar_auth_sessions (
            id            INTEGER PRIMARY KEY AUTOINCREMENT,
            token_hash    TEXT    NOT NULL UNIQUE,
            user_id       INTEGER NOT NULL,
            expires_at    INTEGER NOT NULL,
            created_at    INTEGER NOT NULL,
            last_used_at  INTEGER NOT NULL
        )`,
		`CREATE INDEX IF NOT EXISTS _mar_auth_sessions_token_idx
            ON _mar_auth_sessions(token_hash)`,
		`CREATE INDEX IF NOT EXISTS _mar_auth_sessions_user_idx
            ON _mar_auth_sessions(user_id)`,
	}
	for _, sqlText := range stmts {
		if _, err := db.Exec(sqlText); err != nil {
			return fmt.Errorf("auth.Migrate: %w", err)
		}
	}
	return nil
}

// SweepExpired deletes auth codes that expired more than a day ago,
// and sessions that expired at all. The 24h code grace lets us inspect
// recent activity if a user reports an issue. Sessions don't get the
// same grace — once expired they can't be honored anyway, so keeping
// them around just bloats the table.
//
// Called by the background sweeper started in StartSweeper. Idempotent
// and concurrency-safe (DELETE is atomic per statement); two calls
// racing each other are fine.
func SweepExpired(db *sql.DB, nowUnix int64) error {
	cutoff := nowUnix - 24*3600
	if _, err := db.Exec(`DELETE FROM _mar_auth_codes WHERE expires_at < ?`, cutoff); err != nil {
		return fmt.Errorf("auth.SweepExpired (codes): %w", err)
	}
	if _, err := db.Exec(`DELETE FROM _mar_auth_sessions WHERE expires_at < ?`, nowUnix); err != nil {
		return fmt.Errorf("auth.SweepExpired (sessions): %w", err)
	}
	return nil
}
