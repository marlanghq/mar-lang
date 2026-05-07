// Request log ring buffer — powers Mar.Admin.recentRequests.
//
// In-memory only (no DB persistence): the panel is a live debugging
// tool, not a historical analytics surface. Each new request
// overwrites the oldest once the buffer is full. Default cap 200,
// configurable via mar.json["adminPanel"]["recentRequestsSize"]
// (range 10–5000, validated at compile time).
//
// All methods are safe to call from any goroutine.

package admin

import (
	"sync"
)

// RequestLog is one entry in the in-memory buffer. Mirrors the shape
// the JSON service exposes to the UI; field tags align column
// labels with the SPA's table renderer.
type RequestLog struct {
	AtMs       int64  `json:"atMs"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	DurationMs int64  `json:"durationMs"`
	UserEmail  string `json:"userEmail,omitempty"`
}

// RequestLogger is the ring buffer. NewRequestLogger sizes it once;
// callers Record events as the http.Handler middleware sees them.
// Snapshot returns a copy in newest-first order for the panel.
type RequestLogger struct {
	mu    sync.Mutex
	cap   int
	items []RequestLog // grows up to cap, then wraps via head
	head  int          // index of the oldest item once full
	full  bool         // true once we've written cap entries
}

// NewRequestLogger creates a logger with the given capacity. Sizes
// outside the validated range (10–5000) get clamped, since
// in-buffer logging shouldn't fail a boot if config validation
// somehow let through a bad value.
func NewRequestLogger(cap int) *RequestLogger {
	switch {
	case cap < 10:
		cap = 10
	case cap > 5000:
		cap = 5000
	}
	return &RequestLogger{
		cap:   cap,
		items: make([]RequestLog, 0, cap),
	}
}

// Record appends a request log entry. When the buffer is full, the
// oldest entry is overwritten (ring semantics).
func (r *RequestLogger) Record(entry RequestLog) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		r.items = append(r.items, entry)
		if len(r.items) == r.cap {
			r.full = true
			r.head = 0
		}
		return
	}
	r.items[r.head] = entry
	r.head = (r.head + 1) % r.cap
}

// Snapshot returns a copy of the buffer's contents in newest-first
// order. Safe to call concurrently with Record; the returned slice
// is owned by the caller.
func (r *RequestLogger) Snapshot() []RequestLog {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.full {
		// Buffer hasn't wrapped yet; items[0..n] are oldest→newest.
		out := make([]RequestLog, len(r.items))
		for i, e := range r.items {
			out[len(r.items)-1-i] = e // reverse for newest-first
		}
		return out
	}

	// Wrapped: head points to oldest. Walk backwards from
	// head-1 (newest) for cap entries.
	out := make([]RequestLog, 0, r.cap)
	for i := 0; i < r.cap; i++ {
		idx := (r.head - 1 - i + r.cap) % r.cap
		out = append(out, r.items[idx])
	}
	return out
}

// Len returns the number of entries currently stored. Useful for
// the panel's section header ("Recent requests (N)").
func (r *RequestLogger) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.full {
		return r.cap
	}
	return len(r.items)
}

// Cap returns the configured maximum entries.
func (r *RequestLogger) Cap() int {
	if r == nil {
		return 0
	}
	return r.cap
}
