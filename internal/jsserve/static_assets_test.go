package jsserve

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"mar/internal/pwa"
)

// The cached runtime.js must be indistinguishable ON THE WIRE from the one
// that was compressed per request: same status, same headers, same body bytes,
// same framing. The reference below is the handler as it was written before.
func TestCachedRuntimeJSIsWhatThePerRequestPathSent(t *testing.T) {
	old := withCompression(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = io.WriteString(w, runtimeJS)
	})
	fetch := func(h http.HandlerFunc, accept string) (string, []byte) {
		srv := httptest.NewServer(h)
		defer srv.Close()
		req, _ := http.NewRequest("GET", srv.URL+"/_mar/runtime.js", nil)
		if accept != "" {
			req.Header.Set("Accept-Encoding", accept)
		}
		// DisableCompression: the client must not ask for, or undo, gzip
		// on its own, or the two bodies would be compared after decoding.
		client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var lines []string
		for k, vs := range resp.Header {
			if k == "Date" {
				continue
			}
			lines = append(lines, k+": "+strings.Join(vs, ","))
		}
		sort.Strings(lines)
		head := resp.Status + "\n" + strings.Join(lines, "\n") +
			"\nTransfer-Encoding: " + strings.Join(resp.TransferEncoding, ",")
		return head, body
	}
	for _, accept := range []string{"", "gzip", "gzip, deflate, br", "br;q=1.0, gzip;q=0.8", "identity"} {
		wantHead, wantBody := fetch(old, accept)
		gotHead, gotBody := fetch(handleRuntimeJS, accept)
		if gotHead != wantHead {
			t.Errorf("Accept-Encoding %q: headers differ\n--- per request\n%s\n--- cached\n%s", accept, wantHead, gotHead)
		}
		if !bytes.Equal(gotBody, wantBody) {
			t.Errorf("Accept-Encoding %q: body differs (%d bytes per request, %d cached)", accept, len(wantBody), len(gotBody))
		}
	}
}

// A cached icon is the icon pwa.IconPNG produces, and replacing the file is
// seen on the next request, as it was when every request re-read it.
func TestCachedIconFollowsTheConfigAndTheFile(t *testing.T) {
	cfg := pwa.Config{Name: "X", ShortName: "X", ThemeColor: "#0071e3", BackgroundColor: "#fff"}
	for _, size := range append([]int{pwa.FaviconSize}, pwa.IconSizes...) {
		want, _ := pwa.IconPNG(cfg, size)
		for i := 0; i < 2; i++ {
			got, err := cachedIconPNG(cfg, size)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("generated icon %d (ask %d) differs from pwa.IconPNG: %v", size, i, err)
			}
		}
	}
	// A different theme colour is a different icon.
	other := cfg
	other.ThemeColor = "#ff0000"
	want, _ := pwa.IconPNG(other, 192)
	if got, _ := cachedIconPNG(other, 192); !bytes.Equal(got, want) {
		t.Fatal("a changed themeColor served the previous icon")
	}

	// From a file, and then the file replaced.
	logo := filepath.Join("..", "..", "examples", "mar-website", "public", "mar-logo.png")
	src, err := os.ReadFile(logo)
	if err != nil {
		t.Skip("no sample icon")
	}
	path := filepath.Join(t.TempDir(), "icon.png")
	if err := os.WriteFile(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	fileCfg := cfg
	fileCfg.IconPath = path
	want, _ = pwa.IconPNG(fileCfg, 180)
	for i := 0; i < 2; i++ {
		if got, _ := cachedIconPNG(fileCfg, 180); !bytes.Equal(got, want) {
			t.Fatalf("file icon (ask %d) differs from pwa.IconPNG", i)
		}
	}
	solid, _ := pwa.IconPNG(cfg, 512) // any other valid PNG
	if err := os.WriteFile(path, solid, 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(path, time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	want, _ = pwa.IconPNG(fileCfg, 180)
	if got, _ := cachedIconPNG(fileCfg, 180); !bytes.Equal(got, want) {
		t.Fatal("a replaced icon file served the previous icon")
	}
	// A missing file fails the way it always did, and is not remembered.
	_ = os.Remove(path)
	if _, err := cachedIconPNG(fileCfg, 180); err == nil {
		t.Fatal("a missing icon file was served from the cache")
	}
}
