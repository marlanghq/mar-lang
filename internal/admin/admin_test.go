package admin

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// openTestDB returns an in-memory SQLite handle with the schema
// already applied. Tests use it as the seed for sync/list scenarios.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	return db
}

// TestEnsureSchema_Idempotent — calling twice is a no-op (real boot
// scenario: every restart re-runs EnsureSchema).
func TestEnsureSchema_Idempotent(t *testing.T) {
	db := openTestDB(t)
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("second EnsureSchema: %v", err)
	}
}

// TestSyncAdmins_AddOnEmptyDB — typical first-boot case. mar.json
// declares two admins; DB is empty; both get inserted.
func TestSyncAdmins_AddOnEmptyDB(t *testing.T) {
	db := openTestDB(t)
	added, removed, err := SyncAdmins(db, []string{"a@x.com", "b@x.com"}, 1000)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if added != 2 {
		t.Errorf("added: got %d, want 2", added)
	}
	if removed != 0 {
		t.Errorf("removed: got %d, want 0", removed)
	}

	got, _ := ListAdmins(db)
	if len(got) != 2 {
		t.Fatalf("expected 2 admins; got %d", len(got))
	}
	if got[0].Email != "a@x.com" || got[1].Email != "b@x.com" {
		t.Errorf("expected sorted by email; got %+v", got)
	}
	if got[0].CreatedAtMs != 1000 {
		t.Errorf("createdAt: got %d, want 1000", got[0].CreatedAtMs)
	}
}

// TestSyncAdmins_Idempotent — running twice with the same desired
// list adds nothing on the second call (boot every time = no churn).
func TestSyncAdmins_Idempotent(t *testing.T) {
	db := openTestDB(t)
	if _, _, err := SyncAdmins(db, []string{"a@x.com"}, 1000); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	added, removed, err := SyncAdmins(db, []string{"a@x.com"}, 2000)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if added != 0 || removed != 0 {
		t.Errorf("idempotent sync should be zero churn; got +%d -%d", added, removed)
	}
	// createdAt for the existing row must NOT be bumped to nowMs (2000)
	// — re-sync of the same email should not appear as "the user was
	// just added".
	got, _ := ListAdmins(db)
	if got[0].CreatedAtMs != 1000 {
		t.Errorf("createdAt should be unchanged; got %d, want 1000", got[0].CreatedAtMs)
	}
}

// TestSyncAdmins_RemoveAlsoRevokesCodesAndSessions — the security
// guarantee from §4.1: removing an admin from mar.json wipes any
// pending codes + active sessions in the same transaction.
func TestSyncAdmins_RemoveAlsoRevokesCodesAndSessions(t *testing.T) {
	db := openTestDB(t)
	// Seed: two admins, both with a pending code + an active session.
	_, _, err := SyncAdmins(db, []string{"keep@x.com", "remove@x.com"}, 1000)
	if err != nil {
		t.Fatalf("seed sync: %v", err)
	}
	// Forge a code + session for each. The hashes are arbitrary
	// blobs — we're testing the sync's DELETE, not auth flow.
	for _, email := range []string{"keep@x.com", "remove@x.com"} {
		_, err := db.Exec(`
			INSERT INTO _mar_admin_codes (codeHash, email, expiresAt, createdAt)
			VALUES (?, ?, ?, ?)`,
			[]byte("hash-"+email), email, 9999999, 1000,
		)
		if err != nil {
			t.Fatalf("seed code: %v", err)
		}
		_, err = db.Exec(`
			INSERT INTO _mar_admin_sessions (tokenHash, email, expiresAt, createdAt)
			VALUES (?, ?, ?, ?)`,
			[]byte("token-"+email), email, 9999999, 1000,
		)
		if err != nil {
			t.Fatalf("seed session: %v", err)
		}
	}

	// Now remove "remove@x.com" via sync.
	added, removed, err := SyncAdmins(db, []string{"keep@x.com"}, 2000)
	if err != nil {
		t.Fatalf("sync removal: %v", err)
	}
	if added != 0 || removed != 1 {
		t.Errorf("expected +0 -1; got +%d -%d", added, removed)
	}

	// keep@x.com's code + session must still be there.
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM _mar_admin_codes WHERE email = ?`, "keep@x.com").Scan(&n)
	if n != 1 {
		t.Errorf("keep@x.com code count: got %d, want 1", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM _mar_admin_sessions WHERE email = ?`, "keep@x.com").Scan(&n)
	if n != 1 {
		t.Errorf("keep@x.com session count: got %d, want 1", n)
	}

	// remove@x.com's code + session must be gone.
	db.QueryRow(`SELECT COUNT(*) FROM _mar_admin_codes WHERE email = ?`, "remove@x.com").Scan(&n)
	if n != 0 {
		t.Errorf("remove@x.com code count: got %d, want 0 (revoked)", n)
	}
	db.QueryRow(`SELECT COUNT(*) FROM _mar_admin_sessions WHERE email = ?`, "remove@x.com").Scan(&n)
	if n != 0 {
		t.Errorf("remove@x.com session count: got %d, want 0 (revoked)", n)
	}
}

// TestSyncAdmins_LowercasesAndTrims — admins list might have mixed
// casing or stray whitespace from manual mar.json edits. The sync
// canonicalizes so "Foo@X.com" and "foo@x.com" don't double-insert.
func TestSyncAdmins_LowercasesAndTrims(t *testing.T) {
	db := openTestDB(t)
	added, _, err := SyncAdmins(db, []string{"  Foo@X.com  ", "foo@x.com"}, 1000)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if added != 1 {
		t.Errorf("expected dedupe to 1; got %d adds", added)
	}
	got, _ := ListAdmins(db)
	if len(got) != 1 || got[0].Email != "foo@x.com" {
		t.Errorf("expected single canonical row; got %+v", got)
	}
}

// TestSyncAdmins_AddAndRemoveTogether — typical mid-life sync where
// some admins come, others go.
func TestSyncAdmins_AddAndRemoveTogether(t *testing.T) {
	db := openTestDB(t)
	_, _, _ = SyncAdmins(db, []string{"a@x.com", "b@x.com"}, 1000)
	added, removed, err := SyncAdmins(db, []string{"b@x.com", "c@x.com"}, 2000)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if added != 1 {
		t.Errorf("added: got %d, want 1", added)
	}
	if removed != 1 {
		t.Errorf("removed: got %d, want 1", removed)
	}
	got, _ := ListAdmins(db)
	if len(got) != 2 || got[0].Email != "b@x.com" || got[1].Email != "c@x.com" {
		t.Errorf("post-sync admins: got %+v, want b@x.com + c@x.com", got)
	}
}
