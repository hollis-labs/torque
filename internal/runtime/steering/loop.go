package steering

import (
	"context"
	"log"
	"sync"

	gomsg "github.com/hollis-labs/go-messaging"
)

// Subscriber is the narrow surface Loop needs from the broker — a
// Subscribe method returning a channel of envelopes. broker.Broker
// satisfies it; kept as an interface so tests can drive Loop without a
// SQLite-backed broker. Mirrors reactor.Subscriber.
type Subscriber interface {
	Subscribe(ctx context.Context, to gomsg.Address, f gomsg.Filter) (<-chan gomsg.Envelope, error)
}

// Loop subscribes to broker envelopes and pipes each through a Bridge.
// One goroutine per Loop; serialized delivery preserves per-subscription
// envelope order (so a burst of steering messages to one agent arrives in
// send order).
//
// V0 wiring (internal/runtime/bootstrap/steering.go) runs a single Loop
// subscribed on a zero Address — system-wide — so it sees every envelope
// and the Bridge filters to steerable ones. This runs ALONGSIDE the
// reactor's Loop: both subscribe system-wide, each fanned a private copy
// of every envelope by the messaging Store, and they act on a disjoint
// set of envelopes (the reactor's four routed kinds vs. the bridge's
// notice/response-to-a-live-session — see steering.Steerable).
type Loop struct {
	sub    Subscriber
	bridge *Bridge
	to     gomsg.Address
	filter gomsg.Filter

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewLoop returns a Loop bound to sub + bridge. The `to` address selects
// which envelopes the loop receives (zero Address = system-wide); `filter`
// further narrows by kind/channel/thread. V0 production wiring passes a
// zero Address and empty filter — the Bridge encodes the steerable-address
// rule.
func NewLoop(sub Subscriber, bridge *Bridge, to gomsg.Address, filter gomsg.Filter) *Loop {
	return &Loop{sub: sub, bridge: bridge, to: to, filter: filter}
}

// Start subscribes to the broker and launches the delivery goroutine.
// Safe to call once; subsequent calls without an intervening Close are
// no-ops. Returns an error only when Subscribe itself errors — once Start
// returns nil the loop is running.
func (l *Loop) Start(parent context.Context) error {
	l.mu.Lock()
	if l.cancel != nil {
		l.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	ch, err := l.sub.Subscribe(ctx, l.to, l.filter)
	if err != nil {
		cancel()
		l.mu.Unlock()
		return err
	}
	done := make(chan struct{})
	l.cancel = cancel
	l.done = done
	l.mu.Unlock()

	go l.run(ctx, ch, done)
	return nil
}

// Close cancels the subscription ctx and waits for the delivery goroutine
// to drain. Idempotent.
func (l *Loop) Close() {
	l.mu.Lock()
	cancel := l.cancel
	done := l.done
	l.cancel = nil
	l.done = nil
	l.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func (l *Loop) run(ctx context.Context, ch <-chan gomsg.Envelope, done chan struct{}) {
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case env, ok := <-ch:
			if !ok {
				return
			}
			// Deliver is total and self-logging; the loop's contract is
			// "keep consuming" — a per-envelope steering failure must not
			// halt the bridge. We surface the failed outcome here so it is
			// greppable next to the reactor's dispatch log.
			res := l.bridge.Deliver(ctx, env)
			if res.Outcome == OutcomeFailed {
				log.Printf("[steering] delivery failed env=%s session=%s: %v",
					res.EnvelopeID, res.SessionID, res.Err)
			}
		}
	}
}
