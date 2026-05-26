package service_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/hitl"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
)

// fakeResponseDispatcher records every DispatchResponse call so tests can
// assert the (taskID, correlationID, responseJSON) trio routed through
// CheckpointService.Respond's α.4 dispatch decision. err is the result the
// fake returns on every call — nil for the success path, a sentinel
// (service.ErrNoLiveSessionForTask) for the fallback path.
type fakeResponseDispatcher struct {
	mu    sync.Mutex
	calls []service.CheckpointResponseDispatch
	err   error
}

func (f *fakeResponseDispatcher) DispatchResponse(ctx context.Context, in service.CheckpointResponseDispatch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	return f.err
}

func (f *fakeResponseDispatcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeResponseDispatcher) lastCall() service.CheckpointResponseDispatch {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return service.CheckpointResponseDispatch{}
	}
	return f.calls[len(f.calls)-1]
}

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
// behaviour as an in-run TORQUE_CHECKPOINT signal. Spec §4.2.
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

	err = svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
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

func TestCheckpointService_PRReviewContractRoundTrip(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	payloadJSON, err := json.Marshal(hitl.PRReviewPayload{
		PRURL:   "https://github.com/acme/app/pull/42",
		Title:   "Fix checkout",
		Summary: "Ready for review.",
	})
	require.NoError(t, err)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:            taskID,
		Type:              hitl.TypePRReview,
		PayloadJSON:       string(payloadJSON),
		EmitterSourceType: "system",
	})
	require.NoError(t, err)

	responseJSON, err := json.Marshal(hitl.PRReviewResponse{
		Decision: "approve",
		Summary:  "Looks good.",
	})
	require.NoError(t, err)
	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        string(responseJSON),
		ResponderSourceType: "user",
	}))

	cp, err := svc.Checkpoint.Get(out.CorrelationID)
	require.NoError(t, err)
	assert.Equal(t, hitl.TypePRReview, cp.Type)

	var gotPayload hitl.PRReviewPayload
	require.NoError(t, json.Unmarshal([]byte(cp.PayloadJSON), &gotPayload))
	assert.Equal(t, "https://github.com/acme/app/pull/42", gotPayload.PRURL)

	var gotResponse hitl.PRReviewResponse
	require.NoError(t, json.Unmarshal([]byte(cp.ResponseJSON.String), &gotResponse))
	assert.Equal(t, "approve", gotResponse.Decision)
}

func TestCheckpointService_Respond_AlreadyTerminal_Conflict(t *testing.T) {
	svc := setupService(t)
	taskID := createBlockingDecisionTask(t, svc)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)
	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{}`,
		ResponderSourceType: "user",
	}))

	err = svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{}`,
		ResponderSourceType: "user",
	})
	require.Error(t, err)
	var cerr *service.ConflictError
	require.ErrorAs(t, err, &cerr)
}

func TestCheckpointService_Respond_RequiredWorkflowRejectsDisallowedResponder(t *testing.T) {
	svc := setupService(t)
	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:                "restart approval",
		Description:          "Needs human permission before restart.",
		Kind:                 "decision",
		CheckpointMode:       "blocking",
		OnCheckpointResponse: "resume",
		Manual:               true,
		Metadata: map[string]any{
			"hitl": map[string]any{
				"required_workflow": map[string]any{
					"workflow_type":    hitl.TypeApproval,
					"enforcement_mode": hitl.EnforcementRequired,
					"requirements": map[string]any{
						"response_required":              true,
						"allowed_responder_source_types": []string{"user"},
						"min_responders":                 1,
					},
					"reason": "permission before restart",
				},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, svc.Task.Transition(rec.ID, "doing"))
	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID:            rec.ID,
		Type:              hitl.TypeApproval,
		PayloadJSON:       `{"title":"Restart","prompt":"Approve restart?"}`,
		EmitterSourceType: "agent",
	})
	require.NoError(t, err)

	err = svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"decision":"approved"}`,
		ResponderSourceType: "agent",
	})
	require.Error(t, err)
	var verr *service.ValidationError
	require.ErrorAs(t, err, &verr)
	assert.Equal(t, "required_workflow", verr.Field)

	cp, err := svc.Checkpoint.Get(out.CorrelationID)
	require.NoError(t, err)
	assert.Equal(t, "pending", cp.Status, "rejected required-workflow response must not close the checkpoint")
	got, err := svc.Task.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "review", got.Status, "task stays parked until a satisfying response arrives")

	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"decision":"approved"}`,
		ResponderSourceType: "user",
	}))
	got, err = svc.Task.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "todo", got.Status, "allowed responder satisfies the required workflow and resumes")
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

	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
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

	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
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

	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
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

// Sprint α.4 (CW-20260512-0062): when a CheckpointResponseDispatcher is
// wired and the parked task's on_checkpoint_response is "resume", Respond
// hands off to the dispatcher (which the bootstrap wires to
// agent.Manager.ResumeSession + SendInput) and transitions the task
// review → doing on success. This supersedes the pre-α.4 review → todo
// + scheduler-fresh-boot path (D3 reframe of CW-20260510-0122).
func TestCheckpointService_Respond_DispatchesResume_OnResumeMode(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	taskID := createBlockingDecisionTask(t, svc)

	dispatcher := &fakeResponseDispatcher{}
	svc.Checkpoint.WithResponseDispatcher(dispatcher)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "collect_data", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	// Park the task on this correlation (mirrors what the scheduler /
	// emit path does when a doing+blocking task gets parked).
	require.NoError(t, store.TransitionTaskWithReason(taskID, "review",
		"awaiting checkpoint "+out.CorrelationID))

	respJSON := `{"answer":"ship-it"}`
	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        respJSON,
		ResponderSourceType: "user",
		ResponderSourceRef:  "chrispian",
	}))

	// Dispatcher was called with the exact triple Respond produced.
	require.Equal(t, 1, dispatcher.callCount(), "dispatcher must be called exactly once on resume-mode respond")
	got := dispatcher.lastCall()
	assert.Equal(t, taskID, got.TaskID)
	assert.Equal(t, out.CorrelationID, got.CorrelationID)
	assert.Equal(t, respJSON, got.ResponseJSON,
		"operator response_json flows through unmodified — it becomes the user-turn input via send_input")

	// Task transitioned to doing (dispatcher took ownership; the resumed
	// session is the new live worker). This is the α.4-specific behavior
	// — pre-α.4 transitioned to todo and waited for the scheduler tick.
	task, err := svc.Task.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status,
		"dispatcher-owned resume transitions review → doing (mirrors scheduler dispatch); not legacy → todo")
	assert.Equal(t, "", task.BlockedReason, "BlockedReason clears on dispatch-owned resume")

	// Response is still attached to metadata.checkpoint_responses[corr]
	// regardless of which dispatch path won — this is the durable
	// record the prompt.go checkpointRedispatchPrompt directs agents to
	// read on first turn after dispatch.
	require.True(t, task.Metadata.Valid)
	var md map[string]any
	require.NoError(t, json.Unmarshal([]byte(task.Metadata.String), &md))
	responses := md["checkpoint_responses"].(map[string]any)
	stored := responses[out.CorrelationID].(map[string]any)
	assert.Equal(t, "ship-it", stored["answer"])
}

// When the dispatcher returns ErrNoLiveSessionForTask (no resumable session
// row bound to the task — e.g. one-shot executor task, or a session that
// was never registered with the long-lived manager), Respond falls back
// to the legacy review → todo path so the scheduler fresh-boots on the
// next tick. The contract is documented on CheckpointResponseDispatcher
// and is the bridge that keeps fresh-boot adapters (gemini/copilot/opencode)
// shipping today while the resume-capable adapters (claude/codex) take
// the α.4 path.
func TestCheckpointService_Respond_FallsBackToTodo_OnDispatcherNoLiveSession(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	taskID := createBlockingDecisionTask(t, svc)

	dispatcher := &fakeResponseDispatcher{err: service.ErrNoLiveSessionForTask}
	svc.Checkpoint.WithResponseDispatcher(dispatcher)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "collect_data", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)
	require.NoError(t, store.TransitionTaskWithReason(taskID, "review",
		"awaiting checkpoint "+out.CorrelationID))

	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"answer":"y"}`,
		ResponderSourceType: "user",
	}))

	assert.Equal(t, 1, dispatcher.callCount(), "dispatcher is still consulted; the fallback is its decision")

	task, err := svc.Task.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status,
		"ErrNoLiveSessionForTask falls through to the legacy fresh-boot path: task → todo for scheduler pickup")
	assert.Equal(t, "", task.BlockedReason)
}

// Any non-sentinel dispatcher error is also treated as a fallback. The
// service must never strand a task in review when the dispatcher trips
// — observability of the dispatcher's failure is its own responsibility
// (it logs + breadcrumbs); the service's job is to keep the task moving.
func TestCheckpointService_Respond_FallsBackToTodo_OnDispatcherError(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	taskID := createBlockingDecisionTask(t, svc)

	dispatcher := &fakeResponseDispatcher{err: errors.New("resume blew up")}
	svc.Checkpoint.WithResponseDispatcher(dispatcher)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "collect_data", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)
	require.NoError(t, store.TransitionTaskWithReason(taskID, "review",
		"awaiting checkpoint "+out.CorrelationID))

	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"answer":"y"}`,
		ResponderSourceType: "user",
	}))

	task, err := svc.Task.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status,
		"a transient dispatcher failure must not strand the task in review")
}

// review-mode parking is unaffected by the dispatcher wiring — the
// dispatcher is consulted ONLY when on_checkpoint_response="resume". A
// "review" task stays in review on response (a human drives the next
// transition) regardless of whether a dispatcher is wired.
func TestCheckpointService_Respond_ReviewMode_SkipsDispatcher(t *testing.T) {
	svc, store := setupServiceWithStore(t)

	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:                "review-mode w/ dispatcher",
		Kind:                 "decision",
		CheckpointMode:       "blocking",
		OnCheckpointResponse: "review",
		Manual:               true,
	})
	require.NoError(t, err)
	require.NoError(t, svc.Task.Transition(rec.ID, "doing"))

	dispatcher := &fakeResponseDispatcher{}
	svc.Checkpoint.WithResponseDispatcher(dispatcher)

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: rec.ID, Type: "x", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)
	require.NoError(t, store.TransitionTaskWithReason(rec.ID, "review",
		"awaiting checkpoint "+out.CorrelationID))

	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"answer":"maybe"}`,
		ResponderSourceType: "user",
	}))

	assert.Equal(t, 0, dispatcher.callCount(),
		"review-mode respond never dispatches — the dispatcher hook is keyed on on_checkpoint_response=resume")

	task, err := svc.Task.Get(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "review", task.Status, "review mode keeps the task parked through respond")
}

// Nil dispatcher (the default — legacy callers, test harnesses pre-α.4)
// preserves the original review → todo behavior so the scheduler's
// fresh-boot path keeps working unchanged. This is the back-compat seam
// that lets the α.4 wiring land without breaking any in-tree test that
// constructs CheckpointService without a dispatcher.
func TestCheckpointService_Respond_NilDispatcher_LegacyPath(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	taskID := createBlockingDecisionTask(t, svc)

	// Explicitly DO NOT wire a dispatcher.

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "collect_data", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)
	require.NoError(t, store.TransitionTaskWithReason(taskID, "review",
		"awaiting checkpoint "+out.CorrelationID))

	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"answer":"y"}`,
		ResponderSourceType: "user",
	}))

	task, err := svc.Task.Get(taskID)
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status,
		"nil-dispatcher path matches pre-α.4: review → todo, scheduler does the fresh-boot")
}

// --- Orchestrator redispatch (CW-20260518) --------------------------------

// fakeOrchestratorRedispatcher records every RedispatchForCheckpointResponse
// call so tests can assert the checkpoint→orchestrator-redispatch wiring.
type fakeOrchestratorRedispatcher struct {
	mu    sync.Mutex
	calls []service.OrchestratorRedispatch
	err   error
}

func (f *fakeOrchestratorRedispatcher) RedispatchForCheckpointResponse(
	_ context.Context, in service.OrchestratorRedispatch,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	return f.err
}

func (f *fakeOrchestratorRedispatcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeOrchestratorRedispatcher) lastCall() service.OrchestratorRedispatch {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return service.OrchestratorRedispatch{}
	}
	return f.calls[len(f.calls)-1]
}

// When an OrchestratorRedispatcher is wired, Respond must invoke it for a
// responded checkpoint even though the checkpoint's task is NOT parked on
// the correlation. This is the live-bug scenario: a pr_review checkpoint
// emitted on a CHILD task already in `review` is never re-parked by Emit
// (ParkTaskOnCheckpoint only fires on status=doing), so the parked-gated
// resume path is skipped — but the Orchestrator on the parent plan still
// must be woken. The redispatch hook is therefore unconditional.
func TestCheckpointService_Respond_InvokesOrchestratorRedispatch_WhenNotParked(t *testing.T) {
	svc := setupService(t)
	redispatcher := &fakeOrchestratorRedispatcher{}
	svc.Checkpoint.WithOrchestratorRedispatcher(redispatcher)

	// A child task in `review` — NOT parked on any checkpoint correlation.
	rec, err := svc.Task.Create(service.TaskCreateInput{
		Title:          "child task at review",
		Description:    "reviewer end-agent emitted a pr_review checkpoint here",
		Kind:           "agent",
		CheckpointMode: "none",
		Manual:         true,
	})
	require.NoError(t, err)
	require.NoError(t, svc.Task.Transition(rec.ID, "doing"))
	require.NoError(t, svc.Task.Transition(rec.ID, "review"))

	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: rec.ID, Type: "pr_review", PayloadJSON: `{"pr":"#42"}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"decision":"approve"}`,
		ResponderSourceType: "user",
		ResponderSourceRef:  "chrispian",
	}))

	require.Equal(t, 1, redispatcher.callCount(),
		"orchestrator redispatch must fire even when the checkpoint task is not parked")
	got := redispatcher.lastCall()
	assert.Equal(t, rec.ID, got.TaskID)
	assert.Equal(t, out.CorrelationID, got.CorrelationID)
	assert.JSONEq(t, `{"decision":"approve"}`, got.ResponseJSON)
}

// A redispatcher error must NOT fail the operator's Respond call — the
// checkpoint is already responded and the response is already attached to
// task metadata; a redispatch failure is logged, not surfaced.
func TestCheckpointService_Respond_RedispatchError_DoesNotFailRespond(t *testing.T) {
	svc := setupService(t)
	redispatcher := &fakeOrchestratorRedispatcher{err: errors.New("boom")}
	svc.Checkpoint.WithOrchestratorRedispatcher(redispatcher)

	taskID := createBlockingDecisionTask(t, svc)
	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "collect_data", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)

	// Respond succeeds despite the redispatcher returning an error.
	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"answer":"y"}`,
		ResponderSourceType: "user",
	}))
	assert.Equal(t, 1, redispatcher.callCount())

	cp, err := svc.Checkpoint.Get(out.CorrelationID)
	require.NoError(t, err)
	assert.Equal(t, "responded", cp.Status, "checkpoint is responded even when redispatch fails")
}

// With no OrchestratorRedispatcher wired (legacy/test composition roots),
// Respond behaves exactly as before — no redispatch step, no error.
func TestCheckpointService_Respond_NoRedispatcher_NoOp(t *testing.T) {
	svc := setupService(t)
	// Explicitly DO NOT wire an orchestrator redispatcher.
	taskID := createBlockingDecisionTask(t, svc)
	out, err := svc.Checkpoint.Emit(service.CheckpointEmitInput{
		TaskID: taskID, Type: "collect_data", PayloadJSON: `{}`, EmitterSourceType: "system",
	})
	require.NoError(t, err)
	require.NoError(t, svc.Checkpoint.Respond(context.Background(), service.CheckpointRespondInput{
		CorrelationID:       out.CorrelationID,
		ResponseJSON:        `{"answer":"y"}`,
		ResponderSourceType: "user",
	}))
}
