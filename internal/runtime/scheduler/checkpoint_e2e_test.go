package scheduler_test

import (
	"context"
	"database/sql"
	"encoding/json"
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

// setupE2EStack builds the full service + store stack that scheduler handlers
// + checkpoint service collaborate on. Returns store and service so tests can
// simulate scheduler-side writes and service-side responds without needing
// the full dispatch loop.
func setupE2EStack(t *testing.T) (*sqlstore.Store, *service.Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store, service.New(store)
}

// TestE2E_Checkpoint_Emit_RespondService_TaskResumes exercises the full
// pipeline end-to-end (Phase E shape — TORQUE_CHECKPOINT signal protocol
// retired; emission now goes through service.Checkpoint.Emit, which is the
// same path the MCP tool torque_task_checkpoint_emit invokes):
//  1. A decision task with checkpoint_mode=blocking exists and is "doing".
//  2. The agent emits via the service-layer Emit (was: inline TORQUE_CHECKPOINT).
//     The service persists a pending checkpoint and parks the task in review.
//  3. A responder (mirroring MCP/HTTP) calls CheckpointService.Respond with
//     a JSON answer.
//  4. The task transitions review → todo, BlockedReason clears, and the
//     response is attached under metadata.checkpoint_responses[corr].
//  5. The checkpoint row becomes "responded" with the responder identity.
func TestE2E_Checkpoint_Emit_RespondService_TaskResumes(t *testing.T) {
	store, svc := setupE2EStack(t)

	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:          "e2e decision",
		Description:    "x",
		Kind:           "decision",
		CheckpointMode: "blocking",
		Manual:         true,
	})
	require.NoError(t, err)
	require.NoError(t, svc.Task.Transition(context.Background(), rec.ID, "doing"))

	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID:   rec.ID,
		Executor: "cli",
		Status:   "running",
	})
	require.NoError(t, err)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:      rec.ID,
		RunID:       &runID,
		Type:        "collect_data",
		PayloadJSON: `{"q":"pick one"}`,
	})
	require.NoError(t, err)
	corr := out.CorrelationID

	parked, err := svc.Task.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "review", parked.Status)
	assert.Contains(t, parked.BlockedReason, corr)

	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       corr,
		ResponseJSON:        `{"pick":"a"}`,
		ResponderSourceType: "user",
		ResponderSourceRef:  "chrispian",
	}))

	resumed, err := svc.Task.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "todo", resumed.Status)
	assert.Equal(t, "", resumed.BlockedReason)

	require.True(t, resumed.Metadata.Valid)
	var md map[string]any
	require.NoError(t, json.Unmarshal([]byte(resumed.Metadata.String), &md))
	responses := md["checkpoint_responses"].(map[string]any)
	stored := responses[corr].(map[string]any)
	assert.Equal(t, "a", stored["pick"])

	cp, err := svc.Checkpoint.Get(corr)
	require.NoError(t, err)
	assert.Equal(t, "responded", cp.Status)
	assert.Equal(t, "user", cp.ResponderSourceType.String)
	assert.Equal(t, "chrispian", cp.ResponderSourceRef.String)
}

// TestE2E_Checkpoint_TimeoutSweep_ParkedTaskBlocks exercises the timeout path:
// emit → park → sweeper with past deadline → checkpoint timed_out + task
// transitions review → blocked.
func TestE2E_Checkpoint_TimeoutSweep_ParkedTaskBlocks(t *testing.T) {
	store, svc := setupE2EStack(t)

	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:          "e2e timeout",
		Description:    "x",
		Kind:           "decision",
		CheckpointMode: "blocking",
		Manual:         true,
	})
	require.NoError(t, err)
	require.NoError(t, svc.Task.Transition(context.Background(), rec.ID, "doing"))

	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: rec.ID, Executor: "cli", Status: "running",
	})
	require.NoError(t, err)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:      rec.ID,
		RunID:       &runID,
		Type:        "collect_data",
		PayloadJSON: `{}`,
	})
	require.NoError(t, err)
	corr := out.CorrelationID

	// Set a deadline that's already past.
	require.NoError(t, store.SetCheckpointTimeout(corr, time.Now().UTC().Add(-1*time.Minute)))

	bus := scheduler.NewEventBus()
	defer bus.Close()
	n, err := scheduler.SweepCheckpointTimeouts(store, bus, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	cp, err := svc.Checkpoint.Get(corr)
	require.NoError(t, err)
	assert.Equal(t, "timed_out", cp.Status)

	task, err := svc.Task.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status)
	assert.Contains(t, task.BlockedReason, "timed out")
}

// TestE2E_Checkpoint_Cancel_TaskStaysInReview exercises the cancel path: emit
// → park → cancel via service → checkpoint canceled, task stays in review
// with a canceled BlockedReason (human-driven follow-up per spec §4.5).
func TestE2E_Checkpoint_Cancel_TaskStaysInReview(t *testing.T) {
	store, svc := setupE2EStack(t)

	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:          "e2e cancel",
		Description:    "x",
		Kind:           "decision",
		CheckpointMode: "blocking",
		Manual:         true,
	})
	require.NoError(t, err)
	require.NoError(t, svc.Task.Transition(context.Background(), rec.ID, "doing"))

	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: rec.ID, Executor: "cli", Status: "running",
	})
	require.NoError(t, err)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:      rec.ID,
		RunID:       &runID,
		Type:        "collect_data",
		PayloadJSON: `{}`,
	})
	require.NoError(t, err)
	corr := out.CorrelationID

	require.NoError(t, svc.Checkpoint.Cancel(service.CheckpointCancelInput{
		CorrelationID:      corr,
		Reason:             "no longer relevant",
		CancelerSourceType: "user",
		CancelerSourceRef:  "chrispian",
	}))

	cp, err := svc.Checkpoint.Get(corr)
	require.NoError(t, err)
	assert.Equal(t, "canceled", cp.Status)
	assert.Contains(t, cp.ResponseJSON.String, "no longer relevant")

	task, err := svc.Task.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "review", task.Status, "canceled checkpoint leaves task in review (human drives next step)")
	assert.Contains(t, task.BlockedReason, corr)
}
