package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestProbeHealthy_StatusCodes covers the contract: anything < 500
// counts as "framework is up", anything else (network or 5xx) does not.
// 4xx is the interesting case — a Page.protected app returns 401 at /,
// and we MUST treat that as healthy (the framework parsed and routed,
// it just refused to serve the page without a session).
func TestProbeHealthy_StatusCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   bool
	}{
		{"200 OK", 200, true},
		{"204 No Content", 204, true},
		{"301 Redirect", 301, true},
		{"302 Redirect to sign-in", 302, true},
		{"401 Unauthorized (Page.protected)", 401, true},
		{"404 Not Found", 404, true},
		{"500 Internal Server Error", 500, false},
		{"503 Service Unavailable", 503, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()
			got := probeHealthy(srv.URL)
			if got != tc.want {
				t.Errorf("probeHealthy(%d) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

// TestProbeHealthy_ConnectionRefused exercises the "app not running
// yet" path. A URL pointing at a closed port returns a connection-
// refused error; the probe must swallow it and return false so the
// poller keeps trying.
func TestProbeHealthy_ConnectionRefused(t *testing.T) {
	// Bind to grab a free port, then close immediately so the URL
	// points at nothing live. Avoids hard-coding a port that might
	// already be in use on CI.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	if probeHealthy(url) {
		t.Errorf("probeHealthy(<closed port>) = true, want false")
	}
}

// TestWaitForAppHealthy_QuickSuccess exercises the happy path:
// when the server is already responsive, the poller exits immediately
// (no spin, no timeout drain). The total elapsed wall-clock should be
// well under one poll interval.
func TestWaitForAppHealthy_QuickSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	start := time.Now()
	if err := waitForAppHealthy(srv.URL, 5*time.Second); err != nil {
		t.Fatalf("waitForAppHealthy returned %v, want nil", err)
	}
	elapsed := time.Since(start)
	// First probe happens before any sleep, so a healthy server should
	// be detected near-instantly. Allow plenty of slack for slow CI.
	if elapsed > 500*time.Millisecond {
		t.Errorf("waitForAppHealthy took %s, expected < 500ms for a server that's already up", elapsed)
	}
}

// TestWaitForAppHealthy_EventualSuccess exercises the "boot takes a
// moment" path. The server returns 503 on the first probe (warming
// up) and 200 thereafter. The poller must keep trying instead of
// giving up after the first failure.
func TestWaitForAppHealthy_EventualSuccess(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	if err := waitForAppHealthy(srv.URL, 10*time.Second); err != nil {
		t.Fatalf("waitForAppHealthy returned %v, want nil", err)
	}
	if requests.Load() < 2 {
		t.Errorf("expected ≥2 probes, got %d — the poller didn't retry after the initial 503", requests.Load())
	}
}

// TestColorizeElapsed_Thresholds pins the dim → yellow → red
// escalation. The wait spinner uses this to nudge the operator's
// attention upward as the deploy approaches the timeout. We force
// colors on so the test asserts against ANSI escapes regardless of
// whether the test binary's stdout is a TTY.
//
// Expected bands (against the 60s production timeout):
//
//	< 20%  (0–11s)  → dim
//	20–60% (12–35s) → yellow
//	>= 60% (36s+)   → red
func TestColorizeElapsed_Thresholds(t *testing.T) {
	SetColorEnabled(true)
	t.Cleanup(func() { SetColorEnabled(false) })

	timeout := 60 * time.Second
	cases := []struct {
		name    string
		elapsed time.Duration
		wantSub string // an ANSI fragment unique to the expected color
	}{
		{"0s — dim", 0, ansiDim},
		{"5s — dim", 5 * time.Second, ansiDim},
		{"11s — dim (just under 20%)", 11 * time.Second, ansiDim},
		{"12s — yellow (at 20%)", 12 * time.Second, ansiBoldYellow},
		{"20s — yellow", 20 * time.Second, ansiBoldYellow},
		{"35s — yellow (just under 60%)", 35 * time.Second, ansiBoldYellow},
		{"36s — red (at 60%)", 36 * time.Second, ansiBoldRed},
		{"50s — red", 50 * time.Second, ansiBoldRed},
		{"60s — red (at timeout)", 60 * time.Second, ansiBoldRed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := colorizeElapsed(tc.elapsed, timeout)
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("colorizeElapsed(%s, %s) = %q, want it to contain %q",
					tc.elapsed, timeout, got, tc.wantSub)
			}
		})
	}
}

// TestWaitForAppHealthy_Timeout exercises the failure path: an app
// that never comes up. Server always returns 503; the poller must
// exhaust its deadline and return an error.
//
// Uses a tiny timeout (250ms) so the test runs fast. The 2s default
// poll interval means we'll probably only get a single probe in,
// which is fine — the point is verifying the timeout fires.
func TestWaitForAppHealthy_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srv.Close()

	start := time.Now()
	err := waitForAppHealthy(srv.URL, 250*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("waitForAppHealthy returned nil, want timeout error")
	}
	// Must wait approximately the full timeout — not give up
	// immediately on the first 503.
	if elapsed < 200*time.Millisecond {
		t.Errorf("waitForAppHealthy returned after %s, expected ≥ ~250ms", elapsed)
	}
}
