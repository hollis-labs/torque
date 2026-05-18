package steering_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/runtime/steering"
)

// fakeGateway records SteerTurn calls and resolves a fixed live-session
// table. A SteerTurn error is injected via steerErr.
type fakeGateway struct {
	mu       sync.Mutex
	live     map[string]string // recipient URN -> session ID
	steerErr error
	turns    []steeredTurn
}

type steeredTurn struct {
	SessionID string
	Text      string
}

func (g *fakeGateway) LiveSession(addr gomsg.Address) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	id, ok := g.live[addr.URN()]
	return id, ok
}

func (g *fakeGateway) SteerTurn(_ context.Context, sessionID, text string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.steerErr != nil {
		return g.steerErr
	}
	g.turns = append(g.turns, steeredTurn{SessionID: sessionID, Text: text})
	return nil
}

func (g *fakeGateway) deliveredTurns() []steeredTurn {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]steeredTurn(nil), g.turns...)
}

// fakeConsumer records Consume calls.
type fakeConsumer struct {
	mu       sync.Mutex
	consumed []string // envelope IDs
	err      error
}

func (c *fakeConsumer) Consume(_ context.Context, id string, _ gomsg.Address) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.consumed = append(c.consumed, id)
	return nil
}

func (c *fakeConsumer) ids() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.consumed...)
}

func TestSteerable_RecipientAddressKind(t *testing.T) {
	cases := []struct {
		kind gomsg.AddressKind
		want bool
	}{
		{gomsg.KindSession, true},
		{gomsg.KindAgent, true},
		{gomsg.KindUser, false},
		{gomsg.KindService, false},
		{gomsg.KindWorkflow, false},
		{"", false},
	}
	for _, tc := range cases {
		// Kind=notice is steerable; the recipient address kind is the axis
		// under test here.
		env := gomsg.Envelope{
			Kind: gomsg.MsgKindNotice,
			To:   gomsg.Address{Kind: tc.kind, Authority: "local", ID: "x"},
		}
		assert.Equal(t, tc.want, steering.Steerable(env), "address kind=%s", tc.kind)
	}
}

func TestSteerable_EnvelopeKind(t *testing.T) {
	// Only notice/response are steerable — the bridge stays disjoint from
	// the reactor's escalation/status_update/request/handoff handling even
	// when those are addressed to a live session.
	cases := []struct {
		kind gomsg.Kind
		want bool
	}{
		{gomsg.MsgKindNotice, true},
		{gomsg.MsgKindResponse, true},
		{gomsg.MsgKindEscalation, false},
		{gomsg.MsgKindStatusUpdate, false},
		{gomsg.MsgKindRequest, false},
		{gomsg.MsgKindHandoff, false},
		{"", false},
	}
	for _, tc := range cases {
		env := gomsg.Envelope{Kind: tc.kind, To: sessionAddr("SES-1")}
		assert.Equal(t, tc.want, steering.Steerable(env), "envelope kind=%s", tc.kind)
	}
}

func TestDeliver_HappyPath_InjectsTurnAndConsumes(t *testing.T) {
	to := sessionAddr("SES-RUN")
	gw := &fakeGateway{live: map[string]string{to.URN(): "SES-RUN"}}
	cons := &fakeConsumer{}
	b := steering.New(gw, cons)

	env := gomsg.Envelope{
		ID:      "ENV-OK",
		Kind:    gomsg.MsgKindNotice,
		From:    userAddr(),
		To:      to,
		Payload: []byte(`"steer me"`),
	}
	res := b.Deliver(context.Background(), env)

	assert.Equal(t, steering.OutcomeDelivered, res.Outcome)
	assert.Equal(t, "SES-RUN", res.SessionID)
	assert.NoError(t, res.Err)

	turns := gw.deliveredTurns()
	require.Len(t, turns, 1)
	assert.Equal(t, "SES-RUN", turns[0].SessionID)
	assert.Contains(t, turns[0].Text, "steer me")
	assert.Equal(t, []string{"ENV-OK"}, cons.ids())
}

func TestDeliver_NotAddressed_IsNoop(t *testing.T) {
	gw := &fakeGateway{}
	b := steering.New(gw, &fakeConsumer{})

	env := gomsg.Envelope{ID: "ENV-USER", To: userAddr(), Payload: []byte(`"hi"`)}
	res := b.Deliver(context.Background(), env)

	assert.Equal(t, steering.OutcomeNotAddressed, res.Outcome)
	assert.Empty(t, gw.deliveredTurns())
}

func TestDeliver_NoLiveSession_LeavesEnvelope(t *testing.T) {
	// Addressed to a session, but the gateway resolves no live session.
	gw := &fakeGateway{live: map[string]string{}}
	cons := &fakeConsumer{}
	b := steering.New(gw, cons)

	env := gomsg.Envelope{ID: "ENV-IDLE", Kind: gomsg.MsgKindNotice, To: sessionAddr("SES-GONE"), Payload: []byte(`"steer"`)}
	res := b.Deliver(context.Background(), env)

	assert.Equal(t, steering.OutcomeNoLiveSession, res.Outcome)
	assert.NoError(t, res.Err, "a missed live session is a race, not an error")
	assert.Empty(t, gw.deliveredTurns())
	assert.Empty(t, cons.ids(), "an undelivered envelope must not be consumed")
}

func TestDeliver_SteerTurnFails_NotConsumed(t *testing.T) {
	to := sessionAddr("SES-ERR")
	boom := errors.New("session pipe closed")
	gw := &fakeGateway{live: map[string]string{to.URN(): "SES-ERR"}, steerErr: boom}
	cons := &fakeConsumer{}
	b := steering.New(gw, cons)

	env := gomsg.Envelope{ID: "ENV-FAIL", Kind: gomsg.MsgKindNotice, To: to, Payload: []byte(`"steer"`)}
	res := b.Deliver(context.Background(), env)

	assert.Equal(t, steering.OutcomeFailed, res.Outcome)
	assert.ErrorIs(t, res.Err, boom)
	assert.Empty(t, cons.ids(), "a failed delivery must leave the envelope unconsumed for retry")
}

func TestDeliver_ConsumeFailure_DoesNotUndoDelivery(t *testing.T) {
	to := sessionAddr("SES-C")
	gw := &fakeGateway{live: map[string]string{to.URN(): "SES-C"}}
	cons := &fakeConsumer{err: errors.New("store offline")}
	b := steering.New(gw, cons)

	env := gomsg.Envelope{ID: "ENV-CFAIL", Kind: gomsg.MsgKindNotice, To: to, Payload: []byte(`"steer"`)}
	res := b.Deliver(context.Background(), env)

	// The turn landed; a consume-marking failure is logged but the
	// delivery outcome stands.
	assert.Equal(t, steering.OutcomeDelivered, res.Outcome)
	require.Len(t, gw.deliveredTurns(), 1)
}

func TestDeliver_NilConsumer_StillDelivers(t *testing.T) {
	to := sessionAddr("SES-N")
	gw := &fakeGateway{live: map[string]string{to.URN(): "SES-N"}}
	b := steering.New(gw, nil)

	env := gomsg.Envelope{ID: "ENV-NIL", Kind: gomsg.MsgKindNotice, To: to, Payload: []byte(`"steer"`)}
	res := b.Deliver(context.Background(), env)

	assert.Equal(t, steering.OutcomeDelivered, res.Outcome)
	require.Len(t, gw.deliveredTurns(), 1)
}

func TestDeliver_AgentAddress_Resolves(t *testing.T) {
	// An agent-kind address (id = orchestrator task id) routes the same
	// way once the gateway resolves it to a live session.
	to := gomsg.Address{Kind: gomsg.KindAgent, Authority: "local", ID: "CW-TASK-1"}
	gw := &fakeGateway{live: map[string]string{to.URN(): "SES-FOR-TASK"}}
	b := steering.New(gw, &fakeConsumer{})

	env := gomsg.Envelope{ID: "ENV-AGENT", Kind: gomsg.MsgKindNotice, To: to, Payload: []byte(`"steer the orchestrator"`)}
	res := b.Deliver(context.Background(), env)

	assert.Equal(t, steering.OutcomeDelivered, res.Outcome)
	assert.Equal(t, "SES-FOR-TASK", res.SessionID)
}
