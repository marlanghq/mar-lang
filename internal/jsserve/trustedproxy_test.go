package jsserve

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

// withTrustedProxies installs a trusted-proxy policy for the duration of
// the test and restores the default (unset) afterward. trustedProxyCfg is
// process-global; these tests don't run in parallel against it.
func withTrustedProxies(t *testing.T, cidrs []string) {
	t.Helper()
	SetTrustedProxies(cidrs)
	t.Cleanup(func() { SetTrustedProxies(nil) })
}

func reqWith(remoteAddr, xff string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remoteAddr
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

func TestClientIP_DefaultTrustsPrivatePeerOnly(t *testing.T) {
	withTrustedProxies(t, nil) // default: loopback + private

	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"private peer honors XFF", "10.0.0.1:5000", "203.0.113.9", "203.0.113.9"},
		{"loopback peer honors XFF", "127.0.0.1:5000", "203.0.113.9", "203.0.113.9"},
		// Public peer: the caller is talking to us directly, so XFF is
		// attacker-controlled and must be ignored — this is the spoof
		// the fix closes.
		{"public peer ignores XFF (spoof)", "8.8.8.8:5000", "1.2.3.4", "8.8.8.8"},
		// Trusted peer, two public hops: the prepended (leftmost) entry
		// is a forgery; the rightmost is what our trusted proxy actually
		// observed, so it wins.
		{"right-to-left picks observed client", "10.0.0.1:5000", "1.2.3.4, 5.6.7.8", "5.6.7.8"},
		// A trusted (private) inner proxy between us and the client is
		// skipped; the first untrusted hop is the real client.
		{"skips trailing trusted hop", "10.0.0.1:5000", "203.0.113.9, 10.0.0.2", "203.0.113.9"},
		{"no XFF returns peer", "10.0.0.1:5000", "", "10.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clientIP(reqWith(tc.remoteAddr, tc.xff)); got != tc.want {
				t.Fatalf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestClientIP_ConfiguredCIDRReplacesDefault(t *testing.T) {
	withTrustedProxies(t, []string{"203.0.113.0/24"})

	// Peer inside the configured CIDR: XFF honored.
	if got := clientIP(reqWith("203.0.113.5:5000", "9.9.9.9")); got != "9.9.9.9" {
		t.Fatalf("in-CIDR peer: clientIP = %q, want 9.9.9.9", got)
	}
	// Private peer is NOT in the configured CIDR, and a configured list
	// replaces the private-range default → XFF ignored.
	if got := clientIP(reqWith("10.0.0.1:5000", "9.9.9.9")); got != "10.0.0.1" {
		t.Fatalf("private-but-not-configured peer: clientIP = %q, want 10.0.0.1", got)
	}
}

func TestClientIP_ParanoidEmptyTrustsNoProxy(t *testing.T) {
	withTrustedProxies(t, []string{}) // present but empty = trust nothing

	if got := clientIP(reqWith("10.0.0.1:5000", "9.9.9.9")); got != "10.0.0.1" {
		t.Fatalf("paranoid mode: clientIP = %q, want 10.0.0.1 (XFF ignored)", got)
	}
}

func TestIsHTTPS_ForwardedProtoGatedByTrust(t *testing.T) {
	withTrustedProxies(t, nil) // default

	xfpHTTPS := func(remoteAddr string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remoteAddr
		r.Header.Set("X-Forwarded-Proto", "https")
		return r
	}

	if !isHTTPS(xfpHTTPS("10.0.0.1:5000")) {
		t.Fatal("trusted peer with X-Forwarded-Proto: https should be HTTPS")
	}
	if isHTTPS(xfpHTTPS("8.8.8.8:5000")) {
		t.Fatal("untrusted peer must not claim HTTPS via X-Forwarded-Proto")
	}
	// A real TLS connection is HTTPS regardless of peer/headers.
	rtls := httptest.NewRequest(http.MethodGet, "/", nil)
	rtls.RemoteAddr = "8.8.8.8:5000"
	rtls.TLS = &tls.ConnectionState{}
	if !isHTTPS(rtls) {
		t.Fatal("direct TLS connection should be HTTPS")
	}
}
