package jsserve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// recoverPanic promises a clean 500. Behind withCompression it did not keep
// the promise for a client that accepts gzip, which is every browser: the
// compressor's deferred Close wrote an empty gzip stream on the way out of
// the panic, committing a 200 before recoverPanic ran. The client got
// `200 OK`, `Content-Encoding: gzip`, and a body of an empty stream followed
// by the plain-text error.
func TestAPanicBehindCompressionIsStillA500(t *testing.T) {
	h := recoverPanic(http.HandlerFunc(withCompression(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})))
	srv := httptest.NewServer(h)
	defer srv.Close()
	for _, accept := range []string{"", "gzip", "gzip, deflate, br"} {
		req, _ := http.NewRequest("GET", srv.URL+"/x", nil)
		if accept != "" {
			req.Header.Set("Accept-Encoding", accept)
		}
		client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("Accept-Encoding %q: %v", accept, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("Accept-Encoding %q: status %d, want 500", accept, resp.StatusCode)
		}
		if enc := resp.Header.Get("Content-Encoding"); enc != "" {
			t.Errorf("Accept-Encoding %q: Content-Encoding %q on a plain-text error", accept, enc)
		}
		if string(body) != "internal server error" {
			t.Errorf("Accept-Encoding %q: body %q", accept, body)
		}
	}
}
