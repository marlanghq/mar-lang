package runtime

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"mar/internal/parser"
)

func TestBootstrapAdminRequiresCodeLoginToPromoteFirstUser(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntime(t, filepath.Join(t.TempDir(), "bootstrap-empty.db"))

	rec := httptest.NewRecorder()
	if err := r.handleBootstrapAdmin(rec, "", map[string]any{"email": "owner@example.com"}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	countRow, ok, err := queryRow(r.DB, `SELECT COUNT(*) AS total FROM users`)
	if err != nil {
		t.Fatalf("count users failed: %v", err)
	}
	if !ok {
		t.Fatal("count users returned no rows")
	}
	totalUsers, _ := toInt64(countRow["total"])
	if totalUsers != 1 {
		t.Fatalf("expected 1 user after bootstrap, got %d", totalUsers)
	}

	user, found, err := r.loadAuthUserByEmail("", "owner@example.com")
	if err != nil {
		t.Fatalf("load user failed: %v", err)
	}
	if !found {
		t.Fatal("expected bootstrap user to exist")
	}
	role, _ := user["role"].(string)
	if strings.ToLower(strings.TrimSpace(role)) != "user" {
		t.Fatalf("expected role user before login, got %q", role)
	}

	loginCode := overwriteLatestCodeForEmail(t, r, "owner@example.com")
	token := loginWithCodeAndReadToken(t, r, "owner@example.com", loginCode)
	if strings.TrimSpace(token) == "" {
		t.Fatal("expected token after login")
	}

	user, found, err = r.loadAuthUserByEmail("", "owner@example.com")
	if err != nil {
		t.Fatalf("load user after login failed: %v", err)
	}
	if !found {
		t.Fatal("expected user to exist after login")
	}
	role, _ = user["role"].(string)
	if strings.ToLower(strings.TrimSpace(role)) != "admin" {
		t.Fatalf("expected role admin after login, got %q", role)
	}

	meRec := doRuntimeRequest(r, http.MethodGet, "/auth/me", "", token)
	if meRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /auth/me, got %d body=%s", meRec.Code, meRec.Body.String())
	}
}

func TestBootstrapAdminBlockedWhenAnyUserAlreadyExists(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntime(t, filepath.Join(t.TempDir(), "bootstrap-blocked.db"))

	// First request-code auto-creates a regular user.
	requestRec := httptest.NewRecorder()
	if err := r.handleAuthRequestCode(requestRec, "", map[string]any{"email": "user@example.com"}); err != nil {
		t.Fatalf("request-code failed: %v", err)
	}

	// Bootstrap must now be blocked because there is already at least one user.
	bootstrapRec := httptest.NewRecorder()
	err := r.handleBootstrapAdmin(bootstrapRec, "", map[string]any{"email": "admin@example.com"})
	if err == nil {
		t.Fatal("expected bootstrap to be blocked when users already exist")
	}

	apiErr, ok := err.(*apiError)
	if !ok {
		t.Fatalf("expected apiError, got %T: %v", err, err)
	}
	if apiErr.Status != 409 {
		t.Fatalf("expected status 409, got %d", apiErr.Status)
	}
	if !strings.Contains(apiErr.Message, "no users") {
		t.Fatalf("unexpected error message: %q", apiErr.Message)
	}
}

func TestBootstrapAdminAcceptsRequiredScalarFields(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntimeFromSource(t, filepath.Join(t.TempDir(), "bootstrap-required-scalars.db"), `
app AuthBootstrapApi

entity User {
  id: Int primary auto
  email: String
  role: String
  name: String
  surname: String
}

auth {
  email_transport console
}
`)

	rec := httptest.NewRecorder()
	if err := r.handleBootstrapAdmin(rec, "", map[string]any{
		"email":   "owner@example.com",
		"name":    "Ada",
		"surname": "Lovelace",
	}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	user, found, err := r.loadAuthUserByEmail("", "owner@example.com")
	if err != nil {
		t.Fatalf("load user failed: %v", err)
	}
	if !found {
		t.Fatal("expected bootstrap user to exist")
	}
	if got, _ := user["name"].(string); got != "Ada" {
		t.Fatalf("expected name Ada, got %#v", user["name"])
	}
	if got, _ := user["surname"].(string); got != "Lovelace" {
		t.Fatalf("expected surname Lovelace, got %#v", user["surname"])
	}
}

func TestBootstrapAdminReportsMissingRequiredScalarFields(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntimeFromSource(t, filepath.Join(t.TempDir(), "bootstrap-missing-required.db"), `
app AuthBootstrapApi

entity User {
  id: Int primary auto
  email: String
  role: String
  name: String
  surname: String
}

auth {
  email_transport console
}
`)

	rec := httptest.NewRecorder()
	err := r.handleBootstrapAdmin(rec, "", map[string]any{"email": "owner@example.com"})
	if err == nil {
		t.Fatal("expected bootstrap to fail when required scalar fields are missing")
	}

	apiErr, ok := err.(*apiError)
	if !ok {
		t.Fatalf("expected apiError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", apiErr.Status)
	}
	if apiErr.Code != "bootstrap_fields_required" {
		t.Fatalf("expected bootstrap_fields_required, got %q", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "name, surname") {
		t.Fatalf("unexpected error message: %q", apiErr.Message)
	}
}

func TestBootstrapAdminBlocksRequiredRelationFields(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntimeFromSource(t, filepath.Join(t.TempDir(), "bootstrap-required-relation.db"), `
app AuthBootstrapApi

entity Team {
  id: Int primary auto
  name: String
}

entity User {
  id: Int primary auto
  email: String
  role: String
  belongs_to Team
}

auth {
  email_transport console
}
`)

	rec := httptest.NewRecorder()
	err := r.handleBootstrapAdmin(rec, "", map[string]any{"email": "owner@example.com"})
	if err == nil {
		t.Fatal("expected bootstrap to fail for required relation field")
	}

	apiErr, ok := err.(*apiError)
	if !ok {
		t.Fatalf("expected apiError, got %T: %v", err, err)
	}
	if apiErr.Status != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d", apiErr.Status)
	}
	if apiErr.Code != "bootstrap_relation_not_supported" {
		t.Fatalf("expected bootstrap_relation_not_supported, got %q", apiErr.Code)
	}
	if !strings.Contains(apiErr.Message, "team") {
		t.Fatalf("unexpected error message: %q", apiErr.Message)
	}
}

func TestRequestCodeCreatesAdminWhenNoUsersExist(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntime(t, filepath.Join(t.TempDir(), "request-code-first-admin.db"))

	requestRec := httptest.NewRecorder()
	if err := r.handleAuthRequestCode(requestRec, "", map[string]any{"email": "first@example.com"}); err != nil {
		t.Fatalf("request-code failed: %v", err)
	}

	user, found, err := r.loadAuthUserByEmail("", "first@example.com")
	if err != nil {
		t.Fatalf("load user failed: %v", err)
	}
	if !found {
		t.Fatal("expected first user to be created")
	}
	role, _ := user["role"].(string)
	if strings.ToLower(strings.TrimSpace(role)) != "admin" {
		t.Fatalf("expected first user role to be admin, got %q", role)
	}

	countRow, ok, err := queryRow(r.DB, `SELECT COUNT(*) AS total FROM users`)
	if err != nil {
		t.Fatalf("count users failed: %v", err)
	}
	if !ok {
		t.Fatal("count users returned no rows")
	}
	totalUsers, _ := toInt64(countRow["total"])
	if totalUsers != 1 {
		t.Fatalf("expected exactly 1 user, got %d", totalUsers)
	}
}

func mustNewAuthRuntime(t *testing.T, dbPath string) *Runtime {
	t.Helper()
	app, err := parser.Parse(strings.TrimSpace(`
app AuthBootstrapApi

entity User {
  id: Int primary auto
  email: String
  role: String
}

entity Todo {
  id: Int primary auto
  title: String

  authorize read when user_authenticated
  authorize create when user_authenticated
  authorize update when user_authenticated
  authorize delete when user_authenticated
}

auth {
  email_transport console
}
`) + "\n")
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

func mustNewAuthRuntimeFromSource(t *testing.T, dbPath string, source string) *Runtime {
	t.Helper()
	app, err := parser.Parse(strings.TrimSpace(source) + "\n")
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
