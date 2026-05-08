package jsserve

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/andybalholm/brotli"
)

// withCompression wraps a handler to negotiate Brotli or Gzip via the
// `Accept-Encoding` request header. Brotli wins when both are offered
// (typically ~15-20% smaller for JSON/JS payloads). Clients that don't
// advertise either get the raw response — same shape as before.
//
// The wrapper writes `Content-Encoding` and `Vary: Accept-Encoding`
// when it actually compresses; `Content-Length` is dropped because the
// final size depends on the codec. Streaming bodies still work — both
// codecs are stream-friendly.
func withCompression(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		enc := chooseEncoding(r.Header.Get("Accept-Encoding"))
		if enc == "" {
			h(w, r)
			return
		}
		w.Header().Set("Content-Encoding", enc)
		w.Header().Add("Vary", "Accept-Encoding")
		w.Header().Del("Content-Length") // post-compression size is unknown

		switch enc {
		case "br":
			bw := brotliWriterPool.Get().(*brotli.Writer)
			bw.Reset(w)
			defer func() {
				_ = bw.Close()
				brotliWriterPool.Put(bw)
			}()
			h(&compressedWriter{ResponseWriter: w, w: bw}, r)
		case "gzip":
			gw := gzipWriterPool.Get().(*gzip.Writer)
			gw.Reset(w)
			defer func() {
				_ = gw.Close()
				gzipWriterPool.Put(gw)
			}()
			h(&compressedWriter{ResponseWriter: w, w: gw}, r)
		}
	}
}

// chooseEncoding returns "br" if the client accepts Brotli, "gzip" if
// it accepts gzip but not Brotli, "" if neither. Quality values
// (q=...) are ignored — a client offering brotli at any q level wins
// over gzip; this matches how nginx / cloudfront behave by default and
// keeps the parsing trivial.
func chooseEncoding(header string) string {
	if header == "" {
		return ""
	}
	parts := strings.Split(header, ",")
	hasGzip := false
	for _, p := range parts {
		token := strings.TrimSpace(strings.SplitN(p, ";", 2)[0])
		switch token {
		case "br":
			return "br"
		case "gzip":
			hasGzip = true
		}
	}
	if hasGzip {
		return "gzip"
	}
	return ""
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

// Pools so we don't allocate a fresh codec per request. Both codecs
// are reusable via Reset(); the pool keeps a small number warm.
var (
	gzipWriterPool = sync.Pool{
		New: func() any {
			return gzip.NewWriter(io.Discard)
		},
	}
	brotliWriterPool = sync.Pool{
		New: func() any {
			// Quality 5 is a good trade-off for live HTTP: ~15% smaller
			// than gzip default, ~3x faster than max quality (Q11).
			return brotli.NewWriterLevel(io.Discard, 5)
		},
	}
)
