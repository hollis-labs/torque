package reactor_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/broker"
	"github.com/hollis-labs/torque/internal/runtime/reactor"
)

// fakeSubscriber implements reactor.Subscriber. The returned channel is
// owned by the test; the loop drains it until ctx is canceled.
type fakeSubscriber struct {
	ch  chan gomsg.Envelope
	err error
}

func (f *fakeSubscriber) Subscribe(ctx context.Context, _ gomsg.Address, _ gomsg.Filter) (<-chan gomsg.Envelope, error) {
	if f.err != nil {
		return nil, f.err
	}
	// Auto-close the channel when ctx is canceled so the loop exits cleanly.
	out := make(chan gomsg.Envelope, 8)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case env, ok := <-f.ch:
				if !ok {
					return
				}
				select {
				case out <- env:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

func TestLoop_DispatchesEnvelopesUntilClose(t *testing.T) {
	in := make(chan gomsg.Envelope, 4)
	sub := &fakeSubscriber{ch: in}

	deps, _, bl, _, _, _, _ := newDeps()
	d := reactor.New(deps)
	loop := reactor.NewLoop(sub, d, gomsg.Address{}, gomsg.Filter{})

	require.NoError(t, loop.Start(context.Background()))

	payload, _ := json.Marshal(broker.StatusPayload{State: "blocked", Note: "wait"})
	in <- gomsg.Envelope{
		ID:       "L1",
		Kind:     gomsg.MsgKindStatusUpdate,
		Payload:  payload,
		Metadata: map[string]string{"task_id": "T-LOOP-1"},
	}

	// Spin briefly until the dispatcher sees the envelope. 200ms is plenty
	// for an in-memory channel hop; this is the kind of bound the race
	// detector also exercises (no sleep-loop polling on shared mutable
	// state — bl.calls is mutex-guarded inside the fake).
	require.Eventually(t, func() bool {
		bl.mu.Lock()
		defer bl.mu.Unlock()
		return len(bl.calls) == 1
	}, 200*time.Millisecond, 5*time.Millisecond)

	loop.Close()
	// Close is idempotent.
	loop.Close()
}

func TestLoop_StartSubscribeError(t *testing.T) {
	sub := &fakeSubscriber{err: errors.New("nope")}
	d := reactor.New(reactor.Deps{})
	loop := reactor.NewLoop(sub, d, gomsg.Address{}, gomsg.Filter{})

	err := loop.Start(context.Background())
	assert.ErrorContains(t, err, "nope")
}

func TestLoop_CloseWithoutStartIsNoOp(t *testing.T) {
	loop := reactor.NewLoop(&fakeSubscriber{ch: make(chan gomsg.Envelope)}, reactor.New(reactor.Deps{}), gomsg.Address{}, gomsg.Filter{})
	loop.Close() // must not panic
}

func TestLoop_StartIdempotent(t *testing.T) {
	in := make(chan gomsg.Envelope)
	sub := &fakeSubscriber{ch: in}
	d := reactor.New(reactor.Deps{})
	loop := reactor.NewLoop(sub, d, gomsg.Address{}, gomsg.Filter{})

	require.NoError(t, loop.Start(context.Background()))
	require.NoError(t, loop.Start(context.Background())) // no-op
	loop.Close()
}
