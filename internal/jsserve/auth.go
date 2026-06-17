package jsserve

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"mar/internal/auth"
	"mar/internal/project"
	"mar/internal/runtime"
)

// Auth runtime config — populated by the CLI before ServeLive runs.
// Holds the bits that come from mar.json (session secret + SMTP creds);
// the user-supplied registration (entity, signup hook, etc.) lives in
// runtime.CurrentAuth().
var (
	authMu       sync.RWMutex
	authSecret   string
	authSMTPCfg  auth.SMTPConfig
	authCodeTTL  = 10 * time.Minute
	authSessTTL  = 30 * 24 * time.Hour            // overridden by Auth.config sessionDuration
	emailLimiter = auth.NewLimiter(3, time.Hour)  // per-email
	ipLimiter    = auth.NewLimiter(20, time.Hour) // per-IP
	cookieName   = "mar_session"

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

// maybeVerifySMTP runs the boot-time SMTP connectivity check. Returns
// nil only when there's no SMTP to verify (dev's stdout-sink path: an
// empty Host means auth.Send falls back to printing the email locally
// instead of talking to a real server).
//
// When SMTP IS configured, the check is unconditional: connection
// failure, auth failure, or any other SMTP-level error fails the boot
// before the HTTP listener opens. Caller (ServeLive) surfaces the
// error to the operator and exits the process — fly / systemd /
// whatever supervises sees an unhealthy machine and refuses to mark
// the deploy as live.
//
// The opt-out is structural rather than a hidden env var: an app
// that wants to boot without SMTP just omits the `mail.from` field
// (and SMTP secret refs) from mar.json. Once configured, the check
// runs; you don't get to half-configure SMTP and silently skip
// verification.
func maybeVerifySMTP() error {
	cfg := SMTP()
	if cfg.Host == "" {
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
	// All /_auth/* endpoints sit behind the gateway rate limiter
	// (per-IP, configured via mar.json["rateLimit"]). request-code and
	// verify-code also have tighter per-endpoint limiters inside the
	// handler (emailLimiter, ipLimiter) — the gateway one is the
	// cheap, broad first cut; the inner ones are the strict, auth-
	// specific second cut. Both layers apply.
	mux.HandleFunc("/_auth/request-code", rateLimit(handleRequestCode))
	mux.HandleFunc("/_auth/verify-code", rateLimit(handleVerifyCode))
	mux.HandleFunc("/_auth/logout", rateLimit(handleLogout))
	mux.HandleFunc("/_auth/whoami", rateLimit(handleWhoami))
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

// clientIP returns the address used as the per-IP rate-limit key. The
// X-Forwarded-For header is honored only when the connecting peer is a
// trusted proxy (isTrustedProxy); otherwise a caller could spoof the
// header and rotate their own key, defeating every per-IP limiter. When
// honored, the list is walked right-to-left to the first non-trusted
// hop — the real client even if a malicious upstream prepended a forged
// entry at the head.
func clientIP(r *http.Request) string {
	peer := hostOnly(r.RemoteAddr)
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" || !isTrustedProxy(net.ParseIP(peer)) {
		return peer
	}
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(parts[i])
		ip := net.ParseIP(hop)
		if ip == nil {
			continue
		}
		if !isTrustedProxy(ip) {
			return hop
		}
	}
	// Every named hop is itself a trusted proxy (a fully-internal
	// chain): the leftmost entry is the furthest-upstream client we can
	// name.
	if first := strings.TrimSpace(parts[0]); first != "" {
		return first
	}
	return peer
}

// isHTTPS reports whether the original client connected over TLS.
// Looks at both r.TLS (direct HTTPS to this process) and the
// X-Forwarded-Proto header (TLS terminated by a proxy — the
// standard prod topology on Fly.io, Cloudflare, nginx, …). Without
// the header check, `Secure` cookie flag would always be false in
// production because the proxy→app hop is plain HTTP.
//
// Used to set the `Secure` flag on auth + admin cookies. Returning
// false errs on the unsafe side (cookie still sent over HTTP), but
// the bigger picture is that any prod deploy MUST front the app
// with HTTPS — this helper just makes the flag reflect that.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	// X-Forwarded-Proto is honored only from a trusted proxy (same
	// gating as clientIP); otherwise a direct plaintext caller could
	// claim HTTPS and earn the Secure cookie flag. Fly and most reverse
	// proxies set this header and sit in a private/loopback network, so
	// the default trust policy covers them.
	if isTrustedProxy(net.ParseIP(hostOnly(r.RemoteAddr))) &&
		strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return false
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
		writeAuthError(w, http.StatusServiceUnavailable, "auth_not_configured")
		return
	}
	body, err := readJSON(r)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	email, _ := body["email"].(string)
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		writeAuthError(w, http.StatusBadRequest, "missing_email")
		return
	}
	// Validate email shape before any DB / SMTP work. Without this,
	// garbage like "not-an-email" would still trigger EnsureUser →
	// create a users row → fail at SMTP send. With the gateway rate
	// limit in place the DoS surface is bounded, but the row stays
	// behind. Block it upfront. Shape-only (RFC 5322 we don't try);
	// SMTP is what actually proves deliverability.
	if !project.IsValidEmail(email) {
		writeAuthError(w, http.StatusBadRequest, "invalid_email")
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
		writeAuthError(w, http.StatusServiceUnavailable, "no_database")
		return
	}
	_ = db
	// Ensure user exists — if not, run the signup hook to create one.
	// Log the underlying error server-side so operators can diagnose
	// it; the client only ever sees the stable code. Without this
	// split, a Go-level error string (DB driver message, etc.) would
	// leak into the user's UI via the Result.Err channel.
	if _, err := runtime.EnsureUser(*cfg, email); err != nil {
		fmt.Fprintf(os.Stderr, "[mar auth] signup failed for %s: %v\n", email, err)
		writeAuthError(w, http.StatusInternalServerError, "signup_failed")
		return
	}
	// Generate code, store hash, email it.
	code, err := auth.Code(6)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "code_generation_failed")
		return
	}
	now := time.Now().Unix()
	exp := now + int64(authCodeTTL.Seconds())
	if _, err := db.Exec(
		`INSERT INTO _mar_auth_codes (email, code_hash, expires_at, created_at) VALUES (?, ?, ?, ?)`,
		email, auth.Hash(secret, code), exp, now,
	); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "store_failed")
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
	// From address: mar.json's `mail.from` is the single source of
	// truth — the address registered with the SMTP provider
	// (Resend, SendGrid, SES, …). mailFrom() returns the manifest
	// value at boot. An empty string in dev (no mail block) is
	// fine because the stdout sink doesn't actually transmit
	// anywhere; the fallback below keeps the From: header
	// syntactically valid.
	fromAddr := mailFrom()
	if fromAddr == "" {
		fromAddr = "noreply@localhost"
	}
	if err := auth.Send(SMTP(), auth.Email{
		From:    fromAddr,
		To:      email,
		Subject: cfg.EmailSubject,
		Body:    emailBody,
	}); err != nil {
		// Same pattern as the signup hook above — log full SMTP /
		// provider error for ops, surface only a stable code so the
		// user's UI doesn't end up showing "EOF" or "535 auth
		// failed" or whatever the upstream returned.
		fmt.Fprintf(os.Stderr, "[mar auth] send failed for %s: %v\n", email, err)
		writeAuthError(w, http.StatusInternalServerError, "send_failed")
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
		writeAuthError(w, http.StatusServiceUnavailable, "auth_not_configured")
		return
	}
	body, err := readJSON(r)
	if err != nil {
		writeAuthError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	email, _ := body["email"].(string)
	codeStr, _ := body["code"].(string)
	email = strings.TrimSpace(strings.ToLower(email))
	codeStr = strings.TrimSpace(codeStr)
	if email == "" || codeStr == "" {
		writeAuthError(w, http.StatusBadRequest, "missing_fields")
		return
	}
	db, err := dbHandle()
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "no_database")
		return
	}
	now := time.Now().Unix()
	// Find the most recent unlocked, unexpired code for this email.
	var (
		id         int64
		storedHash string
		attempts   int
		expiresAt  int64
		lockedAt   sql.NullInt64
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
		writeAuthError(w, http.StatusInternalServerError, "token_failed")
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
		writeAuthError(w, http.StatusInternalServerError, "session_failed")
		return
	}
	// Set cookie. SameSite=Lax, HttpOnly, Secure when over HTTPS.
	// `isHTTPS` checks both direct TLS and X-Forwarded-Proto so
	// the flag stays correct behind Fly's proxy (where r.TLS is
	// always nil because TLS terminates upstream).
	//
	// Both Expires AND Max-Age are set on purpose. By RFC 6265,
	// Max-Age alone is enough to mark a cookie as persistent and
	// browsers honor it. But iOS URLSession's HTTPCookieStorage
	// has a long-standing quirk where Set-Cookie headers carrying
	// only Max-Age (no Expires) are treated as session cookies
	// and dropped at app exit — meaning the native iOS app would
	// ask the user to log in again on every cold start. Emitting
	// Expires alongside Max-Age makes URLSession persist the
	// cookie to ~/Library/Cookies/Cookies.binarycookies as
	// intended. The web side is unaffected: when both are
	// present, the RFC says Max-Age wins.
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    tok,
		Path:     "/",
		Expires:  time.Now().Add(sessTTL),
		MaxAge:   int(sessTTL.Seconds()),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
	// Native clients (iOS/Android/etc.) read the token from this
	// header and stash it in platform secure storage to attach as
	// `Authorization: Bearer <tok>` on subsequent requests. Web
	// clients ignore the header — they got the same value as an
	// HttpOnly cookie above, which their browser handles automatically.
	w.Header().Set(bearerTokenHeader, tok)
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
	if tok := extractSessionToken(r); tok != "" {
		secret := AuthSecret()
		if db, err := dbHandle(); err == nil && secret != "" {
			_, _ = db.Exec(`DELETE FROM _mar_auth_sessions WHERE token_hash = ?`,
				auth.Hash(secret, tok))
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: isHTTPS(r), SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleWhoami: GET /_auth/whoami
//
// Returns the current user as JSON, or null if there's no valid session.
// Mirrors the unix `whoami` command's shape — the frontend session-probe
// endpoint the client hits to learn who (if anyone) is logged in.
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
	tok := extractSessionToken(r)
	if tok == "" {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	db, err := dbHandle()
	if err != nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	userID, ok := sessionUserID(db, secret, tok)
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

// extractSessionToken returns the raw session token from a request,
// trying both transports — `Authorization: Bearer <token>` first, then
// the session cookie — and returns "" if neither is present.
//
// Two transports because Mar has two kinds of clients:
//
//   - Web (browser): HttpOnly cookie. JS code can't read or attach it,
//     which is the whole point — XSS can't exfiltrate a session.
//   - Native runtimes (iOS/macOS/Android/Windows): Authorization header.
//     The native app stores the token in the platform's secure storage
//     (Keychain on Apple, EncryptedSharedPreferences on Android, etc.)
//     and attaches it explicitly per request. Cookie persistence across
//     app launches is unreliable on most non-Apple platforms; an
//     explicit header sidesteps the whole stack of per-platform cookie
//     jar bugs.
//
// Bearer wins over cookie if both are present. In practice the same
// client never sends both (web has no token to bear; native disables
// cookies on its HTTP client) — but if a misbehaving proxy were to
// inject a stale cookie alongside a fresh Authorization header, the
// header is the source of truth.
func extractSessionToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		// Header is "Bearer <token>" (case-insensitive scheme per
		// RFC 7235). Trim the scheme, accept the rest as the token.
		const prefix = "bearer "
		if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
			tok := strings.TrimSpace(h[len(prefix):])
			if tok != "" {
				return tok
			}
		}
	}
	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value
	}
	return ""
}

// bearerTokenHeader is the response header carrying the session token to
// native clients on successful /_auth/verify-code. Web clients ignore
// it (they use the Set-Cookie that comes alongside); the iOS/Android/etc
// runtimes read it and stash the value in platform secure storage.
//
// Header (rather than wrapping the response body in `{user, token}`)
// because it keeps the wire shape of the user record stable — Mar code
// receives `Result String User` regardless of platform, and the
// runtime layer hides the credential ferrying.
const bearerTokenHeader = "X-Mar-Auth-Token"

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
