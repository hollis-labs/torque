package scheduler_test

import (
	"database/sql"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
)

func setupCheckpointStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func createDecisionTaskDoing(t *testing.T, store *sqlstore.Store, id string) {
	t.Helper()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:                   id,
		Title:                "decision",
		Status:               "doing",
		Kind:                 "decision",
		SourceType:           "user",
		Trust:                "normal",
		CheckpointMode:       "blocking",
		OnCheckpointResponse: "resume",
		OnDone:               "review",
		OnFail:               "retry",
		OnReview:             "pause",
		OnDoneMerge:          "none",
		Manual:               true,
	}))
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

func encodeCheckpointContent(correlationID, typ, payload string) string {
	return correlationID + " " + typ + " " + base64.StdEncoding.EncodeToString([]byte(payload))
}

func createRunForTask(t *testing.T, store *sqlstore.Store, taskID string) int64 {
	t.Helper()
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID:   taskID,
		Executor: "cli",
		Status:   "running",
	})
	require.NoError(t, err)
	return runID
}

func TestHandleCheckpointSignal_Blocking_ParksTask(t *testing.T) {
	store := setupCheckpointStore(t)
	createDecisionTaskDoing(t, store, "CW-CHK-1")
	runID := createRunForTask(t, store, "CW-CHK-1")

	content := encodeCheckpointContent("CORR-1", "collect_data", `{"q":"hide"}`)
	out, err := scheduler.HandleCheckpointSignal(store, "CW-CHK-1", runID, content, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, "CORR-1", out.CorrelationID)
	assert.True(t, out.ParkTask)
	assert.Contains(t, out.Reason, "CORR-1")

	task, err := store.GetTask("CW-CHK-1")
	require.NoError(t, err)
	assert.Equal(t, "review", task.Status)
	assert.Contains(t, task.BlockedReason, "CORR-1")

	cp, err := store.GetCheckpointByCorrelation("CORR-1")
	require.NoError(t, err)
	assert.Equal(t, "pending", cp.Status)
	assert.Equal(t, `{"q":"hide"}`, cp.PayloadJSON)
	assert.Equal(t, "system", cp.EmitterSourceType)
	assert.True(t, cp.RunID.Valid)
	assert.Equal(t, runID, cp.RunID.Int64)
}

func TestHandleCheckpointSignal_NonBlocking_DoesNotPark(t *testing.T) {
	store := setupCheckpointStore(t)
	createAgentTaskDoing(t, store, "CW-CHK-2", "non_blocking")
	runID := createRunForTask(t, store, "CW-CHK-2")

	content := encodeCheckpointContent("CORR-2", "status_update", `{"progress":0.5}`)
	out, err := scheduler.HandleCheckpointSignal(store, "CW-CHK-2", runID, content, time.Now().UTC())
	require.NoError(t, err)
	assert.False(t, out.ParkTask)

	task, err := store.GetTask("CW-CHK-2")
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status, "non-blocking checkpoint should not transition")

	cp, err := store.GetCheckpointByCorrelation("CORR-2")
	require.NoError(t, err)
	assert.Equal(t, "pending", cp.Status)
}

func TestHandleCheckpointSignal_MalformedPayload(t *testing.T) {
	store := setupCheckpointStore(t)
	createDecisionTaskDoing(t, store, "CW-CHK-3")

	_, err := scheduler.HandleCheckpointSignal(store, "CW-CHK-3", 0, "too few parts", time.Now().UTC())
	require.Error(t, err)
}

func TestSweepCheckpointTimeouts_TransitionsParkedTask(t *testing.T) {
	store := setupCheckpointStore(t)
	createDecisionTaskDoing(t, store, "CW-TO-1")

	// Emit and park
	content := encodeCheckpointContent("CORR-TO", "collect_data", `{}`)
	_, err := scheduler.HandleCheckpointSignal(store, "CW-TO-1", 0, content, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.SetCheckpointTimeout("CORR-TO", time.Now().UTC().Add(-1*time.Minute)))

	bus := scheduler.NewEventBus()
	defer bus.Close()

	n, err := scheduler.SweepCheckpointTimeouts(store, bus, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	cp, err := store.GetCheckpointByCorrelation("CORR-TO")
	require.NoError(t, err)
	assert.Equal(t, "timed_out", cp.Status)

	task, err := store.GetTask("CW-TO-1")
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status)
	assert.Contains(t, task.BlockedReason, "timed out")
}

func TestSweepCheckpointTimeouts_IgnoresFutureDeadlines(t *testing.T) {
	store := setupCheckpointStore(t)
	createDecisionTaskDoing(t, store, "CW-TO-2")

	content := encodeCheckpointContent("CORR-F", "x", `{}`)
	_, err := scheduler.HandleCheckpointSignal(store, "CW-TO-2", 0, content, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.SetCheckpointTimeout("CORR-F", time.Now().UTC().Add(1*time.Hour)))

	n, err := scheduler.SweepCheckpointTimeouts(store, nil, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	task, err := store.GetTask("CW-TO-2")
	require.NoError(t, err)
	assert.Equal(t, "review", task.Status, "task should stay parked when deadline is in the future")
}

func TestSweepCheckpointTimeouts_IgnoresUnparkedTask(t *testing.T) {
	store := setupCheckpointStore(t)
	createAgentTaskDoing(t, store, "CW-TO-3", "non_blocking")

	content := encodeCheckpointContent("CORR-NB", "x", `{}`)
	_, err := scheduler.HandleCheckpointSignal(store, "CW-TO-3", 0, content, time.Now().UTC())
	require.NoError(t, err)
	require.NoError(t, store.SetCheckpointTimeout("CORR-NB", time.Now().UTC().Add(-1*time.Minute)))

	n, err := scheduler.SweepCheckpointTimeouts(store, nil, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, n, "checkpoint itself times out")

	task, err := store.GetTask("CW-TO-3")
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status, "non-blocking tasks should not be flipped to blocked")
}
