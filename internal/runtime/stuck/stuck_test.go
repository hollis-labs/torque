package stuck_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/broker"
	"github.com/hollis-labs/torque/internal/runtime/stuck"
)

// --- fakes ------------------------------------------------------------

type fakeSender struct {
	mu     sync.Mutex
	calls  []sendCall
	err    error
	delay  time.Duration
	notify chan struct{} // closed once SendTurn has been called at least once
}

type sendCall struct {
	SessionID string
	Text      string
}

func newFakeSender() *fakeSender {
	return &fakeSender{notify: make(chan struct{})}
}

func (f *fakeSender) SendTurn(_ context.Context, sessID, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.calls = append(f.calls, sendCall{SessionID: sessID, Text: text})
	if len(f.calls) == 1 {
		close(f.notify)
	}
	return f.err
}

type fakeSource struct {
	mu       sync.Mutex
	envCh    chan gomsg.Envelope
	subErr   error
	filter   gomsg.Filter
	to       gomsg.Address
	subbedCh chan struct{} // closed once Subscribe has been called
}

func newFakeSource() *fakeSource {
	return &fakeSource{
		envCh:    make(chan gomsg.Envelope, 8),
		subbedCh: make(chan struct{}),
	}
}

func (f *fakeSource) Subscribe(ctx context.Context, to gomsg.Address, filter gomsg.Filter) (<-chan gomsg.Envelope, error) {
	f.mu.Lock()
	f.filter = filter
	f.to = to
	f.mu.Unlock()
	if f.subErr != nil {
		return nil, f.subErr
	}
	close(f.subbedCh)
	// Bridge: relay until ctx is canceled or the upstream channel closes.
	out := make(chan gomsg.Envelope, 8)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case env, ok := <-f.envCh:
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

type dispatchCall struct {
	Env gomsg.Envelope
}

type fakeDispatcher struct {
	mu    sync.Mutex
	calls []dispatchCall
	ret   any
}

func (f *fakeDispatcher) Dispatch(_ context.Context, env gomsg.Envelope) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, dispatchCall{Env: env})
	return f.ret
}

type resumeCall struct {
	SessionID      string
	DiagnosticNote string
}

type fakeResume struct {
	mu        sync.Mutex
	calls     []resumeCall
	newSessID string
	err       error
}

func (f *fakeResume) ResumeSession(_ context.Context, sessID, note string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, resumeCall{SessionID: sessID, DiagnosticNote: note})
	if f.err != nil {
		return "", f.err
	}
	return f.newSessID, nil
}

type checkpointCall struct {
	SessionID string
	Payload   string
	Note      string
}

type fakeCheckpoint struct {
	mu    sync.Mutex
	calls []checkpointCall
	cpID  string
	err   error
}

func (f *fakeCheckpoint) Checkpoint(sessID, payload, note string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, checkpointCall{SessionID: sessID, Payload: payload, Note: note})
	if f.err != nil {
		return "", f.err
	}
	return f.cpID, nil
}

// --- helpers ----------------------------------------------------------

func newProbeInput(opts ...func(*stuck.ProbeInput)) (stuck.ProbeInput, *fakeSender, *fakeSource, *fakeDispatcher, *fakeResume, *fakeCheckpoint) {
	sender := newFakeSender()
	source := newFakeSource()
	disp := &fakeDispatcher{ret: "dispatch-result-marker"}
	resume := &fakeResume{newSessID: "SESS-NEW-1"}
	cp := &fakeCheckpoint{cpID: "SCP-1"}

	in := stuck.ProbeInput{
		SessionID:   "SESS-1",
		TaskID:      "T-1",
		WaitTimeout: 50 * time.Millisecond,
		Sender:      sender,
		Source:      source,
		Dispatch:    disp,
		Resume:      resume,
		Checkpoint:  cp,
	}
	for _, o := range opts {
		o(&in)
	}
	return in, sender, source, disp, resume, cp
}

func statusUpdateEnv(id, taskID, state string) gomsg.Envelope {
	payload, _ := json.Marshal(broker.StatusPayload{State: state, Note: "test"})
	return gomsg.Envelope{
		ID:       id,
		Kind:     gomsg.MsgKindStatusUpdate,
		Payload:  payload,
		Metadata: map[string]string{"task_id": taskID},
	}
}

// --- happy paths ------------------------------------------------------

// Response branch: agent emits a status_update envelope; probe routes it
// through the α.3 dispatcher and returns OutcomeResponded.
func TestProbe_ResponseBranch_RoutesEnvelopeAndReturnsResponded(t *testing.T) {
	in, sender, source, disp, resume, cp := newProbeInput(func(in *stuck.ProbeInput) {
		in.WaitTimeout = 500 * time.Millisecond
	})

	done := make(chan stuck.Result, 1)
	go func() {
		done <- stuck.Probe(context.Background(), in)
	}()

	// Wait for SendInput to fire (PROBE phase complete) — the WAIT loop
	// is now blocked on the envelope channel.
	<-sender.notify
	<-source.subbedCh

	// Deliver a matching status_update envelope.
	source.envCh <- statusUpdateEnv("E-1", "T-1", "blocked")

	var res stuck.Result
	select {
	case res = <-done:
	case <-time.After(time.Second):
		t.Fatal("probe did not return within 1s after envelope delivery")
	}

	assert.Equal(t, stuck.OutcomeResponded, res.Outcome)
	assert.Equal(t, "T-1", res.TaskID)
	assert.Equal(t, "SESS-1", res.OriginalSessionID)
	assert.Equal(t, "E-1", res.EnvelopeID)
	assert.NoError(t, res.Err)
	assert.Equal(t, "dispatch-result-marker", res.Dispatch)
	assert.Empty(t, res.ResumedSessionID)
	assert.Empty(t, res.CheckpointID)

	// Verify primitives — SendTurn fired with the probe message, dispatcher
	// got the envelope, resume + checkpoint did NOT fire.
	require.Len(t, sender.calls, 1)
	assert.Equal(t, "SESS-1", sender.calls[0].SessionID)
	assert.Contains(t, sender.calls[0].Text, "status_update")

	require.Len(t, disp.calls, 1)
	assert.Equal(t, "E-1", disp.calls[0].Env.ID)

	assert.Empty(t, resume.calls)
	assert.Empty(t, cp.calls)

	// Verify Subscribe filter targeted MsgKindStatusUpdate only — keeps the
	// WAIT loop from sifting unrelated kinds.
	source.mu.Lock()
	defer source.mu.Unlock()
	require.Len(t, source.filter.Kind, 1)
	assert.Equal(t, gomsg.MsgKindStatusUpdate, source.filter.Kind[0])
}

// Silence branch: WaitTimeout fires with no envelope; probe checkpoints
// + resumes with the diagnostic-note framing.
func TestProbe_SilenceBranch_CheckpointsAndResumesWithDiagnostic(t *testing.T) {
	in, sender, _, disp, resume, cp := newProbeInput(func(in *stuck.ProbeInput) {
		in.WaitTimeout = 30 * time.Millisecond
	})

	res := stuck.Probe(context.Background(), in)

	assert.Equal(t, stuck.OutcomeResumed, res.Outcome)
	assert.Equal(t, "T-1", res.TaskID)
	assert.Equal(t, "SESS-1", res.OriginalSessionID)
	assert.Equal(t, "SESS-NEW-1", res.ResumedSessionID)
	assert.Equal(t, "SCP-1", res.CheckpointID)
	assert.Empty(t, res.EnvelopeID)
	assert.Nil(t, res.Dispatch)
	assert.NoError(t, res.Err)

	require.Len(t, sender.calls, 1, "PROBE phase should SendTurn once")
	require.Len(t, cp.calls, 1, "silence branch must checkpoint")
	require.Len(t, resume.calls, 1, "silence branch must resume")
	assert.Empty(t, disp.calls, "no envelope arrived; dispatcher must not be called")

	// Checkpoint: payload encodes the silence reason for postmortem.
	assert.Equal(t, "SESS-1", cp.calls[0].SessionID)
	assert.Equal(t, "stuck-probe-silence", cp.calls[0].Note)
	assert.Contains(t, cp.calls[0].Payload, `"reason":"stuck-probe-silence"`)
	assert.Contains(t, cp.calls[0].Payload, `"task_id":"T-1"`)

	// Resume: DiagnosticNote carries the WaitTimeout duration so the
	// resumed agent sees the actual silence window.
	assert.Equal(t, "SESS-1", resume.calls[0].SessionID)
	assert.Contains(t, resume.calls[0].DiagnosticNote, "30ms")
	assert.Contains(t, resume.calls[0].DiagnosticNote, "went silent")
	assert.Contains(t, resume.calls[0].DiagnosticNote, "status_update")
}

// --- filtering / multiplexing ----------------------------------------

// Cross-task envelope traffic during WAIT: probe must ignore status_updates
// for OTHER tasks (different metadata.task_id) and keep waiting.
func TestProbe_IgnoresEnvelopesForOtherTasks(t *testing.T) {
	in, sender, source, disp, _, _ := newProbeInput(func(in *stuck.ProbeInput) {
		in.WaitTimeout = 200 * time.Millisecond
	})

	done := make(chan stuck.Result, 1)
	go func() {
		done <- stuck.Probe(context.Background(), in)
	}()

	<-sender.notify
	<-source.subbedCh

	// Deliver three non-matching envelopes — different task_id values.
	source.envCh <- statusUpdateEnv("E-other-1", "T-other", "working")
	source.envCh <- statusUpdateEnv("E-other-2", "T-other-2", "blocked")
	source.envCh <- statusUpdateEnv("E-other-3", "", "blocked") // empty task_id

	// Then a matching one — probe should pick this up.
	time.Sleep(20 * time.Millisecond)
	source.envCh <- statusUpdateEnv("E-match", "T-1", "blocked")

	var res stuck.Result
	select {
	case res = <-done:
	case <-time.After(time.Second):
		t.Fatal("probe did not return after matching envelope")
	}

	assert.Equal(t, stuck.OutcomeResponded, res.Outcome)
	assert.Equal(t, "E-match", res.EnvelopeID)
	require.Len(t, disp.calls, 1)
	assert.Equal(t, "E-match", disp.calls[0].Env.ID)
}

// Envelope source closes before timeout: probe falls through to resume so
// the stuck session still gets recovered (defensive — refusing to act
// would leave the session stuck).
func TestProbe_EnvelopeChannelClosed_FallsThroughToResume(t *testing.T) {
	in, sender, source, disp, resume, cp := newProbeInput(func(in *stuck.ProbeInput) {
		in.WaitTimeout = 500 * time.Millisecond
	})

	done := make(chan stuck.Result, 1)
	go func() {
		done <- stuck.Probe(context.Background(), in)
	}()

	<-sender.notify
	<-source.subbedCh

	// Close the source's upstream channel — the bridge goroutine in
	// fakeSource will close the downstream channel that the probe is
	// reading.
	close(source.envCh)

	var res stuck.Result
	select {
	case res = <-done:
	case <-time.After(time.Second):
		t.Fatal("probe did not return after source closed")
	}

	assert.Equal(t, stuck.OutcomeResumed, res.Outcome)
	require.Len(t, cp.calls, 1)
	require.Len(t, resume.calls, 1)
	assert.Empty(t, disp.calls)
}

// --- error paths ------------------------------------------------------

func TestProbe_SendTurnError_HaltsBeforeWait(t *testing.T) {
	in, sender, _, disp, resume, cp := newProbeInput()
	sender.err = errors.New("input pipe closed")

	res := stuck.Probe(context.Background(), in)

	assert.Equal(t, stuck.OutcomeFailed, res.Outcome)
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "send_turn")
	assert.Contains(t, res.Err.Error(), "input pipe closed")

	assert.Empty(t, disp.calls)
	assert.Empty(t, resume.calls)
	assert.Empty(t, cp.calls)
}

func TestProbe_SubscribeError_HaltsBeforeProbe(t *testing.T) {
	in, sender, source, disp, resume, cp := newProbeInput()
	source.subErr = errors.New("broker offline")

	res := stuck.Probe(context.Background(), in)

	assert.Equal(t, stuck.OutcomeFailed, res.Outcome)
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "subscribe")

	// Critical: SendTurn must NOT have fired — we couldn't subscribe,
	// so probing the agent would produce a status_update we can't hear.
	assert.Empty(t, sender.calls)
	assert.Empty(t, disp.calls)
	assert.Empty(t, resume.calls)
	assert.Empty(t, cp.calls)
}

func TestProbe_CheckpointError_HaltsBeforeResume(t *testing.T) {
	in, _, _, _, resume, cp := newProbeInput(func(in *stuck.ProbeInput) {
		in.WaitTimeout = 20 * time.Millisecond
	})
	cp.err = errors.New("db locked")

	res := stuck.Probe(context.Background(), in)

	assert.Equal(t, stuck.OutcomeFailed, res.Outcome)
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "checkpoint")
	assert.Contains(t, res.Err.Error(), "db locked")

	// Resume must NOT have fired — we couldn't checkpoint for audit,
	// so resume would lose the linkage. Fail closed.
	assert.Empty(t, resume.calls)
}

func TestProbe_ResumeError_PreservesCheckpointID(t *testing.T) {
	in, _, _, _, resume, cp := newProbeInput(func(in *stuck.ProbeInput) {
		in.WaitTimeout = 20 * time.Millisecond
	})
	resume.err = errors.New("boot failed")

	res := stuck.Probe(context.Background(), in)

	assert.Equal(t, stuck.OutcomeFailed, res.Outcome)
	require.Error(t, res.Err)
	assert.Contains(t, res.Err.Error(), "resume_session")
	assert.Contains(t, res.Err.Error(), "boot failed")

	// Checkpoint fired before resume failed — surfacing its id keeps the
	// audit row queryable even though the resume didn't take.
	require.Len(t, cp.calls, 1)
	assert.Equal(t, "SCP-1", res.CheckpointID)
}

// --- ctx cancellation -------------------------------------------------

func TestProbe_CtxCancelledDuringWait_ReturnsFailed(t *testing.T) {
	in, sender, source, disp, resume, cp := newProbeInput(func(in *stuck.ProbeInput) {
		in.WaitTimeout = 5 * time.Second
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan stuck.Result, 1)
	go func() {
		done <- stuck.Probe(ctx, in)
	}()

	<-sender.notify
	<-source.subbedCh

	cancel()

	var res stuck.Result
	select {
	case res = <-done:
	case <-time.After(time.Second):
		t.Fatal("probe did not return after ctx cancel")
	}

	assert.Equal(t, stuck.OutcomeFailed, res.Outcome)
	require.Error(t, res.Err)
	assert.True(t, errors.Is(res.Err, context.Canceled))

	// Resume / checkpoint / dispatcher must NOT have fired — ctx cancel
	// means the orchestrator wants us out.
	assert.Empty(t, disp.calls)
	assert.Empty(t, resume.calls)
	assert.Empty(t, cp.calls)
}

// --- input validation -------------------------------------------------

func TestProbe_ValidateInput_RequiredFields(t *testing.T) {
	base, _, _, _, _, _ := newProbeInput()

	cases := []struct {
		name   string
		mutate func(*stuck.ProbeInput)
		want   string
	}{
		{"missing SessionID", func(in *stuck.ProbeInput) { in.SessionID = "" }, "SessionID required"},
		{"missing TaskID", func(in *stuck.ProbeInput) { in.TaskID = "" }, "TaskID required"},
		{"missing Sender", func(in *stuck.ProbeInput) { in.Sender = nil }, "Sender required"},
		{"missing Source", func(in *stuck.ProbeInput) { in.Source = nil }, "Source required"},
		{"missing Dispatch", func(in *stuck.ProbeInput) { in.Dispatch = nil }, "Dispatch required"},
		{"missing Resume", func(in *stuck.ProbeInput) { in.Resume = nil }, "Resume required"},
		{"missing Checkpoint", func(in *stuck.ProbeInput) { in.Checkpoint = nil }, "Checkpoint required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := base
			c.mutate(&in)
			res := stuck.Probe(context.Background(), in)
			assert.Equal(t, stuck.OutcomeFailed, res.Outcome)
			require.Error(t, res.Err)
			assert.Contains(t, res.Err.Error(), c.want)
		})
	}
}

// --- defaults ---------------------------------------------------------

func TestProbe_DefaultsApplyWhenEmpty(t *testing.T) {
	in, sender, _, _, resume, _ := newProbeInput(func(in *stuck.ProbeInput) {
		in.WaitTimeout = 0     // → DefaultWaitTimeout
		in.ProbeMessage = ""   // → DefaultProbeMessage
		in.DiagnosticNote = "" // → BuildSilenceNote
	})

	// We can't afford a 90s default timeout in test — override to a tiny
	// value but keep the empty ProbeMessage + DiagnosticNote so the
	// defaults kick in for those.
	in.WaitTimeout = 20 * time.Millisecond

	res := stuck.Probe(context.Background(), in)

	require.Len(t, sender.calls, 1)
	assert.Contains(t, sender.calls[0].Text, stuck.DefaultProbeMessage)

	require.Len(t, resume.calls, 1)
	assert.Contains(t, resume.calls[0].DiagnosticNote, "went silent")

	assert.Equal(t, stuck.OutcomeResumed, res.Outcome)
}

func TestBuildSilenceNote_FormatsDuration(t *testing.T) {
	note := stuck.BuildSilenceNote(45 * time.Second)
	assert.Contains(t, note, "45s")
	assert.Contains(t, note, "went silent")

	// Zero → DefaultWaitTimeout (90s by default).
	zero := stuck.BuildSilenceNote(0)
	assert.Contains(t, zero, stuck.DefaultWaitTimeout.String())
}

// --- DefaultProbeMessage sanity --------------------------------------

func TestDefaultProbeMessage_MentionsStatusUpdate(t *testing.T) {
	// Belt-and-suspenders: if the default ever gets shortened to a form
	// that doesn't tell the agent to emit a status_update envelope, the
	// WAIT phase will never see anything matchable. Pin the contract.
	require.True(t, strings.Contains(stuck.DefaultProbeMessage, "status_update"))
}
