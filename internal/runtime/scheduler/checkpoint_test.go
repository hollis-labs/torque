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
