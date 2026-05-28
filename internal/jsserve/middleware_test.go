package jsserve

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"mar/internal/ratelimit"
)

// withInstalledLimiter swaps in a limiter for the duration of the test
// and restores whatever was there before (nil in fresh test runs;
// could be non-nil if tests run in an order that left state).
//
// SetRateLimit is process-global. Tests must not run in parallel
// with one another (or anything that holds the limiter pointer);
// the Go test runner serializes by default unless t.Parallel() is
// called, and these tests don't.
func withInstalledLimiter(t *testing.T, l *ratelimit.Limiter) {
	t.Helper()
	prev := currentRateLimiter()
	SetRateLimit(l)
	t.Cleanup(func() { SetRateLimit(prev) })
}

// okHandler is the inner handler under test — always writes "ok".
func okHandler(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, "ok")
}

// TestRateLimit_NoLimiterInstalled — when SetRateLimit(nil) the
// middleware is a transparent pass-through. Protects the test path
// + the "limiter not yet plumbed" boot window.
func TestRateLimit_NoLimiterInstalled(t *testing.T) {
	withInstalledLimiter(t, nil)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	rateLimit(okHandler)(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("want 200 (pass-through); got %d", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Errorf("want body 'ok'; got %q", rr.Body.String())
	}
}

// TestRateLimit_AllowsWithinBudget — under the per-IP budget,
// every request reaches the inner handler.
func TestRateLimit_AllowsWithinBudget(t *testing.T) {
	l := ratelimit.New(ratelimit.Policy{Rate: 100, Burst: 5})
	defer l.Stop()
	withInstalledLimiter(t, l)

	for i := 0; i < 5; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		req.RemoteAddr = "1.2.3.4:5555"
		rateLimit(okHandler)(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("hit %d should pass within burst=5; got %d", i+1, rr.Code)
		}
	}
}

// TestRateLimit_RejectsBeyondBudget — once the bucket is empty,
// requests get 429 with Retry-After + JSON body.
func TestRateLimit_RejectsBeyondBudget(t *testing.T) {
	// Rate=0.001/s effectively pins the bucket empty after burst.
	l := ratelimit.New(ratelimit.Policy{Rate: 0.001, Burst: 1})
	defer l.Stop()
	withInstalledLimiter(t, l)

	// Drain the bucket.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.RemoteAddr = "1.2.3.4:5555"
	rateLimit(okHandler)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first request should pass; got %d", rr.Code)
	}

	// Second request from same IP must be rejected.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.RemoteAddr = "1.2.3.4:5555"
	rateLimit(okHandler)(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429; got %d", rr.Code)
	}
	if got := rr.Header().Get("Retry-After"); got == "" {
		t.Error("Retry-After header missing")
	} else if n, err := strconv.Atoi(got); err != nil || n < 1 {
		t.Errorf("Retry-After should be a positive integer; got %q", got)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type should be JSON; got %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body should be JSON; got %q (%v)", rr.Body.String(), err)
	}
	if body["error"] != "rate_limited" {
		t.Errorf("body.error should be 'rate_limited'; got %v", body["error"])
	}
	// retryAfterSeconds round-trips through JSON as float64; check >= 1.
	if v, ok := body["retryAfterSeconds"].(float64); !ok || v < 1 {
		t.Errorf("body.retryAfterSeconds should be >=1; got %v", body["retryAfterSeconds"])
	}
}

// TestRateLimit_KeysPerIP — exhausting one IP's bucket doesn't
// block requests from a different IP.
func TestRateLimit_KeysPerIP(t *testing.T) {
	l := ratelimit.New(ratelimit.Policy{Rate: 0.001, Burst: 1})
	defer l.Stop()
	withInstalledLimiter(t, l)

	// Drain IP A.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.RemoteAddr = "1.1.1.1:1111"
	rateLimit(okHandler)(rr, req)
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.RemoteAddr = "1.1.1.1:1111"
	rateLimit(okHandler)(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("IP A should be exhausted; got %d", rr.Code)
	}

	// IP B is fresh.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.RemoteAddr = "2.2.2.2:2222"
	rateLimit(okHandler)(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("IP B should be fresh; got %d", rr.Code)
	}
}

// TestRateLimit_RespectsXForwardedFor — when the proxy header is
// set, keying uses the originating IP rather than the proxy
// connection address. Critical: behind Fly's proxy every request
// has the same RemoteAddr (the proxy), so keying on it would
// collapse every user into one bucket.
func TestRateLimit_RespectsXForwardedFor(t *testing.T) {
	l := ratelimit.New(ratelimit.Policy{Rate: 0.001, Burst: 1})
	defer l.Stop()
	withInstalledLimiter(t, l)

	// Both requests come through the same proxy RemoteAddr but
	// from different upstream IPs — they must NOT share a bucket.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.RemoteAddr = "10.0.0.1:443" // proxy
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	rateLimit(okHandler)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first req should pass; got %d", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.RemoteAddr = "10.0.0.1:443" // same proxy
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	rateLimit(okHandler)(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("second req (different XFF) should not share bucket; got %d", rr.Code)
	}
}
