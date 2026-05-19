package bootstrap_test

import (
	"context"
	"testing"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/go-messaging/memstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/broker"
	"github.com/hollis-labs/torque/internal/runtime/bootstrap"
)

func TestSteeringBridge_NilBrokerErrors(t *testing.T) {
	closer, err := bootstrap.SteeringBridge(context.Background(), nil, nil, nil)
	assert.Error(t, err)
	require.NotNil(t, closer, "closer is always non-nil so the caller can defer it")
	closer() // must not panic
}

func TestSteeringBridge_StartsAndCloses(t *testing.T) {
	// A real broker over the in-memory reference Store; nil sessions is
	// tolerated (the gateway misses every resolution). This is a wiring
	// smoke test — steering behavior is covered in internal/runtime/steering.
	brk := broker.New(memstore.New(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	closer, err := bootstrap.SteeringBridge(ctx, brk, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, closer)

	// The loop is subscribed; sending a steering envelope must not panic
	// even with no agent manager wired.
	_, err = brk.Send(ctx, gomsg.Envelope{
		Kind:        gomsg.MsgKindNotice,
		From:        gomsg.Address{Kind: gomsg.KindUser, Authority: "local", ID: "op"},
		To:          gomsg.Address{Kind: gomsg.KindSession, Authority: "local", ID: "SES-X"},
		Payload:     []byte(`"steer"`),
		ContentType: "application/json",
	})
	require.NoError(t, err)

	closer()
	closer() // idempotent
}
