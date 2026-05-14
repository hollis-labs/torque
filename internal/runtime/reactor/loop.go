package reactor

import (
	"context"
	"log"
	"sync"

	gomsg "github.com/hollis-labs/go-messaging"
)

// Subscriber is the narrow surface Loop needs from the broker — a Subscribe
// method returning a channel of envelopes. Matches broker.Broker.Subscribe;
// kept as an interface so tests can drive Loop without a real SQLite-backed
// broker.
type Subscriber interface {
	Subscribe(ctx context.Context, to gomsg.Address, f gomsg.Filter) (<-chan gomsg.Envelope, error)
}

// Loop is the thin runner that subscribes to broker envelopes and pipes
// each through a Dispatcher. One goroutine per Loop; serialized dispatch
// preserves envelope ordering per subscription (matters when a fast burst
// of escalation + status_update arrives for the same task).
//
// V0 wires a single Loop in cmd/torque/serve.go subscribing on a zero
// Address (system-wide); future sprints may run multiple Loops scoped to
// specific authorities.
type Loop struct {
	sub    Subscriber
	disp   *Dispatcher
	to     gomsg.Address
	filter gomsg.Filter

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// NewLoop returns a Loop bound to sub + disp. The `to` address selects
// which envelopes the loop receives (zero Address = system-wide; see
// internal/messaging/sqlstore.go fanOut). `filter` further narrows by
// kind/channel/thread — V0 production wiring passes an empty filter so
// every kind reaches Dispatcher, which itself encodes the V0 routing
// table.
func NewLoop(sub Subscriber, disp *Dispatcher, to gomsg.Address, filter gomsg.Filter) *Loop {
	return &Loop{
		sub:    sub,
		disp:   disp,
		to:     to,
		filter: filter,
	}
}

// Start subscribes to the broker and launches the dispatch goroutine. Safe
// to call once per Loop; subsequent calls without an intervening Close are
// no-ops. Returns an error only when Subscribe itself errors — once Start
// returns nil, the loop is running.
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

// Close cancels the subscription ctx and waits for the dispatch goroutine
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
			// Dispatch errors are logged here (rather than thrown) because
			// the Loop's contract is "keep consuming"; a transient surface
			// failure on one envelope must not silently halt the reactor.
			// The Dispatch call itself already log-trails routing misses.
			res := l.disp.Dispatch(ctx, env)
			if res.Err != nil {
				log.Printf("[reactor] dispatch err env=%s kind=%s action=%s: %v",
					env.ID, env.Kind, res.Action, res.Err)
			}
		}
	}
}
