package service

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// CheckpointService owns the emit/respond/cancel/list flows for checkpoints.
// Scheduler and lifecycle wiring (parking on blocking emit, resume on respond)
// live in their own packages and consume CheckpointService.
type CheckpointService struct {
	store *sqlstore.Store
}

// CheckpointEmitInput is the service-level input for recording a new
// checkpoint. RunID is optional (nullable on the row); TimeoutAt is optional.
type CheckpointEmitInput struct {
	TaskID            string
	RunID             *int64
	Type              string
	PayloadJSON       string
	EmitterSourceType string
	EmitterSourceRef  string
	TimeoutAt         *time.Time
}

// CheckpointEmitOutput returns the generated correlation_id + row id so the
// caller (scheduler, MCP tool) can reference the checkpoint.
type CheckpointEmitOutput struct {
	CorrelationID string
	ID            int64
	Status        string
}

// CheckpointRespondInput records a response + responder identity for a
// pending checkpoint.
type CheckpointRespondInput struct {
	CorrelationID       string
	ResponseJSON        string
	ResponderSourceType string
	ResponderSourceRef  string
}

// CheckpointCancelInput cancels a pending checkpoint with an attached reason.
// Reuses the responder columns to record the canceler.
type CheckpointCancelInput struct {
	CorrelationID      string
	Reason             string
	CancelerSourceType string
	CancelerSourceRef  string
}

// Emit creates a pending checkpoint. Generates a ULID correlation_id, applies
// source defaults, persists the row, and — when the task is actively running
// (status=doing) and its checkpoint_mode is "blocking" — parks it in review
// with BlockedReason="awaiting checkpoint <corr>" so MCP/HTTP emits behave
// the same as an in-run CLOCKWORK_CHECKPOINT signal handled by the scheduler
// (spec §4.2).
//
// Non-blocking tasks, already-parked tasks, and tasks that haven't started
// (todo / any terminal state) are left alone — Emit records the checkpoint
// row but doesn't manufacture a transition. Idempotency: if the task is
// already in review on a prior correlation, BlockedReason is preserved so
// respond/cancel still match the original correlation_id.
func (s *CheckpointService) Emit(in CheckpointEmitInput) (*CheckpointEmitOutput, error) {
	if in.TaskID == "" {
		return nil, &ValidationError{Field: "task_id", Message: "task_id required"}
	}
	if in.Type == "" {
		return nil, &ValidationError{Field: "type", Message: "type required"}
	}
	if in.EmitterSourceType == "" {
		in.EmitterSourceType = "system"
	}
	if !validSourceTypes[in.EmitterSourceType] {
		return nil, &ValidationError{Field: "emitter_source_type", Message: "invalid emitter_source_type"}
	}

	task, err := s.store.GetTask(in.TaskID)
	if err != nil {
		if errors.Is(err, sqlstore.ErrTaskNotFound) {
			return nil, &ValidationError{Field: "task_id", Message: "task not found"}
		}
		return nil, err
	}

	corr := ulid.Make().String()

	cp := &sqlstore.CheckpointRecord{
		TaskID:            in.TaskID,
		CorrelationID:     corr,
		Type:              in.Type,
		PayloadJSON:       in.PayloadJSON,
		EmitterSourceType: in.EmitterSourceType,
		Status:            "pending",
	}
	if in.RunID != nil {
		cp.RunID.Int64 = *in.RunID
		cp.RunID.Valid = true
	}
	if in.EmitterSourceRef != "" {
		cp.EmitterSourceRef.String = in.EmitterSourceRef
		cp.EmitterSourceRef.Valid = true
	}
	if in.TimeoutAt != nil {
		cp.TimeoutAt.Time = *in.TimeoutAt
		cp.TimeoutAt.Valid = true
	}
	if err := s.store.CreateCheckpoint(cp); err != nil {
		return nil, fmt.Errorf("create checkpoint: %w", err)
	}

	// Park the task only when it's actively running and configured for
	// blocking checkpoints. Anything else is the caller's responsibility.
	if task.Status == "doing" && task.CheckpointMode == "blocking" {
		reason := "awaiting checkpoint " + corr
		if err := s.store.TransitionTaskWithReason(in.TaskID, "review", reason); err != nil {
			return nil, fmt.Errorf("park task on checkpoint: %w", err)
		}
	}

	return &CheckpointEmitOutput{CorrelationID: corr, ID: cp.ID, Status: cp.Status}, nil
}

// Respond writes a response to a pending checkpoint. Returns ConflictError
// if the checkpoint is already terminal (responded/canceled/timed_out).
// Downstream task resume (on_checkpoint_response) is the lifecycle
// manager's responsibility (B7).
func (s *CheckpointService) Respond(in CheckpointRespondInput) error {
	if in.CorrelationID == "" {
		return &ValidationError{Field: "correlation_id", Message: "correlation_id required"}
	}
	if in.ResponderSourceType == "" {
		return &ValidationError{Field: "responder_source_type", Message: "responder_source_type required"}
	}
	if !validSourceTypes[in.ResponderSourceType] {
		return &ValidationError{Field: "responder_source_type", Message: "invalid responder_source_type"}
	}
	cp, err := s.store.GetCheckpointByCorrelation(in.CorrelationID)
	if err != nil {
		return err
	}
	if cp.Status != "pending" {
		return &ConflictError{Message: "checkpoint " + cp.Status}
	}
	if err := s.store.RespondCheckpoint(
		in.CorrelationID, in.ResponseJSON,
		in.ResponderSourceType, in.ResponderSourceRef,
		time.Now().UTC(),
	); err != nil {
		return err
	}
	return s.applyOnCheckpointResponse(cp, in.ResponseJSON)
}

// applyOnCheckpointResponse enforces the task's on_checkpoint_response rule
// after a successful Respond. If the task is parked on this correlation_id
// (status=review AND blocked_reason mentions it):
//   - resume: transition review → todo, clear BlockedReason, attach response
//   - review: stay in review, keep BlockedReason, attach response
//   - custom: plugin hook hand-off (MVP: attach response + stay)
//
// If the task isn't parked on this correlation (e.g. non_blocking mode, or a
// responder racing the executor), only the metadata is attached.
func (s *CheckpointService) applyOnCheckpointResponse(cp *sqlstore.CheckpointRecord, responseJSON string) error {
	task, err := s.store.GetTask(cp.TaskID)
	if err != nil {
		return err
	}
	if err := s.attachCheckpointResponseToMetadata(task, cp.CorrelationID, responseJSON); err != nil {
		return err
	}
	parked := task.Status == "review" && strings.Contains(task.BlockedReason, cp.CorrelationID)
	if !parked {
		return nil
	}
	switch task.OnCheckpointResponse {
	case "resume":
		return s.store.TransitionTaskWithReason(cp.TaskID, "todo", "")
	case "review", "custom":
		// Stay parked. Human (or plugin hook) resolves.
		return nil
	}
	return nil
}

// attachCheckpointResponseToMetadata writes the response into
// metadata.checkpoint_responses[correlation_id]. If the response string is
// valid JSON it's stored structurally; otherwise the raw string is stored so
// downstream tooling still sees it.
func (s *CheckpointService) attachCheckpointResponseToMetadata(
	task *sqlstore.TaskRecord, correlationID, responseJSON string,
) error {
	md := map[string]any{}
	if task.Metadata.Valid && task.Metadata.String != "" {
		_ = unmarshalJSON([]byte(task.Metadata.String), &md)
	}
	responses, _ := md["checkpoint_responses"].(map[string]any)
	if responses == nil {
		responses = map[string]any{}
	}
	var parsed any
	if err := unmarshalJSON([]byte(responseJSON), &parsed); err != nil {
		parsed = responseJSON
	}
	responses[correlationID] = parsed
	md["checkpoint_responses"] = responses

	newMD := marshalJSON(md)
	return s.store.UpdateTask(task.ID, sqlstore.TaskUpdate{
		Metadata: &sql.NullString{String: newMD, Valid: true},
	})
}

// Cancel flips a pending checkpoint to canceled, recording the canceler as
// the responder. Returns ConflictError if the checkpoint is already
// terminal.
//
// CancelerSourceType defaults to "system" when empty (matching Emit's
// behaviour) and is validated against validSourceTypes so provenance is
// consistent across emit/respond/cancel.
//
// Per spec §4.5, if the task is parked on this correlation (status=review
// with BlockedReason mentioning the correlation_id), Cancel also updates
// the task's BlockedReason to "checkpoint <corr> canceled: <reason>" so
// the human driving the follow-up transition sees why it was canceled.
func (s *CheckpointService) Cancel(in CheckpointCancelInput) error {
	if in.CorrelationID == "" {
		return &ValidationError{Field: "correlation_id", Message: "correlation_id required"}
	}
	if in.CancelerSourceType == "" {
		in.CancelerSourceType = "system"
	}
	if !validSourceTypes[in.CancelerSourceType] {
		return &ValidationError{
			Field:   "canceler_source_type",
			Message: "invalid canceler_source_type",
		}
	}
	cp, err := s.store.GetCheckpointByCorrelation(in.CorrelationID)
	if err != nil {
		return err
	}
	if cp.Status != "pending" {
		return &ConflictError{Message: "checkpoint " + cp.Status}
	}
	if err := s.store.CancelCheckpoint(
		in.CorrelationID, in.Reason,
		in.CancelerSourceType, in.CancelerSourceRef,
		time.Now().UTC(),
	); err != nil {
		return err
	}
	return s.applyCancelToParkedTask(cp, in.Reason)
}

// applyCancelToParkedTask updates the parked task's BlockedReason to reflect
// the cancellation per spec §4.5. If the task is no longer parked on this
// correlation (already moved on, or non_blocking mode), this is a noop.
func (s *CheckpointService) applyCancelToParkedTask(cp *sqlstore.CheckpointRecord, reason string) error {
	task, err := s.store.GetTask(cp.TaskID)
	if err != nil {
		return err
	}
	parked := task.Status == "review" && strings.Contains(task.BlockedReason, cp.CorrelationID)
	if !parked {
		return nil
	}
	blockedReason := "checkpoint " + cp.CorrelationID + " canceled"
	if reason != "" {
		blockedReason += ": " + reason
	}
	return s.store.TransitionTaskWithReason(cp.TaskID, "review", blockedReason)
}

// Get returns a checkpoint by correlation_id.
func (s *CheckpointService) Get(correlationID string) (*sqlstore.CheckpointRecord, error) {
	return s.store.GetCheckpointByCorrelation(correlationID)
}

// ListForTask returns all checkpoints emitted against a task.
func (s *CheckpointService) ListForTask(taskID string) ([]sqlstore.CheckpointRecord, error) {
	return s.store.ListCheckpointsForTask(taskID)
}

// ListPending returns every pending checkpoint across all tasks, oldest
// first. Used by the scheduler's timeout sweeper and by the MCP
// "pending" tool.
func (s *CheckpointService) ListPending() ([]sqlstore.CheckpointRecord, error) {
	return s.store.ListPendingCheckpoints()
}

// SweepTimedOut moves pending checkpoints past their timeout_at to
// "timed_out" status and returns the number affected.
func (s *CheckpointService) SweepTimedOut(now time.Time) (int, error) {
	return s.store.SweepTimedOutCheckpoints(now)
}
