package scheduler

import (
	"testing"
	"time"
)

func TestProgressThrottlerFirstAllowed(t *testing.T) {
	tr := newProgressThrottler(2 * time.Second)
	now := time.Unix(1_000_000_000, 0)

	if !tr.allow(42, now) {
		t.Fatal("first emission for a run must be allowed")
	}
}

func TestProgressThrottlerWithinWindowRejected(t *testing.T) {
	tr := newProgressThrottler(2 * time.Second)
	now := time.Unix(1_000_000_000, 0)

	tr.allow(42, now)
	if tr.allow(42, now.Add(500*time.Millisecond)) {
		t.Fatal("emission inside window must be rejected")
	}
	if tr.allow(42, now.Add(1_999*time.Millisecond)) {
		t.Fatal("emission just inside window must be rejected")
	}
}

func TestProgressThrottlerAfterWindowAllowed(t *testing.T) {
	tr := newProgressThrottler(2 * time.Second)
	now := time.Unix(1_000_000_000, 0)

	tr.allow(42, now)
	if !tr.allow(42, now.Add(2*time.Second)) {
		t.Fatal("emission at window boundary must be allowed")
	}
	if !tr.allow(42, now.Add(5*time.Second)) {
		t.Fatal("emission past window must be allowed")
	}
}

func TestProgressThrottlerPerRunIsolated(t *testing.T) {
	tr := newProgressThrottler(2 * time.Second)
	now := time.Unix(1_000_000_000, 0)

	if !tr.allow(1, now) {
		t.Fatal("run 1 first emission must be allowed")
	}
	if !tr.allow(2, now) {
		t.Fatal("run 2 first emission must be allowed — throttler is per-run")
	}
	if tr.allow(1, now.Add(time.Second)) {
		t.Fatal("run 1 second emission in window must be rejected")
	}
	if tr.allow(2, now.Add(time.Second)) {
		t.Fatal("run 2 second emission in window must be rejected")
	}
}

func TestProgressThrottlerReleaseResets(t *testing.T) {
	tr := newProgressThrottler(2 * time.Second)
	now := time.Unix(1_000_000_000, 0)

	tr.allow(42, now)
	tr.release(42)

	if !tr.allow(42, now.Add(100*time.Millisecond)) {
		t.Fatal("after release, next emission must be allowed even inside the original window")
	}
}
