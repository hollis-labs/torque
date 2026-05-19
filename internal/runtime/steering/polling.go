package steering

import (
	"sync"
	"time"
)

// DefaultPollTTL is the freshness window for a polling opt-in. An agent
// that has called torque_inbox_poll within this window is treated as
// "actively polling" and the steering bridge stops injecting turns into
// it (see Bridge.Deliver / OutcomePolling). Once the window lapses with
// no further poll, the opt-in expires and the bridge resumes the
// inject-at-turn-boundary default — so an agent that crashes or simply
// stops polling self-heals back to the safe default with no operator
// action and no stranded messages.
//
// 90s is comfortably longer than the gap between an actively
// communicating agent's tool calls, yet short enough that a stalled
// agent reverts to inject-at-turn within a couple of turns.
const DefaultPollTTL = 90 * time.Second

// PollRegistry tracks which recipient addresses have opted into
// mid-session inbox polling (CW-20260518-0042, messaging epic A).
//
// Design decision #1 (LOCKED 2026-05-18, torque-messaging-design.md):
// the default delivery mode is inject-at-turn-boundary; agents that are
// actively communicating may OPT IN to polling their inbox between tool
// calls instead. The opt-in is mutually exclusive with inject-at-turn
// per recipient — an agent that pulls its own inbox must not also have
// envelopes pushed into its turn loop, or every steering message is
// handled twice. This registry is the single shared piece of state that
// makes the two modes exclusive: the torque_inbox_poll MCP tool writes
// to it (MarkPolling / Release) and the steering Bridge reads from it
// (IsPolling).
//
// Opt-in is keyed by the recipient's canonical URN (gomsg.Address.URN())
// — the same string the Bridge sees as env.To.URN() — so the poll tool
// and the bridge agree without sharing an address type.
//
// Lifetime: opt-ins are time-bounded (see DefaultPollTTL). Every poll
// refreshes the timestamp; IsPolling treats a stale entry as opted-out
// and prunes it. All methods are safe for concurrent use and safe to
// call on a nil *PollRegistry (an unwired bridge / MCP host degrades to
// "nobody is polling" — the inject-at-turn default).
type PollRegistry struct {
	ttl time.Duration
	now func() time.Time

	mu   sync.Mutex
	seen map[string]time.Time // recipient URN -> last poll time
}

// NewPollRegistry returns a registry whose opt-ins expire after ttl. A
// non-positive ttl falls back to DefaultPollTTL.
func NewPollRegistry(ttl time.Duration) *PollRegistry {
	if ttl <= 0 {
		ttl = DefaultPollTTL
	}
	return &PollRegistry{
		ttl:  ttl,
		now:  time.Now,
		seen: make(map[string]time.Time),
	}
}

// MarkPolling records (or refreshes) a polling opt-in for urn. Called by
// the torque_inbox_poll MCP tool on every poll: the first call opts the
// recipient in, every subsequent call slides the TTL forward so an
// agent that keeps polling stays opted in. A nil registry or empty urn
// is a no-op.
func (r *PollRegistry) MarkPolling(urn string) {
	if r == nil || urn == "" {
		return
	}
	r.mu.Lock()
	r.seen[urn] = r.now()
	r.mu.Unlock()
}

// Release drops a polling opt-in immediately, reverting urn to the
// inject-at-turn default without waiting out the TTL. Called by
// torque_inbox_poll with release=true — for an agent that is about to
// go heads-down and wants steering turns to resume at once. A nil
// registry or empty urn is a no-op; releasing an unknown urn is also a
// no-op.
func (r *PollRegistry) Release(urn string) {
	if r == nil || urn == "" {
		return
	}
	r.mu.Lock()
	delete(r.seen, urn)
	r.mu.Unlock()
}

// IsPolling reports whether urn has a live polling opt-in — i.e. it
// called MarkPolling within the TTL window. A stale entry is treated as
// opted-out and pruned in passing, so the map does not accumulate dead
// recipients across a long daemon uptime. A nil registry or empty urn
// always reports false (the inject-at-turn default).
func (r *PollRegistry) IsPolling(urn string) bool {
	if r == nil || urn == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	last, ok := r.seen[urn]
	if !ok {
		return false
	}
	if r.now().Sub(last) > r.ttl {
		delete(r.seen, urn)
		return false
	}
	return true
}

// TTL returns the opt-in freshness window. Surfaced to the MCP tool so a
// polling agent knows how soon it must poll again to stay opted in. A
// nil registry returns 0.
func (r *PollRegistry) TTL() time.Duration {
	if r == nil {
		return 0
	}
	return r.ttl
}

// Active returns the number of recipients with a live (non-stale)
// polling opt-in, pruning any expired entries it walks past. For
// observability/tests only — not on any hot path. A nil registry
// returns 0.
func (r *PollRegistry) Active() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	n := 0
	for urn, last := range r.seen {
		if now.Sub(last) > r.ttl {
			delete(r.seen, urn)
			continue
		}
		n++
	}
	return n
}
