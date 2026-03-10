package runtime

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
)

func TestClampAuthRateLimitBoundaries(t *testing.T) {
	if got := clampAuthRateLimit(0, defaultAuthRequestCodeRateLimitPerMinute); got != defaultAuthRequestCodeRateLimitPerMinute {
		t.Fatalf("expected default request-code limit %d, got %d", defaultAuthRequestCodeRateLimitPerMinute, got)
	}
	if got := clampAuthRateLimit(-10, defaultAuthLoginRateLimitPerMinute); got != defaultAuthLoginRateLimitPerMinute {
		t.Fatalf("expected default login limit %d, got %d", defaultAuthLoginRateLimitPerMinute, got)
	}
	if got := clampAuthRateLimit(999999, defaultAuthLoginRateLimitPerMinute); got != maxAuthRateLimitPerMinute {
		t.Fatalf("expected max auth rate limit %d, got %d", maxAuthRateLimitPerMinute, got)
	}
	if got := clampAuthRateLimit(12, defaultAuthLoginRateLimitPerMinute); got != 12 {
		t.Fatalf("expected unchanged auth rate limit 12, got %d", got)
	}
}

func TestAuthRequestCodeRateLimitUsesDefaultPerMinute(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntimeWithSystem(t, filepath.Join(t.TempDir(), "auth-rate-default-request-code.db"), "")

	email := "rate-default@example.com"
	body := `{"email":"` + email + `"}`
	for i := 0; i < defaultAuthRequestCodeRateLimitPerMinute; i++ {
		rec := doRuntimeRequest(r, http.MethodPost, "/auth/request-code", body, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("expected request-code status 200 for attempt %d, got %d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}

	blocked := doRuntimeRequest(r, http.MethodPost, "/auth/request-code", body, "")
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after hitting request-code limit, got %d body=%s", blocked.Code, blocked.Body.String())
	}
	if !strings.Contains(blocked.Body.String(), "You requested too many codes. Please wait a minute and try again.") {
		t.Fatalf("expected request-code rate-limit message, got %s", blocked.Body.String())
	}
	if !strings.Contains(blocked.Body.String(), `"errorCode":"rate_limit_request_code"`) {
		t.Fatalf("expected request-code rate-limit error code, got %s", blocked.Body.String())
	}
}

func TestAuthLoginRateLimitUsesDefaultPerMinute(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntimeWithSystem(t, filepath.Join(t.TempDir(), "auth-rate-default-login.db"), "")
	email := "rate-login@example.com"
	loginCode := requestCodeAndUseKnownCode(t, r, email)
	if strings.TrimSpace(loginCode) == "" {
		t.Fatal("expected login code")
	}

	for i := 0; i < defaultAuthLoginRateLimitPerMinute; i++ {
		rec := doRuntimeRequest(r, http.MethodPost, "/auth/login", `{"email":"`+email+`","code":"999999"}`, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized login attempt %d, got %d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}

	blocked := doRuntimeRequest(r, http.MethodPost, "/auth/login", `{"email":"`+email+`","code":"999999"}`, "")
	if blocked.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after hitting login limit, got %d body=%s", blocked.Code, blocked.Body.String())
	}
	if !strings.Contains(blocked.Body.String(), "Too many sign-in attempts. Please wait a minute and try again.") {
		t.Fatalf("expected login rate-limit message, got %s", blocked.Body.String())
	}
	if !strings.Contains(blocked.Body.String(), `"errorCode":"rate_limit_login"`) {
		t.Fatalf("expected login rate-limit error code, got %s", blocked.Body.String())
	}
}

func TestAuthRateLimitCanBeOverriddenFromSystem(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntimeWithSystem(t, filepath.Join(t.TempDir(), "auth-rate-custom.db"), `
system {
  auth_request_code_rate_limit_per_minute 2
  auth_login_rate_limit_per_minute 3
}
`)

	email := "rate-custom@example.com"
	body := `{"email":"` + email + `"}`

	first := doRuntimeRequest(r, http.MethodPost, "/auth/request-code", body, "")
	if first.Code != http.StatusOK {
		t.Fatalf("expected status 200 on first request-code, got %d body=%s", first.Code, first.Body.String())
	}
	second := doRuntimeRequest(r, http.MethodPost, "/auth/request-code", body, "")
	if second.Code != http.StatusOK {
		t.Fatalf("expected status 200 on second request-code, got %d body=%s", second.Code, second.Body.String())
	}
	third := doRuntimeRequest(r, http.MethodPost, "/auth/request-code", body, "")
	if third.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on third request-code with custom limit 2, got %d body=%s", third.Code, third.Body.String())
	}

	for i := 0; i < 3; i++ {
		rec := doRuntimeRequest(r, http.MethodPost, "/auth/login", `{"email":"`+email+`","code":"999999"}`, "")
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected unauthorized login attempt %d, got %d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
	fourth := doRuntimeRequest(r, http.MethodPost, "/auth/login", `{"email":"`+email+`","code":"999999"}`, "")
	if fourth.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on fourth login with custom limit 3, got %d body=%s", fourth.Code, fourth.Body.String())
	}
}

func mustNewAuthRuntimeWithSystem(t *testing.T, dbPath, systemBlock string) *Runtime {
	t.Helper()

	source := strings.TrimSpace(`
app AuthRateApi
`+systemBlock+`

entity User {
  id: Int primary auto
  email: String
  role: String
}

entity Todo {
  id: Int primary auto
  title: String
}

auth {
  user_entity User
  email_field email
  role_field role
  email_transport console
}
`) + "\n"

	app, err := parser.Parse(source)
	if err != nil {
		t.Fatalf("failed to parse app: %v", err)
	}
	app.Database = dbPath

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}
	return r
}
