package ratelimit

import (
	"testing"
	"time"
)

// TestAllow_FirstHitGetsThrough — first Allow() on a fresh key
// always succeeds (bucket starts at full burst).
func TestAllow_FirstHitGetsThrough(t *testing.T) {
	l := New(Policy{Rate: 10, Burst: 5})
	defer l.Stop()

	ok, retry := l.Allow("ip-1")
	if !ok {
		t.Errorf("first hit should pass; got reject (retry=%v)", retry)
	}
}

// TestAllow_BurstThenReject — Burst hits in a row all pass, the
// (Burst+1)th rejects with a positive retry duration.
func TestAllow_BurstThenReject(t *testing.T) {
	l := New(Policy{Rate: 1, Burst: 3}) // 1 token/sec, 3 burst
	defer l.Stop()

	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("ip"); !ok {
			t.Fatalf("hit %d should pass within burst; got reject", i+1)
		}
	}
	ok, retry := l.Allow("ip")
	if ok {
		t.Errorf("hit 4 should reject (over burst)")
	}
	if retry <= 0 || retry > time.Second*2 {
		t.Errorf("retryAfter should be ~1s; got %v", retry)
	}
}

// TestAllow_KeysAreIsolated — one IP exhausting its bucket
// doesn't affect another IP's bucket.
func TestAllow_KeysAreIsolated(t *testing.T) {
	l := New(Policy{Rate: 1, Burst: 2})
	defer l.Stop()

	l.Allow("ip-A")
	l.Allow("ip-A")
	if ok, _ := l.Allow("ip-A"); ok {
		t.Fatal("ip-A should be exhausted")
	}
	if ok, _ := l.Allow("ip-B"); !ok {
		t.Errorf("ip-B should be fresh (separate bucket)")
	}
}

// TestAllow_RefillsOverTime — after the bucket is empty, waiting
// long enough should let new requests through. Uses a fast rate
// (1000/s) so the test runs in millis, not seconds.
func TestAllow_RefillsOverTime(t *testing.T) {
	l := New(Policy{Rate: 1000, Burst: 2})
	defer l.Stop()

	// Drain the bucket.
	l.Allow("ip")
	l.Allow("ip")
	if ok, _ := l.Allow("ip"); ok {
		t.Fatal("should reject immediately after drain")
	}
	// Wait for ~2 tokens to refill (2ms at 1000/s).
	time.Sleep(5 * time.Millisecond)
	if ok, _ := l.Allow("ip"); !ok {
		t.Errorf("should pass after refill window")
	}
}

// TestAllow_RetryAfterIsAccurate — the duration returned when
// rejecting matches roughly the time until 1 token refills.
func TestAllow_RetryAfterIsAccurate(t *testing.T) {
	l := New(Policy{Rate: 2, Burst: 1}) // 2/s, burst 1
	defer l.Stop()

	l.Allow("ip") // consume the only token
	ok, retry := l.Allow("ip")
	if ok {
		t.Fatal("second hit should reject")
	}
	// 1 token at 2/s refills in 0.5s. Allow some slack for time
	// elapsed during the call.
	want := 500 * time.Millisecond
	if retry < want-100*time.Millisecond || retry > want+100*time.Millisecond {
		t.Errorf("retryAfter: got %v, want ~%v", retry, want)
	}
}

// TestEviction_DropsIdleKeys — keys that fully refilled and went
// idle past the threshold get evicted, freeing the map.
func TestEviction_DropsIdleKeys(t *testing.T) {
	l := New(Policy{Rate: 1000, Burst: 100})
	defer l.Stop()

	l.Allow("idle-1")
	l.Allow("idle-2")
	if l.numKeys() != 2 {
		t.Fatalf("expected 2 keys after Allow; got %d", l.numKeys())
	}

	// Manually invoke eviction with a faked "now" 2 hours later.
	// Buckets should be fully refilled (Rate=1000 over 2h = way
	// past Burst) AND past the 1h idle threshold.
	l.evict(time.Now().Add(2 * time.Hour))
	if l.numKeys() != 0 {
		t.Errorf("expected eviction to drop both keys; got %d", l.numKeys())
	}
}

// TestEviction_KeepsActiveKeys — a key that was hit recently is
// kept regardless of bucket level.
func TestEviction_KeepsActiveKeys(t *testing.T) {
	l := New(Policy{Rate: 1, Burst: 5})
	defer l.Stop()

	l.Allow("active")
	// Run eviction "now" — under 1h since last access, key stays.
	l.evict(time.Now())
	if l.numKeys() != 1 {
		t.Errorf("expected active key to survive eviction; got %d", l.numKeys())
	}
}
