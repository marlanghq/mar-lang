package admin

import (
	"testing"
	"time"
)

const testSecret = "test-secret-32-bytes-long-padding-padding"

// TestIsAdmin_PresentAndAbsent — the gate-membership probe used by
// RequestCode to decide whether to send (vs silently no-op).
func TestIsAdmin_PresentAndAbsent(t *testing.T) {
	db := openTestDB(t)
	if _, _, err := SyncAdmins(db, []string{"admin@x.com"}, 1000); err != nil {
		t.Fatal(err)
	}
	if !IsAdmin(db, "admin@x.com") {
		t.Error("expected admin@x.com to be an admin")
	}
	if IsAdmin(db, "stranger@x.com") {
		t.Error("expected stranger@x.com to NOT be an admin")
	}
}

// TestIssueAndVerifyCode_HappyPath — issue + verify with the same
// plaintext returns OK and burns the row.
func TestIssueAndVerifyCode_HappyPath(t *testing.T) {
	db := openTestDB(t)
	now := time.UnixMilli(1_000_000)
	code, err := IssueCode(db, testSecret, "admin@x.com", now)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if len(code) != 6 {
		t.Errorf("code length: got %d, want 6", len(code))
	}
	res, err := VerifyCode(db, testSecret, "admin@x.com", code, now.Add(1*time.Second))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res != VerifyOK {
		t.Errorf("got %v, want VerifyOK", res)
	}
	// Code row must be gone.
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM _mar_admin_codes`).Scan(&count)
	if count != 0 {
		t.Errorf("expected code to be deleted; got %d rows", count)
	}
}

// TestVerifyCode_WrongCode — bad code returns Invalid + bumps
// attempts. Doesn't burn the row (so the right code can still be
// tried).
func TestVerifyCode_WrongCode(t *testing.T) {
	db := openTestDB(t)
	now := time.UnixMilli(1_000_000)
	_, _ = IssueCode(db, testSecret, "admin@x.com", now)

	res, err := VerifyCode(db, testSecret, "admin@x.com", "000000", now.Add(1*time.Second))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if res != VerifyInvalid {
		t.Errorf("got %v, want VerifyInvalid", res)
	}
	var attempts int
	db.QueryRow(`SELECT attempts FROM _mar_admin_codes`).Scan(&attempts)
	if attempts != 1 {
		t.Errorf("attempts: got %d, want 1", attempts)
	}
}

// TestVerifyCode_TooManyAttempts — after MaxCodeAttempts wrong
// guesses the row is deleted, and subsequent verifies return Invalid
// (no separate "locked" status leaking the lock signal).
func TestVerifyCode_TooManyAttempts(t *testing.T) {
	db := openTestDB(t)
	now := time.UnixMilli(1_000_000)
	_, _ = IssueCode(db, testSecret, "admin@x.com", now)

	for i := 0; i < MaxCodeAttempts-1; i++ {
		res, _ := VerifyCode(db, testSecret, "admin@x.com", "000000", now)
		if res != VerifyInvalid {
			t.Fatalf("attempt %d: got %v, want VerifyInvalid", i, res)
		}
	}
	// Final attempt — crosses the threshold.
	res, _ := VerifyCode(db, testSecret, "admin@x.com", "000000", now)
	if res != VerifyTooManyAttempts {
		t.Errorf("got %v, want VerifyTooManyAttempts", res)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM _mar_admin_codes`).Scan(&n)
	if n != 0 {
		t.Errorf("expected row to be deleted after lock; got %d rows", n)
	}
}

// TestVerifyCode_Expired — once the clock is past expiresAt, the
// lookup returns no row → Invalid (the row itself is left alone, a
// future sweeper would clean up).
func TestVerifyCode_Expired(t *testing.T) {
	db := openTestDB(t)
	now := time.UnixMilli(1_000_000)
	code, _ := IssueCode(db, testSecret, "admin@x.com", now)

	future := now.Add(CodeTTL + time.Second)
	res, _ := VerifyCode(db, testSecret, "admin@x.com", code, future)
	if res != VerifyInvalid {
		t.Errorf("expired code should be invalid; got %v", res)
	}
}

// TestSession_CreateAndLookup — happy path: create, look up,
// retrieve email.
func TestSession_CreateAndLookup(t *testing.T) {
	db := openTestDB(t)
	if _, _, err := SyncAdmins(db, []string{"admin@x.com"}, 1000); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1_000_000)
	tok, err := CreateSession(db, testSecret, "admin@x.com", now)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if tok == "" {
		t.Fatal("token is empty")
	}
	got, err := LookupSession(db, testSecret, tok, now.Add(1*time.Second))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got != "admin@x.com" {
		t.Errorf("email: got %q, want admin@x.com", got)
	}
	// CreateSession must have bumped lastLoginAt on the admins row.
	var lastLogin int64
	db.QueryRow(`SELECT lastLoginAt FROM _mar_admins WHERE email = ?`, "admin@x.com").Scan(&lastLogin)
	if lastLogin != now.UnixMilli() {
		t.Errorf("lastLoginAt: got %d, want %d", lastLogin, now.UnixMilli())
	}
}

// TestSession_ExpiredIsCleanedUp — looking up an expired session
// returns ErrNoSession AND deletes the row, so subsequent lookups
// don't even find it.
func TestSession_ExpiredIsCleanedUp(t *testing.T) {
	db := openTestDB(t)
	_, _, _ = SyncAdmins(db, []string{"admin@x.com"}, 1000)
	now := time.UnixMilli(1_000_000)
	tok, _ := CreateSession(db, testSecret, "admin@x.com", now)

	future := now.Add(SessionTTL + time.Second)
	_, err := LookupSession(db, testSecret, tok, future)
	if err == nil || err != ErrNoSession {
		t.Errorf("expected ErrNoSession; got %v", err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM _mar_admin_sessions`).Scan(&n)
	if n != 0 {
		t.Errorf("expired session should have been deleted; got %d rows", n)
	}
}

// TestSession_DeleteRevokes — DeleteSession is what /logout calls.
// Lookup of the same token after delete returns ErrNoSession.
func TestSession_DeleteRevokes(t *testing.T) {
	db := openTestDB(t)
	_, _, _ = SyncAdmins(db, []string{"admin@x.com"}, 1000)
	now := time.UnixMilli(1_000_000)
	tok, _ := CreateSession(db, testSecret, "admin@x.com", now)

	if err := DeleteSession(db, testSecret, tok); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := LookupSession(db, testSecret, tok, now.Add(1*time.Second))
	if err != ErrNoSession {
		t.Errorf("expected ErrNoSession after delete; got %v", err)
	}
}

// TestSession_DeleteIsIdempotent — logging out twice (or with a
// stale cookie) is a no-op.
func TestSession_DeleteIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	if err := DeleteSession(db, testSecret, "no-such-token"); err != nil {
		t.Errorf("expected nil for unknown token; got %v", err)
	}
}

// TestSyncRevokesSessions_AfterAdminRemoval — the §4.1a guarantee:
// removing an admin from the desired list deletes their sessions.
// LookupSession then returns ErrNoSession on the next request.
func TestSyncRevokesSessions_AfterAdminRemoval(t *testing.T) {
	db := openTestDB(t)
	_, _, _ = SyncAdmins(db, []string{"admin@x.com"}, 1000)
	now := time.UnixMilli(1_000_000)
	tok, _ := CreateSession(db, testSecret, "admin@x.com", now)

	// Remove from desired list — sync should wipe the session.
	_, removed, err := SyncAdmins(db, []string{}, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Errorf("expected removed=1; got %d", removed)
	}
	_, err = LookupSession(db, testSecret, tok, now.Add(1*time.Second))
	if err != ErrNoSession {
		t.Errorf("expected ErrNoSession after sync removal; got %v", err)
	}
}
