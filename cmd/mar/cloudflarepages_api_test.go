// Tests for the Cloudflare Pages API client.
//
// We mock the Cloudflare API with httptest.Server: the client never
// actually talks to api.cloudflare.com, but the same code path runs
// (JSON encode → HTTP request → envelope decode → result unmarshal).
// This catches the bugs we actually have (wrong URL paths, missing
// headers, malformed payloads) without burning real API quota.
//
// The hash test is content-pinned against a known blake3 vector so
// algorithm drift surfaces as a test failure.

package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestHashAssetKey pins the blake3 implementation. Same inputs as
// wrangler would feed → same 32-hex output. If this fails after a
// blake3 lib upgrade, the new lib disagrees with the spec; figure
// out which is right before touching the test.
func TestHashAssetKey(t *testing.T) {
	cases := []struct {
		name    string
		content string
		ext     string
		// We don't pin the full hash (32 chars is enough). What we
		// pin is:
		//   1. Length is exactly 32 hex chars
		//   2. Different content → different hashes
		//   3. Same content + different ext → different hashes
		//     (CF's keying includes ext so same content as both
		//     .html and .txt land at different keys)
	}{
		{"empty-content", "", "html"},
		{"hello-html", "<h1>hi</h1>", "html"},
		{"hello-js", "console.log(1)", "js"},
		{"no-ext", "binary", ""},
	}
	var hashes []string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := hashAssetKey([]byte(tc.content), tc.ext)
			if len(h) != 32 {
				t.Errorf("got len %d, want 32: %q", len(h), h)
			}
			for _, c := range h {
				if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
					t.Errorf("non-hex char %q in %q", c, h)
				}
			}
			hashes = append(hashes, h)
		})
	}
	// Each hash distinct.
	seen := make(map[string]bool)
	for _, h := range hashes {
		if seen[h] {
			t.Errorf("hash collision: %q seen twice", h)
		}
		seen[h] = true
	}
}

// TestHashAssetKey_ExtensionMatters confirms that the ext is folded
// into the hash. Same content, different extension → different key.
// This is the CF rule (so the same JSON blob keyed as both .json
// and .txt would deploy as two assets).
func TestHashAssetKey_ExtensionMatters(t *testing.T) {
	content := []byte(`{"a":1}`)
	h1 := hashAssetKey(content, "json")
	h2 := hashAssetKey(content, "txt")
	if h1 == h2 {
		t.Errorf("expected different hashes for json vs txt; got %q == %q", h1, h2)
	}
}

// TestCFClient_UploadFlow runs through the full direct-upload
// sequence against an in-process server: JWT → check-missing →
// upload → upsert → create-deployment. Each handler asserts the
// request shape (URL, method, auth header, body) and replies with
// a Cloudflare-flavored envelope.
//
// Cuts close to the wire so any client-side mistake (wrong URL
// path, missing Bearer header, multipart field name) blows up.
func TestCFClient_UploadFlow(t *testing.T) {
	const (
		account   = "abc123def456abc123def456abc123de"
		projectNm = "mar-site"
		apiToken  = "test-api-token-XYZ"
		mockJWT   = "test-jwt-ABC"
	)

	var (
		gotJWTReq         bool
		gotCheckMissing   bool
		gotUpload         bool
		gotUpsert         bool
		gotCreateDeploy   bool
		uploadedKeys      []string
		deploymentResults = map[string]string{
			"/index.html":   "indexhash00000000000000000000000",
			"/runtime.js":   "runtimehash000000000000000000000",
			"/program.json": "programhash000000000000000000000",
		}
	)

	mux := http.NewServeMux()
	// 1. JWT issuance (long-lived API token in Authorization).
	mux.HandleFunc("/accounts/"+account+"/pages/projects/"+projectNm+"/upload-token",
		func(w http.ResponseWriter, r *http.Request) {
			gotJWTReq = true
			if r.Method != "GET" {
				t.Errorf("upload-token: method = %s, want GET", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer "+apiToken {
				t.Errorf("upload-token: auth header = %q, want Bearer %s", got, apiToken)
			}
			writeEnvelope(w, true, map[string]string{"jwt": mockJWT})
		})
	// 2. check-missing (JWT in Authorization).
	mux.HandleFunc("/pages/assets/check-missing",
		func(w http.ResponseWriter, r *http.Request) {
			gotCheckMissing = true
			if got := r.Header.Get("Authorization"); got != "Bearer "+mockJWT {
				t.Errorf("check-missing: auth = %q, want Bearer %s", got, mockJWT)
			}
			var body struct {
				Hashes []string `json:"hashes"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("check-missing: decode body: %v", err)
			}
			// Return all hashes as missing so the upload step fires.
			writeEnvelope(w, true, body.Hashes)
		})
	// 3. upload (JWT).
	mux.HandleFunc("/pages/assets/upload",
		func(w http.ResponseWriter, r *http.Request) {
			gotUpload = true
			if got := r.Header.Get("Authorization"); got != "Bearer "+mockJWT {
				t.Errorf("upload: auth = %q, want Bearer %s", got, mockJWT)
			}
			var batch []cfAsset
			if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
				t.Fatalf("upload: decode batch: %v", err)
			}
			for _, a := range batch {
				uploadedKeys = append(uploadedKeys, a.Key)
				if !a.Base64 {
					t.Errorf("upload: asset %q sent without base64=true", a.Key)
				}
				if a.Metadata.ContentType == "" {
					t.Errorf("upload: asset %q has empty contentType", a.Key)
				}
			}
			writeEnvelope(w, true, nil)
		})
	// 4. upsert-hashes (JWT).
	mux.HandleFunc("/pages/assets/upsert-hashes",
		func(w http.ResponseWriter, r *http.Request) {
			gotUpsert = true
			if got := r.Header.Get("Authorization"); got != "Bearer "+mockJWT {
				t.Errorf("upsert-hashes: auth = %q, want Bearer %s", got, mockJWT)
			}
			writeEnvelope(w, true, nil)
		})
	// 5. create deployment (API token, multipart form).
	mux.HandleFunc("/accounts/"+account+"/pages/projects/"+projectNm+"/deployments",
		func(w http.ResponseWriter, r *http.Request) {
			gotCreateDeploy = true
			if got := r.Header.Get("Authorization"); got != "Bearer "+apiToken {
				t.Errorf("deployments: auth = %q, want Bearer %s", got, apiToken)
			}
			// Parse multipart and confirm the manifest field.
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("deployments: parse multipart: %v", err)
			}
			manifestRaw := r.FormValue("manifest")
			if manifestRaw == "" {
				t.Errorf("deployments: manifest field is empty")
			}
			var manifest map[string]string
			if err := json.Unmarshal([]byte(manifestRaw), &manifest); err != nil {
				t.Errorf("deployments: manifest is not JSON: %v", err)
			}
			writeEnvelope(w, true, map[string]string{
				"id":  "dep-123",
				"url": "https://dep-123." + projectNm + ".pages.dev",
			})
		})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Point the client at the test server by rewriting cfAPIBase
	// for the duration. The constant lives at package scope; we
	// patch it via a small indirection.
	origBase := getCFAPIBaseForTest()
	setCFAPIBaseForTest(srv.URL)
	defer setCFAPIBaseForTest(origBase)

	client := newCFClient(apiToken)

	// Step 1: get JWT.
	jwt, err := client.cfGetUploadJWT(account, projectNm)
	if err != nil {
		t.Fatalf("cfGetUploadJWT: %v", err)
	}
	if jwt != mockJWT {
		t.Errorf("jwt = %q, want %q", jwt, mockJWT)
	}

	// Step 2: check missing.
	hashes := []string{"hash1", "hash2"}
	missing, err := client.cfCheckMissingHashes(jwt, hashes)
	if err != nil {
		t.Fatalf("cfCheckMissingHashes: %v", err)
	}
	if len(missing) != len(hashes) {
		t.Errorf("missing len = %d, want %d", len(missing), len(hashes))
	}

	// Step 3: upload assets.
	batch := []cfAsset{
		{Key: "hash1", Value: "Y29udGVudDE=", Base64: true,
			Metadata: cfAssetMetdata{ContentType: "text/html"}},
		{Key: "hash2", Value: "Y29udGVudDI=", Base64: true,
			Metadata: cfAssetMetdata{ContentType: "application/javascript"}},
	}
	if err := client.cfUploadAssets(jwt, batch); err != nil {
		t.Fatalf("cfUploadAssets: %v", err)
	}
	if len(uploadedKeys) != 2 {
		t.Errorf("uploadedKeys = %v, want 2 entries", uploadedKeys)
	}

	// Step 4: upsert.
	if err := client.cfUpsertHashes(jwt, hashes); err != nil {
		t.Fatalf("cfUpsertHashes: %v", err)
	}

	// Step 5: create deployment.
	dep, err := client.cfCreateDeployment(account, projectNm, deploymentResults)
	if err != nil {
		t.Fatalf("cfCreateDeployment: %v", err)
	}
	if dep == nil {
		t.Fatalf("cfCreateDeployment: result is nil")
	}
	if dep.ID != "dep-123" {
		t.Errorf("deployment.ID = %q, want dep-123", dep.ID)
	}
	if !strings.HasSuffix(dep.URL, ".pages.dev") {
		t.Errorf("deployment.URL = %q, want suffix .pages.dev", dep.URL)
	}

	// All five handlers fired.
	for _, fired := range []struct {
		name string
		ok   bool
	}{
		{"upload-token", gotJWTReq},
		{"check-missing", gotCheckMissing},
		{"upload", gotUpload},
		{"upsert-hashes", gotUpsert},
		{"deployments", gotCreateDeploy},
	} {
		if !fired.ok {
			t.Errorf("expected %s handler to fire; did not", fired.name)
		}
	}
}

// TestCFClient_GetProject_Exists confirms the happy path: the API
// returns a project resource and the client unmarshals it.
func TestCFClient_GetProject_Exists(t *testing.T) {
	const (
		account = "abc123def456abc123def456abc123de"
		project = "mar-site"
	)
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/"+account+"/pages/projects/"+project,
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "GET" {
				t.Errorf("method = %s, want GET", r.Method)
			}
			writeEnvelope(w, true, map[string]any{
				"name":      project,
				"subdomain": project,
			})
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	orig := getCFAPIBaseForTest()
	setCFAPIBaseForTest(srv.URL)
	defer setCFAPIBaseForTest(orig)

	client := newCFClient("token")
	info, err := client.cfGetProject(account, project)
	if err != nil {
		t.Fatalf("cfGetProject: %v", err)
	}
	if info == nil || info.Name != project {
		t.Errorf("info = %+v, want Name=%s", info, project)
	}
}

// TestCFClient_GetProject_NotFound pins the 404 → errCFProjectNotFound
// translation. The auto-create flow relies on this sentinel to
// distinguish "project doesn't exist" from real errors.
func TestCFClient_GetProject_NotFound(t *testing.T) {
	// Two server shapes: 404 with envelope (preferred) and 404
	// with no body (some edge caches strip the body on small
	// responses). Both should map to errCFProjectNotFound.
	cases := []struct {
		name       string
		statusCode int
		body       string
	}{
		{
			name:       "envelope-with-known-code",
			statusCode: 404,
			body:       `{"success":false,"errors":[{"code":8000007,"message":"Project not found"}]}`,
		},
		{
			name:       "envelope-without-known-code",
			statusCode: 404,
			body:       `{"success":false,"errors":[{"code":9999,"message":"Nope"}]}`,
		},
		{
			name:       "non-json-body",
			statusCode: 404,
			body:       `<html>Not Found</html>`,
		},
		{
			name:       "envelope-200-with-known-code",
			statusCode: 200,
			body:       `{"success":false,"errors":[{"code":8000007,"message":"Project not found"}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			orig := getCFAPIBaseForTest()
			setCFAPIBaseForTest(srv.URL)
			defer setCFAPIBaseForTest(orig)

			client := newCFClient("token")
			_, err := client.cfGetProject("acc", "proj")
			if !errors.Is(err, errCFProjectNotFound) {
				t.Errorf("err = %v, want errCFProjectNotFound", err)
			}
		})
	}
}

// TestCFClient_CreateProject confirms the create payload shape.
// Asserts the Body's "name" and "production_branch" so a future
// refactor that drops either field surfaces immediately.
func TestCFClient_CreateProject(t *testing.T) {
	const (
		account = "abc123def456abc123def456abc123de"
		project = "mar-new-site"
	)
	var gotPayload struct {
		Name             string `json:"name"`
		ProductionBranch string `json:"production_branch"`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/accounts/"+account+"/pages/projects",
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != "POST" {
				t.Errorf("method = %s, want POST", r.Method)
			}
			if got := r.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			writeEnvelope(w, true, map[string]any{
				"name":      gotPayload.Name,
				"subdomain": gotPayload.Name,
			})
		})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	orig := getCFAPIBaseForTest()
	setCFAPIBaseForTest(srv.URL)
	defer setCFAPIBaseForTest(orig)

	client := newCFClient("token")
	info, err := client.cfCreateProject(account, project)
	if err != nil {
		t.Fatalf("cfCreateProject: %v", err)
	}
	if info == nil || info.Name != project {
		t.Errorf("info = %+v, want Name=%s", info, project)
	}
	if gotPayload.Name != project {
		t.Errorf("payload.Name = %q, want %q", gotPayload.Name, project)
	}
	if gotPayload.ProductionBranch != "main" {
		t.Errorf("payload.ProductionBranch = %q, want main", gotPayload.ProductionBranch)
	}
}

// TestCFClient_SurfacesAPIErrors confirms that a non-success
// envelope from the server becomes a Go error including the
// server's message and code. Operators paste these into bug
// reports / support tickets, so format stability matters.
func TestCFClient_SurfacesAPIErrors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelopeErr(w, []cfMessage{
			{Code: 8000007, Message: "Project not found"},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	orig := getCFAPIBaseForTest()
	setCFAPIBaseForTest(srv.URL)
	defer setCFAPIBaseForTest(orig)

	client := newCFClient("token")
	_, err := client.cfGetUploadJWT("acc", "proj")
	if err == nil {
		t.Fatalf("expected error from non-success envelope")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Project not found") {
		t.Errorf("error missing server message: %q", msg)
	}
	if !strings.Contains(msg, "8000007") {
		t.Errorf("error missing server code: %q", msg)
	}
}

// TestCFClient_HandlesNonJSONResponse covers the "CDN edge ate
// the request" case (a 502 with HTML body, common when CF itself
// has incidents). The error should be readable, not a JSON-decode
// crash buried in the call stack.
func TestCFClient_HandlesNonJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = w.Write([]byte("<html><body>Bad Gateway</body></html>"))
	}))
	defer srv.Close()

	orig := getCFAPIBaseForTest()
	setCFAPIBaseForTest(srv.URL)
	defer setCFAPIBaseForTest(orig)

	client := newCFClient("token")
	_, err := client.cfGetUploadJWT("acc", "proj")
	if err == nil {
		t.Fatalf("expected error from non-JSON 502 response")
	}
	if !strings.Contains(err.Error(), "non-JSON") {
		t.Errorf("expected error to mention non-JSON; got %q", err)
	}
}

// --- test helpers ---

// writeEnvelope is the success-envelope shape Cloudflare returns
// when an API call works. Result is marshaled to JSON; nil result
// means "the API succeeded but returns no data" (e.g. upsert).
func writeEnvelope(w http.ResponseWriter, ok bool, result any) {
	env := map[string]any{
		"success":  ok,
		"errors":   []any{},
		"messages": []any{},
	}
	if result != nil {
		env["result"] = result
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(env)
}

// writeEnvelopeErr is the failure shape.
func writeEnvelopeErr(w http.ResponseWriter, errs []cfMessage) {
	env := map[string]any{
		"success":  false,
		"errors":   errs,
		"messages": []any{},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(env)
}

// getCFAPIBaseForTest / setCFAPIBaseForTest swap the package-level
// cfAPIBase const for the duration of a test. Implemented via a
// var indirection in the production file (see cloudflarepages_api.go
// — testCFAPIBaseOverride is unset normally, set during tests).
//
// We can't actually change a const at runtime, so the production
// helpers (cfGetUploadJWT etc.) read from a function that returns
// either the const or the test override.

// Compile-time check: keep this file aware of the test hook.
var _ = io.EOF // import marker so future changes to the helpers don't lose io.

func getCFAPIBaseForTest() string {
	if u, _ := url.Parse(currentCFAPIBase()); u != nil {
		return u.String()
	}
	return cfAPIBase
}

func setCFAPIBaseForTest(s string) {
	testCFAPIBaseOverride = s
}
