package jsserve

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"mar/internal/auth"
	"mar/internal/runtime"
)

// Auth runtime config — populated by the CLI before ServeLive runs.
// Holds the bits that come from mar.json (session secret + SMTP creds);
// the user-supplied registration (entity, signup hook, etc.) lives in
// runtime.CurrentAuth().
var (
	authMu        sync.RWMutex
	authSecret    string
	authSMTPCfg   auth.SMTPConfig
	authCodeTTL   = 10 * time.Minute
	authSessTTL   = 30 * 24 * time.Hour // overridden by Auth.config sessionDuration
	emailLimiter  = auth.NewLimiter(3, time.Hour)  // per-email
	ipLimiter     = auth.NewLimiter(20, time.Hour) // per-IP
	cookieName    = "mar_session"

	// sweeperOnce ensures the background expired-sessions/codes sweeper
	// is started at most once per process, even if mountAuthHandlers
	// is called repeatedly (in practice it isn't, but the guard is
	// cheap and prevents a leaked goroutine if the contract changes).
	sweeperOnce sync.Once
)

// sweepInterval is the cadence at which expired auth codes and sessions
// are deleted. Var (not const) so tests can shrink it.
var sweepInterval = 24 * time.Hour

// SetAuthRuntime is called by the CLI once at startup to plumb in
// secrets and SMTP credentials from mar.json. Safe to call before any
// dispatch.
func SetAuthRuntime(secret string, smtp auth.SMTPConfig) {
	authMu.Lock()
	authSecret = secret
	authSMTPCfg = smtp
	authMu.Unlock()
}

// AuthSecret returns the configured session secret. The empty string
// means auth isn't operational — `Auth.config` was used in mar code but
// the CLI didn't plumb a secret. Handlers refuse to function in that
// state.
func AuthSecret() string {
	authMu.RLock()
	defer authMu.RUnlock()
	return authSecret
}

// SMTP returns the configured mail credentials.
func SMTP() auth.SMTPConfig {
	authMu.RLock()
	defer authMu.RUnlock()
	return authSMTPCfg
}

// maybeVerifySMTP runs the boot-time SMTP connectivity check unless
// it shouldn't. Returns nil in three skip cases:
//
//   1. Host is empty — dev's stdout-sink path. There's no SMTP to
//      verify; auth.Send falls back to printing the email locally.
//   2. MAR_SKIP_SMTP_CHECK env var is set — escape hatch for demos
//      that boot with auth turned off, or restore-from-backup
//      scenarios where infra isn't fully wired yet.
//   3. (Future) any other case the spec adds.
//
// Otherwise calls auth.VerifySMTPConfig and returns any error.
// Caller (ServeLive) is expected to surface this to the operator
// and exit the process — boot fails before the listener opens, so
// fly / systemd / whatever supervises it sees an unhealthy machine.
func maybeVerifySMTP() error {
	cfg := SMTP()
	if cfg.Host == "" {
		return nil
	}
	if os.Getenv("MAR_SKIP_SMTP_CHECK") != "" {
		return nil
	}
	return auth.VerifySMTPConfig(cfg)
}

// mountAuthHandlers registers the /_auth/* endpoints on mux and starts
// the background sweeper that deletes expired codes/sessions. Called
// by ServeLive when there's a registered Auth.config AND a session
// secret is available. The path prefix is reserved (manifest check
// forbids user routes under /_auth).
func mountAuthHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/_auth/request-code", handleRequestCode)
	mux.HandleFunc("/_auth/verify-code", handleVerifyCode)
	mux.HandleFunc("/_auth/logout", handleLogout)
	mux.HandleFunc("/_auth/whoami", handleWhoami)
	startAuthSweeper()
}

// startAuthSweeper kicks off the background goroutine that calls
// auth.SweepExpired on every `sweepInterval`. Idempotent (sync.Once);
// no-op if the DB isn't reachable, since auth wouldn't work either
// in that state — failing loudly here would just duplicate the error
// the first request would surface.
//
// Returned stop function from auth.StartSweeper is intentionally
// discarded: the sweeper runs for the process lifetime. Test code
// that needs to stop the sweeper should call auth.StartSweeper
// directly with its own DB handle.
func startAuthSweeper() {
	sweeperOnce.Do(func() {
		db, err := dbHandle()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mar auth] sweeper not started: %v\n", err)
			return
		}
		_ = auth.StartSweeper(context.Background(), db, sweepInterval)
	})
}

// dbHandle returns the project's SQLite handle, ensuring auth tables
// are migrated on first use. Errors out cleanly when the project has
// no database configured (Auth requires a DB).
func dbHandle() (*sql.DB, error) {
	db, err := runtime.AuthDB()
	if err != nil {
		return nil, err
	}
	if err := auth.Migrate(db); err != nil {
		return nil, err
	}
	return db, nil
}

// readJSON reads + decodes the request body into a map.
func readJSON(r *http.Request) (map[string]any, error) {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<14)) // 16KB cap
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAuthError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	return r.RemoteAddr
}

// handleRequestCode: POST /_auth/request-code  { email }
//
// Always returns 200 (after a small minimum delay) regardless of
// whether the email exists, to avoid enumeration. Errors are reserved
// for transport / rate-limiting issues.
func handleRequestCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := runtime.CurrentAuth()
	secret := AuthSecret()
	if cfg == nil || secret == "" {
		writeAuthError(w, http.StatusServiceUnavailable, "auth not configured")
		return
	}
	body, err := readJSON(r)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid json")
		return
	}
	email, _ := body["email"].(string)
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		writeAuthError(w, http.StatusBadRequest, "missing email")
		return
	}
	// Rate limit before any DB / email work.
	if ok, retry := emailLimiter.Allow(email); !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": "rate_limited", "retryAfterSeconds": int(retry.Seconds()),
		})
		return
	}
	if ok, retry := ipLimiter.Allow(clientIP(r)); !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": "rate_limited", "retryAfterSeconds": int(retry.Seconds()),
		})
		return
	}
	db, err := dbHandle()
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "no database")
		return
	}
	_ = db
	// Ensure user exists — if not, run the signup hook to create one.
	if _, err := runtime.EnsureUser(*cfg, email); err != nil {
		writeAuthError(w, http.StatusInternalServerError, fmt.Sprintf("signup: %v", err))
		return
	}
	// Generate code, store hash, email it.
	code, err := auth.Code(6)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "code generation")
		return
	}
	now := time.Now().Unix()
	exp := now + int64(authCodeTTL.Seconds())
	if _, err := db.Exec(
		`INSERT INTO _mar_auth_codes (email, code_hash, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		email, auth.Hash(secret, code), exp, now,
	); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "store")
		return
	}
	ttlMin := int(authCodeTTL.Minutes())
	emailBody := auth.DefaultBody(code, ttlMin)
	// User-supplied `email.body : String -> Int -> String` overrides
	// the default. Errors during user-fn evaluation surface in the
	// server log but don't block sending — fall back to the default
	// so a malformed template doesn't lock users out.
	if cfg.EmailBody != nil {
		if custom, err := runtime.ApplyEmailBody(cfg.EmailBody, code, ttlMin); err != nil {
			fmt.Fprintf(os.Stderr, "[mar auth] email.body fn failed (%v); using default body\n", err)
		} else {
			emailBody = custom
		}
	}
	if err := auth.Send(SMTP(), auth.Email{
		From:    cfg.EmailFrom,
		To:      email,
		Subject: cfg.EmailSubject,
		Body:    emailBody,
	}); err != nil {
		writeAuthError(w, http.StatusInternalServerError, fmt.Sprintf("send: %v", err))
		return
	}
	// Constant-ish minimum delay to mask whether the email existed.
	time.Sleep(150 * time.Millisecond)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleVerifyCode: POST /_auth/verify-code  { email, code }
//
// On success: deletes the code, mints a session, sets a cookie, returns
// the user record as JSON. On failure: generic error to avoid info leak.
func handleVerifyCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := runtime.CurrentAuth()
	secret := AuthSecret()
	if cfg == nil || secret == "" {
		writeAuthError(w, http.StatusServiceUnavailable, "auth not configured")
		return
	}
	body, err := readJSON(r)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid json")
		return
	}
	email, _ := body["email"].(string)
	codeStr, _ := body["code"].(string)
	email = strings.TrimSpace(strings.ToLower(email))
	codeStr = strings.TrimSpace(codeStr)
	if email == "" || codeStr == "" {
		writeAuthError(w, http.StatusBadRequest, "missing fields")
		return
	}
	db, err := dbHandle()
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "no database")
		return
	}
	now := time.Now().Unix()
	// Find the most recent unlocked, unexpired code for this email.
	var (
		id        int64
		storedHash string
		attempts  int
		expiresAt int64
		lockedAt  sql.NullInt64
	)
	row := db.QueryRow(
		`SELECT id, code_hash, attempts, expires_at, locked_at
		 FROM _mar_auth_codes
		 WHERE email = ? AND locked_at IS NULL AND expires_at >= ?
		 ORDER BY id DESC LIMIT 1`,
		email, now,
	)
	if err := row.Scan(&id, &storedHash, &attempts, &expiresAt, &lockedAt); err != nil {
		writeAuthError(w, http.StatusUnauthorized, "invalid_code")
		return
	}
	candidate := auth.Hash(secret, codeStr)
	if !auth.Equal(candidate, storedHash) {
		// Increment attempts; lock at 5.
		newAttempts := attempts + 1
		if newAttempts >= 5 {
			_, _ = db.Exec(`UPDATE _mar_auth_codes SET attempts = ?, locked_at = ? WHERE id = ?`,
				newAttempts, now, id)
			writeAuthError(w, http.StatusUnauthorized, "too_many_attempts")
			return
		}
		_, _ = db.Exec(`UPDATE _mar_auth_codes SET attempts = ? WHERE id = ?`, newAttempts, id)
		writeAuthError(w, http.StatusUnauthorized, "invalid_code")
		return
	}
	// Code matched — burn it.
	_, _ = db.Exec(`DELETE FROM _mar_auth_codes WHERE id = ?`, id)
	// Resolve user.
	userID, err := runtime.LookupUserID(*cfg, email)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "user_lookup")
		return
	}
	// Mint session.
	tok, err := auth.Token()
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "token")
		return
	}
	sessTTL := authSessTTL
	if cfg.SessionDuration > 0 {
		sessTTL = time.Duration(cfg.SessionDuration) * time.Second
	}
	exp := time.Now().Add(sessTTL).Unix()
	if _, err := db.Exec(
		`INSERT INTO _mar_auth_sessions (token_hash, user_id, expires_at, created_at, last_used_at) VALUES (?, ?, ?, ?, ?)`,
		auth.Hash(secret, tok), userID, exp, now, now,
	); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "session")
		return
	}
	// Set cookie. SameSite=Lax, HttpOnly, Secure when over HTTPS.
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    tok,
		Path:     "/",
		MaxAge:   int(sessTTL.Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	// Return the user record.
	userJSON, err := runtime.LoadUserJSON(*cfg, userID)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "load_user")
		return
	}
	writeJSON(w, http.StatusOK, userJSON)
}

// handleLogout: POST /_auth/logout
//
// Server-side: deletes the session row by hash. Client-side: cookie is
// overwritten with Max-Age=0. Idempotent.
func handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		secret := AuthSecret()
		if db, err := dbHandle(); err == nil && secret != "" {
			_, _ = db.Exec(`DELETE FROM _mar_auth_sessions WHERE token_hash = ?`,
				auth.Hash(secret, c.Value))
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleWhoami: GET /_auth/whoami
//
// Returns the current user as JSON, or null if there's no valid session.
// Mirrors the unix `whoami` command's shape — Mar uses the same name on
// the framework session-probe endpoints (this one + /_mar/admin/api/whoami)
// for consistency.
func handleWhoami(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	cfg := runtime.CurrentAuth()
	secret := AuthSecret()
	if cfg == nil || secret == "" {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	db, err := dbHandle()
	if err != nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	userID, ok := sessionUserID(db, secret, c.Value)
	if !ok {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	userJSON, err := runtime.LoadUserJSON(*cfg, userID)
	if err != nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, userJSON)
}

// sessionUserID returns the user_id for a valid (unexpired) session
// token, or false if the token doesn't match a live session.
func sessionUserID(db *sql.DB, secret, rawToken string) (int64, bool) {
	now := time.Now().Unix()
	var userID int64
	row := db.QueryRow(
		`SELECT user_id FROM _mar_auth_sessions WHERE token_hash = ? AND expires_at >= ?`,
		auth.Hash(secret, rawToken), now,
	)
	if err := row.Scan(&userID); err != nil {
		return 0, false
	}
	_, _ = db.Exec(`UPDATE _mar_auth_sessions SET last_used_at = ? WHERE token_hash = ?`,
		now, auth.Hash(secret, rawToken))
	return userID, true
}
