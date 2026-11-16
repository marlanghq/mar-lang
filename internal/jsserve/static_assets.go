package jsserve

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"mar/internal/pwa"
)

// The assets below are the same bytes for every request, and each used to be
// rebuilt per request: runtime.js went through gzip every time a page loaded
// (three quarters of a megabyte, on a response marked no-store so it is never
// skipped), and every icon was decoded, resampled and re-encoded per fetch.
// They are computed on first use and kept.
//
// What is sent does not change: the same headers, and the same bytes the
// per-request path produced, because they come out of the same encoder fed the
// same input in the same single Write.

var (
	runtimeJSGzipOnce sync.Once
	runtimeJSGzipped  []byte
)

// runtimeJSGzip is runtime.js exactly as withCompression would have streamed
// it: one Write of the whole source through a default-level gzip.Writer.
func runtimeJSGzip() []byte {
	runtimeJSGzipOnce.Do(func() {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = io.WriteString(gw, runtimeJS)
		_ = gw.Close()
		runtimeJSGzipped = buf.Bytes()
	})
	return runtimeJSGzipped
}

// handleRuntimeJS serves the embedded runtime.
//
// no-store on dev runtime: hot-reload changes the embedded runtime.js on
// every `mar dev` restart, but Safari/Chrome happily serve a cached copy
// unless we tell them not to. `no-store` is stricter than `no-cache`:
// guarantees a network fetch, not a 304 Not Modified.
func handleRuntimeJS(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	if acceptsGzip(r.Header.Get("Accept-Encoding")) {
		// The three header edits withCompression makes, in its order.
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		h.Del("Content-Length")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Type", "application/javascript; charset=utf-8")
		_, _ = w.Write(runtimeJSGzip())
		return
	}
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = io.WriteString(w, runtimeJS)
}

// iconKey is everything pwa.IconPNG's output depends on: the config, the
// requested size, and, when the icon comes from a file, that file's identity.
// The file is stat'ed per request so replacing the icon while `mar dev` runs
// still shows up on the next fetch, as it did when every fetch re-read it.
type iconKey struct {
	cfg   pwa.Config
	size  int
	mtime time.Time
	bytes int64
}

var (
	iconCacheMu sync.Mutex
	iconCache   = map[int]iconCacheEntry{} // by size: at most one entry per endpoint
)

type iconCacheEntry struct {
	key iconKey
	png []byte
}

// cachedIconPNG is pwa.IconPNG, remembered per size until the config or the
// icon file changes. A failure is never remembered.
func cachedIconPNG(cfg pwa.Config, size int) ([]byte, error) {
	key := iconKey{cfg: cfg, size: size}
	if cfg.IconPath != "" {
		info, err := os.Stat(cfg.IconPath)
		if err != nil {
			// Let IconPNG produce the error it always produced.
			return pwa.IconPNG(cfg, size)
		}
		key.mtime, key.bytes = info.ModTime(), info.Size()
	}
	iconCacheMu.Lock()
	entry, ok := iconCache[size]
	iconCacheMu.Unlock()
	if ok && entry.key == key {
		return entry.png, nil
	}
	b, err := pwa.IconPNG(cfg, size)
	if err != nil {
		return nil, err
	}
	iconCacheMu.Lock()
	iconCache[size] = iconCacheEntry{key: key, png: b}
	iconCacheMu.Unlock()
	return b, nil
}
