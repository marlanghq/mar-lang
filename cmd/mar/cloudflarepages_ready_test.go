// Tests for the post-deploy readiness check that gates the browser
// open. The check must only treat the deployment as live when it
// serves the EXACT index.html we built (matching blake3 content key),
// so neither Cloudflare's "Nothing is here yet" placeholder nor a
// stale earlier deploy gets opened.

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCFServesIndexHash(t *testing.T) {
	ourIndex := []byte("<!DOCTYPE html><div id=\"mar-root\"></div><!-- program v123 -->")
	expectedKey := hashAssetKey(ourIndex, "html")

	cases := []struct {
		name   string
		status int
		body   []byte
		want   bool
	}{
		{
			name:   "serves our exact bundle",
			status: http.StatusOK,
			body:   ourIndex,
			want:   true,
		},
		{
			name:   "cloudflare placeholder (200 but wrong body)",
			status: http.StatusOK,
			body:   []byte("<html><body>Nothing is here yet</body></html>"),
			want:   false,
		},
		{
			name:   "stale previous deploy (different mar bundle)",
			status: http.StatusOK,
			body:   []byte("<!DOCTYPE html><div id=\"mar-root\"></div><!-- program v999 -->"),
			want:   false,
		},
		{
			name:   "not ready (404)",
			status: http.StatusNotFound,
			body:   []byte("not found"),
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write(tc.body)
			}))
			defer srv.Close()

			got := cfServesIndexHash(srv.Client(), srv.URL, expectedKey)
			if got != tc.want {
				t.Fatalf("cfServesIndexHash = %v, want %v", got, tc.want)
			}
		})
	}
}
