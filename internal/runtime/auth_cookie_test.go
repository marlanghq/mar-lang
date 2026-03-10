package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mar/internal/parser"
)

func TestAuthSessionCookieSupportsAdminFrontendFlow(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntime(t, filepath.Join(t.TempDir(), "auth-cookie.db"))
	devCode := requestCodeAndReadDevCode(t, r, "owner@example.com")

	loginRec := doRuntimeRequest(r, http.MethodPost, "/auth/login", `{"email":"owner@example.com","code":"`+devCode+`"}`, "")
	if loginRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /auth/login, got %d body=%s", loginRec.Code, loginRec.Body.String())
	}

	sessionCookie := findCookie(loginRec, sessionCookieName)
	if sessionCookie == nil {
		t.Fatalf("expected %s cookie to be set on login", sessionCookieName)
	}
	if sessionCookie.Value == "" {
		t.Fatalf("expected %s cookie value to be populated", sessionCookieName)
	}
	if !sessionCookie.HttpOnly {
		t.Fatalf("expected %s cookie to be HttpOnly", sessionCookieName)
	}

	meRec := doRuntimeRequestWithCookie(r, http.MethodGet, "/auth/me", "", sessionCookie)
	if meRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /auth/me with cookie, got %d body=%s", meRec.Code, meRec.Body.String())
	}

	logoutRec := doRuntimeRequestWithCookie(r, http.MethodPost, "/auth/logout", "", sessionCookie)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /auth/logout with cookie, got %d body=%s", logoutRec.Code, logoutRec.Body.String())
	}

	clearedCookie := findCookie(logoutRec, sessionCookieName)
	if clearedCookie == nil {
		t.Fatalf("expected %s cookie to be cleared on logout", sessionCookieName)
	}
	if clearedCookie.MaxAge >= 0 {
		t.Fatalf("expected cleared cookie to have negative MaxAge, got %d", clearedCookie.MaxAge)
	}

	afterLogoutRec := doRuntimeRequestWithCookie(r, http.MethodGet, "/auth/me", "", sessionCookie)
	if afterLogoutRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 from /auth/me after logout, got %d body=%s", afterLogoutRec.Code, afterLogoutRec.Body.String())
	}
}

func TestAuthLoginUsesAdminUICookieTTLWhenRequested(t *testing.T) {
	requireSQLite3(t)

	src := `
app TodoApi
database "` + filepath.Join(t.TempDir(), "auth-cookie-admin-ttl.db") + `"

system {
  admin_ui_session_ttl_hours 2
}

entity User {
  email: String
  role: String
}

auth {
  user_entity User
  session_ttl_hours 24
  dev_expose_code true
}
`

	app, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}

	r, err := New(app)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	defer r.Close()

	devCode := requestCodeAndReadDevCode(t, r, "owner@example.com")

	loginRec := doRuntimeRequestWithHeaders(
		r,
		http.MethodPost,
		"/auth/login",
		`{"email":"owner@example.com","code":"`+devCode+`"}`,
		map[string]string{adminUISessionHeader: "true"},
	)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /auth/login, got %d body=%s", loginRec.Code, loginRec.Body.String())
	}

	var payload struct {
		ExpiresAt int64 `json:"expiresAt"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode login response: %v", err)
	}

	now := time.Now().UnixMilli()
	gotTTL := payload.ExpiresAt - now
	minExpected := int64((110 * time.Minute).Milliseconds())
	maxExpected := int64((130 * time.Minute).Milliseconds())
	if gotTTL < minExpected || gotTTL > maxExpected {
		t.Fatalf("expected admin UI session TTL near 2 hours, got %d ms", gotTTL)
	}
}

func doRuntimeRequestWithCookie(r *Runtime, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	payload := bytes.NewBufferString(body)
	req := httptest.NewRequest(method, path, payload)
	if strings.TrimSpace(body) != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	r.handleHTTP(rec, req)
	return rec
}

func doRuntimeRequestWithHeaders(r *Runtime, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	payload := bytes.NewBufferString(body)
	req := httptest.NewRequest(method, path, payload)
	if strings.TrimSpace(body) != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	r.handleHTTP(rec, req)
	return rec
}

func findCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
