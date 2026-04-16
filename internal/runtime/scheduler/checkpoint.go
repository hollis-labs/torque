package scheduler

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// CheckpointEmitOutcome describes what the scheduler decided to do after
// seeing a CLOCKWORK_CHECKPOINT signal. Callers act on ParkTask to stop
// further executor-event processing for the current run.
type CheckpointEmitOutcome struct {
	CorrelationID string
	Type          string
	PayloadJSON   string
	ParkTask      bool
	Reason        string
}

// HandleCheckpointSignal parses the inline CLOCKWORK_CHECKPOINT payload, writes
// a checkpoints row, and — if the task's checkpoint_mode is "blocking" —
// transitions the task doing → review with an "awaiting checkpoint <corr>"
// reason. Returns an outcome whose ParkTask is true for blocking tasks so
// the caller can release the worker slot.
//
// The emitter source is recorded as "system" (the executor running the task);
// this mirrors spec §4.2 which treats CLOCKWORK_* signals as system-emitted.
func HandleCheckpointSignal(
	store *sqlstore.Store,
	taskID string,
	runID int64,
	content string,
	now time.Time,
) (CheckpointEmitOutcome, error) {
	correlationID, typ, payloadJSON, err := executor.ParseCheckpointPayload(content)
	if err != nil {
		return CheckpointEmitOutcome{}, fmt.Errorf("parse checkpoint signal: %w", err)
	}

	task, err := store.GetTask(taskID)
	if err != nil {
		return CheckpointEmitOutcome{}, fmt.Errorf("load task %s: %w", taskID, err)
	}

	cp := &sqlstore.CheckpointRecord{
		TaskID:            taskID,
		CorrelationID:     correlationID,
		Type:              typ,
		PayloadJSON:       payloadJSON,
		EmitterSourceType: "system",
		Status:            "pending",
	}
	if runID > 0 {
		cp.RunID.Int64 = runID
		cp.RunID.Valid = true
	}
	if err := store.CreateCheckpoint(cp); err != nil {
		return CheckpointEmitOutcome{}, fmt.Errorf("create checkpoint: %w", err)
	}

	outcome := CheckpointEmitOutcome{
		CorrelationID: correlationID,
		Type:          typ,
		PayloadJSON:   payloadJSON,
	}

	if task.CheckpointMode == "blocking" {
		reason := fmt.Sprintf("awaiting checkpoint %s", correlationID)
		if err := store.TransitionTaskWithReason(taskID, "review", reason); err != nil {
			return outcome, fmt.Errorf("park task %s on checkpoint: %w", taskID, err)
		}
		outcome.ParkTask = true
		outcome.Reason = reason
	}

	return outcome, nil
}

// SweepCheckpointTimeouts flips pending checkpoints past their timeout_at to
// "timed_out" and transitions any task that is currently parked on one of
// those checkpoints from review → blocked. Returns the number of checkpoints
// flipped.
//
// Task-state coupling: a task is considered "parked on" a checkpoint if its
// status is "review" and its blocked_reason mentions the correlation_id.
// This is the same marker written by HandleCheckpointSignal.
func SweepCheckpointTimeouts(store *sqlstore.Store, bus *EventBus, now time.Time) (int, error) {
	// Snapshot the pending checkpoints that are about to time out so we can
	// still look up their IDs after the UPDATE flips their status.
	pending, err := store.ListPendingCheckpoints()
	if err != nil {
		return 0, err
	}

	flipped, err := store.SweepTimedOutCheckpoints(now)
	if err != nil {
		return 0, err
	}
	if flipped == 0 {
		return 0, nil
	}

	for _, cp := range pending {
		if !cp.TimeoutAt.Valid || cp.TimeoutAt.Time.After(now) {
			continue
		}
		task, err := store.GetTask(cp.TaskID)
		if err != nil {
			if errors.Is(err, sqlstore.ErrTaskNotFound) {
				continue
			}
			return flipped, err
		}
		if task.CheckpointMode != "blocking" {
			continue
		}
		if task.Status == "review" && strings.Contains(task.BlockedReason, cp.CorrelationID) {
			reason := fmt.Sprintf("checkpoint %s timed out", cp.CorrelationID)
			if err := store.TransitionTaskWithReason(cp.TaskID, "blocked", reason); err != nil {
				return flipped, err
			}
			if bus != nil {
				bus.Publish(SchedulerEvent{
					Type:   "checkpoint.timed_out",
					TaskID: cp.TaskID,
					Data: map[string]interface{}{
						"correlation_id": cp.CorrelationID,
					},
				})
			}
		}
	}

	return flipped, nil
}
