package agent

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"

	"github.com/hollis-labs/agentkit/agentsessions"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReconcileInterruptedRuns_BlocksExplicitCrashedRun(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	defer store.Close()
	mgr := NewManager(&Dependencies{Store: store, StateWriter: writeq.NewDirect(store)})

	runID := seedInterruptedRun(t, store, "CW-ORPHAN-1", false)
	seedSession(t, store, "SES-CRASHED", "crashed", "CW-ORPHAN-1", runID)

	count, err := mgr.ReconcileInterruptedRuns(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	run, err := store.GetRun(runID)
	require.NoError(t, err)
	assert.Equal(t, sqlstore.RunStatusKilled, run.Status)
	assert.Contains(t, run.ErrorMessage, "partial work may exist")

	task, err := store.GetTask("CW-ORPHAN-1")
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status)
	assert.Contains(t, task.BlockedReason, "resume or repair manually")
}

func TestReconcileInterruptedRuns_SkipsUnsafeBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		taskID    string
		status    string
		manual    bool
		newerRun  bool
		liveTwin  bool
		legacy    bool
		wantTask  string
		wantRun   string
		wantCount int
	}{
		{name: "manual doing", taskID: "CW-ORPHAN-MANUAL", status: "doing", manual: true, wantTask: "doing", wantRun: sqlstore.RunStatusKilled, wantCount: 1},
		{name: "review decision", taskID: "CW-ORPHAN-REVIEW", status: "review", wantTask: "review", wantRun: sqlstore.RunStatusKilled, wantCount: 1},
		{name: "paused decision", taskID: "CW-ORPHAN-PAUSED", status: "paused", wantTask: "paused", wantRun: sqlstore.RunStatusKilled, wantCount: 1},
		{name: "newer run exists", taskID: "CW-ORPHAN-NEWER", status: "doing", newerRun: true, wantTask: "doing", wantRun: sqlstore.RunStatusKilled, wantCount: 1},
		{name: "live session linked", taskID: "CW-ORPHAN-LIVE", status: "doing", liveTwin: true, wantTask: "doing", wantRun: sqlstore.RunStatusRunning},
		{name: "legacy missing run link", taskID: "CW-ORPHAN-LEGACY", status: "doing", legacy: true, wantTask: "doing", wantRun: sqlstore.RunStatusRunning},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := sqlitetest.OpenStore(t)
			defer store.Close()
			mgr := NewManager(&Dependencies{Store: store, StateWriter: writeq.NewDirect(store)})

			runID := seedTaskRun(t, store, tt.taskID, tt.status, tt.manual)
			if tt.newerRun {
				_, err := store.CreateRun(&sqlstore.RunRecord{TaskID: tt.taskID, Executor: "cli", Status: sqlstore.RunStatusDone})
				require.NoError(t, err)
			}
			if tt.legacy {
				seedSessionMeta(t, store, "SES-CRASHED", "crashed", tt.taskID, `{}`)
			} else {
				seedSession(t, store, "SES-CRASHED", "crashed", tt.taskID, runID)
			}
			if tt.liveTwin {
				seedSession(t, store, "SES-LIVE", "running", tt.taskID, runID)
			}

			count, err := mgr.ReconcileInterruptedRuns(context.Background())
			require.NoError(t, err)
			assert.Equal(t, tt.wantCount, count)

			run, err := store.GetRun(runID)
			require.NoError(t, err)
			assert.Equal(t, tt.wantRun, run.Status)
			task, err := store.GetTask(tt.taskID)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTask, task.Status)
		})
	}
}

func TestManagerSweep_SparesPIDZeroLaunchingSession(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	defer store.Close()
	mgr := NewManager(&Dependencies{Store: store})
	seedSessionMeta(t, store, "SES-PID0", "launching", "CW-PID0", `{}`)

	count, err := mgr.Sweep()
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	sess, err := store.GetSession("SES-PID0")
	require.NoError(t, err)
	assert.Equal(t, "launching", sess.State)
}

func TestManagerTerminalFailureProtectionBlocksDelayedDoneOverwrite(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	defer store.Close()
	mgr := NewManager(&Dependencies{Store: store, StateWriter: writeq.NewDirect(store)})
	seedSessionMeta(t, store, "SES-TERMINAL-FAIL", "running", "CW-TERMINAL-FAIL", `{}`)

	sinkAtBoundary := make(chan struct{})
	releaseSink := make(chan struct{})
	var hookOnce sync.Once
	mgr.terminalSinkBeforeWrite = func() {
		hookOnce.Do(func() {
			close(sinkAtBoundary)
			<-releaseSink
		})
	}
	doneWritten := make(chan error, 1)
	go func() {
		zero := 0
		sink := &storeStateSink{deps: mgr.deps, manager: mgr}
		doneWritten <- sink.UpdateSessionState("SES-TERMINAL-FAIL", agentsessions.StateDone, 0, &zero)
	}()
	<-sinkAtBoundary

	protected := make(chan error, 1)
	go func() {
		protected <- mgr.protectTerminalFailure(context.Background(), "SES-TERMINAL-FAIL")
	}()

	close(releaseSink)

	require.NoError(t, <-protected)
	require.NoError(t, <-doneWritten)

	emitter := &recordingSessionEmitter{}
	eventSink := &busEventSink{events: emitter, manager: mgr}
	eventSink.Emit(context.Background(), agentsessions.LifecycleEvent{
		SessionID: "SES-TERMINAL-FAIL",
		From:      agentsessions.StateLaunching,
		To:        agentsessions.StateRunning,
	})
	zero := 0
	eventSink.Emit(context.Background(), agentsessions.LifecycleEvent{
		SessionID: "SES-TERMINAL-FAIL",
		From:      agentsessions.StateRunning,
		To:        agentsessions.StateDone,
		ExitCode:  &zero,
	})

	sess, err := store.GetSession("SES-TERMINAL-FAIL")
	require.NoError(t, err)
	assert.Equal(t, "failed", sess.State)
	require.True(t, sess.ExitCode.Valid)
	assert.EqualValues(t, -1, sess.ExitCode.Int64)

	events := emitter.snapshot()
	require.Len(t, events, 2)
	assert.Equal(t, "running", events[0]["to"])
	assert.Equal(t, "failed", events[1]["to"])
	assert.Equal(t, -1, events[1]["exit_code"])
}

func TestManagerTerminalCanceledProtectionBlocksDelayedDoneOverwrite(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	defer store.Close()
	mgr := NewManager(&Dependencies{Store: store, StateWriter: writeq.NewDirect(store)})
	seedSessionMeta(t, store, "SES-TERMINAL-CANCEL", "running", "CW-TERMINAL-CANCEL", `{}`)

	sinkAtBoundary := make(chan struct{})
	releaseSink := make(chan struct{})
	var hookOnce sync.Once
	mgr.terminalSinkBeforeWrite = func() {
		hookOnce.Do(func() {
			close(sinkAtBoundary)
			<-releaseSink
		})
	}
	doneWritten := make(chan error, 1)
	go func() {
		zero := 0
		sink := &storeStateSink{deps: mgr.deps, manager: mgr}
		doneWritten <- sink.UpdateSessionState("SES-TERMINAL-CANCEL", agentsessions.StateDone, 0, &zero)
	}()
	<-sinkAtBoundary

	protected := make(chan error, 1)
	go func() {
		protected <- mgr.protectTerminalCanceled(context.Background(), "SES-TERMINAL-CANCEL")
	}()

	close(releaseSink)

	require.NoError(t, <-protected)
	require.NoError(t, <-doneWritten)

	emitter := &recordingSessionEmitter{}
	eventSink := &busEventSink{events: emitter, manager: mgr}
	eventSink.Emit(context.Background(), agentsessions.LifecycleEvent{
		SessionID: "SES-TERMINAL-CANCEL",
		From:      agentsessions.StateLaunching,
		To:        agentsessions.StateRunning,
	})
	zero := 0
	eventSink.Emit(context.Background(), agentsessions.LifecycleEvent{
		SessionID: "SES-TERMINAL-CANCEL",
		From:      agentsessions.StateRunning,
		To:        agentsessions.StateDone,
		ExitCode:  &zero,
	})

	sess, err := store.GetSession("SES-TERMINAL-CANCEL")
	require.NoError(t, err)
	assert.Equal(t, "canceled", sess.State)
	require.True(t, sess.ExitCode.Valid)
	assert.EqualValues(t, -1, sess.ExitCode.Int64)
	assert.True(t, sess.EndedAt.Valid)

	events := emitter.snapshot()
	require.Len(t, events, 2)
	assert.Equal(t, "running", events[0]["to"])
	assert.Equal(t, "canceled", events[1]["to"])
	assert.Equal(t, -1, events[1]["exit_code"])
}

type recordingSessionEmitter struct {
	mu     sync.Mutex
	events []map[string]interface{}
}

func (r *recordingSessionEmitter) EmitSessionEvent(_ string, data map[string]interface{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copyData := make(map[string]interface{}, len(data))
	for k, v := range data {
		copyData[k] = v
	}
	r.events = append(r.events, copyData)
}

func (r *recordingSessionEmitter) snapshot() []map[string]interface{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]interface{}, len(r.events))
	copy(out, r.events)
	return out
}

func seedInterruptedRun(t *testing.T, store *sqlstore.Store, taskID string, manual bool) int64 {
	t.Helper()
	return seedTaskRun(t, store, taskID, "doing", manual)
}

func seedTaskRun(t *testing.T, store *sqlstore.Store, taskID, taskStatus string, manual bool) int64 {
	t.Helper()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: taskID, Title: taskID, Status: taskStatus, Manual: manual,
		Executor: "cli", AgentProfile: "codex", OnFail: "retry", MaxRetries: 3,
	}))
	runID, err := store.CreateRun(&sqlstore.RunRecord{TaskID: taskID, Executor: "cli", Status: sqlstore.RunStatusRunning})
	require.NoError(t, err)
	return runID
}

func seedSession(t *testing.T, store *sqlstore.Store, id, state, taskID string, runID int64) {
	t.Helper()
	seedSessionMeta(t, store, id, state, taskID, fmt.Sprintf(`{"%s":"%d"}`, metaKeyRunID, runID))
}

func seedSessionMeta(t *testing.T, store *sqlstore.Store, id, state, taskID, meta string) {
	t.Helper()
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{
		ID: id, AgentProfile: "codex", Provider: "codex", RuntimeID: "torque-cli/codex",
		RuntimeKind: "jsonrpc-stdio", Workdir: t.TempDir(), TaskID: sql.NullString{String: taskID, Valid: taskID != ""},
		State: state, MetaJSON: meta,
	}))
}
