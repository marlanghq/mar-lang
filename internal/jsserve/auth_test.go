package jsserve

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"mar/internal/auth"
	"mar/internal/runtime"
)

// Integration tests for /_auth/*. Boots a real httptest server with
// the framework auth handlers mounted, registers a runtime VAuth
// with a hand-built signup hook + identify projection, and exercises
// the request-code → verify-code → me → logout flow against a
// temp SQLite database.

func authTestServer(t *testing.T) (server *httptest.Server, cleanup func()) {
	t.Helper()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "test.db")
	runtime.SetDBPath(dbPath)
	SetAuthRuntime("test-secret", auth.SMTPConfig{}) // empty SMTP → stdout sink

	// User entity: { id : Serial, email : Text NotNull }.
	users := runtime.VEntity{
		Table: "users",
		Fields: []runtime.EntityField{
			{Name: "id", SQLType: "INTEGER", NotNull: true, Serial: true},
			{Name: "email", SQLType: "TEXT", NotNull: true},
		},
	}
	identify := runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			rec, _ := args[0].(runtime.VRecord)
			email, _ := rec.Fields["email"].(runtime.VString)
			return email, nil
		},
	}
	signup := runtime.VFn{
		Arity: 1,
		Native: func(args []runtime.Value) (runtime.Value, error) {
			email, _ := args[0].(runtime.VString)
			return runtime.VRecord{
				Fields: map[string]runtime.Value{"email": email},
				Order:  []string{"email"},
			}, nil
		},
	}
	runtime.RegisterAuth(runtime.VAuth{
		Entity:          users,
		Identify:        identify,
		EmailSubject:    "Sign in",
		Signup:          signup,
		SessionDuration: 3600,
	})

	mux := http.NewServeMux()
	mountAuthHandlers(mux)
	server = httptest.NewServer(mux)
	cleanup = func() {
		server.Close()
		// Reset rate limiters between tests so independent runs
		// don't carry over budget exhaustion from earlier tests.
		// Defaults match what initialization in auth.go uses.
		emailLimiter = auth.NewLimiter(3, time.Hour)
		ipLimiter = auth.NewLimiter(20, time.Hour)
	}
	return server, cleanup
}

// captureStdout redirects os.Stdout to a buffer for the duration of
// `fn`, returning what was written. Used to read the dev sink's email
// (no SMTP configured in tests, so emails go to stdout).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan []byte)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.Bytes()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
	return string(<-done)
}

var codeRegex = regexp.MustCompile(`\b\d{6}\b`)

func extractSinkCode(t *testing.T, output string) string {
	t.Helper()
	m := codeRegex.FindString(output)
	if m == "" {
		t.Fatalf("no 6-digit code found in stdout sink: %q", output)
	}
	return m
}

func postJSON(t *testing.T, client *http.Client, url string, body any) (*http.Response, []byte) {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(http.MethodPost, url, bodyReader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, respBody
}

// TestRequestCode_RejectsInvalidEmailShape — malformed emails are
// rejected upfront (400 + "invalid_email") before they can hit
// EnsureUser and pollute the users table with garbage rows. Same
// shape check used at compile time for admins / mail.from.
func TestRequestCode_RejectsInvalidEmailShape(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	client := server.Client()

	bad := []string{
		"not-an-email",
		"missing-at.com",
		"@nohost.com",
		"newline@x.com\nBcc: attacker@evil.com",
	}
	for _, email := range bad {
		t.Run(email, func(t *testing.T) {
			resp, body := postJSON(t, client, server.URL+"/_auth/request-code",
				map[string]string{"email": email})
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("email %q: status = %d (want 400), body = %s",
					email, resp.StatusCode, body)
			}
			if !strings.Contains(string(body), "invalid_email") {
				t.Errorf("email %q: body should mention invalid_email, got %s",
					email, body)
			}
		})
	}
}

// TestVerify_CookieSecureFromXForwardedProto — cookies set during
// verify-code respect X-Forwarded-Proto: https so they get the
// Secure flag even when this process is behind a TLS-terminating
// proxy (Fly, Cloudflare, nginx). Without this, r.TLS is always
// nil in prod and the Secure flag would always be false.
func TestVerify_CookieSecureFromXForwardedProto(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	client := server.Client()

	// Request a code, fish it out of the stdout sink.
	var code string
	out := captureStdout(t, func() {
		resp, _ := postJSON(t, client, server.URL+"/_auth/request-code",
			map[string]string{"email": "alice@example.com"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request-code: %d", resp.StatusCode)
		}
	})
	code = extractSinkCode(t, out)

	// Verify with X-Forwarded-Proto: https — Secure flag should be true.
	req, _ := http.NewRequest(http.MethodPost,
		server.URL+"/_auth/verify-code",
		bytes.NewReader(mustJSON(map[string]string{
			"email": "alice@example.com", "code": code,
		})))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify: status %d", resp.StatusCode)
	}
	var sawCookie bool
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			sawCookie = true
			if !c.Secure {
				t.Errorf("cookie Secure should be true under X-Forwarded-Proto: https")
			}
		}
	}
	if !sawCookie {
		t.Fatal("session cookie not set")
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestRequestCodeAlwaysOK(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	client := server.Client()

	// Both an existing-ish email and a brand new one yield the same
	// response — anti-enumeration guarantee.
	for _, email := range []string{"alice@example.com", "stranger@nowhere.test"} {
		out := captureStdout(t, func() {
			resp, body := postJSON(t, client, server.URL+"/_auth/request-code",
				map[string]string{"email": email})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s: status = %d, body = %s", email, resp.StatusCode, body)
			}
			if !strings.Contains(string(body), `"ok":true`) {
				t.Errorf("%s: body = %s", email, body)
			}
		})
		_ = extractSinkCode(t, out) // ensures the email was actually sent
	}
}

func TestVerifyCodeSuccessSetsCookieAndReturnsUser(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	client := server.Client()

	out := captureStdout(t, func() {
		resp, _ := postJSON(t, client, server.URL+"/_auth/request-code",
			map[string]string{"email": "alice@example.com"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request-code failed: %d", resp.StatusCode)
		}
	})
	code := extractSinkCode(t, out)

	resp, body := postJSON(t, client, server.URL+"/_auth/verify-code",
		map[string]any{"email": "alice@example.com", "code": code})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify-code: status = %d, body = %s", resp.StatusCode, body)
	}
	if cookie := resp.Header.Get("Set-Cookie"); !strings.Contains(cookie, "mar_session=") {
		t.Errorf("missing session cookie; Set-Cookie = %q", cookie)
	}
	var user map[string]any
	if err := json.Unmarshal(body, &user); err != nil {
		t.Fatalf("user JSON: %v (body: %s)", err, body)
	}
	if user["email"] != "alice@example.com" {
		t.Errorf("user.email = %v, want alice@example.com", user["email"])
	}
	if user["id"] == nil {
		t.Errorf("user.id missing")
	}
}

func TestVerifyCodeWrongCodeIs401(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	client := server.Client()

	_ = captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_auth/request-code",
			map[string]string{"email": "alice@example.com"})
	})

	resp, body := postJSON(t, client, server.URL+"/_auth/verify-code",
		map[string]any{"email": "alice@example.com", "code": "000000"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d (body: %s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "invalid_code") {
		t.Errorf("expected invalid_code error, got: %s", body)
	}
}

func TestVerifyCodeLocksOutAfterTooManyAttempts(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	client := server.Client()

	_ = captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_auth/request-code",
			map[string]string{"email": "alice@example.com"})
	})

	// 4 wrong attempts → still invalid_code (incrementing).
	for i := 0; i < 4; i++ {
		resp, body := postJSON(t, client, server.URL+"/_auth/verify-code",
			map[string]any{"email": "alice@example.com", "code": "000000"})
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i+1, resp.StatusCode)
		}
		if !strings.Contains(string(body), "invalid_code") {
			t.Fatalf("attempt %d: expected invalid_code, got %s", i+1, body)
		}
	}
	// 5th attempt → too_many_attempts; row is now locked.
	resp, body := postJSON(t, client, server.URL+"/_auth/verify-code",
		map[string]any{"email": "alice@example.com", "code": "000000"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("5th attempt: expected 401, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "too_many_attempts") {
		t.Errorf("5th attempt: expected too_many_attempts, got %s", body)
	}
}

func TestRateLimitOnRequestCode(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	client := server.Client()

	// Default per-email limit is 3/hour. Attempts 1-3 succeed; 4th 429s.
	for i := 1; i <= 3; i++ {
		_ = captureStdout(t, func() {
			resp, _ := postJSON(t, client, server.URL+"/_auth/request-code",
				map[string]string{"email": "alice@example.com"})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("attempt %d: expected 200, got %d", i, resp.StatusCode)
			}
		})
	}
	resp, body := postJSON(t, client, server.URL+"/_auth/request-code",
		map[string]string{"email": "alice@example.com"})
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("4th attempt: expected 429, got %d (body: %s)", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "rate_limited") {
		t.Errorf("expected rate_limited, got %s", body)
	}
}

func TestWhoamiWithoutCookieReturnsNull(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	client := server.Client()

	resp, body := getJSON(t, client, server.URL+"/_auth/whoami")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if strings.TrimSpace(string(body)) != "null" {
		t.Errorf("expected `null`, got %s", body)
	}
}

func TestEndToEndLoginAndLogout(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	jar := newJar(t)
	client := &http.Client{Jar: jar}

	out := captureStdout(t, func() {
		resp, _ := postJSON(t, client, server.URL+"/_auth/request-code",
			map[string]string{"email": "alice@example.com"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request-code: %d", resp.StatusCode)
		}
	})
	code := extractSinkCode(t, out)

	resp, _ := postJSON(t, client, server.URL+"/_auth/verify-code",
		map[string]any{"email": "alice@example.com", "code": code})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify-code: %d", resp.StatusCode)
	}

	// /whoami with cookie returns the user.
	resp, body := getJSON(t, client, server.URL+"/_auth/whoami")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("whoami: %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"email":"alice@example.com"`) {
		t.Errorf("expected user JSON, got %s", body)
	}

	// Logout invalidates server-side AND clears the cookie.
	resp, _ = postJSON(t, client, server.URL+"/_auth/logout", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout: %d", resp.StatusCode)
	}

	// /whoami after logout: cookie is cleared (Max-Age=0); jar drops it.
	_, body = getJSON(t, client, server.URL+"/_auth/whoami")
	if strings.TrimSpace(string(body)) != "null" {
		t.Errorf("after logout, /whoami should be null, got %s", body)
	}
}

func getJSON(t *testing.T, client *http.Client, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

// newJar returns a cookiejar.Jar; declared via factory so the
// dependency is local to this test file.
func newJar(t *testing.T) http.CookieJar {
	t.Helper()
	jar, err := cookieJar()
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return jar
}

// ---- Bearer-token transport (native runtimes) -----------------------
//
// Native runtimes (iOS, eventually Android/Windows) don't use cookies —
// they read the session token from the X-Mar-Auth-Token response header
// on verify-code, stash it in platform secure storage (Keychain etc.),
// and attach `Authorization: Bearer …` on every subsequent request.
// The tests below pin that contract end-to-end.

// TestVerifyCodeEmitsBearerTokenHeader checks that the server returns
// the freshly-minted session token in the X-Mar-Auth-Token response
// header alongside the Set-Cookie. Native clients read this header;
// web clients ignore it and rely on the cookie.
func TestVerifyCodeEmitsBearerTokenHeader(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	client := server.Client()

	out := captureStdout(t, func() {
		resp, _ := postJSON(t, client, server.URL+"/_auth/request-code",
			map[string]string{"email": "alice@example.com"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("request-code: %d", resp.StatusCode)
		}
	})
	code := extractSinkCode(t, out)

	resp, body := postJSON(t, client, server.URL+"/_auth/verify-code",
		map[string]any{"email": "alice@example.com", "code": code})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify-code: %d, body: %s", resp.StatusCode, body)
	}
	bearer := resp.Header.Get(bearerTokenHeader)
	if bearer == "" {
		t.Fatalf("missing %s response header", bearerTokenHeader)
	}
	// Same value must also be in the Set-Cookie so the cookie and
	// the bearer point at the same session row server-side.
	setCookie := resp.Header.Get("Set-Cookie")
	if !strings.Contains(setCookie, "mar_session="+bearer) {
		t.Errorf("Set-Cookie %q does not carry the same token as %s = %q",
			setCookie, bearerTokenHeader, bearer)
	}
}

// TestWhoamiWithBearerAuthSucceeds: a cookieless request that carries
// Authorization: Bearer <token> is authenticated as the cookie request
// would have been. Mirrors the iOS app's request shape — no cookie jar,
// just a header.
func TestWhoamiWithBearerAuthSucceeds(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	client := server.Client() // NO cookie jar — native shape.

	out := captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_auth/request-code",
			map[string]string{"email": "alice@example.com"})
	})
	code := extractSinkCode(t, out)

	resp, _ := postJSON(t, client, server.URL+"/_auth/verify-code",
		map[string]any{"email": "alice@example.com", "code": code})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("verify-code: %d", resp.StatusCode)
	}
	tok := resp.Header.Get(bearerTokenHeader)
	if tok == "" {
		t.Fatalf("missing bearer token header")
	}

	// GET /whoami with no cookie + Authorization header — the only
	// transport an iOS app needs.
	req, err := http.NewRequest(http.MethodGet, server.URL+"/_auth/whoami", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `"email":"alice@example.com"`) {
		t.Errorf("expected authed user JSON, got %s", body)
	}
}

// TestBearerLogoutRevokesSession: native logout sends Authorization
// instead of relying on a cookie. The server must delete the row keyed
// off the bearer; a subsequent whoami with the same bearer returns
// null.
func TestBearerLogoutRevokesSession(t *testing.T) {
	server, cleanup := authTestServer(t)
	defer cleanup()
	client := server.Client()

	out := captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_auth/request-code",
			map[string]string{"email": "alice@example.com"})
	})
	code := extractSinkCode(t, out)
	resp, _ := postJSON(t, client, server.URL+"/_auth/verify-code",
		map[string]any{"email": "alice@example.com", "code": code})
	tok := resp.Header.Get(bearerTokenHeader)
	if tok == "" {
		t.Fatalf("missing bearer header")
	}

	// Bearer-credentialed logout.
	logoutReq, _ := http.NewRequest(http.MethodPost, server.URL+"/_auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+tok)
	resp, err := client.Do(logoutReq)
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout: %d", resp.StatusCode)
	}

	// Same bearer is no longer accepted.
	whoamiReq, _ := http.NewRequest(http.MethodGet, server.URL+"/_auth/whoami", nil)
	whoamiReq.Header.Set("Authorization", "Bearer "+tok)
	resp, err = client.Do(whoamiReq)
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.TrimSpace(string(body)) != "null" {
		t.Errorf("after bearer logout, whoami should be null, got %s", body)
	}
}

// TestExtractSessionTokenPrecedence exercises the precedence rule
// directly: Bearer wins over cookie when both are present (sanity
// check for a paranoid future where a stale cookie hangs around
// alongside a fresh header).
func TestExtractSessionTokenPrecedence(t *testing.T) {
	cases := []struct {
		name   string
		header string
		cookie string
		want   string
	}{
		{"both", "Bearer bearer-tok", "cookie-tok", "bearer-tok"},
		{"bearer only", "Bearer bearer-tok", "", "bearer-tok"},
		{"cookie only", "", "cookie-tok", "cookie-tok"},
		{"neither", "", "", ""},
		{"case-insensitive scheme", "bearer lower-tok", "", "lower-tok"},
		{"whitespace around token", "Bearer   spaced-tok  ", "", "spaced-tok"},
		{"empty bearer scheme only", "Bearer ", "cookie-tok", "cookie-tok"},
		{"non-bearer scheme falls through", "Basic abc==", "cookie-tok", "cookie-tok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: cookieName, Value: tc.cookie})
			}
			if got := extractSessionToken(req); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
