package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
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

// checkpointEscalationTTL is how long a checkpoint of each type may stay
// pending before it is escalated; other types use
// defaultCheckpointEscalationTTL. A `message` is informational, so it waits
// longer than a gate that holds work (CW-20260520-0007).
var checkpointEscalationTTL = map[string]time.Duration{
	"approval":  24 * time.Hour,
	"pr_review": 24 * time.Hour,
	"decision":  24 * time.Hour,
	"question":  24 * time.Hour,
	"message":   72 * time.Hour,
}

const defaultCheckpointEscalationTTL = 24 * time.Hour

// checkpointEscalation is a checkpoint's escalation policy: its type's TTL,
// overridden by an optional `escalation` object in its payload:
//
//	{"escalation": {"after_seconds": 3600}}  escalate after an hour
//	{"escalation": {"disabled": true}}       never escalate
type checkpointEscalation struct {
	After    time.Duration
	Disabled bool
}

func checkpointEscalationFor(cp sqlstore.CheckpointRecord) checkpointEscalation {
	pol := checkpointEscalation{After: defaultCheckpointEscalationTTL}
	if ttl, ok := checkpointEscalationTTL[cp.Type]; ok {
		pol.After = ttl
	}
	var payload struct {
		Escalation *struct {
			AfterSeconds int64 `json:"after_seconds"`
			Disabled     bool  `json:"disabled"`
		} `json:"escalation"`
	}
	// A payload that is not an object, or has no escalation key, keeps the
	// type's defaults.
	if json.Unmarshal([]byte(cp.PayloadJSON), &payload) != nil || payload.Escalation == nil {
		return pol
	}
	pol.Disabled = payload.Escalation.Disabled
	if payload.Escalation.AfterSeconds > 0 {
		pol.After = time.Duration(payload.Escalation.AfterSeconds) * time.Second
	}
	return pol
}

// EscalateStaleCheckpoints escalates every pending checkpoint that has gone
// unanswered past its escalation TTL, once (CW-20260520-0007). Flipping a
// task to blocked at timeout_at is passive: nothing tells anyone a gate is
// going stale. An escalation is active and durable:
//
//   - a `[system/checkpoint]` comment on the task, saying how long the
//     checkpoint has waited and when it times out;
//   - a checkpoint.escalated event on the bus, which SSE subscribers see;
//   - a log line.
//
// Exactly once: store.MarkCheckpointEscalated sets escalated_at with a
// conditional UPDATE, and only the sweep that wins it notifies, so later
// sweeps, a restarted daemon or a concurrent sweeper do not repeat it. A
// checkpoint already past its timeout_at is left to SweepCheckpointTimeouts.
// Returns the number of checkpoints escalated.
func EscalateStaleCheckpoints(store *sqlstore.Store, bus *EventBus, now time.Time) (int, error) {
	pending, err := store.ListPendingCheckpoints()
	if err != nil {
		return 0, err
	}
	escalated := 0
	for _, cp := range pending {
		if cp.EscalatedAt.Valid {
			continue
		}
		pol := checkpointEscalationFor(cp)
		if pol.Disabled || now.Before(cp.EmittedAt.Add(pol.After)) {
			continue
		}
		if cp.TimeoutAt.Valid && !cp.TimeoutAt.Time.After(now) {
			continue
		}
		won, err := store.MarkCheckpointEscalated(cp.ID, now)
		if err != nil {
			return escalated, err
		}
		if !won {
			continue
		}
		escalated++

		waited := now.Sub(cp.EmittedAt).Round(time.Minute)
		timeout := "it has no timeout, so it waits until someone responds or cancels it"
		if cp.TimeoutAt.Valid {
			timeout = fmt.Sprintf("it times out at %s", cp.TimeoutAt.Time.UTC().Format(time.RFC3339))
		}
		content := fmt.Sprintf("Escalation: checkpoint %s (%s) has waited %s for a response, past its %s escalation threshold; %s. Respond with torque_task_checkpoint_respond or cancel it.",
			cp.CorrelationID, cp.Type, waited, pol.After, timeout)
		if err := store.AddComment(&sqlstore.CommentRecord{
			EntityType: "task",
			EntityID:   cp.TaskID,
			Author:     "[system/checkpoint]",
			Content:    content,
		}); err != nil {
			log.Printf("[checkpoint] escalation comment for %s on %s failed: %v", cp.CorrelationID, cp.TaskID, err)
		}
		log.Printf("[checkpoint] escalated %s (%s) on %s after %s", cp.CorrelationID, cp.Type, cp.TaskID, waited)
		if bus != nil {
			bus.Publish(SchedulerEvent{
				Type:   "checkpoint.escalated",
				TaskID: cp.TaskID,
				Data: map[string]interface{}{
					"correlation_id": cp.CorrelationID,
					"type":           cp.Type,
					"emitted_at":     cp.EmittedAt,
					"waited_seconds": int64(now.Sub(cp.EmittedAt).Seconds()),
				},
			})
		}
	}
	return escalated, nil
}
