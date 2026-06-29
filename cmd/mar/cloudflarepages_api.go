// HTTP client for the Cloudflare Pages Direct Upload API.
//
// Five endpoints together form the deploy flow. Each one returns a
// `success` envelope; the helpers below unwrap it into a clean Go
// value (or an error with the server's message preserved).
//
//   1. cfGetUploadJWT           — short-lived JWT to upload to the asset store
//   2. cfCheckMissingHashes     — server tells us which file hashes it lacks
//   3. cfUploadAssets           — upload missing files in batches
//   4. cfUpsertHashes           — confirm uploads (commits to project's keyspace)
//   5. cfCreateDeployment       — create a deployment from the asset manifest
//
// Hashing scheme: blake3(content || extension) → 32 hex chars. This
// matches what wrangler does so the asset store keys are shared
// across both tools — useful if the same project is deployed from
// wrangler AND mar (which shouldn't happen but is harmless if it does).

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/zeebo/blake3"
)

// testCFAPIBaseOverride lets tests point the client at a local
// httptest.Server instead of the real api.cloudflare.com. Empty
// in production (currentCFAPIBase falls back to cfAPIBase).
var testCFAPIBaseOverride string

// currentCFAPIBase returns the API root the client should use.
// Tests set testCFAPIBaseOverride; production callers use the
// constant.
func currentCFAPIBase() string {
	if testCFAPIBaseOverride != "" {
		return testCFAPIBaseOverride
	}
	return cfAPIBase
}

const (
	// cfAPIBase is the root for all Cloudflare Pages API calls.
	cfAPIBase = "https://api.cloudflare.com/client/v4"

	// cfRequestTimeout caps a single API call. Upload batches can
	// genuinely take 15-20s on slow links, so the timeout is
	// generous; the deploy as a whole is the spinner-worthy wait.
	cfRequestTimeout = 60 * time.Second

	// cfUploadBatchSize caps how many assets we upload per request.
	// Cloudflare's documented limit is "max 5000 files OR 50 MB per
	// batch". Mar dist/ bundles are tiny in both dimensions, so a
	// single batch always fits — but we keep the constant explicit
	// so future bigger bundles surface the right limit without a
	// magic number to chase.
	cfUploadBatchSize = 100
)

// cfEnvelope is the standard Cloudflare API response wrapper.
// Errors come back inside this even for 4xx/5xx status codes,
// so we always decode into this first.
type cfEnvelope struct {
	Success  bool              `json:"success"`
	Errors   []cfMessage       `json:"errors,omitempty"`
	Messages []cfMessage       `json:"messages,omitempty"`
	Result   json.RawMessage   `json:"result,omitempty"`
	Info     map[string]string `json:"result_info,omitempty"`
}

type cfMessage struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// asError turns a non-success envelope into a *cfAPIError that
// preserves every server-reported message AND the structured
// codes. Callers can `errors.As` for *cfAPIError and call
// HasCode to detect specific failure modes (auth, rate limit,
// project-not-found, etc.) without string-matching the message.
func (e *cfEnvelope) asError(context string) error {
	if e.Success {
		return nil
	}
	return &cfAPIError{Context: context, Errors: e.Errors}
}

// cfAPIError carries a structured Cloudflare API error response.
// The Error() format is stable (operators paste it into bug
// reports / support tickets), so callers should rely on HasCode
// rather than string-matching when they need to branch on a
// specific failure.
type cfAPIError struct {
	Context string      // e.g. "get project", "create deployment"
	Errors  []cfMessage // 1+ entries; CF can return multiple per request
}

func (e *cfAPIError) Error() string {
	if len(e.Errors) == 0 {
		return fmt.Sprintf("%s: cloudflare api reported failure with no error details", e.Context)
	}
	if len(e.Errors) == 1 {
		return fmt.Sprintf("%s: cloudflare api: %s (code %d)",
			e.Context, e.Errors[0].Message, e.Errors[0].Code)
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s: cloudflare api reported %d errors:", e.Context, len(e.Errors))
	for _, m := range e.Errors {
		fmt.Fprintf(&b, "\n  - %s (code %d)", m.Message, m.Code)
	}
	return b.String()
}

// HasCode reports whether any of the reported errors carry the
// given Cloudflare error code. Used to detect failure modes that
// deserve structured guidance (e.g. auth failures vs. random 500s).
func (e *cfAPIError) HasCode(code int) bool {
	for _, m := range e.Errors {
		if m.Code == code {
			return true
		}
	}
	return false
}

// cfClient bundles the HTTP client + base auth so individual call
// sites stay focused on payload shape rather than transport setup.
// Two separate auth modes: API token (account-scoped, used for
// project and deployment endpoints) and JWT (short-lived, scoped to
// asset uploads on this specific project).
type cfClient struct {
	http     *http.Client
	apiToken string // long-lived, from CLOUDFLARE_API_TOKEN
}

func newCFClient(apiToken string) *cfClient {
	return &cfClient{
		http: &http.Client{
			Timeout: cfRequestTimeout,
		},
		apiToken: apiToken,
	}
}

// doJSON sends a JSON request with the API token and decodes the
// response envelope. The success flag is checked here; on failure
// the caller gets a single composed error message.
func (c *cfClient) doJSON(method, url string, body any, context string, into any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%s: marshal request: %w", context, err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("%s: new request: %w", context, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", context, err)
	}
	defer resp.Body.Close()
	return decodeCFResponse(resp, context, into)
}

// doJSONWithJWT mirrors doJSON but uses an asset-upload JWT instead
// of the long-lived API token. JWTs only work against
// /pages/assets/* — for account/project endpoints, doJSON is right.
func (c *cfClient) doJSONWithJWT(method, url, jwt string, body any, context string, into any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%s: marshal request: %w", context, err)
		}
		bodyReader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("%s: new request: %w", context, err)
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", context, err)
	}
	defer resp.Body.Close()
	return decodeCFResponse(resp, context, into)
}

// decodeCFResponse handles the envelope decode + success check
// uniformly across both auth modes. Reads the body once, attempts
// to decode the envelope, and surfaces a clean error if the API
// reports failure.
func decodeCFResponse(resp *http.Response, context string, into any) error {
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read response body: %w", context, err)
	}
	var env cfEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// Non-JSON body (e.g. 502 from CDN edge). Surface the
		// status + first 200 bytes so debugging doesn't require
		// firing up Wireshark.
		snippet := string(raw)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return fmt.Errorf("%s: cloudflare api returned %s (non-JSON): %s",
			context, resp.Status, snippet)
	}
	if err := env.asError(context); err != nil {
		return err
	}
	if into != nil && len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, into); err != nil {
			return fmt.Errorf("%s: decode result: %w", context, err)
		}
	}
	return nil
}

// errCFProjectNotFound is the sentinel returned by cfGetProject
// when the API responds with the "project not found" code (CF
// error 8000007, sometimes paired with HTTP 404). Callers use
// errors.Is to detect this case and trigger the auto-create flow.
//
// Other errors from cfGetProject (auth lapsed, network blip, 5xx)
// stay as regular wrapped errors so they're not silently
// converted into "the project doesn't exist".
var errCFProjectNotFound = fmt.Errorf("cloudflare pages: project not found")

// cfProjectInfo is the subset of the project resource we care
// about. Cloudflare returns 30+ fields (build config, env vars,
// deployment configs, etc.) but for our flow we only need to
// confirm the project exists and read back its subdomain.
type cfProjectInfo struct {
	Name      string `json:"name"`
	Subdomain string `json:"subdomain"`
}

// cfGetProject checks whether `projectName` exists under `accountID`.
// Returns the project info on success, errCFProjectNotFound when
// the project doesn't exist, or a wrapped error otherwise.
//
// The dance with HTTP status + envelope error code exists because
// Cloudflare's API is inconsistent about how it signals "not
// found": some endpoints return 404 with no body, others return
// 200 with an envelope error. We check both signals to be safe.
//
// CF error code reference: 8000007 = "Project not found". Pinning
// the number rather than parsing the message text — codes are
// the documented stable interface.
func (c *cfClient) cfGetProject(accountID, projectName string) (*cfProjectInfo, error) {
	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s",
		currentCFAPIBase(), accountID, projectName)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("get project: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get project: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("get project: read response body: %w", err)
	}
	var env cfEnvelope
	if jerr := json.Unmarshal(raw, &env); jerr != nil {
		// Non-JSON body. If status is 404 still treat as
		// not-found (some edge caches strip the body); otherwise
		// surface the raw response.
		if resp.StatusCode == 404 {
			return nil, errCFProjectNotFound
		}
		snippet := string(raw)
		if len(snippet) > 200 {
			snippet = snippet[:200] + "…"
		}
		return nil, fmt.Errorf("get project: cloudflare api returned %s (non-JSON): %s",
			resp.Status, snippet)
	}
	if !env.Success {
		for _, e := range env.Errors {
			if e.Code == cfErrCodeProjectNotFound {
				return nil, errCFProjectNotFound
			}
		}
		// HTTP 404 with valid envelope but a different code:
		// still treat as not-found. Belt + suspenders.
		if resp.StatusCode == 404 {
			return nil, errCFProjectNotFound
		}
		return nil, env.asError("get project")
	}
	var info cfProjectInfo
	if len(env.Result) > 0 {
		if err := json.Unmarshal(env.Result, &info); err != nil {
			return nil, fmt.Errorf("get project: decode result: %w", err)
		}
	}
	return &info, nil
}

// Documented Cloudflare API error codes that we recognize and
// branch on. Pinned as named constants so a future protocol change
// shows up here, not embedded in conditional logic spread across
// the file.
const (
	// 8000007 — Pages project not found (also used as a "doesn't
	// exist yet" signal by cfGetProject).
	cfErrCodeProjectNotFound = 8000007

	// 9106 — generic "Authentication failed". Returned when the
	// API token is wrong, expired, revoked, or doesn't carry the
	// right permission scope.
	cfErrCodeAuthFailed = 9106

	// 10000 — "Authentication error". Alternate auth-rejection
	// code CF emits for some endpoints. Treat identically.
	cfErrCodeAuthError = 10000
)

// cfCreateProject creates a new Pages project under `accountID`
// configured for Direct Upload (no git integration, no build
// config — all of which would be irrelevant since `mar build`
// produces the bundle locally).
//
// `productionBranch` is just metadata — we pass "main" because
// CF requires *some* string here, but for Direct Upload projects
// the branch only affects which deployments get promoted to the
// production URL. Since we always upload to production by
// omitting `branch` in the deployment payload, the project-level
// branch is moot.
func (c *cfClient) cfCreateProject(accountID, projectName string) (*cfProjectInfo, error) {
	url := fmt.Sprintf("%s/accounts/%s/pages/projects",
		currentCFAPIBase(), accountID)
	body := map[string]any{
		"name":              projectName,
		"production_branch": "main",
	}
	var info cfProjectInfo
	if err := c.doJSON("POST", url, body, "create project", &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// cfGetUploadJWT fetches a short-lived JWT scoped to the project's
// asset store. Subsequent /pages/assets/* calls must use this JWT
// instead of the long-lived API token.
func (c *cfClient) cfGetUploadJWT(accountID, projectName string) (string, error) {
	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s/upload-token",
		currentCFAPIBase(), accountID, projectName)
	var result struct {
		JWT string `json:"jwt"`
	}
	if err := c.doJSON("GET", url, nil, "fetch upload token", &result); err != nil {
		return "", err
	}
	if result.JWT == "" {
		return "", fmt.Errorf("fetch upload token: cloudflare returned an empty jwt")
	}
	return result.JWT, nil
}

// cfCheckMissingHashes asks the asset store which of `hashes` it
// doesn't already have. Returns the subset that still needs to be
// uploaded — typically a small minority on subsequent deploys
// where most assets haven't changed.
func (c *cfClient) cfCheckMissingHashes(jwt string, hashes []string) ([]string, error) {
	url := fmt.Sprintf("%s/pages/assets/check-missing", currentCFAPIBase())
	var result []string
	body := map[string]any{"hashes": hashes}
	if err := c.doJSONWithJWT("POST", url, jwt, body, "check missing hashes", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// cfAsset is the wire shape of a single asset in the upload batch.
// `base64: true` tells the server the value is base64-encoded
// content; metadata.contentType is what gets served back in the
// Content-Type header at request time.
type cfAsset struct {
	Key      string         `json:"key"`
	Value    string         `json:"value"`
	Base64   bool           `json:"base64"`
	Metadata cfAssetMetdata `json:"metadata"`
}

type cfAssetMetdata struct {
	ContentType string `json:"contentType"`
}

// cfUploadAssets uploads a single batch of assets. Caller is
// responsible for chunking into ≤ cfUploadBatchSize batches.
func (c *cfClient) cfUploadAssets(jwt string, batch []cfAsset) error {
	url := fmt.Sprintf("%s/pages/assets/upload", currentCFAPIBase())
	return c.doJSONWithJWT("POST", url, jwt, batch, "upload assets", nil)
}

// cfUpsertHashes commits uploaded assets to the project's keyspace.
// Without this step, the assets exist in the global store but
// aren't reachable for deployment manifests referencing them.
func (c *cfClient) cfUpsertHashes(jwt string, hashes []string) error {
	url := fmt.Sprintf("%s/pages/assets/upsert-hashes", currentCFAPIBase())
	body := map[string]any{"hashes": hashes}
	return c.doJSONWithJWT("POST", url, jwt, body, "upsert hashes", nil)
}

// cfDeploymentResult is the subset of the create-deployment
// response we care about. The full payload has 30+ fields (build
// metadata, alias domains, deployment trigger info); we want the
// canonical URL and the unique deployment URL.
type cfDeploymentResult struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// cfCreateDeployment posts the deployment manifest to Cloudflare,
// which materializes the deployment from the previously uploaded
// asset hashes. The manifest is a JSON map of "/path/in/site" →
// "asset-hash". Cloudflare accepts it as a multipart form field.
//
// `manifest` is JSON-encoded by the caller; we just transport it.
func (c *cfClient) cfCreateDeployment(accountID, projectName string, manifest map[string]string) (*cfDeploymentResult, error) {
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("create deployment: marshal manifest: %w", err)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField("manifest", string(manifestJSON)); err != nil {
		return nil, fmt.Errorf("create deployment: write manifest field: %w", err)
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("create deployment: close multipart: %w", err)
	}

	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s/deployments",
		currentCFAPIBase(), accountID, projectName)
	req, err := http.NewRequest("POST", url, &body)
	if err != nil {
		return nil, fmt.Errorf("create deployment: new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create deployment: %w", err)
	}
	defer resp.Body.Close()

	var result cfDeploymentResult
	if err := decodeCFResponse(resp, "create deployment", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// hashAssetKey returns the Cloudflare-flavored content key for a
// single asset: blake3(content || ext) → first 32 hex chars (= 16
// bytes of entropy, 128-bit collision space). This matches the
// wrangler implementation so projects can share asset stores across
// tools without re-uploading.
//
// `ext` is the file extension WITHOUT the leading dot ("html",
// "js", "json"). Empty string is fine and is what we use for
// extensionless files like a `manifest` without a suffix.
func hashAssetKey(content []byte, ext string) string {
	h := blake3.New()
	// Order matters: content first, then extension. Reversing
	// would not collide with wrangler's keys.
	_, _ = h.Write(content)
	_, _ = h.Write([]byte(ext))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:32]
}

// base64Encode wraps the std encoder for the asset value payload.
// Tiny helper so the upload path reads as `base64Encode(content)`
// instead of mentioning the encoding twice (encoder + call).
func base64Encode(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
