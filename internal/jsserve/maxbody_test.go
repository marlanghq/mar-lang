package jsserve

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"mar/internal/runtime"
)

// withMaxBodyBytes pins the per-request body cap for the duration of
// the test and restores the prior value (1 MiB default) afterward.
// maxBodyBytes is process-global; tests don't run in parallel against
// it.
func withMaxBodyBytes(t *testing.T, n int64) {
	t.Helper()
	prev := maxBodyBytes.Load()
	SetMaxBodyBytes(n)
	t.Cleanup(func() { maxBodyBytes.Store(prev) })
}

// echoRoute builds a minimal backend route record that returns 200
// with the request body echoed back. Used to exercise dispatchBackend
// without standing up the whole Mar runtime program loader.
func echoRoute() runtime.Value {
	return runtime.VRecord{
		Order: []string{"method", "path", "handler"},
		Fields: map[string]runtime.Value{
			"method": runtime.VString{V: "POST"},
			"path":   runtime.VString{V: "/echo"},
			// handler: req -> Effect { status = 200, body = req.body }
			"handler": runtime.VFn{
				Arity: 1,
				Native: func(args []runtime.Value) (runtime.Value, error) {
					req, _ := args[0].(runtime.VRecord)
					body, _ := req.Fields["body"].(runtime.VString)
					return runtime.VEffect{
						Tag: "echo",
						Run: func() (runtime.Value, error) {
							return runtime.VRecord{
								Order: []string{"status", "body"},
								Fields: map[string]runtime.Value{
									"status": runtime.VInt{V: 200},
									"body":   body,
								},
							}, nil
						},
					}, nil
				},
			},
		},
	}
}

// TestSetMaxBodyBytes_PinsOutOfRange — defensive callers that pass
// a value below MinMaxBodyBytes or above MaxMaxBodyBytes get
// silently pinned to the documented default rather than crashing.
// validateServer is the canonical gatekeeper; this is the safety net
// for paths that bypass it (test code, tools).
func TestSetMaxBodyBytes_PinsOutOfRange(t *testing.T) {
	prev := maxBodyBytes.Load()
	t.Cleanup(func() { maxBodyBytes.Store(prev) })

	cases := []struct {
		name string
		in   int64
		want int64
	}{
		{"below min pins to default", 0, 1 << 20},
		{"negative pins to default", -1, 1 << 20},
		{"above max pins to default", (32 << 20) + 1, 1 << 20},
		{"in range passes through", 5 << 20, 5 << 20},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SetMaxBodyBytes(tc.in)
			if got := maxBodyBytes.Load(); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

// TestDispatchBackend_Rejects413OverLimit — when the request body
// exceeds the configured cap, dispatchBackend responds with 413
// before invoking the user's handler. Without this, a malicious
// client could send arbitrarily large bodies and exhaust server
// memory at io.ReadAll.
func TestDispatchBackend_Rejects413OverLimit(t *testing.T) {
	// 1 KiB is the smallest value SetMaxBodyBytes accepts without
	// pinning to the default (1 MiB). The test sends 2 KiB so the
	// overflow is unambiguous regardless of any framing overhead
	// MaxBytesReader counts.
	withMaxBodyBytes(t, 1024)

	routes := []runtime.Value{echoRoute()}
	handler := func(w http.ResponseWriter, r *http.Request) {
		dispatchBackend(routes, r.URL.Path, w, r)
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	big := bytes.Repeat([]byte("x"), 2048) // 2 KiB > 1 KiB cap
	resp, err := srv.Client().Post(srv.URL+"/echo", "text/plain", bytes.NewReader(big))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("status: got %d, want 413", resp.StatusCode)
	}
}

// TestDispatchBackend_AllowsUnderLimit — bodies within the cap go
// through cleanly. Pins the happy path so future tightenings of the
// limiter don't accidentally lock out legitimate traffic.
func TestDispatchBackend_AllowsUnderLimit(t *testing.T) {
	withMaxBodyBytes(t, 1<<20) // 1 MiB

	routes := []runtime.Value{echoRoute()}
	handler := func(w http.ResponseWriter, r *http.Request) {
		dispatchBackend(routes, r.URL.Path, w, r)
	}
	srv := httptest.NewServer(http.HandlerFunc(handler))
	defer srv.Close()

	small := []byte("hello")
	resp, err := srv.Client().Post(srv.URL+"/echo", "text/plain", bytes.NewReader(small))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
}

// TestIsHTTPS — the helper used to set the Secure cookie flag must
// recognize both direct TLS connections and the X-Forwarded-Proto
// header (the standard proxy hint). The header is honored only from a
// trusted proxy (see TestIsHTTPS_ForwardedProtoGatedByTrust); these
// cases pin the header parsing itself with a trusted (private) peer.
func TestIsHTTPS(t *testing.T) {
	withTrustedProxies(t, nil) // default: private peer below is trusted
	type tc struct {
		name  string
		setup func(*http.Request)
		want  bool
	}
	cases := []tc{
		{"plain HTTP, no header", func(r *http.Request) {}, false},
		{
			"X-Forwarded-Proto: https",
			func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") },
			true,
		},
		{
			"X-Forwarded-Proto: HTTPS (case-insensitive)",
			func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "HTTPS") },
			true,
		},
		{
			"X-Forwarded-Proto: http (lowercase, plain)",
			func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "http") },
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			req.RemoteAddr = "10.0.0.1:1234" // trusted (private) peer
			c.setup(req)
			if got := isHTTPS(req); got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}
