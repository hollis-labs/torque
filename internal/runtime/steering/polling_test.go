package steering

// White-box tests for PollRegistry — same package so the tests can drive
// the injectable clock (the unexported `now` field) and exercise TTL
// expiry deterministically without sleeping.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// newTestRegistry returns a registry with a controllable clock. The
// returned advance() slides the clock forward.
func newTestRegistry(ttl time.Duration) (*PollRegistry, func(time.Duration)) {
	r := NewPollRegistry(ttl)
	now := time.Now()
	r.now = func() time.Time { return now }
	return r, func(d time.Duration) { now = now.Add(d) }
}

func TestPollRegistry_MarkThenIsPolling(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	assert.False(t, r.IsPolling("msg://agent/local/a"), "unknown urn is not polling")

	r.MarkPolling("msg://agent/local/a")
	assert.True(t, r.IsPolling("msg://agent/local/a"), "marked urn is polling")
	assert.False(t, r.IsPolling("msg://agent/local/b"), "a different urn is unaffected")
}

func TestPollRegistry_ExpiresAfterTTL(t *testing.T) {
	r, advance := newTestRegistry(time.Minute)
	r.MarkPolling("msg://agent/local/a")

	advance(59 * time.Second)
	assert.True(t, r.IsPolling("msg://agent/local/a"), "still within the TTL window")

	advance(2 * time.Second) // now 61s since mark — past the 60s TTL
	assert.False(t, r.IsPolling("msg://agent/local/a"), "opt-in lapses after the TTL")
}

func TestPollRegistry_MarkRefreshesTTL(t *testing.T) {
	r, advance := newTestRegistry(time.Minute)
	r.MarkPolling("msg://agent/local/a")

	advance(50 * time.Second)
	r.MarkPolling("msg://agent/local/a") // re-poll slides the window forward

	advance(50 * time.Second) // 100s since first mark, only 50s since refresh
	assert.True(t, r.IsPolling("msg://agent/local/a"), "a re-poll keeps the opt-in fresh")
}

func TestPollRegistry_ReleaseRevertsImmediately(t *testing.T) {
	r, _ := newTestRegistry(time.Hour)
	r.MarkPolling("msg://agent/local/a")
	assert.True(t, r.IsPolling("msg://agent/local/a"))

	r.Release("msg://agent/local/a")
	assert.False(t, r.IsPolling("msg://agent/local/a"), "release opts out without waiting out the TTL")

	r.Release("msg://agent/local/unknown") // releasing an unknown urn is a no-op
}

func TestPollRegistry_ExpiredEntryIsPruned(t *testing.T) {
	r, advance := newTestRegistry(time.Minute)
	r.MarkPolling("msg://agent/local/a")
	advance(2 * time.Minute)

	// IsPolling on a stale entry both reports false and prunes it.
	assert.False(t, r.IsPolling("msg://agent/local/a"))
	r.mu.Lock()
	_, present := r.seen["msg://agent/local/a"]
	r.mu.Unlock()
	assert.False(t, present, "a stale entry is pruned in passing, not left to leak")
}

func TestPollRegistry_Active(t *testing.T) {
	r, advance := newTestRegistry(time.Minute)
	r.MarkPolling("msg://agent/local/a")
	r.MarkPolling("msg://agent/local/b")
	assert.Equal(t, 2, r.Active())

	advance(90 * time.Second)
	assert.Equal(t, 0, r.Active(), "stale opt-ins do not count and are pruned")
}

func TestPollRegistry_NonPositiveTTLFallsBack(t *testing.T) {
	assert.Equal(t, DefaultPollTTL, NewPollRegistry(0).TTL())
	assert.Equal(t, DefaultPollTTL, NewPollRegistry(-time.Second).TTL())
	assert.Equal(t, 5*time.Second, NewPollRegistry(5*time.Second).TTL())
}

func TestPollRegistry_NilReceiverIsSafe(t *testing.T) {
	var r *PollRegistry
	// Every method must tolerate a nil registry — an unwired MCP host /
	// bridge degrades to "nobody is polling".
	assert.NotPanics(t, func() {
		r.MarkPolling("msg://agent/local/a")
		r.Release("msg://agent/local/a")
	})
	assert.False(t, r.IsPolling("msg://agent/local/a"))
	assert.Equal(t, time.Duration(0), r.TTL())
	assert.Equal(t, 0, r.Active())
}

func TestPollRegistry_EmptyURNIgnored(t *testing.T) {
	r, _ := newTestRegistry(time.Minute)
	r.MarkPolling("")
	assert.False(t, r.IsPolling(""), "an empty urn is never tracked")
	assert.Equal(t, 0, r.Active())
}
