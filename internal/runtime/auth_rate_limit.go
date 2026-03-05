package runtime

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"belm/internal/model"
)

const (
	defaultAuthRequestCodeRateLimitPerMinute = 5
	defaultAuthLoginRateLimitPerMinute       = 10
	minAuthRateLimitPerMinute                = 1
	maxAuthRateLimitPerMinute                = 10000
)

type authRateCounter struct {
	windowMinute int64
	count        int
}

// authRateLimiter enforces per-minute limits for auth endpoints in-memory.
type authRateLimiter struct {
	mu               sync.Mutex
	requestCodeLimit int
	loginLimit       int
	now              func() time.Time
	counters         map[string]authRateCounter
	calls            uint64
}

func newAuthRateLimiter(requestCodeLimit, loginLimit int) *authRateLimiter {
	return &authRateLimiter{
		requestCodeLimit: clampAuthRateLimit(requestCodeLimit, defaultAuthRequestCodeRateLimitPerMinute),
		loginLimit:       clampAuthRateLimit(loginLimit, defaultAuthLoginRateLimitPerMinute),
		now:              time.Now,
		counters:         map[string]authRateCounter{},
	}
}

func (l *authRateLimiter) allowRequestCode(req *http.Request, email string) bool {
	if l == nil {
		return true
	}
	return l.allow("request-code", authRateLimitKey(req, email), l.requestCodeLimit)
}

func (l *authRateLimiter) allowLogin(req *http.Request, email string) bool {
	if l == nil {
		return true
	}
	return l.allow("login", authRateLimitKey(req, email), l.loginLimit)
}

func (l *authRateLimiter) allow(scope, key string, limit int) bool {
	if strings.TrimSpace(key) == "" {
		key = "anonymous"
	}
	if limit < minAuthRateLimitPerMinute {
		limit = minAuthRateLimitPerMinute
	}
	if limit > maxAuthRateLimitPerMinute {
		limit = maxAuthRateLimitPerMinute
	}
	nowMinute := l.now().Unix() / 60
	counterKey := scope + "|" + key

	l.mu.Lock()
	defer l.mu.Unlock()

	current := l.counters[counterKey]
	if current.windowMinute != nowMinute {
		current.windowMinute = nowMinute
		current.count = 0
	}
	if current.count >= limit {
		l.maybeCleanup(nowMinute)
		return false
	}

	current.count++
	l.counters[counterKey] = current
	l.maybeCleanup(nowMinute)
	return true
}

func (l *authRateLimiter) maybeCleanup(currentMinute int64) {
	l.calls++
	if l.calls%256 != 0 {
		return
	}
	for key, counter := range l.counters {
		if currentMinute-counter.windowMinute > 2 {
			delete(l.counters, key)
		}
	}
}

func authRateLimitKey(req *http.Request, email string) string {
	host := "unknown"
	if req != nil {
		host = clientHost(req.RemoteAddr)
	}
	normalizedEmail := normalizeEmail(email)
	if normalizedEmail == "" {
		return "host:" + host
	}
	return "email:" + normalizedEmail + "|host:" + host
}

func clientHost(remoteAddr string) string {
	trimmed := strings.TrimSpace(remoteAddr)
	if trimmed == "" {
		return "unknown"
	}
	if host, _, err := net.SplitHostPort(trimmed); err == nil && strings.TrimSpace(host) != "" {
		return host
	}
	return trimmed
}

func authRequestCodeRateLimitPerMinute(app *model.App) int {
	if app == nil || app.System == nil || app.System.AuthRequestCodeRateLimit == nil {
		return defaultAuthRequestCodeRateLimitPerMinute
	}
	return clampAuthRateLimit(*app.System.AuthRequestCodeRateLimit, defaultAuthRequestCodeRateLimitPerMinute)
}

func authLoginRateLimitPerMinute(app *model.App) int {
	if app == nil || app.System == nil || app.System.AuthLoginRateLimit == nil {
		return defaultAuthLoginRateLimitPerMinute
	}
	return clampAuthRateLimit(*app.System.AuthLoginRateLimit, defaultAuthLoginRateLimitPerMinute)
}

func clampAuthRateLimit(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value < minAuthRateLimitPerMinute {
		return minAuthRateLimitPerMinute
	}
	if value > maxAuthRateLimitPerMinute {
		return maxAuthRateLimitPerMinute
	}
	return value
}
