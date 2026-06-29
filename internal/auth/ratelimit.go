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
//
// Eviction: a background goroutine started by NewLimiter sweeps the
// map every `window/2` and drops keys whose newest hit is older than
// the window — past that point, no information is preserved and the
// next request from the key would get a fresh bucket anyway. Without
// the sweep, the map grows monotonically with unique
// emails/IPs over the process lifetime.
type Limiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
	stopCh chan struct{}
	once   sync.Once
}

// NewLimiter returns a Limiter that allows `max` events per `window`,
// with a background goroutine evicting idle keys. Caller is responsible
// for Stop() when the limiter goes out of scope; production callers
// keep limiters at package scope for process lifetime and skip Stop.
// Tests should `t.Cleanup(l.Stop)` to keep goroutines from leaking
// across test cases.
func NewLimiter(max int, window time.Duration) *Limiter {
	l := &Limiter{
		hits:   make(map[string][]time.Time),
		max:    max,
		window: window,
		stopCh: make(chan struct{}),
	}
	go l.evictionLoop()
	return l
}

// Stop terminates the eviction goroutine. Idempotent.
func (l *Limiter) Stop() {
	l.once.Do(func() { close(l.stopCh) })
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

// evictionLoop runs until Stop(). On each tick it drops keys whose
// newest hit is older than the window — by then there\'s no in-window
// data left to preserve and a fresh entry would be created on the
// next request to the same key anyway. Ticker interval is window/2,
// long enough to make sweeps cheap, short enough that stale keys
// don\'t linger more than ~1.5x window after their last access.
func (l *Limiter) evictionLoop() {
	interval := l.window / 2
	if interval < time.Second {
		// Floor for very small windows (typically tests). Avoids
		// busy-looping when the limiter is configured with a
		// sub-second window.
		interval = time.Second
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-l.stopCh:
			return
		case now := <-tick.C:
			l.evict(now)
		}
	}
}

func (l *Limiter) evict(now time.Time) {
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	for key, hits := range l.hits {
		// All hits stale (or empty slice) → no information to keep.
		// `hits` is appended-to in Allow, so the last entry is the
		// most recent — checking just that one is enough.
		if len(hits) == 0 || !hits[len(hits)-1].After(cutoff) {
			delete(l.hits, key)
		}
	}
}
