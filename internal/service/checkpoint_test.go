package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
)

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
