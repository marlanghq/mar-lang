// Package ratelimit implements an in-memory token-bucket rate
// limiter keyed by an arbitrary string (typically the client IP).
//
// Token-bucket semantics:
//   - The bucket has a capacity ("burst") and refills at "rate"
//     tokens per second.
//   - Each Allow(key) call deducts 1 token from the matching
//     bucket; first-time keys start at full burst.
//   - Empty bucket → Allow returns (false, retryAfter) where
//     retryAfter is how long until 1 token has refilled.
//
// Why token bucket (not sliding window): O(1) memory per key
// (just two floats), allows bursting (real users hit refresh hard
// during normal use), and refill semantics are intuitive.
//
// Eviction: a goroutine sweeps idle keys every minute. A key is
// idle when the bucket is full AND it's been more than 1 hour
// since the last access. Without eviction the map grows
// monotonically with unique IPs.
package ratelimit

import (
	"context"
	"sync"
	"time"
)

// Policy describes a rate-limit shape.
type Policy struct {
	// Rate is tokens added per second. e.g. 10 means 10 req/s
	// sustained. Must be > 0.
	Rate float64

	// Burst is the bucket capacity — the max tokens at any moment
	// and the initial state for first-seen keys. Must be >= 1.
	Burst int
}

// Limiter is a per-key token-bucket rate limiter. Safe for
// concurrent use.
type Limiter struct {
	policy Policy
	mu     sync.Mutex
	keys   map[string]*bucket
	stopCh chan struct{}
	once   sync.Once
}

type bucket struct {
	tokens float64
	last   time.Time
}

// New returns a Limiter that enforces `policy`. Caller must call
// Stop() to release the eviction goroutine; in production the
// limiter lives for process lifetime so Stop is rarely needed.
//
// Starts the eviction goroutine immediately.
func New(policy Policy) *Limiter {
	l := &Limiter{
		policy: policy,
		keys:   make(map[string]*bucket),
		stopCh: make(chan struct{}),
	}
	go l.evictionLoop(context.Background())
	return l
}

// Allow consumes 1 token for `key`. Returns:
//   - (true, 0) when the request fits in the bucket
//   - (false, retryAfter) when the bucket is empty; retryAfter is
//     the duration until 1 token has refilled
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.keys[key]
	if !ok {
		// First seen — bucket starts at full burst, deduct 1.
		b = &bucket{tokens: float64(l.policy.Burst) - 1, last: now}
		l.keys[key] = b
		return true, 0
	}

	// Refill since last access, capped at burst.
	elapsed := now.Sub(b.last).Seconds()
	b.tokens += elapsed * l.policy.Rate
	if b.tokens > float64(l.policy.Burst) {
		b.tokens = float64(l.policy.Burst)
	}
	b.last = now

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}

	// Empty bucket — compute how long until 1 token refills.
	needed := 1 - b.tokens
	wait := time.Duration(needed / l.policy.Rate * float64(time.Second))
	return false, wait
}

// Stop terminates the eviction goroutine. Idempotent.
func (l *Limiter) Stop() {
	l.once.Do(func() { close(l.stopCh) })
}

// evictionLoop drops idle keys every minute. A key is idle when
// its bucket has fully refilled AND it's been >1h since last
// access — by then there's no information to preserve and
// dropping the entry just makes future Allow(key) start fresh.
func (l *Limiter) evictionLoop(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-l.stopCh:
			return
		case now := <-tick.C:
			l.evict(now)
		}
	}
}

func (l *Limiter) evict(now time.Time) {
	// Idle threshold — past this, drop regardless of token level.
	// Even if the bucket isn't notionally "full" by our stored
	// state, after an hour without any traffic the next request
	// from this key gets a fresh bucket either way, so keeping
	// the entry costs map space for no benefit.
	const idleThreshold = time.Hour

	l.mu.Lock()
	defer l.mu.Unlock()
	for key, b := range l.keys {
		if now.Sub(b.last) > idleThreshold {
			delete(l.keys, key)
		}
	}
}

// numKeys returns the current key count. Test helper; not part
// of the public API.
func (l *Limiter) numKeys() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.keys)
}
