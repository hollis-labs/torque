package scheduler_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/service"
)

// TORQUE_CHECKPOINT signal-protocol tests retired with Phase E
// (CW-20260427-0043) — agents now emit checkpoints via the
// torque_task_checkpoint_emit MCP tool, which calls
// service.CheckpointService.Emit. Coverage of the parking/no-parking/malformed
// branches lives in internal/service/checkpoint_test.go.
//
// What stays here: SweepCheckpointTimeouts tests, which exercise the
// scheduler-package timeout sweeper that's independent of the emission path.

func setupCheckpointStack(t *testing.T) (*sqlstore.Store, *service.Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store, service.New(store)
}

func createDecisionTaskDoing(t *testing.T, svc *service.Service) string {
	t.Helper()
	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:                "decision",
		Description:          "x",
		Kind:                 "decision",
		CheckpointMode:       "blocking",
		OnCheckpointResponse: "resume",
		Manual:               true,
	})
	require.NoError(t, err)
	require.NoError(t, svc.Task.Transition(context.Background(), rec.ID, "doing"))
	return rec.ID
}

func createAgentTaskDoing(t *testing.T, store *sqlstore.Store, id, checkpointMode string) {
	t.Helper()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:                   id,
		Title:                "agent",
		Status:               "doing",
		Kind:                 "agent",
		Executor:             "cli",
		SourceType:           "user",
		Trust:                "normal",
		CheckpointMode:       checkpointMode,
		OnCheckpointResponse: "resume",
		OnDone:               "review",
		OnFail:               "retry",
		OnReview:             "pause",
		OnDoneMerge:          "none",
	}))
}

func TestSweepCheckpointTimeouts_TransitionsParkedTask(t *testing.T) {
	store, svc := setupCheckpointStack(t)
	taskID := createDecisionTaskDoing(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:      taskID,
		Type:        "collect_data",
		PayloadJSON: `{}`,
	})
	require.NoError(t, err)
	corr := out.CorrelationID
	require.NoError(t, store.SetCheckpointTimeout(corr, time.Now().UTC().Add(-1*time.Minute)))

	bus := scheduler.NewEventBus()
	defer bus.Close()

	n, err := scheduler.SweepCheckpointTimeouts(store, bus, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	cp, err := store.GetCheckpointByCorrelation(corr)
	require.NoError(t, err)
	assert.Equal(t, "timed_out", cp.Status)

	task, err := store.GetTask(taskID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status)
	assert.Contains(t, task.BlockedReason, "timed out")
}

func TestSweepCheckpointTimeouts_IgnoresFutureDeadlines(t *testing.T) {
	store, svc := setupCheckpointStack(t)
	taskID := createDecisionTaskDoing(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:      taskID,
		Type:        "x",
		PayloadJSON: `{}`,
	})
	require.NoError(t, err)
	require.NoError(t, store.SetCheckpointTimeout(out.CorrelationID, time.Now().UTC().Add(1*time.Hour)))

	n, err := scheduler.SweepCheckpointTimeouts(store, nil, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	task, err := store.GetTask(taskID)
	require.NoError(t, err)
	assert.Equal(t, "review", task.Status, "task should stay parked when deadline is in the future")
}

func TestSweepCheckpointTimeouts_IgnoresUnparkedTask(t *testing.T) {
	store, svc := setupCheckpointStack(t)
	createAgentTaskDoing(t, store, "CW-TO-3", "non_blocking")

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:      "CW-TO-3",
		Type:        "x",
		PayloadJSON: `{}`,
	})
	require.NoError(t, err)
	require.NoError(t, store.SetCheckpointTimeout(out.CorrelationID, time.Now().UTC().Add(-1*time.Minute)))

	n, err := scheduler.SweepCheckpointTimeouts(store, nil, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "checkpoint itself times out")

	task, err := store.GetTask("CW-TO-3")
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status, "non-blocking tasks should not be flipped to blocked")
}

// escalationComments returns the [system/checkpoint] comments on a task.
func escalationComments(t *testing.T, store *sqlstore.Store, taskID string) []sqlstore.CommentRecord {
	t.Helper()
	all, err := store.ListCommentsForEntity("task", taskID)
	require.NoError(t, err)
	var out []sqlstore.CommentRecord
	for _, c := range all {
		if c.Author == "[system/checkpoint]" {
			out = append(out, c)
		}
	}
	return out
}

// CW-20260520-0007: a pending checkpoint that outlives its type's TTL is
// escalated exactly once (a comment on the task, a bus event, escalated_at),
// and sweeps after that do not repeat it.
func TestEscalateStaleCheckpoints_OnceAfterTypeTTL(t *testing.T) {
	store, svc := setupCheckpointStack(t)
	taskID := createDecisionTaskDoing(t, svc)
	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{TaskID: taskID, Type: "approval", PayloadJSON: `{"title":"ship it?"}`})
	require.NoError(t, err)
	cp, err := store.GetCheckpointByCorrelation(out.CorrelationID)
	require.NoError(t, err)

	bus := scheduler.NewEventBus()
	defer bus.Close()
	events := bus.Subscribe()

	n, err := scheduler.EscalateStaleCheckpoints(store, bus, cp.EmittedAt.Add(23*time.Hour))
	require.NoError(t, err)
	assert.Zero(t, n, "an approval is not escalated before its 24h TTL")

	at := cp.EmittedAt.Add(25 * time.Hour)
	n, err = scheduler.EscalateStaleCheckpoints(store, bus, at)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	got, err := store.GetCheckpointByCorrelation(out.CorrelationID)
	require.NoError(t, err)
	assert.Equal(t, "pending", got.Status, "escalation does not resolve the checkpoint")
	require.True(t, got.EscalatedAt.Valid)

	comments := escalationComments(t, store, taskID)
	require.Len(t, comments, 1)
	assert.Contains(t, comments[0].Content, out.CorrelationID)
	assert.Contains(t, comments[0].Content, "approval")

	select {
	case ev := <-events:
		assert.Equal(t, "checkpoint.escalated", ev.Type)
		assert.Equal(t, taskID, ev.TaskID)
		data, ok := ev.Data.(map[string]interface{})
		require.True(t, ok, "event data is %T", ev.Data)
		assert.Equal(t, out.CorrelationID, data["correlation_id"])
	case <-time.After(time.Second):
		t.Fatal("no checkpoint.escalated event")
	}

	for _, later := range []time.Duration{26 * time.Hour, 100 * time.Hour} {
		n, err = scheduler.EscalateStaleCheckpoints(store, bus, cp.EmittedAt.Add(later))
		require.NoError(t, err)
		assert.Zero(t, n, "an escalated checkpoint is not escalated again")
	}
	assert.Len(t, escalationComments(t, store, taskID), 1)
}

// Per-type defaults and the payload's escalation object.
func TestEscalateStaleCheckpoints_TTLPolicy(t *testing.T) {
	cases := []struct {
		name, typ, payload string
		notBefore, by      time.Duration // not escalated at notBefore, escalated at by
	}{
		{"message waits 72h", "message", `{"subject":"fyi"}`, 71 * time.Hour, 73 * time.Hour},
		{"unknown type uses 24h", "collect_data", `{}`, 23 * time.Hour, 25 * time.Hour},
		{"payload after_seconds overrides", "approval", `{"escalation":{"after_seconds":600}}`, 9 * time.Minute, 11 * time.Minute},
		{"non-object payload keeps the default", "approval", `"not an object"`, 23 * time.Hour, 25 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, svc := setupCheckpointStack(t)
			taskID := createDecisionTaskDoing(t, svc)
			out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{TaskID: taskID, Type: tc.typ, PayloadJSON: tc.payload})
			require.NoError(t, err)
			cp, err := store.GetCheckpointByCorrelation(out.CorrelationID)
			require.NoError(t, err)

			n, err := scheduler.EscalateStaleCheckpoints(store, nil, cp.EmittedAt.Add(tc.notBefore))
			require.NoError(t, err)
			assert.Zero(t, n)
			n, err = scheduler.EscalateStaleCheckpoints(store, nil, cp.EmittedAt.Add(tc.by))
			require.NoError(t, err)
			assert.Equal(t, 1, n)
		})
	}

	t.Run("payload disabled never escalates", func(t *testing.T) {
		store, svc := setupCheckpointStack(t)
		taskID := createDecisionTaskDoing(t, svc)
		out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{TaskID: taskID, Type: "approval", PayloadJSON: `{"escalation":{"disabled":true}}`})
		require.NoError(t, err)
		cp, err := store.GetCheckpointByCorrelation(out.CorrelationID)
		require.NoError(t, err)
		n, err := scheduler.EscalateStaleCheckpoints(store, nil, cp.EmittedAt.Add(1000*time.Hour))
		require.NoError(t, err)
		assert.Zero(t, n)
		assert.Empty(t, escalationComments(t, store, taskID))
	})
}

// A checkpoint that has been answered, or is already past its timeout_at
// (SweepCheckpointTimeouts' job), is not escalated.
func TestEscalateStaleCheckpoints_SkipsAnsweredAndTimedOut(t *testing.T) {
	store, svc := setupCheckpointStack(t)

	answeredTask := createDecisionTaskDoing(t, svc)
	answered, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{TaskID: answeredTask, Type: "approval", PayloadJSON: `{}`})
	require.NoError(t, err)
	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID: answered.CorrelationID, ResponseJSON: `{"approved":true}`, ResponderSourceType: "user",
	}))

	timedTask := createDecisionTaskDoing(t, svc)
	timed, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{TaskID: timedTask, Type: "approval", PayloadJSON: `{}`})
	require.NoError(t, err)
	cp, err := store.GetCheckpointByCorrelation(timed.CorrelationID)
	require.NoError(t, err)
	require.NoError(t, store.SetCheckpointTimeout(timed.CorrelationID, cp.EmittedAt.Add(10*time.Hour)))

	n, err := scheduler.EscalateStaleCheckpoints(store, nil, cp.EmittedAt.Add(30*time.Hour))
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Empty(t, escalationComments(t, store, answeredTask))
	assert.Empty(t, escalationComments(t, store, timedTask))
}

// MarkCheckpointEscalated is the exactly-once gate: only the first caller
// wins, and an answered checkpoint is never marked.
func TestMarkCheckpointEscalated_FirstCallerWins(t *testing.T) {
	store, svc := setupCheckpointStack(t)
	taskID := createDecisionTaskDoing(t, svc)
	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{TaskID: taskID, Type: "approval", PayloadJSON: `{}`})
	require.NoError(t, err)
	cp, err := store.GetCheckpointByCorrelation(out.CorrelationID)
	require.NoError(t, err)

	won, err := store.MarkCheckpointEscalated(cp.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.True(t, won)
	won, err = store.MarkCheckpointEscalated(cp.ID, time.Now().UTC())
	require.NoError(t, err)
	assert.False(t, won, "a second mark loses")
}
