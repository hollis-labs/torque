package reactor_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/broker"
	"github.com/hollis-labs/torque/internal/runtime/reactor"
)

// --- fakes ------------------------------------------------------------

type fakeCheckpoints struct {
	mu      sync.Mutex
	calls   []checkpointCall
	corrID  string
	emitErr error
}

type checkpointCall struct {
	TaskID  string
	Payload string
}

func (f *fakeCheckpoints) Emit(taskID, payload string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, checkpointCall{TaskID: taskID, Payload: payload})
	return f.corrID, f.emitErr
}

type fakeBlocker struct {
	mu    sync.Mutex
	calls []blockerCall
	err   error
}

type blockerCall struct {
	TaskID string
	Status string
	Reason string
}

func (f *fakeBlocker) TransitionTaskWithReason(taskID, status, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, blockerCall{TaskID: taskID, Status: status, Reason: reason})
	return f.err
}

type fakeSessions struct {
	mu      sync.Mutex
	stopped []string
	err     error
}

func (f *fakeSessions) Stop(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, sessionID)
	return f.err
}

type fakeFanout struct {
	mu   sync.Mutex
	sent []gomsg.Envelope
	err  error
}

func (f *fakeFanout) Send(_ context.Context, env gomsg.Envelope) (gomsg.Envelope, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, env)
	return env, f.err
}

type fakeReassign struct {
	mu    sync.Mutex
	calls []reassignCall
	err   error
}

type reassignCall struct {
	TaskID  string
	Profile string
}

func (f *fakeReassign) ReassignTask(taskID, profile string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, reassignCall{TaskID: taskID, Profile: profile})
	return f.err
}

type fakeNotifier struct {
	mu    sync.Mutex
	calls []notifierCall
}

type notifierCall struct {
	Type   string
	TaskID string
	Data   map[string]any
}

func (f *fakeNotifier) Notify(t, taskID string, data map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, notifierCall{Type: t, TaskID: taskID, Data: data})
}

// --- helpers ----------------------------------------------------------

func addr(kind gomsg.AddressKind, authority, id string) gomsg.Address {
	return gomsg.Address{Kind: kind, Authority: authority, ID: id}
}

func newDeps() (reactor.Deps, *fakeCheckpoints, *fakeBlocker, *fakeSessions, *fakeFanout, *fakeReassign, *fakeNotifier) {
	cp := &fakeCheckpoints{corrID: "CORR-1"}
	bl := &fakeBlocker{}
	ss := &fakeSessions{}
	pf := &fakeFanout{}
	ar := &fakeReassign{}
	nt := &fakeNotifier{}
	return reactor.Deps{
		Checkpoints: cp,
		Blocker:     bl,
		Sessions:    ss,
		Fanout:      pf,
		Reassign:    ar,
		Notifier:    nt,
	}, cp, bl, ss, pf, ar, nt
}

// --- escalation -------------------------------------------------------

func TestDispatch_Escalation_EmitsCheckpoint(t *testing.T) {
	deps, cp, _, _, _, _, _ := newDeps()
	d := reactor.New(deps)

	payload, err := json.Marshal(broker.EscalationPayload{
		Severity: broker.SeverityCritical,
		Reason:   "agent stuck on PR review",
	})
	require.NoError(t, err)

	env := gomsg.Envelope{
		ID:        "E1",
		Kind:      gomsg.MsgKindEscalation,
		From:      addr(gomsg.KindAgent, "test", "alice"),
		To:        addr(gomsg.KindUser, "test", "operator"),
		Payload:   payload,
		Metadata:  map[string]string{"task_id": "T-100"},
		CreatedAt: time.Now(),
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionEscalationCheckpoint, res.Action)
	assert.Equal(t, "T-100", res.TaskID)
	assert.Equal(t, "CORR-1", res.CorrelationID)
	assert.NoError(t, res.Err)

	require.Len(t, cp.calls, 1)
	assert.Equal(t, "T-100", cp.calls[0].TaskID)
	assert.JSONEq(t, string(payload), cp.calls[0].Payload)
}

func TestDispatch_Escalation_MissingTaskID_Noop(t *testing.T) {
	deps, cp, _, _, _, _, _ := newDeps()
	d := reactor.New(deps)

	env := gomsg.Envelope{
		ID:      "E2",
		Kind:    gomsg.MsgKindEscalation,
		From:    addr(gomsg.KindAgent, "test", "alice"),
		To:      addr(gomsg.KindUser, "test", "operator"),
		Payload: json.RawMessage(`{"severity":"warn","reason":"no task"}`),
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionNoop, res.Action)
	assert.Empty(t, cp.calls)
}

func TestDispatch_Escalation_EmitErrorSurfaces(t *testing.T) {
	deps, cp, _, _, _, _, _ := newDeps()
	cp.emitErr = errors.New("db down")
	d := reactor.New(deps)

	env := gomsg.Envelope{
		ID:       "E3",
		Kind:     gomsg.MsgKindEscalation,
		From:     addr(gomsg.KindAgent, "test", "alice"),
		To:       addr(gomsg.KindUser, "test", "operator"),
		Payload:  json.RawMessage(`{"severity":"warn","reason":"x"}`),
		Metadata: map[string]string{"task_id": "T-42"},
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionEscalationCheckpoint, res.Action)
	assert.ErrorContains(t, res.Err, "db down")
}

// --- status_update ----------------------------------------------------

func TestDispatch_StatusUpdate_Blocked_PausesAndBlocks(t *testing.T) {
	deps, _, bl, ss, _, _, nt := newDeps()
	d := reactor.New(deps)

	payload, err := json.Marshal(broker.StatusPayload{
		State: "blocked",
		Note:  "waiting on credentials",
	})
	require.NoError(t, err)

	env := gomsg.Envelope{
		ID:       "S1",
		Kind:     gomsg.MsgKindStatusUpdate,
		From:     addr(gomsg.KindSession, "test", "sess-1"),
		To:       addr(gomsg.KindService, "test", "scheduler"),
		Payload:  payload,
		Metadata: map[string]string{"task_id": "T-200", "session_id": "sess-1"},
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionPauseAndBlock, res.Action)
	assert.Equal(t, "T-200", res.TaskID)
	assert.NoError(t, res.Err)

	require.Len(t, bl.calls, 1)
	assert.Equal(t, "T-200", bl.calls[0].TaskID)
	assert.Equal(t, "blocked", bl.calls[0].Status)
	assert.Equal(t, "waiting on credentials", bl.calls[0].Reason)

	require.Len(t, ss.stopped, 1)
	assert.Equal(t, "sess-1", ss.stopped[0])

	require.Len(t, nt.calls, 1)
	assert.Equal(t, "envelope.paused", nt.calls[0].Type)
	assert.Equal(t, "T-200", nt.calls[0].TaskID)
}

func TestDispatch_StatusUpdate_NonBlocked_Noop(t *testing.T) {
	deps, _, bl, ss, _, _, nt := newDeps()
	d := reactor.New(deps)

	payload, err := json.Marshal(broker.StatusPayload{
		State:    "running",
		Progress: 42,
	})
	require.NoError(t, err)

	env := gomsg.Envelope{
		ID:       "S2",
		Kind:     gomsg.MsgKindStatusUpdate,
		From:     addr(gomsg.KindSession, "test", "sess-2"),
		To:       addr(gomsg.KindService, "test", "scheduler"),
		Payload:  payload,
		Metadata: map[string]string{"task_id": "T-201", "session_id": "sess-2"},
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionNoop, res.Action)
	assert.Empty(t, bl.calls)
	assert.Empty(t, ss.stopped)
	assert.Empty(t, nt.calls)
}

func TestDispatch_StatusUpdate_Blocked_MissingTaskID_Noop(t *testing.T) {
	deps, _, bl, ss, _, _, nt := newDeps()
	d := reactor.New(deps)

	env := gomsg.Envelope{
		ID:      "S3",
		Kind:    gomsg.MsgKindStatusUpdate,
		From:    addr(gomsg.KindSession, "test", "sess-3"),
		To:      addr(gomsg.KindService, "test", "scheduler"),
		Payload: json.RawMessage(`{"state":"blocked","note":"x"}`),
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionNoop, res.Action)
	assert.Empty(t, bl.calls)
	assert.Empty(t, ss.stopped)
	assert.Empty(t, nt.calls)
}

func TestDispatch_StatusUpdate_Blocked_DefaultReasonWhenNoteEmpty(t *testing.T) {
	deps, _, bl, _, _, _, _ := newDeps()
	d := reactor.New(deps)

	env := gomsg.Envelope{
		ID:       "S4",
		Kind:     gomsg.MsgKindStatusUpdate,
		Payload:  json.RawMessage(`{"state":"blocked"}`),
		Metadata: map[string]string{"task_id": "T-204"},
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionPauseAndBlock, res.Action)
	require.Len(t, bl.calls, 1)
	assert.Contains(t, bl.calls[0].Reason, "status_update envelope")
}

// --- request fanout ---------------------------------------------------

func TestDispatch_Request_Fanout(t *testing.T) {
	deps, _, _, _, pf, _, _ := newDeps()
	d := reactor.New(deps)

	env := gomsg.Envelope{
		ID:      "R1",
		Kind:    gomsg.MsgKindRequest,
		From:    addr(gomsg.KindAgent, "test", "alice"),
		To:      addr(gomsg.KindAgent, "test", "bob"),
		Payload: json.RawMessage(`{"q":"need review"}`),
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionFanout, res.Action)
	assert.NoError(t, res.Err)

	require.Len(t, pf.sent, 1)
	assert.Equal(t, gomsg.MsgKindRequest, pf.sent[0].Kind)
	assert.Equal(t, "bob", pf.sent[0].To.ID)
	// The forwarded envelope clears the ID so the store assigns a fresh one.
	assert.Empty(t, pf.sent[0].ID)
}

func TestDispatch_Request_FanoutErrorSurfaces(t *testing.T) {
	deps, _, _, _, pf, _, _ := newDeps()
	pf.err = errors.New("broker offline")
	d := reactor.New(deps)

	env := gomsg.Envelope{
		ID:   "R2",
		Kind: gomsg.MsgKindRequest,
		From: addr(gomsg.KindAgent, "test", "alice"),
		To:   addr(gomsg.KindAgent, "test", "bob"),
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionFanout, res.Action)
	assert.ErrorContains(t, res.Err, "broker offline")
}

// --- handoff reassign -------------------------------------------------

func TestDispatch_Handoff_Reassigns(t *testing.T) {
	deps, _, _, _, _, ar, _ := newDeps()
	d := reactor.New(deps)

	payload, err := json.Marshal(broker.HandoffPayload{
		TaskID: "T-301",
		From:   "alice",
		To:     "senior",
		Note:   "needs senior",
	})
	require.NoError(t, err)

	env := gomsg.Envelope{
		ID:      "H1",
		Kind:    gomsg.MsgKindHandoff,
		Payload: payload,
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionReassign, res.Action)
	assert.Equal(t, "T-301", res.TaskID)
	assert.NoError(t, res.Err)

	require.Len(t, ar.calls, 1)
	assert.Equal(t, "T-301", ar.calls[0].TaskID)
	assert.Equal(t, "senior", ar.calls[0].Profile)
}

func TestDispatch_Handoff_MissingTaskID_Noop(t *testing.T) {
	deps, _, _, _, _, ar, _ := newDeps()
	d := reactor.New(deps)

	env := gomsg.Envelope{
		ID:      "H2",
		Kind:    gomsg.MsgKindHandoff,
		Payload: json.RawMessage(`{"from":"alice","to":"bob"}`),
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionNoop, res.Action)
	assert.Empty(t, ar.calls)
}

func TestDispatch_Handoff_MalformedPayload_Noop(t *testing.T) {
	deps, _, _, _, _, ar, _ := newDeps()
	d := reactor.New(deps)

	env := gomsg.Envelope{
		ID:      "H3",
		Kind:    gomsg.MsgKindHandoff,
		Payload: json.RawMessage(`not json`),
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionNoop, res.Action)
	assert.Empty(t, ar.calls)
}

// --- unknown / unrouted -----------------------------------------------

func TestDispatch_UnknownKind_NoopNoPanic(t *testing.T) {
	deps, cp, bl, ss, pf, ar, nt := newDeps()
	d := reactor.New(deps)

	env := gomsg.Envelope{
		ID:   "N1",
		Kind: gomsg.MsgKindNotice, // V0 does NOT route notice
		From: addr(gomsg.KindAgent, "test", "alice"),
		To:   addr(gomsg.KindAgent, "test", "bob"),
	}

	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionNoop, res.Action)
	assert.NoError(t, res.Err)
	assert.Empty(t, cp.calls)
	assert.Empty(t, bl.calls)
	assert.Empty(t, ss.stopped)
	assert.Empty(t, pf.sent)
	assert.Empty(t, ar.calls)
	assert.Empty(t, nt.calls)
}

func TestDispatch_TotallyUnknownKind_NoopNoPanic(t *testing.T) {
	deps, _, _, _, _, _, _ := newDeps()
	d := reactor.New(deps)

	env := gomsg.Envelope{
		ID:   "N2",
		Kind: gomsg.Kind("totally_made_up"),
	}

	// Must not panic.
	res := d.Dispatch(context.Background(), env)
	assert.Equal(t, reactor.ActionNoop, res.Action)
	assert.NoError(t, res.Err)
}

// --- nil-tolerance ----------------------------------------------------

// Each routed kind degrades gracefully when its surface is nil — important
// because tests routinely build partial Deps.
func TestDispatch_NilSurfaces_DegradeToNoop(t *testing.T) {
	d := reactor.New(reactor.Deps{}) // everything nil

	t.Run("escalation", func(t *testing.T) {
		env := gomsg.Envelope{
			ID:       "x1",
			Kind:     gomsg.MsgKindEscalation,
			Payload:  json.RawMessage(`{"severity":"warn","reason":"x"}`),
			Metadata: map[string]string{"task_id": "T-1"},
		}
		res := d.Dispatch(context.Background(), env)
		assert.Equal(t, reactor.ActionNoop, res.Action)
	})

	t.Run("request", func(t *testing.T) {
		env := gomsg.Envelope{ID: "x2", Kind: gomsg.MsgKindRequest}
		res := d.Dispatch(context.Background(), env)
		assert.Equal(t, reactor.ActionNoop, res.Action)
	})

	t.Run("handoff", func(t *testing.T) {
		payload, _ := json.Marshal(broker.HandoffPayload{TaskID: "T-1", From: "a", To: "b"})
		env := gomsg.Envelope{ID: "x3", Kind: gomsg.MsgKindHandoff, Payload: payload}
		res := d.Dispatch(context.Background(), env)
		assert.Equal(t, reactor.ActionNoop, res.Action)
	})
}
