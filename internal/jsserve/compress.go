package jsserve

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// withCompression wraps a handler to gzip the response when the
// client advertises `Accept-Encoding: gzip`. Clients that don't
// advertise gzip get the raw response.
//
// Why only gzip (and not also brotli): brotli's library is ~530 KB
// in the linked binary — about 4% of the production runtime stub —
// in exchange for ~15% better compression on JSON/JS payloads. For
// a JSON API that ships single-digit-MB binaries to Fly machines,
// the binary-size cost pays for itself in faster deploys, while the
// compression delta on typical small responses is negligible
// (1–2 KB saved per response). If a project ships large static JS
// assets and wants brotli at the edge, a CDN can do that without
// the runtime carrying the encoder.
//
// The wrapper writes `Content-Encoding` and `Vary: Accept-Encoding`
// when it actually compresses; `Content-Length` is dropped because
// the final size depends on the codec. Streaming bodies still work.
func withCompression(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			h(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length") // post-compression size is unknown

		gw := gzipWriterPool.Get().(*gzip.Writer)
		gw.Reset(w)
		defer func() {
			_ = gw.Close()
			gzipWriterPool.Put(gw)
		}()
		h(&compressedWriter{ResponseWriter: w, w: gw}, r)
	}
}

// acceptsGzip reports whether the client advertised gzip in
// `Accept-Encoding`. Quality values (q=...) are ignored — any
// mention of gzip is treated as acceptance, matching how nginx /
// cloudflare behave by default and keeping the parsing trivial.
func acceptsGzip(header string) bool {
	if header == "" {
		return false
	}
	for _, p := range strings.Split(header, ",") {
		// strings.Cut returns the prefix before ";" (the codec name)
		// and discards any "q=..." weight — we treat any mention of
		// gzip as acceptance, matching nginx/cloudflare defaults.
		coding, _, _ := strings.Cut(p, ";")
		if strings.TrimSpace(coding) == "gzip" {
			return true
		}
	}
	return false
}

// compressedWriter wraps the original ResponseWriter so the handler's
// Write calls go through the compressor. Header() and WriteHeader()
// pass through untouched.
type compressedWriter struct {
	http.ResponseWriter
	w io.Writer
}

func (cw *compressedWriter) Write(b []byte) (int, error) {
	return cw.w.Write(b)
}

// Pool so we don't allocate a fresh gzip writer per request. The
// codec is reusable via Reset(); the pool keeps a small number warm.
var gzipWriterPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(io.Discard)
	},
}
