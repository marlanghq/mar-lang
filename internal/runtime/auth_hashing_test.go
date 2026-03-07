package runtime

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuthCodesAndSessionsAreStoredAsHashes(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntime(t, filepath.Join(t.TempDir(), "auth-hashing.db"))
	email := "hashed@example.com"

	devCode := requestCodeAndReadDevCode(t, r, email)
	codeRow, ok, err := queryRow(r.DB, `SELECT code FROM mar_auth_codes WHERE email = ? ORDER BY id DESC LIMIT 1`, email)
	if err != nil {
		t.Fatalf("load auth code failed: %v", err)
	}
	if !ok {
		t.Fatal("expected auth code row")
	}
	storedCode, _ := codeRow["code"].(string)
	if storedCode == strings.TrimSpace(devCode) {
		t.Fatalf("expected auth code to be stored hashed, got raw value %q", storedCode)
	}
	if !strings.HasPrefix(storedCode, "sha256:") {
		t.Fatalf("expected auth code hash prefix, got %q", storedCode)
	}

	token := loginWithCodeAndReadToken(t, r, email, devCode)
	sessionRow, ok, err := queryRow(r.DB, `SELECT token FROM mar_sessions WHERE email = ? ORDER BY created_at DESC LIMIT 1`, email)
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	if !ok {
		t.Fatal("expected session row")
	}
	storedToken, _ := sessionRow["token"].(string)
	if storedToken == strings.TrimSpace(token) {
		t.Fatalf("expected session token to be stored hashed, got raw value %q", storedToken)
	}
	if !strings.HasPrefix(storedToken, "sha256:") {
		t.Fatalf("expected session token hash prefix, got %q", storedToken)
	}
}

func TestLegacyPlainTextCodesAndSessionsStillWork(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntime(t, filepath.Join(t.TempDir(), "auth-legacy-compat.db"))
	email := "legacy@example.com"
	_ = requestCodeAndReadDevCode(t, r, email)

	user, found, err := r.loadAuthUserByEmail(email)
	if err != nil {
		t.Fatalf("load auth user failed: %v", err)
	}
	if !found {
		t.Fatal("expected auth user to exist")
	}

	userID := user["id"]
	if r.usesAppAuthEntity() {
		userID = user[r.authUser.PrimaryKey]
	}

	legacyCode := "123456"
	now := time.Now().UnixMilli()
	if _, err := r.DB.Exec(
		`INSERT INTO mar_auth_codes (email, user_id, code, grant_role, expires_at, used, created_at) VALUES (?, ?, ?, ?, ?, 0, ?)`,
		email,
		userID,
		legacyCode,
		"",
		now+600000,
		now+1,
	); err != nil {
		t.Fatalf("insert legacy auth code failed: %v", err)
	}

	loginRec := doRuntimeRequest(r, http.MethodPost, "/auth/login", `{"email":"`+email+`","code":"`+legacyCode+`"}`, "")
	if loginRec.Code != http.StatusOK {
		t.Fatalf("expected legacy auth code login to succeed, got %d body=%s", loginRec.Code, loginRec.Body.String())
	}

	legacyToken := "legacy-plain-token"
	if _, err := r.DB.Exec(
		`INSERT INTO mar_sessions (token, user_id, email, expires_at, revoked, created_at) VALUES (?, ?, ?, ?, 0, ?)`,
		legacyToken,
		userID,
		email,
		now+600000,
		now+2,
	); err != nil {
		t.Fatalf("insert legacy session failed: %v", err)
	}

	meRec := doRuntimeRequest(r, http.MethodGet, "/auth/me", "", legacyToken)
	if meRec.Code != http.StatusOK {
		t.Fatalf("expected legacy session token to authenticate, got %d body=%s", meRec.Code, meRec.Body.String())
	}
}
