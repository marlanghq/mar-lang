package jsserve

// HTTP middleware shared by the dev/prod server. Currently houses the
// per-IP rate limiter that fronts the request-handling routes
// (/api/*, /services/*, /_mar/admin/api/*, /_auth/*). Tighter per-route
// limiters in auth.go / admin.go (e.g. emailLimiter, adminIPLimiter)
// run *inside* their handlers and apply on top of this gateway —
// this one is the cheap, broad first line of defense, sized by the
// operator via mar.json["rateLimit"].

import (
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
// mar.json); nil means "no rate limiting" — only used by tests that
// haven't installed a limiter. In production the CLI always installs
// one because the manifest always resolves to a default policy.
var (
	requestLimiterMu sync.RWMutex
	requestLimiter   *ratelimit.Limiter
)

// SetRateLimit installs the process-wide gateway limiter. Called
// once at boot from the CLI (cmd/mar) after reading mar.json, before
// ServeLive opens the listener. Passing nil disables rate limiting —
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
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Fprintf(os.Stderr, "[mar] panic serving %s %s: %v\n%s\n",
					r.Method, r.URL.Path, rec, debug.Stack())
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

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
