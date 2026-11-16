package jsserve

// HTTP middleware shared by the dev/prod server. Currently houses the
// per-IP rate limiter that fronts the request-handling routes
// (/api/*, /services/*, /_mar/admin/api/*, /_auth/*). Tighter per-route
// limiters in auth.go / admin.go (e.g. emailLimiter, adminIPLimiter)
// run *inside* their handlers and apply on top of this gateway:
// this one is the cheap, broad first line of defense, sized by the
// operator via mar.json["rateLimit"].

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"sync"

	"mar/internal/ratelimit"
)

// requestLimiter holds the process-wide gateway limiter. Populated
// once at startup by SetRateLimit (called from the CLI after reading
// mar.json); nil means "no rate limiting": only used by tests that
// haven't installed a limiter. In production the CLI always installs
// one because the manifest always resolves to a default policy.
var (
	requestLimiterMu sync.RWMutex
	requestLimiter   *ratelimit.Limiter
)

// SetRateLimit installs the process-wide gateway limiter. Called
// once at boot from the CLI (cmd/mar) after reading mar.json, before
// ServeLive opens the listener. Passing nil disables rate limiting:
// useful for tests, never for production.
//
// Safe to call concurrently with in-flight requests, though in
// practice it's only called during startup.
func SetRateLimit(l *ratelimit.Limiter) {
	requestLimiterMu.Lock()
	requestLimiter = l
	requestLimiterMu.Unlock()
}

// currentRateLimiter returns the installed limiter or nil.
func currentRateLimiter() *ratelimit.Limiter {
	requestLimiterMu.RLock()
	defer requestLimiterMu.RUnlock()
	return requestLimiter
}

// recoverPanic wraps the whole handler chain so a panic in any handler
// (an eval bug, a nil deref, a residual unchecked assertion) becomes a clean
// 500 with the stack logged server-side, instead of a dropped connection and
// a stack trace leaked toward the client. Installed as the outermost
// middleware so it also covers the version-header and admin-instrument layers.
//
// A 500 is only possible while the response has not started. Once a status
// or a body byte has gone out, there is no status left to set, and this used
// to write anyway: "internal server error" was appended to the body and the
// response finished normally, as a 200. A JSON answer became invalid, the
// shell showed the sentence under the page, and behind compression browsers
// and the phone dropped the trailing text and took the response as a
// success. Now a panic after the response started breaks the connection
// instead: what was written is flushed, so the client has the headers and
// reads a broken response rather than an empty one it might send again (a
// browser re-sent a POST two or three times when nothing had reached it),
// and then http.ErrAbortHandler closes the connection without the end of
// the body. Every client that reads a body to its end sees that as an error,
// except URLSession over HTTP/1.1, which returns a cut chunked body as a
// successful 200 (measured); the phone's decoder is the line of defence
// there. So no handler should panic after it starts: every handler builds
// its body before writing it, and the work adminInstrument does after a
// response has its own recover.
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := &commitTracker{ResponseWriter: w}
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if err, ok := rec.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(rec) // a handler already chose to abort
			}
			fmt.Fprintf(os.Stderr, "[mar] panic serving %s %s: %v\n%s\n",
				r.Method, r.URL.Path, rec, debug.Stack())
			if tw.committed {
				_ = http.NewResponseController(w).Flush()
				panic(http.ErrAbortHandler)
			}
			// Nothing has gone out, so the headers are still ours: the
			// handler's are about a response that will not be sent (a JSON
			// Content-Type, a Content-Length the 500 does not have), and
			// only the version headers outer middleware set are kept.
			h := w.Header()
			runtimeVer, program := h.Get("X-Mar-Runtime"), h.Get("X-Mar-Program")
			clear(h)
			if runtimeVer != "" {
				h.Set("X-Mar-Runtime", runtimeVer)
			}
			if program != "" {
				h.Set("X-Mar-Program", program)
			}
			h.Set("Content-Type", "text/plain; charset=utf-8")
			h.Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, "internal server error")
		}()
		next.ServeHTTP(tw, r)
	})
}

// commitTracker records whether a response has started: a final status, a
// body byte or a flush. It keeps what handlers reach the writer for: Flush
// (the reload stream asserts http.Flusher), Unwrap (so ResponseController can
// clear the reload stream's write deadline), and ReadFrom (so a file copy
// keeps the fast path).
type commitTracker struct {
	http.ResponseWriter
	committed bool
}

func (t *commitTracker) WriteHeader(code int) {
	if code >= 200 {
		t.committed = true
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *commitTracker) Write(b []byte) (int, error) {
	t.committed = true
	return t.ResponseWriter.Write(b)
}

func (t *commitTracker) Flush() {
	t.committed = true
	_ = http.NewResponseController(t.ResponseWriter).Flush()
}

func (t *commitTracker) ReadFrom(src io.Reader) (int64, error) {
	t.committed = true
	if rf, ok := t.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(struct{ io.Writer }{t.ResponseWriter}, src)
}

func (t *commitTracker) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// rateLimit wraps next with the gateway per-IP rate limiter. When the
// limit is exceeded, responds with:
//
//	HTTP 429 Too Many Requests
//	Retry-After: <seconds>
//	Content-Type: application/json
//	{"error":"rate_limited","retryAfterSeconds":<seconds>}
//
// Body shape matches the per-endpoint limiters in auth.go /
// admin.go (writeJSON with `error` + `retryAfterSeconds`) so any
// client handling one already handles the other.
//
// When no limiter is installed (SetRateLimit hasn't been called, or
// was called with nil), the middleware is a no-op pass-through.
func rateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l := currentRateLimiter()
		if l == nil {
			next(w, r)
			return
		}
		ok, retry := l.Allow(clientIP(r))
		if ok {
			next(w, r)
			return
		}
		// Round up so a sub-second wait still surfaces as "1s" to
		// clients rather than 0 (which they'd interpret as "retry
		// immediately" and just bounce off the limiter again).
		secs := int(retry.Seconds())
		if secs < 1 {
			secs = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":             "rate_limited",
			"retryAfterSeconds": secs,
		})
	}
}
