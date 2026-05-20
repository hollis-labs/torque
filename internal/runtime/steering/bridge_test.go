package steering_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

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
	tasks    map[string]string // sessionID -> task ID (for TaskIDForSession)
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

func (g *fakeGateway) TaskIDForSession(sessionID string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tasks == nil {
		return "", false
	}
	id, ok := g.tasks[sessionID]
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

func TestDeliver_RecipientPolling_SkipsInjection(t *testing.T) {
	// CW-20260518-0042: a recipient that has opted into inbox polling must
	// NOT get a turn injected — the bridge yields the envelope to the
	// agent's own poll.
	to := sessionAddr("SES-POLL")
	gw := &fakeGateway{live: map[string]string{to.URN(): "SES-POLL"}}
	cons := &fakeConsumer{}
	reg := steering.NewPollRegistry(time.Hour)
	reg.MarkPolling(to.URN())
	b := steering.New(gw, cons).WithPolling(reg)

	env := gomsg.Envelope{ID: "ENV-POLL", Kind: gomsg.MsgKindNotice, To: to, Payload: []byte(`"steer"`)}
	res := b.Deliver(context.Background(), env)

	assert.Equal(t, steering.OutcomePolling, res.Outcome)
	assert.NoError(t, res.Err)
	assert.Empty(t, gw.deliveredTurns(), "a polling recipient gets no injected turn")
	assert.Empty(t, cons.ids(), "the envelope is left unconsumed for the agent's own poll to drain")
}

func TestDeliver_PollingReleased_ResumesInjection(t *testing.T) {
	// Releasing the opt-in reverts the recipient to the inject-at-turn default.
	to := sessionAddr("SES-REL")
	gw := &fakeGateway{live: map[string]string{to.URN(): "SES-REL"}}
	reg := steering.NewPollRegistry(time.Hour)
	reg.MarkPolling(to.URN())
	reg.Release(to.URN())
	b := steering.New(gw, &fakeConsumer{}).WithPolling(reg)

	env := gomsg.Envelope{ID: "ENV-REL", Kind: gomsg.MsgKindNotice, To: to, Payload: []byte(`"steer"`)}
	res := b.Deliver(context.Background(), env)

	assert.Equal(t, steering.OutcomeDelivered, res.Outcome, "after release the inject-at-turn default resumes")
	require.Len(t, gw.deliveredTurns(), 1)
}

func TestDeliver_NoPollRegistry_DefaultsToInjection(t *testing.T) {
	// A bridge with no poll registry wired treats every recipient as
	// inject-at-turn — the default holds when the opt-in path is absent.
	to := sessionAddr("SES-NOREG")
	gw := &fakeGateway{live: map[string]string{to.URN(): "SES-NOREG"}}
	b := steering.New(gw, &fakeConsumer{}) // no WithPolling

	env := gomsg.Envelope{ID: "ENV-NOREG", Kind: gomsg.MsgKindNotice, To: to, Payload: []byte(`"steer"`)}
	res := b.Deliver(context.Background(), env)

	assert.Equal(t, steering.OutcomeDelivered, res.Outcome)
	require.Len(t, gw.deliveredTurns(), 1)
}

func TestDeliver_PollingNonSteerable_StillNoop(t *testing.T) {
	// The polling check sits behind the Steerable gate: a non-steerable
	// envelope (wrong kind) is OutcomeNotAddressed regardless of opt-in.
	to := sessionAddr("SES-NS")
	reg := steering.NewPollRegistry(time.Hour)
	reg.MarkPolling(to.URN())
	b := steering.New(&fakeGateway{}, &fakeConsumer{}).WithPolling(reg)

	env := gomsg.Envelope{ID: "ENV-NS", Kind: gomsg.MsgKindEscalation, To: to, Payload: []byte(`"x"`)}
	res := b.Deliver(context.Background(), env)
	assert.Equal(t, steering.OutcomeNotAddressed, res.Outcome)
}

func TestDeliver_RemindersRecordedOnSuccessfulDelivery(t *testing.T) {
	// CW-20260519-0065: the bridge records every successful injection
	// against the recipient's task id so the long-lived runtime can
	// re-surface unaddressed envelopes at the next turn boundary.
	to := sessionAddr("SES-REMIND")
	gw := &fakeGateway{
		live:  map[string]string{to.URN(): "SES-REMIND"},
		tasks: map[string]string{"SES-REMIND": "CW-TASK-1"},
	}
	reg := steering.NewReminderRegistry()
	b := steering.New(gw, &fakeConsumer{}).WithReminder(reg)

	env := gomsg.Envelope{
		ID:      "ENV-REMIND",
		Kind:    gomsg.MsgKindNotice,
		From:    userAddr(),
		To:      to,
		Payload: []byte(`"please look"`),
	}
	res := b.Deliver(context.Background(), env)
	assert.Equal(t, steering.OutcomeDelivered, res.Outcome)

	snap := reg.Snapshot("CW-TASK-1")
	require.Len(t, snap, 1, "successful delivery must be recorded for the bound task")
	assert.Equal(t, "ENV-REMIND", snap[0].EnvelopeID)
	assert.Equal(t, "please look", snap[0].Subject)
}

func TestDeliver_NoReminderRegistry_StillDelivers(t *testing.T) {
	// A bridge with no reminder registry wired must still deliver and
	// consume — the reminder pass is purely additive.
	to := sessionAddr("SES-NORE")
	gw := &fakeGateway{
		live:  map[string]string{to.URN(): "SES-NORE"},
		tasks: map[string]string{"SES-NORE": "CW-TASK-2"},
	}
	cons := &fakeConsumer{}
	b := steering.New(gw, cons) // no WithReminder

	env := gomsg.Envelope{ID: "ENV-NORE", Kind: gomsg.MsgKindNotice, To: to, From: userAddr(), Payload: []byte(`"x"`)}
	res := b.Deliver(context.Background(), env)
	assert.Equal(t, steering.OutcomeDelivered, res.Outcome)
	assert.Equal(t, []string{"ENV-NORE"}, cons.ids())
}

func TestDeliver_RemindersSkippedWhenNoTaskBinding(t *testing.T) {
	// A live session that has no task binding (manual session, test
	// fixture) records nothing — the reminder registry needs a task id
	// to key against; absence is a no-op, not an error.
	to := sessionAddr("SES-NOTASK")
	gw := &fakeGateway{
		live: map[string]string{to.URN(): "SES-NOTASK"},
		// tasks: nil — gateway returns ok=false on TaskIDForSession
	}
	reg := steering.NewReminderRegistry()
	b := steering.New(gw, &fakeConsumer{}).WithReminder(reg)

	env := gomsg.Envelope{ID: "ENV-NOTASK", Kind: gomsg.MsgKindNotice, To: to, From: userAddr(), Payload: []byte(`"x"`)}
	res := b.Deliver(context.Background(), env)
	assert.Equal(t, steering.OutcomeDelivered, res.Outcome)

	// No task id ↔ no record. The Snapshot of the empty taskID is nil.
	assert.Nil(t, reg.Snapshot(""))
}

func TestDeliver_FailedSteer_DoesNotRecordReminder(t *testing.T) {
	// An OutcomeFailed delivery must not pollute the reminder registry —
	// the envelope never landed in the agent's loop, so there is nothing
	// to nag about.
	to := sessionAddr("SES-FAIL")
	gw := &fakeGateway{
		live:     map[string]string{to.URN(): "SES-FAIL"},
		tasks:    map[string]string{"SES-FAIL": "CW-TASK-FAIL"},
		steerErr: errors.New("pipe closed"),
	}
	reg := steering.NewReminderRegistry()
	b := steering.New(gw, &fakeConsumer{}).WithReminder(reg)

	env := gomsg.Envelope{ID: "ENV-FAIL", Kind: gomsg.MsgKindNotice, To: to, From: userAddr(), Payload: []byte(`"x"`)}
	res := b.Deliver(context.Background(), env)
	assert.Equal(t, steering.OutcomeFailed, res.Outcome)
	assert.Nil(t, reg.Snapshot("CW-TASK-FAIL"), "failed delivery must not be recorded")
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
