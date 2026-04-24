package runtime

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func TestHealthReportsStartupPendingAndFailed(t *testing.T) {
	requireSQLite3(t)

	app := mustParseApp(t, `
(define todo
  (entity
    (fields
      ((title string)))))

(define-app startup-health-api
  (entities todo))
`)
	app.Database = filepath.Join(t.TempDir(), "startup-health.db")

	r, err := New(app)
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	r.beginStartupReadiness()

	pendingRec := doRuntimeRequest(r, http.MethodGet, "/health", "", "")
	if pendingRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 while startup checks are pending, got %d body=%s", pendingRec.Code, pendingRec.Body.String())
	}
	var pending map[string]any
	if err := json.Unmarshal(pendingRec.Body.Bytes(), &pending); err != nil {
		t.Fatalf("decode pending health failed: %v", err)
	}
	if pending["status"] != "starting" {
		t.Fatalf("expected pending status to be starting, got %+v", pending)
	}

	r.startupMu.Lock()
	r.startupErr = errTestStartupFailure
	r.startupMu.Unlock()

	failedRec := doRuntimeRequest(r, http.MethodGet, "/health", "", "")
	if failedRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 after startup failure, got %d body=%s", failedRec.Code, failedRec.Body.String())
	}
	var failed map[string]any
	if err := json.Unmarshal(failedRec.Body.Bytes(), &failed); err != nil {
		t.Fatalf("decode failed health failed: %v", err)
	}
	if failed["status"] != "failed" {
		t.Fatalf("expected failed status to be failed, got %+v", failed)
	}
}

var errTestStartupFailure = newAPIError(http.StatusServiceUnavailable, "startup_check_failed", "boom")
