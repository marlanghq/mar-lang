package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteErrorOmitsLegacyErrorField(t *testing.T) {
	rec := httptest.NewRecorder()
	var r Runtime

	r.writeError(rec, newAPIError(http.StatusUnauthorized, "auth_required", "Authentication required"))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", rec.Code, rec.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to decode error payload: %v body=%s", err, rec.Body.String())
	}

	if _, exists := payload["error"]; exists {
		t.Fatalf("expected error payload to omit legacy error field, got body=%s", rec.Body.String())
	}
	if payload["message"] != "Authentication required" {
		t.Fatalf("expected message field, got %#v", payload["message"])
	}
	if payload["errorCode"] != "auth_required" {
		t.Fatalf("expected errorCode auth_required, got %#v", payload["errorCode"])
	}
}
