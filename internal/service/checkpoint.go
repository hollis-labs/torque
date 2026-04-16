package service

import (
	"errors"
	"fmt"
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
// source defaults, and persists. Per-kind blocking transition of the task is
// the scheduler's responsibility (B5), not this method's.
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

	if _, err := s.store.GetTask(in.TaskID); err != nil {
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
	return s.store.RespondCheckpoint(
		in.CorrelationID, in.ResponseJSON,
		in.ResponderSourceType, in.ResponderSourceRef,
		time.Now().UTC(),
	)
}

// Cancel flips a pending checkpoint to canceled, recording the canceler as
// the responder. Returns ConflictError if the checkpoint is already
// terminal.
func (s *CheckpointService) Cancel(in CheckpointCancelInput) error {
	if in.CorrelationID == "" {
		return &ValidationError{Field: "correlation_id", Message: "correlation_id required"}
	}
	cp, err := s.store.GetCheckpointByCorrelation(in.CorrelationID)
	if err != nil {
		return err
	}
	if cp.Status != "pending" {
		return &ConflictError{Message: "checkpoint " + cp.Status}
	}
	return s.store.CancelCheckpoint(
		in.CorrelationID, in.Reason,
		in.CancelerSourceType, in.CancelerSourceRef,
		time.Now().UTC(),
	)
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
