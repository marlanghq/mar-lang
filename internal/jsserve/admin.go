// Admin HTTP handlers — the parallel of auth.go's /_auth/* endpoints,
// for the framework's built-in admin panel served at /_mar/admin.
//
// Routes mounted by mountAdminHandlers (full list documented inline
// at that function — auth, static assets, services, page shell).
//
// All endpoints are rate-limited per-IP (separate buckets from
// /_auth/* so an attacker pounding user-auth doesn't block admin
// login). Cookies are HMAC-signed using the same auth.sessionSecret
// the user-auth uses (see docs/admin-panel.md §3.2a).

package jsserve

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"mar/internal/admin"
	"mar/internal/auth"
	"mar/internal/runtime"
)

const (
	// adminCookieName is intentionally distinct from `mar_session` so
	// the two flows can coexist on the same domain without colliding.
	adminCookieName = "mar_admin_session"

	// adminAuthDelayMs floors the response time of request-code so an
	// attacker can't time-probe admin membership. 150ms matches the
	// user-auth value.
	adminAuthDelayMs = 150
)

// adminIPLimiter is a separate rate bucket from ipLimiter (which
// covers /_auth/*). Admin login is rare; 20/hour is generous for
// genuine ops use and tight enough that brute-forcing the
// 6-digit code over the wire is infeasible (10^6 / 20 per hour ≈
// 50000 hours).
var adminIPLimiter = auth.NewLimiter(20, time.Hour)

// mountAdminHandlers registers all /_mar/admin/* routes on mux.
// Called from ServeLive once the admin schema is ready. Routes:
//
//   /_mar/admin/auth/{request-code, verify-code, logout}
//      — passwordless email-code flow (Phase 2)
//
//   /_mar/admin/static/{admin.css, admin.js}
//      — embedded UI assets
//
//   /_mar/admin/api/whoami
//      — session probe; 200 OK with the session record OR null
//        (mirrors /_auth/whoami's shape).
//
//   /_mar/admin/api/{server-info, db-stats, recent-requests, entity-rows}
//      — JSON services consumed by the UI.
//
//   /_mar/admin/   (catch-all)
//      — serves index.html (the SPA shell). Login state is detected
//        client-side via /api/whoami so the same shell handles both
//        unauthenticated and authenticated views.
func mountAdminHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/_mar/admin/auth/request-code", handleAdminRequestCode)
	mux.HandleFunc("/_mar/admin/auth/verify-code", handleAdminVerifyCode)
	mux.HandleFunc("/_mar/admin/auth/logout", handleAdminLogout)

	staticFS := http.FS(admin.WebFS())
	mux.Handle("/_mar/admin/static/", http.StripPrefix("/_mar/admin/static/",
		http.FileServer(staticFS)))

	// /api/* — services consumed by the embedded SPA.
	//
	// /api/whoami follows the same shape as /_auth/whoami: 200 OK
	// with the session record when authenticated, 200 OK + null
	// otherwise. SPA-friendly (no try/catch on 401), and matches
	// the user-auth convention so future frontend code looks
	// identical for both. Other /api/* endpoints (which expose
	// data, not session probes) keep the 401-on-no-session pattern.
	mux.HandleFunc("/_mar/admin/api/whoami", handleAdminWhoami)
	mux.HandleFunc("/_mar/admin/api/server-info", handleAdminServerInfo)
	mux.HandleFunc("/_mar/admin/api/db-stats", handleAdminDBStats)
	mux.HandleFunc("/_mar/admin/api/recent-requests", handleAdminRecentRequests)
	mux.HandleFunc("/_mar/admin/api/entity-rows", handleAdminEntityRows)

	// Catch-all — serve the SPA shell. Path "/" matches "/_mar/admin"
	// and any sub-route the JS router renders client-side. Must be
	// registered last so the more-specific /api and /auth routes win.
	mux.HandleFunc("/_mar/admin", handleAdminPage)
	mux.HandleFunc("/_mar/admin/", handleAdminPage)
}

// handleAdminPage serves the SPA shell (index.html). The page itself
// has no auth gate at the HTTP level — login state is determined by
// the client calling /_mar/admin/api/whoami after load. This keeps
// the page simple and ensures unauthenticated visitors see the
// login screen rather than a 401 in DevTools.
func handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	indexHTML, err := fs.ReadFile(admin.WebFS(), "index.html")
	if err != nil {
		http.Error(w, "admin: index missing", http.StatusInternalServerError)
		return
	}
	// no-store so the embedded SPA refreshes on every framework
	// upgrade — the admin panel updates with `mar`, not on a separate
	// cache lifetime.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(indexHTML)
}

// handleAdminWhoami returns the active admin's session record, or
// null if there's no valid session. Mirrors /_auth/whoami's shape
// (always 200, body is null vs. record) so SPA code reads the
// same on both auth tracks. Called by the embedded panel on load
// to pick between the login screen and the dashboard.
func handleAdminWhoami(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Whoami is the friendly probe — both ErrNoSession and DB errors
	// resolve to "200 OK + null". The SPA boot path treats null as
	// "not signed in" and routes to login; data endpoints distinguish
	// 401 vs 503 separately.
	email, err := requireAdminSession(r)
	if err != nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"email": email})
}

// requireAdminSession is the per-request auth check used by every
// /_mar/admin/api/* handler that needs to know who's logged in.
//
// Reads the mar_admin_session cookie, looks up the row in
// _mar_admin_sessions (DB-per-request — no in-memory cache), returns
// the admin's email on hit.
//
// Errors are distinguished by the caller:
//   - admin.ErrNoSession → missing/invalid/expired cookie or
//     non-admin email. Caller renders 401 ("you're not signed in").
//   - any other error → DB / infrastructure failure. Caller should
//     render 503 ("session check unavailable") rather than 401, so
//     a transient contention doesn't masquerade as a logged-out user
//     and trigger the SPA to render "unavailable" panels.
//
// Belt-and-suspenders: even when the cookie resolves to a session,
// re-check that the email is still in _mar_admins. Closes the gap
// between an admin removal triggering session DELETE and a still-
// in-flight request that loaded the session row before the DELETE
// landed.
func requireAdminSession(r *http.Request) (string, error) {
	c, err := r.Cookie(adminCookieName)
	if err != nil || c.Value == "" {
		return "", admin.ErrNoSession
	}
	secret := AuthSecret()
	if secret == "" {
		return "", admin.ErrNoSession
	}
	db, err := adminDB()
	if err != nil {
		// Real DB error — caller should 503, not 401.
		return "", fmt.Errorf("admin session: %w", err)
	}
	email, err := admin.LookupSession(db, secret, c.Value, time.Now())
	if err != nil {
		// LookupSession already returns ErrNoSession for missing /
		// expired rows; any other error is a real DB error. Pass
		// both up unchanged — caller decides 401 vs 503 by checking
		// errors.Is(err, admin.ErrNoSession).
		return "", err
	}
	if !admin.IsAdmin(db, email) {
		return "", admin.ErrNoSession
	}
	return email, nil
}

// gateAdminSession is the typical handler-level entry point. Returns
// the admin email when authenticated, or writes the right HTTP error
// (401 for no-session, 503 for DB failure) and returns ("", false).
// Centralizes the response shape so each /api/* handler stays a
// one-liner.
func gateAdminSession(w http.ResponseWriter, r *http.Request) (email string, ok bool) {
	email, err := requireAdminSession(r)
	if err == nil {
		return email, true
	}
	if errors.Is(err, admin.ErrNoSession) {
		writeAuthError(w, http.StatusUnauthorized, "no_session")
		return "", false
	}
	writeAuthError(w, http.StatusServiceUnavailable, "session_check_failed")
	return "", false
}

// handleAdminRequestCode: POST /_mar/admin/auth/request-code  { email }
//
// Always returns 200 (after a small minimum delay) regardless of
// whether the email is in _mar_admins, to avoid enumeration. Errors
// are reserved for transport / rate-limiting issues.
//
// In dev (no SMTP configured), the code prints to the terminal so
// `mar dev` works zero-config. In prod, the code is sent via the
// shared SMTP from mar.json["mail"].
func handleAdminRequestCode(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	defer minDelay(startedAt, adminAuthDelayMs)

	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	secret := AuthSecret()
	if secret == "" {
		writeAuthError(w, http.StatusServiceUnavailable, "admin not configured")
		return
	}
	if ok, retry := adminIPLimiter.Allow(clientIP(r)); !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error": "rate_limited", "retryAfterSeconds": int(retry.Seconds()),
		})
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

	db, err := adminDB()
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "no_database")
		return
	}

	// Always return ok:true downstream. Whether we actually issue +
	// send is gated on IsAdmin without exposing the result.
	if !admin.IsAdmin(db, email) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}

	now := time.Now()
	code, err := admin.IssueCode(db, secret, email, now)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "issue_code")
		return
	}

	// Send. auth.Send routes through MailSink.Stdout when the SMTP
	// host is empty, so dev-mode (no SMTP configured) just prints
	// the code to the terminal — same path the user-auth dev flow
	// already uses. In prod, it goes via the SMTP from mar.json.
	smtpCfg := SMTP()
	from := smtpCfg.Username // sane fallback if mail.from isn't plumbed
	if maybe := mailFrom(); maybe != "" {
		from = maybe
	}
	if err := auth.Send(smtpCfg, auth.Email{
		From:    from,
		To:      email,
		Subject: "Admin sign-in code",
		Body: fmt.Sprintf(
			"Your admin sign-in code is %s.\n\nIt expires in %d minutes.\n",
			code, int(admin.CodeTTL.Minutes()),
		),
	}); err != nil {
		writeAuthError(w, http.StatusInternalServerError, "send")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAdminVerifyCode: POST /_mar/admin/auth/verify-code  { email, code }
//
// On success: mints a session, sets the admin cookie, returns
// {email}. On failure: generic 401 with no enumeration leak.
func handleAdminVerifyCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	secret := AuthSecret()
	if secret == "" {
		writeAuthError(w, http.StatusServiceUnavailable, "admin not configured")
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

	db, err := adminDB()
	if err != nil {
		writeAuthError(w, http.StatusServiceUnavailable, "no_database")
		return
	}

	now := time.Now()
	res, err := admin.VerifyCode(db, secret, email, codeStr, now)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "verify")
		return
	}
	switch res {
	case admin.VerifyTooManyAttempts:
		writeAuthError(w, http.StatusUnauthorized, "too_many_attempts")
		return
	case admin.VerifyInvalid:
		writeAuthError(w, http.StatusUnauthorized, "invalid_code")
		return
	case admin.VerifyOK:
		// fall through
	}

	// Defense in depth: even though the code matched, only mint a
	// session if the email is still in _mar_admins. Catches the
	// edge case where an admin was removed between request-code and
	// verify-code in the same window.
	if !admin.IsAdmin(db, email) {
		writeAuthError(w, http.StatusUnauthorized, "invalid_code")
		return
	}

	tok, err := admin.CreateSession(db, secret, email, now)
	if err != nil {
		writeAuthError(w, http.StatusInternalServerError, "session")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    tok,
		Path:     "/_mar/admin",
		MaxAge:   int(admin.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]string{"email": email})
}

// handleAdminLogout: POST /_mar/admin/auth/logout
//
// Server-side: deletes the session row by hash. Client-side: cookie
// is overwritten with Max-Age=0. Idempotent.
func handleAdminLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c, err := r.Cookie(adminCookieName); err == nil && c.Value != "" {
		secret := AuthSecret()
		if db, dbErr := adminDB(); dbErr == nil && secret != "" {
			_ = admin.DeleteSession(db, secret, c.Value)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name:     adminCookieName,
		Value:    "",
		Path:     "/_mar/admin",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// adminDB opens the project's SQLite handle for admin endpoints.
// Same DB the user-auth uses (one mar.db per project), but bypasses
// auth.Migrate — admin schema was already created at boot, and we
// don't want a user-auth schema problem to lock out the admin panel.
func adminDB() (*sql.DB, error) {
	return runtime.OpenDB()
}

// minDelay sleeps until at least `floorMs` milliseconds have
// elapsed since `started`. Used by request-code to mask whether
// the email matched an admin row.
func minDelay(started time.Time, floorMs int) {
	elapsed := time.Since(started)
	floor := time.Duration(floorMs) * time.Millisecond
	if elapsed < floor {
		time.Sleep(floor - elapsed)
	}
}

// mailFrom returns the configured `mail.from` address (used as the
// envelope From for admin sign-in emails). Falls back to "" if no
// mail block is configured. Reads from the same SMTP config the
// user-auth uses; if both are configured, they share the From.
//
// The actual lookup is a thin pass-through to the manifest because
// jsserve doesn't keep a separate "mail.from" cache — but to avoid
// re-reading mar.json per request, we plumb it via SetAdminMailFrom
// at boot.
func mailFrom() string {
	authMu.RLock()
	defer authMu.RUnlock()
	return adminMailFrom
}

var adminMailFrom string

// SetAdminMailFrom is called by the CLI (mar dev / mar-runtime)
// at boot to wire the manifest's `mail.from` for admin emails. Same
// lifecycle as SetAuthRuntime.
func SetAdminMailFrom(from string) {
	authMu.Lock()
	adminMailFrom = from
	authMu.Unlock()
}

