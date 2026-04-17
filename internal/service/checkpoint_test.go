package service_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

// setupServiceWithStore is like setupService but exposes the underlying store
// so tests can simulate scheduler-side transitions (e.g. parking a task on a
// checkpoint via TransitionTaskWithReason) that are normally owned by the
// scheduler package.
func setupServiceWithStore(t *testing.T) (*service.Service, *sqlstore.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return service.New(store), store
}

func createBlockingDecisionTask(t *testing.T, svc *service.Service) string {
	t.Helper()
	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:          "decision task",
		Description:    "x",
		Kind:           "decision",
		CheckpointMode: "blocking",
		Manual:         true,
	})
	require.NoError(t, err)
	// Move to doing so a checkpoint emit has a meaningful parking transition.
	require.NoError(t, svc.Task.Transition(rec.ID, "doing"))
	return rec.ID
}

func TestCheckpointService_Emit(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:            taskID,
		Type:              "collect_data",
		PayloadJSON:       `{"q":"answer?"}`,
		EmitterSourceType: "system",
	})
	require.NoError(t, err)
	require.NotEmpty(t, out.CorrelationID)
	assert.Equal(t, "pending", out.Status)

	cp, err := svc.Checkpoint.Get(out.CorrelationID)
	require.NoError(t, err)
	assert.Equal(t, taskID, cp.TaskID)
	assert.Equal(t, "collect_data", cp.Type)
	assert.Equal(t, "system", cp.EmitterSourceType)
}

func TestCheckpointService_Emit_TaskNotFound(t *testing.T) {
	svc := setupService(t)
	_, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:            "CW-NONE",
		Type:              "collect_data",
		PayloadJSON:       `{}`,
		EmitterSourceType: "system",
	})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "task_id", verr.Field)
}

func TestCheckpointService_Emit_ValidationErrors(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	_, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "task_id", verr.Field)

	_, err = svc.Checkpoint.Emit(service.CheckpointEmitInput{TaskID: taskID})
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "type", verr.Field)

	_, err = svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:            taskID,
		Type:              "x",
		EmitterSourceType: "bogus",
	})
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "emitter_source_type", verr.Field)
}

func TestCheckpointService_Emit_WithTimeout(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	at := time.Now().UTC().Add(1 * time.Hour)
	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:            taskID,
		Type:              "x",
		PayloadJSON:       `{}`,
		EmitterSourceType: "system",
		TimeoutAt:         &at,
	})
	require.NoError(t, err)

	cp, err := svc.Checkpoint.Get(out.CorrelationID)
	require.NoError(t, err)
	assert.True(t, cp.TimeoutAt.Valid)
	assert.WithinDuration(t, at, cp.TimeoutAt.Time, time.Second)
}

// Parity with scheduler.HandleCheckpointSignal: CheckpointService.Emit must
// park a doing+blocking task in review so MCP/HTTP emits get the same
// behaviour as an in-run CLOCKWORK_CHECKPOINT signal. Spec §4.2.
func TestCheckpointService_Emit_ParksDoingBlockingTask(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "collect_data", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	got, err := svc.Task.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, "review", got.Status,
		"doing+blocking emit should park the task in review")
	assert.Contains(t, got.BlockedReason, out.CorrelationID,
		"BlockedReason should name the correlation so respond/cancel can match")
	assert.Contains(t, got.BlockedReason, "awaiting checkpoint",
		"BlockedReason should use the same 'awaiting checkpoint <corr>' wording as the scheduler")
}

// Non-blocking mode: emit records the row but must NOT touch task state.
func TestCheckpointService_Emit_NonBlocking_LeavesTaskRunning(t *testing.T) {
	svc := setupService(t)
	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:          "non-blocking work",
		CheckpointMode: "non_blocking",
		Executor:       "cli",
	})
	require.NoError(t, err)
	require.NoError(t, svc.Task.Transition(rec.ID, "doing"))

	_, err = svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: rec.ID, Type: "progress", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	got, err := svc.Task.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "doing", got.Status, "non_blocking emit should not transition the task")
	assert.Equal(t, "", got.BlockedReason)
}

// A blocking task that hasn't started yet (still in todo) shouldn't be
// parked by an Emit — the caller might be pre-creating checkpoints before
// the task runs. Park only when actively in doing.
func TestCheckpointService_Emit_TodoBlocking_LeavesTaskInTodo(t *testing.T) {
	svc := setupService(t)
	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:          "pre-staged decision",
		Kind:           "decision",
		CheckpointMode: "blocking",
		Manual:         true,
	})
	require.NoError(t, err)

	_, err = svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: rec.ID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	got, err := svc.Task.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "todo", got.Status, "Emit should only park when the task is actively doing")
}

// Second emit on an already-parked (review) task must not rewrite
// BlockedReason — the first correlation stays recorded so respond/cancel
// still match it. Defensive check for idempotency.
func TestCheckpointService_Emit_AlreadyParked_DoesNotRewriteBlockedReason(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	taskID := createBlockingDecisionTask(t, svc)

	first, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	// The task is now in review; emit again (scheduler would normally prevent
	// this, but MCP/HTTP callers can do it).
	_, err = svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	got, err := store.GetTask(taskID)
	require.NoError(t, err)
	assert.Contains(t, got.BlockedReason, first.CorrelationID,
		"first correlation should still be the parked-on id so respond/cancel match it")
}

func TestCheckpointService_Respond(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:            taskID,
		Type:              "x",
		PayloadJSON:       `{}`,
		EmitterSourceType: "system",
	})
	require.NoError(t, err)

	err = svc.Checkpoint.Respond(service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"answer":"yes"}`,
		ResponderSourceType: "user",
		ResponderSourceRef:  "chrispian",
	})
	require.NoError(t, err)

	cp, err := svc.Checkpoint.Get(out.CorrelationID)
	require.NoError(t, err)
	assert.Equal(t, "responded", cp.Status)
	assert.Equal(t, `{"answer":"yes"}`, cp.ResponseJSON.String)
}

func TestCheckpointService_Respond_AlreadyTerminal_Conflict(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)
	require.NoError(t, svc.Checkpoint.Respond(service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{}`,
		ResponderSourceType: "user",
	}))

	err = svc.Checkpoint.Respond(service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{}`,
		ResponderSourceType: "user",
	})
	require.Error(t, err)
	var cerr *service.ConflictError
	require.ErrorAs(t, err, &cerr)
}

// Spec §4.5: a canceled checkpoint should leave the parked task in review
// with BlockedReason = "checkpoint <corr> canceled: <reason>" so humans
// see why the task is still there and can transition it manually.
func TestCheckpointService_Cancel_UpdatesParkedTaskBlockedReason(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	taskID := createBlockingDecisionTask(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "collect_data", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	// Park the task on this correlation (simulating the scheduler).
	require.NoError(t, store.TransitionTaskWithReason(taskID, "review",
		"awaiting checkpoint "+out.CorrelationID))

	require.NoError(t, svc.Checkpoint.Cancel(service.CheckpointCancelInput{
		CorrelationID:      out.CorrelationID,
		Reason:             "no longer relevant",
		CancelerSourceType: "user",
		CancelerSourceRef:  "chrispian",
	}))

	got, err := svc.Task.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, "review", got.Status, "task stays in review per spec §4.5")
	assert.Contains(t, got.BlockedReason, "canceled")
	assert.Contains(t, got.BlockedReason, out.CorrelationID)
	assert.Contains(t, got.BlockedReason, "no longer relevant")
}

func TestCheckpointService_Cancel_DefaultsCancelerSourceType(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	// Omit canceler_source_type — should default to "system" like Emit.
	require.NoError(t, svc.Checkpoint.Cancel(service.CheckpointCancelInput{
		CorrelationID: out.CorrelationID,
		Reason:        "no reason",
	}))

	cp, err := svc.Checkpoint.Get(out.CorrelationID)
	require.NoError(t, err)
	assert.Equal(t, "system", cp.ResponderSourceType.String)
}

func TestCheckpointService_Cancel_InvalidCancelerSourceType_422(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	err = svc.Checkpoint.Cancel(service.CheckpointCancelInput{
		CorrelationID:      out.CorrelationID,
		CancelerSourceType: "bogus",
	})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "canceler_source_type", verr.Field)
}

func TestCheckpointService_Cancel(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	err = svc.Checkpoint.Cancel(service.CheckpointCancelInput{
		CorrelationID:      out.CorrelationID,
		Reason:             "no longer relevant",
		CancelerSourceType: "user",
		CancelerSourceRef:  "chrispian",
	})
	require.NoError(t, err)

	cp, err := svc.Checkpoint.Get(out.CorrelationID)
	require.NoError(t, err)
	assert.Equal(t, "canceled", cp.Status)
	assert.Contains(t, cp.ResponseJSON.String, "no longer relevant")
}

func TestCheckpointService_Cancel_UnknownCorrelation(t *testing.T) {
	svc := setupService(t)
	err := svc.Checkpoint.Cancel(service.CheckpointCancelInput{
		CorrelationID:      "missing",
		CancelerSourceType: "user",
	})
	require.Error(t, err)
	// Can be either NotFound or similar; we just verify it's an error type
	// the caller can distinguish from ValidationError.
	var verr *service.ValidationError
	assert.False(t, errors.As(err, &verr))
}

// When on_checkpoint_response=resume and the task is parked in review on a
// blocking checkpoint, Respond must:
//   - transition the task review → todo (lifecycle resume)
//   - clear BlockedReason
//   - attach the decoded response under metadata.checkpoint_responses[corr]
func TestCheckpointService_Respond_TransitionsTaskToTodo_OnResume(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	taskID := createBlockingDecisionTask(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "collect_data", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	// Simulate the scheduler parking the task on this correlation.
	require.NoError(t, store.TransitionTaskWithReason(taskID, "review",
		"awaiting checkpoint "+out.CorrelationID))

	require.NoError(t, svc.Checkpoint.Respond(service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"answer":"yes"}`,
		ResponderSourceType: "user",
		ResponderSourceRef:  "chrispian",
	}))

	got, err := svc.Task.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, "todo", got.Status, "task should resume to todo after response when on_checkpoint_response=resume")
	assert.Equal(t, "", got.BlockedReason, "BlockedReason should clear on resume")

	// Response should be attached to metadata.checkpoint_responses[<corr>]
	require.True(t, got.Metadata.Valid)
	var md map[string]any
	require.NoError(t, json.Unmarshal([]byte(got.Metadata.String), &md))
	responses := md["checkpoint_responses"].(map[string]any)
	stored := responses[out.CorrelationID].(map[string]any)
	assert.Equal(t, "yes", stored["answer"])
}

// When on_checkpoint_response=review, the task stays in review and the
// response is still attached to metadata — a human will transition manually.
func TestCheckpointService_Respond_StaysInReview_OnReviewMode(t *testing.T) {
	svc, store := setupServiceWithStore(t)

	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:                "decision review-mode",
		Description:          "x",
		Kind:                 "decision",
		CheckpointMode:       "blocking",
		OnCheckpointResponse: "review",
		Manual:               true,
	})
	require.NoError(t, err)
	require.NoError(t, svc.Task.Transition(rec.ID, "doing"))

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: rec.ID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	require.NoError(t, store.TransitionTaskWithReason(rec.ID, "review",
		"awaiting checkpoint "+out.CorrelationID))

	require.NoError(t, svc.Checkpoint.Respond(service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"answer":"maybe"}`,
		ResponderSourceType: "user",
	}))

	got, err := svc.Task.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "review", got.Status, "on_checkpoint_response=review keeps task in review")
	assert.Contains(t, got.BlockedReason, out.CorrelationID, "BlockedReason stays so human sees why it's parked")

	var md map[string]any
	require.NoError(t, json.Unmarshal([]byte(got.Metadata.String), &md))
	resp := md["checkpoint_responses"].(map[string]any)
	assert.Contains(t, resp, out.CorrelationID)
}

// When the task was never parked on this checkpoint, Respond still records
// the answer but does not touch task state.
// A Respond against a checkpoint whose task isn't parked (non_blocking mode
// here — emit does NOT park, so the task stays in doing) should attach the
// response to metadata but not transition task state.
func TestCheckpointService_Respond_UnparkedTask_NoStateChange(t *testing.T) {
	svc := setupService(t)

	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:          "non-blocking emitter",
		CheckpointMode: "non_blocking",
		Executor:       "cli",
	})
	require.NoError(t, err)
	require.NoError(t, svc.Task.Transition(rec.ID, "doing"))

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: rec.ID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	require.NoError(t, svc.Checkpoint.Respond(service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"answer":"n"}`,
		ResponderSourceType: "user",
	}))

	got, err := svc.Task.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "doing", got.Status, "non_blocking task stays in doing through emit + respond")
}

func TestCheckpointService_ListForTask(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	for i := 0; i < 3; i++ {
		_, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
			TaskID: taskID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
		})
		require.NoError(t, err)
	}

	list, err := svc.Checkpoint.ListForTask(taskID)
	require.NoError(t, err)
	assert.Len(t, list, 3)
}
