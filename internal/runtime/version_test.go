package runtime

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func TestVersionEndpointPublicPayload(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntime(t, filepath.Join(t.TempDir(), "version-public.db"))
	r.SetVersionInfo(VersionInfo{
		BelmVersion:   "v1.2.3",
		BelmCommit:    "abc123",
		BelmBuildTime: "2026-03-04T16:00:00Z",
		AppBuildTime:  "2026-03-04T16:10:00Z",
		ManifestHash:  "sha256:deadbeef",
	})

	rec := doRuntimeRequest(r, http.MethodGet, "/_belm/version", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /_belm/version, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode /_belm/version failed: %v body=%s", err, rec.Body.String())
	}
	if _, hasBelm := payload["belm"]; hasBelm {
		t.Fatalf("public version payload must not include belm details: %+v", payload)
	}
	app, ok := payload["app"].(map[string]any)
	if !ok {
		t.Fatalf("expected app object in payload: %+v", payload)
	}
	if app["name"] != "AuthBootstrapApi" {
		t.Fatalf("unexpected app.name: %+v", app)
	}
	if app["manifestHash"] != "sha256:deadbeef" {
		t.Fatalf("unexpected app.manifestHash: %+v", app)
	}
}

func TestVersionEndpointAdminRequiresAdminRole(t *testing.T) {
	requireSQLite3(t)

	r := mustNewAuthRuntime(t, filepath.Join(t.TempDir(), "version-admin.db"))
	r.SetVersionInfo(VersionInfo{
		BelmVersion:   "v1.2.3",
		BelmCommit:    "abc123",
		BelmBuildTime: "2026-03-04T16:00:00Z",
		AppBuildTime:  "2026-03-04T16:10:00Z",
		ManifestHash:  "sha256:deadbeef",
	})

	unauth := doRuntimeRequest(r, http.MethodGet, "/_belm/version/admin", "", "")
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d body=%s", unauth.Code, unauth.Body.String())
	}

	devCode := requestCodeAndReadDevCode(t, r, "admin@example.com")
	token := loginWithCodeAndReadToken(t, r, "admin@example.com", devCode)

	rec := doRuntimeRequest(r, http.MethodGet, "/_belm/version/admin", "", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from /_belm/version/admin, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload struct {
		App struct {
			Name string `json:"name"`
		} `json:"app"`
		Belm struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
		} `json:"belm"`
		Runtime struct {
			GoVersion string `json:"goVersion"`
			Platform  string `json:"platform"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode /_belm/version/admin failed: %v body=%s", err, rec.Body.String())
	}
	if payload.Belm.Version != "v1.2.3" || payload.Belm.Commit != "abc123" {
		t.Fatalf("unexpected belm payload: %+v", payload.Belm)
	}
	if payload.Runtime.GoVersion == "" || payload.Runtime.Platform == "" {
		t.Fatalf("expected runtime payload fields, got: %+v", payload.Runtime)
	}
}
