package steering_test

import (
	"context"
	"errors"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/runtime/steering"
)

// fakeSubscriber implements steering.Subscriber. The returned channel is
// owned by the test; the loop drains it until ctx is canceled.
type fakeSubscriber struct {
	ch  chan gomsg.Envelope
	err error
}

func (f *fakeSubscriber) Subscribe(ctx context.Context, _ gomsg.Address, _ gomsg.Filter) (<-chan gomsg.Envelope, error) {
	if f.err != nil {
		return nil, f.err
	}
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

func TestLoop_DeliversSteeringEnvelopesUntilClose(t *testing.T) {
	in := make(chan gomsg.Envelope, 4)
	sub := &fakeSubscriber{ch: in}

	to := sessionAddr("SES-LOOP")
	gw := &fakeGateway{live: map[string]string{to.URN(): "SES-LOOP"}}
	cons := &fakeConsumer{}
	loop := steering.NewLoop(sub, steering.New(gw, cons), gomsg.Address{}, gomsg.Filter{})

	require.NoError(t, loop.Start(context.Background()))

	// A steering envelope and a non-steerable one — only the first lands.
	in <- gomsg.Envelope{ID: "L-STEER", Kind: gomsg.MsgKindNotice, To: to, Payload: []byte(`"go"`)}
	in <- gomsg.Envelope{ID: "L-USER", Kind: gomsg.MsgKindNotice, To: userAddr(), Payload: []byte(`"ignored"`)}

	require.Eventually(t, func() bool {
		return len(gw.deliveredTurns()) == 1
	}, 200*time.Millisecond, 5*time.Millisecond)

	assert.Equal(t, []string{"L-STEER"}, cons.ids())

	loop.Close()
	loop.Close() // idempotent
}

func TestLoop_StartSubscribeError(t *testing.T) {
	sub := &fakeSubscriber{err: errors.New("nope")}
	loop := steering.NewLoop(sub, steering.New(&fakeGateway{}, nil), gomsg.Address{}, gomsg.Filter{})
	assert.ErrorContains(t, loop.Start(context.Background()), "nope")
}

func TestLoop_CloseWithoutStartIsNoOp(t *testing.T) {
	loop := steering.NewLoop(&fakeSubscriber{ch: make(chan gomsg.Envelope)},
		steering.New(&fakeGateway{}, nil), gomsg.Address{}, gomsg.Filter{})
	loop.Close() // must not panic
}

func TestLoop_StartIdempotent(t *testing.T) {
	sub := &fakeSubscriber{ch: make(chan gomsg.Envelope)}
	loop := steering.NewLoop(sub, steering.New(&fakeGateway{}, nil), gomsg.Address{}, gomsg.Filter{})
	require.NoError(t, loop.Start(context.Background()))
	require.NoError(t, loop.Start(context.Background())) // no-op
	loop.Close()
}

func TestLoop_KeepsConsumingAfterDeliveryFailure(t *testing.T) {
	in := make(chan gomsg.Envelope, 4)
	sub := &fakeSubscriber{ch: in}

	to := sessionAddr("SES-RESILIENT")
	// SteerTurn always errors — the loop must not halt.
	gw := &fakeGateway{live: map[string]string{to.URN(): "SES-RESILIENT"}, steerErr: errors.New("boom")}
	loop := steering.NewLoop(sub, steering.New(gw, &fakeConsumer{}), gomsg.Address{}, gomsg.Filter{})

	require.NoError(t, loop.Start(context.Background()))
	in <- gomsg.Envelope{ID: "F1", Kind: gomsg.MsgKindNotice, To: to, Payload: []byte(`"a"`)}
	in <- gomsg.Envelope{ID: "F2", Kind: gomsg.MsgKindNotice, To: to, Payload: []byte(`"b"`)}

	// The loop is still alive and consuming after a failed delivery: a
	// later non-steering envelope still flows through without deadlock.
	require.Eventually(t, func() bool {
		select {
		case in <- gomsg.Envelope{ID: "F3", To: userAddr()}:
			return true
		default:
			return false
		}
	}, 200*time.Millisecond, 5*time.Millisecond)

	loop.Close()
}
