package auth

import (
	"sync"
	"time"
)

// Limiter is a small in-memory sliding-window rate limiter keyed by
// arbitrary string (email, IP, etc.). It's process-local — restarting
// the server resets the counters; that's fine for v1 because the per-
// code attempts counter (in the DB) covers the persistent abuse case.
//
// Two windows are tracked per key. Default thresholds (set at New): 3
// requests per hour per email, 20 per hour per IP. The thresholds are
// fixed for now; they're encapsulated here so future configuration is a
// one-place change.
type Limiter struct {
	mu      sync.Mutex
	hits    map[string][]time.Time
	max     int
	window  time.Duration
}

// NewLimiter returns a Limiter that allows `max` events per `window`.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{
		hits:   make(map[string][]time.Time),
		max:    max,
		window: window,
	}
}

// Allow records an event for `key` and returns (allowed, retryAfter).
// When `allowed` is false, `retryAfter` says how long until the oldest
// in-window hit ages out (and a new attempt would be allowed).
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	hits := l.hits[key]
	// Drop entries outside the window.
	kept := hits[:0]
	for _, t := range hits {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		retry := kept[0].Add(l.window).Sub(now)
		l.hits[key] = kept
		return false, retry
	}
	kept = append(kept, now)
	l.hits[key] = kept
	return true, 0
}
