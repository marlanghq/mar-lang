package admin

import "testing"

// TestRequestLogger_OrdersNewestFirst — small buffer, fewer entries
// than cap. Snapshot should reverse insertion order.
func TestRequestLogger_OrdersNewestFirst(t *testing.T) {
	r := NewRequestLogger(10)
	for i := 1; i <= 3; i++ {
		r.Record(RequestLog{AtMs: int64(i), Path: "/p" + itoa(i)})
	}
	got := r.Snapshot()
	if len(got) != 3 {
		t.Fatalf("len: got %d, want 3", len(got))
	}
	if got[0].Path != "/p3" || got[2].Path != "/p1" {
		t.Errorf("order: got %v %v %v; want newest-first /p3,/p2,/p1",
			got[0].Path, got[1].Path, got[2].Path)
	}
}

// TestRequestLogger_RingOverwritesOldest — fill past cap, the
// oldest entries vanish, newest stay. Cap=10 is the minimum
// allowed by the validator; record 12 entries to force one full
// wrap.
func TestRequestLogger_RingOverwritesOldest(t *testing.T) {
	r := NewRequestLogger(10)
	for i := 1; i <= 12; i++ {
		r.Record(RequestLog{AtMs: int64(i), Path: "/p" + itoa(i)})
	}
	got := r.Snapshot()
	if len(got) != 10 {
		t.Fatalf("len: got %d, want 10", len(got))
	}
	// Newest-first: /p12 first, /p3 last (the two oldest, /p1 and
	// /p2, were overwritten).
	if got[0].Path != "/p12" {
		t.Errorf("newest: got %q, want /p12", got[0].Path)
	}
	if got[9].Path != "/p3" {
		t.Errorf("oldest-kept: got %q, want /p3", got[9].Path)
	}
}

// TestRequestLogger_ClampsOutOfRangeSize — defensive clamp so a
// programming error elsewhere can't OOM the runtime. Validated by
// recording cap+1 entries and observing that the buffer wraps at
// the clamped value, not the requested value.
func TestRequestLogger_ClampsOutOfRangeSize(t *testing.T) {
	// Asked for 99999 → should clamp to 5000. Filling 5001 entries
	// proves the cap is 5000 (oldest gets dropped).
	r := NewRequestLogger(99999)
	for i := 1; i <= 5001; i++ {
		r.Record(RequestLog{AtMs: int64(i)})
	}
	if got := len(r.Snapshot()); got != 5000 {
		t.Errorf("over-cap clamp: snapshot len got %d, want 5000", got)
	}

	// Asked for 2 → should clamp to 10. Same trick with 11 entries.
	r2 := NewRequestLogger(2)
	for i := 1; i <= 11; i++ {
		r2.Record(RequestLog{AtMs: int64(i)})
	}
	if got := len(r2.Snapshot()); got != 10 {
		t.Errorf("under-cap clamp: snapshot len got %d, want 10", got)
	}
}

// TestRequestLogger_NilSafe — Record/Snapshot/Len all tolerate a
// nil receiver, since the boot-time wiring may legitimately not
// install a logger (mar dev with no ServeLive yet).
func TestRequestLogger_NilSafe(t *testing.T) {
	var r *RequestLogger
	r.Record(RequestLog{}) // must not panic
	if got := r.Snapshot(); got != nil {
		t.Errorf("nil Snapshot: got %v, want nil", got)
	}
	if r.Len() != 0 {
		t.Errorf("nil Len: got %d, want 0", r.Len())
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	if i < 0 {
		return "-" + itoa(-i)
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
