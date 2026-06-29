// Admin auth — primitives for the passwordless email-code flow,
// kept separate from package auth to avoid leaking framework session
// details into the user-auth surface. Reuses auth.Hash / auth.Code /
// auth.Token / auth.Equal so the cryptographic shape matches.
//
// Tables (created by EnsureSchema in admin.go):
//
//   _mar_admin_codes      ephemeral 6-digit codes (hashed), TTL minutes
//   _mar_admin_sessions   session token (hashed) + email + expiresAt
//
// Both code and token storage use TEXT (base64) — same convention as
// _mar_auth_codes / _mar_auth_sessions on the user-auth side.

package admin

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"mar/internal/auth"
)

// CodeTTL is how long a generated 6-digit code stays valid before
// it's no longer accepted by VerifyCode. Mirrors the user-auth
// default; intentionally short so a leaked email transcript can't
// be replayed days later.
const CodeTTL = 10 * time.Minute

// SessionTTL is the lifetime of an admin session cookie. Shorter
// than user-auth's 30 days because admin = sensitive (see §3.3).
const SessionTTL = 12 * time.Hour

// MaxCodeAttempts is how many wrong code submissions can happen
// against the same code row before it's locked. Matches user-auth.
const MaxCodeAttempts = 5

// IsAdmin returns true when `email` (already canonicalized) is in
// _mar_admins. Used by RequestCode to decide whether to send.
//
// Returns false for any unexpected error too — RequestCode treats
// "is this an admin" probes as opaque (no enumeration leak).
func IsAdmin(db *sql.DB, email string) bool {
	if db == nil || email == "" {
		return false
	}
	var n int
	err := db.QueryRow(`SELECT 1 FROM _mar_admins WHERE email = ? LIMIT 1`, email).Scan(&n)
	return err == nil
}

// IssueCode generates a fresh 6-digit code, hashes it with the
// framework session secret, persists the hash with a TTL, and
// returns the plaintext code so the caller can email it.
//
// IssueCode does NOT check admin membership — that's IsAdmin's job.
// We always issue a code (or pretend to) regardless of membership
// to avoid timing-based enumeration; only IsAdmin's return decides
// whether the code is actually delivered.
//
// `now` is injected so tests can pin time. Production passes
// time.Now().
func IssueCode(db *sql.DB, secret, email string, now time.Time) (code string, err error) {
	if db == nil {
		return "", errors.New("admin.IssueCode: nil db")
	}
	if secret == "" {
		return "", errors.New("admin.IssueCode: empty session secret")
	}
	code, err = auth.Code(6)
	if err != nil {
		return "", fmt.Errorf("admin.IssueCode: %w", err)
	}
	hash := auth.Hash(secret, code)
	expires := now.Add(CodeTTL).UnixMilli()
	_, err = db.Exec(`
		INSERT INTO _mar_admin_codes (codeHash, email, expiresAt, attempts, createdAt)
		VALUES (?, ?, ?, 0, ?)
	`, hash, email, expires, now.UnixMilli())
	if err != nil {
		return "", fmt.Errorf("admin.IssueCode insert: %w", err)
	}
	return code, nil
}

// VerifyCodeResult is the outcome of VerifyCode. Each variant maps
// to a specific HTTP status the handler returns.
type VerifyCodeResult int

const (
	// VerifyOK — code matched, row deleted, caller should mint a session.
	VerifyOK VerifyCodeResult = iota
	// VerifyInvalid — no matching, non-expired code for this email,
	// or the code didn't match. Caller returns 401 with a generic
	// "invalid_code" message.
	VerifyInvalid
	// VerifyTooManyAttempts — the code's attempts counter just hit
	// the lock threshold. Caller returns 401 with "too_many_attempts".
	VerifyTooManyAttempts
)

// VerifyCode looks up the most recent unexpired code for `email`,
// constant-time-compares against the candidate, and on match deletes
// the row. On mismatch increments attempts; locks (deletes) the code
// once attempts crosses MaxCodeAttempts so a brute-force attacker
// gets exactly that many tries per code.
//
// Returns VerifyOK on a successful match (caller mints session).
func VerifyCode(db *sql.DB, secret, email, candidate string, now time.Time) (VerifyCodeResult, error) {
	if db == nil {
		return VerifyInvalid, errors.New("admin.VerifyCode: nil db")
	}
	if secret == "" {
		return VerifyInvalid, errors.New("admin.VerifyCode: empty session secret")
	}
	row := db.QueryRow(`
		SELECT codeHash, attempts
		FROM _mar_admin_codes
		WHERE email = ? AND expiresAt >= ?
		ORDER BY createdAt DESC
		LIMIT 1
	`, email, now.UnixMilli())
	var storedHash string
	var attempts int
	if err := row.Scan(&storedHash, &attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return VerifyInvalid, nil
		}
		return VerifyInvalid, fmt.Errorf("admin.VerifyCode lookup: %w", err)
	}
	candidateHash := auth.Hash(secret, candidate)
	if !auth.Equal(candidateHash, storedHash) {
		newAttempts := attempts + 1
		if newAttempts >= MaxCodeAttempts {
			// Burn the row; further attempts should look identical
			// to "no code on file" (no error message hints at the lock).
			_, _ = db.Exec(`DELETE FROM _mar_admin_codes WHERE codeHash = ?`, storedHash)
			return VerifyTooManyAttempts, nil
		}
		_, _ = db.Exec(`UPDATE _mar_admin_codes SET attempts = ? WHERE codeHash = ?`,
			newAttempts, storedHash)
		return VerifyInvalid, nil
	}
	// Match — burn the code so it can't be replayed.
	_, _ = db.Exec(`DELETE FROM _mar_admin_codes WHERE codeHash = ?`, storedHash)
	return VerifyOK, nil
}

// CreateSession mints a session token, persists its hash, returns
// the plaintext token (which becomes the cookie value).
func CreateSession(db *sql.DB, secret, email string, now time.Time) (token string, err error) {
	token, err = auth.Token()
	if err != nil {
		return "", fmt.Errorf("admin.CreateSession: %w", err)
	}
	hash := auth.Hash(secret, token)
	expires := now.Add(SessionTTL).UnixMilli()
	_, err = db.Exec(`
		INSERT INTO _mar_admin_sessions (tokenHash, email, expiresAt, createdAt)
		VALUES (?, ?, ?, ?)
	`, hash, email, expires, now.UnixMilli())
	if err != nil {
		return "", fmt.Errorf("admin.CreateSession insert: %w", err)
	}
	// Update last login on the admin row so `mar fly admin list` can
	// surface "who actually used this".
	_, _ = db.Exec(`UPDATE _mar_admins SET lastLoginAt = ? WHERE email = ?`,
		now.UnixMilli(), email)
	return token, nil
}

// LookupSession resolves a cookie value to an admin email by hash
// lookup. Returns "" + ErrNoSession when the token is unknown,
// expired, or revoked. This is the per-request validation step the
// middleware calls — the source of truth is the DB row, not the
// cookie itself.
//
// Cleanup of expired rows happens lazily here: when a session is
// looked up and found expired, we DELETE it. Older expired rows
// stick around until something prunes them (a periodic sweeper, or
// a future EnsureSchema migration adding a TTL trigger).
func LookupSession(db *sql.DB, secret, token string, now time.Time) (string, error) {
	if db == nil || secret == "" || token == "" {
		return "", ErrNoSession
	}
	hash := auth.Hash(secret, token)
	var email string
	var expires int64
	err := db.QueryRow(`
		SELECT email, expiresAt
		FROM _mar_admin_sessions
		WHERE tokenHash = ?
		LIMIT 1
	`, hash).Scan(&email, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNoSession
		}
		return "", fmt.Errorf("admin.LookupSession: %w", err)
	}
	if expires < now.UnixMilli() {
		_, _ = db.Exec(`DELETE FROM _mar_admin_sessions WHERE tokenHash = ?`, hash)
		return "", ErrNoSession
	}
	return email, nil
}

// DeleteSession revokes a session by token. Used by the logout
// handler. Idempotent — unknown tokens are a no-op.
func DeleteSession(db *sql.DB, secret, token string) error {
	if db == nil || secret == "" || token == "" {
		return nil
	}
	hash := auth.Hash(secret, token)
	_, err := db.Exec(`DELETE FROM _mar_admin_sessions WHERE tokenHash = ?`, hash)
	if err != nil {
		return fmt.Errorf("admin.DeleteSession: %w", err)
	}
	return nil
}

// ErrNoSession is returned by LookupSession when the cookie doesn't
// resolve to a live session row. Distinguished from a real DB error
// so the middleware can return 401 (not 500) without losing the
// cause for logging.
var ErrNoSession = errors.New("admin: no session")
