package jsserve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The SPA catch-all must never answer a request under the framework's
// reserved /_mar/ namespace. Real /_mar/ assets (runtime.js, program.json,
// manifest, icons, admin panel) each register their own mux handler and never
// reach serveStaticOrShell; anything under /_mar/ that DOES reach it is a
// framework path that doesn't exist for this app — e.g. /_mar/admin on a
// frontend-only app, where the admin panel isn't mounted. It must 404, not
// leak the HTML shell. (lp is nil here: the reserved-path branch returns
// before renderShell is reached.)
func TestServeStaticOrShellReservesMarNamespace(t *testing.T) {
	for _, p := range []string{"/_mar/admin", "/_mar/admin/", "/_mar/", "/_mar/whatever"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		serveStaticOrShell(rec, req, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: got HTTP %d, want 404 (reserved namespace)", p, rec.Code)
		}
	}
}
