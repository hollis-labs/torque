package scheduler

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
)

// SweepCheckpointTimeouts flips pending checkpoints past their timeout_at to
// "timed_out" and transitions any task that is currently parked on one of
// those checkpoints from review → blocked. Returns the number of checkpoints
// flipped.
//
// Task-state coupling: a task is considered "parked on" a checkpoint if its
// status is "review" and its blocked_reason mentions the correlation_id.
// This is the marker written by service.CheckpointService.Emit when it parks
// a doing+blocking task.
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
