package runtime

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func TestLegacyBackupAliasIsNotServed(t *testing.T) {
	requireSQLite3(t)

	r := mustNewRuntimeFromSource(t, filepath.Join(t.TempDir(), "backups-route.db"), `
app TodoApi

auth {
  email_transport console
  email_from "no-reply@example.com"
  email_subject "Your login code"
}

entity Todo {
  title: String
  authorize all when user_role == "admin"
}
`)

	rec := doRuntimeRequest(r, http.MethodPost, "/_mar/backup", "", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for legacy backup alias, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminCanDownloadBackupByName(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntime(t, filepath.Join(t.TempDir(), "backups-download.db"))
	loginCode := requestCodeAndUseKnownCode(t, r, "owner@example.com")

	loginRec := doRuntimeRequest(r, http.MethodPost, "/auth/login", `{"email":"owner@example.com","code":"`+loginCode+`"}`, "")
	if loginRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /auth/login, got %d body=%s", loginRec.Code, loginRec.Body.String())
	}

	var loginPayload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginPayload); err != nil {
		t.Fatalf("failed to decode login response: %v body=%s", err, loginRec.Body.String())
	}
	if loginPayload.Token == "" {
		t.Fatalf("expected login token in response, got body=%s", loginRec.Body.String())
	}

	createRec := doRuntimeRequest(r, http.MethodPost, "/_mar/backups", "", loginPayload.Token)
	if createRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /_mar/backups, got %d body=%s", createRec.Code, createRec.Body.String())
	}

	var createPayload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(createRec.Body.Bytes(), &createPayload); err != nil {
		t.Fatalf("failed to decode backup create response: %v body=%s", err, createRec.Body.String())
	}

	name := filepath.Base(createPayload.Path)
	downloadRec := doRuntimeRequest(r, http.MethodGet, "/_mar/backups/download?name="+name, "", loginPayload.Token)
	if downloadRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from backup download, got %d body=%s", downloadRec.Code, downloadRec.Body.String())
	}
	if got := downloadRec.Header().Get("Content-Disposition"); got == "" || got != `attachment; filename="`+name+`"` {
		t.Fatalf("expected attachment content disposition for %s, got %q", name, got)
	}
	if downloadRec.Body.Len() == 0 {
		t.Fatalf("expected backup download body to be non-empty")
	}
}
