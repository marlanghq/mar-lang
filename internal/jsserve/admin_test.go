package jsserve

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mar/internal/admin"
	"mar/internal/auth"
	"mar/internal/runtime"
)

// adminTestServer boots a real httptest server with the admin auth
// handlers mounted, an empty SMTP config (so codes go to stdout),
// and a single seeded admin in the DB. Returns the server + a
// cleanup func that restores rate limiters between tests so
// independent runs don't carry over budget exhaustion.
func adminTestServer(t *testing.T, seedAdmins []string) (*httptest.Server, func()) {
	t.Helper()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	runtime.SetDBPath(dbPath)
	SetAuthRuntime("test-secret-32bytes-padding-padding-padding", auth.SMTPConfig{})
	SetAdminMailFrom("admin-test@x.com")

	db, err := runtime.OpenDB()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := admin.EnsureSchema(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, _, err := admin.SyncAdmins(db, seedAdmins, time.Now().UnixMilli()); err != nil {
		t.Fatalf("seed admins: %v", err)
	}

	mux := http.NewServeMux()
	mountAdminHandlers(mux)
	server := httptest.NewServer(mux)
	cleanup := func() {
		server.Close()
		// Reset rate limiter so subsequent tests don't inherit
		// budget exhaustion.
		adminIPLimiter = auth.NewLimiter(20, time.Hour)
	}
	return server, cleanup
}

// TestAdminRequestCode_AlwaysOK_NoEnumeration — both an admin and a
// stranger get the same 200 OK. The admin's code reaches stdout;
// the stranger's silently no-ops.
func TestAdminRequestCode_AlwaysOK_NoEnumeration(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	client := server.Client()

	for _, email := range []string{"admin@x.com", "stranger@x.com"} {
		out := captureStdout(t, func() {
			resp, body := postJSON(t, client, server.URL+"/_mar/admin/auth/request-code",
				map[string]string{"email": email})
			if resp.StatusCode != http.StatusOK {
				t.Errorf("%s: status = %d, body = %s", email, resp.StatusCode, body)
			}
		})
		// Admin should produce a code in the sink; stranger should not.
		_, hasCode := codeRegex.FindStringIndex(out), out != ""
		_ = hasCode
		if email == "admin@x.com" && codeRegex.FindString(out) == "" {
			t.Errorf("admin: expected a code in stdout sink; got %q", out)
		}
		if email == "stranger@x.com" && codeRegex.FindString(out) != "" {
			t.Errorf("stranger: expected no code (no enumeration); got %q", out)
		}
	}
}

// TestAdminRequestCode_RejectsMissingEmail — the basic shape error.
// 400 (not 200), so a probe with empty email gets a different
// status, but that's a CLI/contract error, not enumeration —
// hostile clients won't bother.
func TestAdminRequestCode_RejectsMissingEmail(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	resp, body := postJSON(t, server.Client(), server.URL+"/_mar/admin/auth/request-code",
		map[string]string{"email": ""})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, body = %s", resp.StatusCode, body)
	}
}

// TestAdminVerifyCode_HappyPath_SetsCookie — full request → verify
// flow. Captures the code from stdout, posts it back, expects 200
// + Set-Cookie: mar_admin_session=...
func TestAdminVerifyCode_HappyPath_SetsCookie(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	client := server.Client()

	var code string
	out := captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_mar/admin/auth/request-code",
			map[string]string{"email": "admin@x.com"})
	})
	code = extractSinkCode(t, out)

	resp, body := postJSON(t, client, server.URL+"/_mar/admin/auth/verify-code",
		map[string]string{"email": "admin@x.com", "code": code})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify status = %d, body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "admin@x.com") {
		t.Errorf("expected email in response; got %s", body)
	}
	cookies := resp.Cookies()
	var found bool
	for _, c := range cookies {
		if c.Name == "mar_admin_session" {
			if c.Value == "" {
				t.Error("admin cookie value is empty")
			}
			if !c.HttpOnly {
				t.Error("admin cookie should be HttpOnly")
			}
			found = true
		}
	}
	if !found {
		t.Errorf("expected mar_admin_session cookie; got %d cookies: %v", len(cookies), cookies)
	}
}

// TestAdminVerifyCode_WrongCodeIs401 — bad code returns 401 with
// generic "invalid_code" (no leak about which part was wrong).
func TestAdminVerifyCode_WrongCodeIs401(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	client := server.Client()

	captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_mar/admin/auth/request-code",
			map[string]string{"email": "admin@x.com"})
	})
	resp, body := postJSON(t, client, server.URL+"/_mar/admin/auth/verify-code",
		map[string]string{"email": "admin@x.com", "code": "000000"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_code") {
		t.Errorf("expected invalid_code in body; got %s", body)
	}
}

// TestAdminLogout_ClearsCookie — logout returns 200 + Set-Cookie
// with Max-Age=-1 (clearing it client-side).
func TestAdminLogout_ClearsCookie(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	client := server.Client()

	resp, _ := postJSON(t, client, server.URL+"/_mar/admin/auth/logout", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == "mar_admin_session" {
			if c.MaxAge >= 0 {
				t.Errorf("expected MaxAge<0 to clear cookie; got %d", c.MaxAge)
			}
		}
	}
}

// TestAdminPage_ServesSPAShell — GET /_mar/admin returns the
// embedded HTML, content-type text/html, no-store. Both the bare
// path and the trailing-slash variant work.
func TestAdminPage_ServesSPAShell(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	for _, url := range []string{server.URL + "/_mar/admin", server.URL + "/_mar/admin/"} {
		resp, err := server.Client().Get(url)
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		if resp.StatusCode != 200 {
			t.Errorf("%s: status = %d", url, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type = %q", url, ct)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("%s: Cache-Control = %q", url, cc)
		}
		resp.Body.Close()
	}
}

// TestAdminStatic_ServesEmbeddedAssets — admin.js and admin.css are
// reachable under /_mar/admin/static/.
func TestAdminStatic_ServesEmbeddedAssets(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	for _, name := range []string{"admin.js", "admin.css"} {
		resp, err := server.Client().Get(server.URL + "/_mar/admin/static/" + name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if resp.StatusCode != 200 {
			t.Errorf("%s: status = %d", name, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// TestAdminWhoami_Unauthenticated — matches /_auth/whoami's shape:
// 200 OK + body null. SPA-friendly, lets the client read the body
// without branching on the status code.
func TestAdminWhoami_Unauthenticated(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	resp, body := getJSON(t, server.Client(), server.URL+"/_mar/admin/api/whoami")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, body = %s", resp.StatusCode, body)
	}
	// `null` body — same convention /_auth/whoami uses for "no session".
	if strings.TrimSpace(string(body)) != "null" {
		t.Errorf("expected body 'null'; got %q", body)
	}
}

// TestAdminWhoami_Authenticated — after a successful sign-in,
// /whoami returns the {email} record.
func TestAdminWhoami_Authenticated(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	client := server.Client()

	// Sign in.
	out := captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_mar/admin/auth/request-code",
			map[string]string{"email": "admin@x.com"})
	})
	code := extractSinkCode(t, out)
	verifyResp, _ := postJSON(t, client, server.URL+"/_mar/admin/auth/verify-code",
		map[string]string{"email": "admin@x.com", "code": code})
	var token string
	for _, c := range verifyResp.Cookies() {
		if c.Name == "mar_admin_session" {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("no session cookie set; aborting")
	}

	// Now hit /whoami with the cookie.
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/_mar/admin/api/whoami", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: token})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

// TestAdminLogout_RevokesSession — log in, log out, the session
// must be deleted from the DB so a stale cookie can't be used.
func TestAdminLogout_RevokesSession(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	client := server.Client()

	// Sign in first.
	out := captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_mar/admin/auth/request-code",
			map[string]string{"email": "admin@x.com"})
	})
	code := extractSinkCode(t, out)
	verifyResp, _ := postJSON(t, client, server.URL+"/_mar/admin/auth/verify-code",
		map[string]string{"email": "admin@x.com", "code": code})
	var token string
	for _, c := range verifyResp.Cookies() {
		if c.Name == "mar_admin_session" {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("no session cookie set; aborting")
	}

	// Now log out, sending the cookie back.
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/_mar/admin/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: token})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("logout status = %d", resp.StatusCode)
	}

	// Session must be gone from the DB.
	db, _ := runtime.OpenDB()
	email, lookErr := admin.LookupSession(db, AuthSecret(), token, time.Now())
	if lookErr != admin.ErrNoSession {
		t.Errorf("expected ErrNoSession; got email=%q err=%v", email, lookErr)
	}
}
