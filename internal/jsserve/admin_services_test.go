package jsserve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"mar/internal/admin"
	"mar/internal/runtime"
)

// TestAdminServerInfo_BasicShape confirms the endpoint returns the
// expected fields with sensible defaults. We don't pin specific
// values because many fields are environment-dependent.
func TestAdminServerInfo_BasicShape(t *testing.T) {
	// Use the simpler non-helper path because the authedClient
	// abstraction needs an httptest.Server cast we'd rather skip.
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	noteServerBooted()
	client := server.Client()
	out := captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_mar/admin/auth/request-code",
			map[string]string{"email": "admin@x.com"})
	})
	code := extractSinkCode(t, out)
	verifyResp, _ := postJSON(t, client, server.URL+"/_mar/admin/auth/verify-code",
		map[string]string{"email": "admin@x.com", "code": code})
	var token string
	for _, c := range verifyResp.Cookies() {
		if c.Name == "mar_admin_session" {
			token = c.Value
		}
	}

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/_mar/admin/api/server-info", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: token})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status: %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"marVersion", "goVersion", "buildTarget", "bootedAtMs", "requestsTotal", "requestsInFlight"} {
		if _, ok := got[key]; !ok {
			t.Errorf("missing field: %s", key)
		}
	}
}

// TestAdminServerInfo_RequiresAuth — without a session cookie, 401.
func TestAdminServerInfo_RequiresAuth(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	resp, _ := server.Client().Get(server.URL + "/_mar/admin/api/server-info")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401; got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestAdminDBStats_SeparatesFrameworkFromBusiness — _mar_* tables
// land in `frameworkTables`, user tables in `entities`. Lets the
// SPA render two distinct sub-sections so framework noise doesn't
// crowd out the operator's own model.
func TestAdminDBStats_SeparatesFrameworkFromBusiness(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"a@x.com", "b@x.com"})
	defer cleanup()
	client := server.Client()

	// Add a non-framework table to confirm the split — admin test
	// server only seeds _mar_admin_* tables; without something
	// user-flavored, `entities` would always be empty.
	db, _ := runtime.OpenDB()
	_, _ = db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`)
	_, _ = db.Exec(`INSERT INTO notes (id, body) VALUES (1, 'hello')`)

	// Sign in.
	out := captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_mar/admin/auth/request-code",
			map[string]string{"email": "a@x.com"})
	})
	code := extractSinkCode(t, out)
	verifyResp, _ := postJSON(t, client, server.URL+"/_mar/admin/auth/verify-code",
		map[string]string{"email": "a@x.com", "code": code})
	var token string
	for _, c := range verifyResp.Cookies() {
		if c.Name == "mar_admin_session" {
			token = c.Value
		}
	}

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/_mar/admin/api/db-stats", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: token})
	resp, _ := client.Do(req)
	defer resp.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	// Business: should contain `notes` (user-defined), no _mar_*.
	entities, ok := got["entities"].([]any)
	if !ok {
		t.Fatalf("entities not a list: %T", got["entities"])
	}
	var notesFound bool
	for _, e := range entities {
		name, _ := e.(map[string]any)["name"].(string)
		if strings.HasPrefix(name, "_mar_") {
			t.Errorf("entities should NOT contain framework tables; found %q", name)
		}
		if name == "notes" {
			notesFound = true
		}
	}
	if !notesFound {
		t.Errorf("expected `notes` in entities; got %v", entities)
	}

	// Framework: should contain _mar_admins (with rowCount 2 from
	// the seeded admins), no business tables.
	frameworkTables, ok := got["frameworkTables"].([]any)
	if !ok {
		t.Fatalf("frameworkTables not a list: %T", got["frameworkTables"])
	}
	var adminsFound bool
	for _, e := range frameworkTables {
		ent, _ := e.(map[string]any)
		name, _ := ent["name"].(string)
		if !strings.HasPrefix(name, "_mar_") {
			t.Errorf("frameworkTables should only contain _mar_* tables; found %q", name)
		}
		if name == "_mar_admins" {
			adminsFound = true
			rc, _ := ent["rowCount"].(float64)
			if int(rc) != 2 {
				t.Errorf("_mar_admins rowCount: got %v, want 2", rc)
			}
		}
	}
	if !adminsFound {
		t.Errorf("expected _mar_admins in frameworkTables; got %v", frameworkTables)
	}
}

// TestAdminRecentRequests_CaptureAndOrder — the request log
// middleware captures requests in the buffer; recent-requests
// returns them newest-first.
func TestAdminRecentRequests_CaptureAndOrder(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	SetAdminRequestBufferSize(50)
	t.Cleanup(func() { SetAdminRequestBufferSize(0) })

	client := server.Client()
	// Manually fire a few request log entries (the test server
	// doesn't run through adminInstrument because mountAdminHandlers
	// is called on a bare mux — to test the endpoint we just
	// inject directly into the buffer).
	now := time.Now().UnixMilli()
	for i := 1; i <= 3; i++ {
		adminRecord(admin.RequestLog{
			AtMs:       now + int64(i),
			Method:     "GET",
			Path:       "/p" + string(rune('0'+i)),
			Status:     200,
			DurationMs: int64(i),
		})
	}

	// Sign in.
	out := captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_mar/admin/auth/request-code",
			map[string]string{"email": "admin@x.com"})
	})
	code := extractSinkCode(t, out)
	verifyResp, _ := postJSON(t, client, server.URL+"/_mar/admin/auth/verify-code",
		map[string]string{"email": "admin@x.com", "code": code})
	var token string
	for _, c := range verifyResp.Cookies() {
		if c.Name == "mar_admin_session" {
			token = c.Value
		}
	}

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/_mar/admin/api/recent-requests", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: token})
	resp, _ := client.Do(req)
	defer resp.Body.Close()
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	items, ok := got["items"].([]any)
	if !ok {
		t.Fatalf("items not a list: %T", got["items"])
	}
	if len(items) != 3 {
		t.Errorf("expected 3 items; got %d", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["path"] != "/p3" {
		t.Errorf("expected newest first (/p3); got %v", first["path"])
	}
}

// TestAdminEntityRows_BrowsesUserTable — create a synthetic table,
// insert some rows, ask the endpoint for them.
func TestAdminEntityRows_BrowsesUserTable(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()

	db, _ := runtime.OpenDB()
	_, _ = db.Exec(`CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT)`)
	for i := 1; i <= 5; i++ {
		_, _ = db.Exec(`INSERT INTO notes (id, body) VALUES (?, ?)`, i, "note "+string(rune('0'+i)))
	}

	client := server.Client()
	out := captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_mar/admin/auth/request-code",
			map[string]string{"email": "admin@x.com"})
	})
	code := extractSinkCode(t, out)
	verifyResp, _ := postJSON(t, client, server.URL+"/_mar/admin/auth/verify-code",
		map[string]string{"email": "admin@x.com", "code": code})
	var token string
	for _, c := range verifyResp.Cookies() {
		if c.Name == "mar_admin_session" {
			token = c.Value
		}
	}

	req, _ := http.NewRequest(http.MethodGet,
		server.URL+"/_mar/admin/api/entity-rows?entity=notes&limit=3", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: token})
	resp, _ := client.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	items, _ := got["items"].([]any)
	if len(items) != 3 {
		t.Errorf("expected 3 items (limit=3); got %d", len(items))
	}
	if got["nextCursor"] == nil {
		t.Errorf("expected nextCursor to be set (more rows available)")
	}
	cols, _ := got["columns"].([]any)
	if len(cols) != 2 || cols[0] != "id" || cols[1] != "body" {
		t.Errorf("columns: got %v", cols)
	}
}

// TestRequestLog_SkipsFrameworkInternalPaths — the panel polling
// itself shouldn't crowd out the user's actual app traffic in the
// recent requests view. /_mar/admin/* and /_mar/reload don't get
// recorded; everything else does.
func TestRequestLog_SkipsFrameworkInternalPaths(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/", false},
		{"/api/users", false},
		{"/_auth/whoami", false},
		{"/_mar/admin", true},
		{"/_mar/admin/", true},
		{"/_mar/admin/api/whoami", true},
		{"/_mar/admin/api/server-info", true},
		{"/_mar/admin/auth/request-code", true},
		{"/_mar/admin/static/admin.js", true},
		{"/_mar/reload", true},
	}
	for _, tc := range cases {
		got := isFrameworkInternalPath(tc.path)
		if got != tc.want {
			t.Errorf("path=%q: got %v, want %v", tc.path, got, tc.want)
		}
	}
}

// TestAdminEntityRows_RejectsUnknownEntity — the entity param is
// whitelisted against the live schema, so an attacker can't pivot
// arbitrary SQL through it.
func TestAdminEntityRows_RejectsUnknownEntity(t *testing.T) {
	server, cleanup := adminTestServer(t, []string{"admin@x.com"})
	defer cleanup()
	client := server.Client()

	out := captureStdout(t, func() {
		_, _ = postJSON(t, client, server.URL+"/_mar/admin/auth/request-code",
			map[string]string{"email": "admin@x.com"})
	})
	code := extractSinkCode(t, out)
	verifyResp, _ := postJSON(t, client, server.URL+"/_mar/admin/auth/verify-code",
		map[string]string{"email": "admin@x.com", "code": code})
	var token string
	for _, c := range verifyResp.Cookies() {
		if c.Name == "mar_admin_session" {
			token = c.Value
		}
	}

	req, _ := http.NewRequest(http.MethodGet,
		server.URL+"/_mar/admin/api/entity-rows?entity=does_not_exist", nil)
	req.AddCookie(&http.Cookie{Name: "mar_admin_session", Value: token})
	resp, _ := client.Do(req)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404; got %d", resp.StatusCode)
	}
}
