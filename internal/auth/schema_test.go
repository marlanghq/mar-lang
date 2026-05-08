package auth

import (
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// modernc.org/sqlite gives each *connection* a separate :memory:
	// database — so the goroutine pool's second connection wouldn't
	// see the tables Migrate ran on the first. Pinning to a single
	// connection makes the in-memory DB behave as one shared store
	// across all queries (including from background sweeper goroutines).
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateCreatesTables(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, table := range []string{"_mar_auth_codes", "_mar_auth_sessions"} {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
			table,
		).Scan(&name)
		if err != nil {
			t.Fatalf("table %s missing: %v", table, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	for i := 0; i < 3; i++ {
		if err := Migrate(db); err != nil {
			t.Fatalf("Migrate iteration %d: %v", i, err)
		}
	}
}

func TestSweepDeletesOldCodesAndSessions(t *testing.T) {
	db := openTestDB(t)
	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Now().Unix()
	stale := now - 48*3600     // expired 2 days ago — past the 1-day grace
	live := now + 600          // expires 10 min from now

	mustExec(t, db,
		`INSERT INTO _mar_auth_codes(email, code_hash, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		"alice@example.com", "h1", stale, stale)
	mustExec(t, db,
		`INSERT INTO _mar_auth_codes(email, code_hash, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		"alice@example.com", "h2", live, now)
	mustExec(t, db,
		`INSERT INTO _mar_auth_sessions(token_hash, user_id, expires_at, created_at, last_used_at) VALUES (?, ?, ?, ?, ?)`,
		"t1", 7, now-3600, now-7200, now-3600)
	mustExec(t, db,
		`INSERT INTO _mar_auth_sessions(token_hash, user_id, expires_at, created_at, last_used_at) VALUES (?, ?, ?, ?, ?)`,
		"t2", 7, now+86400, now, now)

	if err := SweepExpired(db, now); err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}

	if got := count(t, db, `SELECT COUNT(*) FROM _mar_auth_codes`); got != 1 {
		t.Errorf("codes after sweep = %d, want 1 (live one kept)", got)
	}
	if got := count(t, db, `SELECT COUNT(*) FROM _mar_auth_sessions`); got != 1 {
		t.Errorf("sessions after sweep = %d, want 1 (live one kept)", got)
	}
}

func mustExec(t *testing.T, db *sql.DB, sqlText string, args ...any) {
	t.Helper()
	if _, err := db.Exec(sqlText, args...); err != nil {
		t.Fatalf("exec %q: %v", sqlText, err)
	}
}

func count(t *testing.T, db *sql.DB, sqlText string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(sqlText).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", sqlText, err)
	}
	return n
}
