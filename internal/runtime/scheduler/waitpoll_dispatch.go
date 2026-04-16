package scheduler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/waitpoll"
)

// DispatchWait polls the wait-task's predicate once. If the predicate fires,
// the task transitions per its on_done rule (review/close/notify). If the
// predicate returns an error (typed task-not-found, store failure, etc.)
// the task moves to blocked with the error as BlockedReason. Otherwise the
// task stays in todo and the next scheduler tick re-polls.
//
// Called from Scheduler.Tick for kind=wait tasks — they bypass the executor
// and worker pool entirely. A failure here surfaces as a scheduler-log error
// and leaves the task's state unchanged (next tick retries).
func DispatchWait(
	ctx context.Context,
	store *sqlstore.Store,
	predicates *waitpoll.Registry,
	bus *EventBus,
	task *sqlstore.TaskRecord,
) error {
	if task.Kind != "wait" {
		return fmt.Errorf("DispatchWait called on non-wait task %s (kind=%s)", task.ID, task.Kind)
	}

	predType, params, err := parseWaitMetadata(task)
	if err != nil {
		return store.TransitionTaskWithReason(task.ID, "blocked", err.Error())
	}

	pred, ok := predicates.Get(predType)
	if !ok {
		return store.TransitionTaskWithReason(task.ID, "blocked",
			fmt.Sprintf("unknown wait predicate: %s", predType))
	}

	fired, err := pred.Evaluate(ctx, params)
	if err != nil {
		return store.TransitionTaskWithReason(task.ID, "blocked",
			fmt.Sprintf("wait predicate %s: %v", predType, err))
	}
	if !fired {
		// Still waiting — leave status=todo so the next tick re-polls.
		return nil
	}

	// Predicate fired — apply on_done. Wait tasks don't produce deliverables
	// so the deliverables gate doesn't apply.
	return applyWaitOnDone(store, bus, task)
}

// parseWaitMetadata extracts the predicate_type + params from
// metadata.wait. Returns a typed error if the shape is missing/malformed.
// validateTaskKind already guarantees predicate_type is present for
// kind=wait create; this is a defense-in-depth re-check at dispatch time.
func parseWaitMetadata(task *sqlstore.TaskRecord) (predType string, params map[string]any, err error) {
	if !task.Metadata.Valid || task.Metadata.String == "" {
		return "", nil, fmt.Errorf("wait task missing metadata.wait")
	}
	var md map[string]any
	if err := json.Unmarshal([]byte(task.Metadata.String), &md); err != nil {
		return "", nil, fmt.Errorf("wait task metadata unmarshal: %w", err)
	}
	waitCfg, _ := md["wait"].(map[string]any)
	if waitCfg == nil {
		return "", nil, fmt.Errorf("wait task missing metadata.wait")
	}
	predType, _ = waitCfg["predicate_type"].(string)
	if predType == "" {
		return "", nil, fmt.Errorf("wait task missing metadata.wait.predicate_type")
	}
	params, _ = waitCfg["params"].(map[string]any)
	if params == nil {
		params = map[string]any{}
	}
	return predType, params, nil
}

// applyWaitOnDone is the on_done branch for wait tasks. Mirrors the
// lifecycle manager's handleDone path for review/close/notify but skips
// the deliverables gate (wait tasks don't produce artifacts).
func applyWaitOnDone(store *sqlstore.Store, bus *EventBus, task *sqlstore.TaskRecord) error {
	switch task.OnDone {
	case "close":
		if err := store.TransitionTask(task.ID, "done"); err != nil {
			return err
		}
	case "notify":
		if err := store.TransitionTask(task.ID, "done"); err != nil {
			return err
		}
		if bus != nil {
			bus.Publish(SchedulerEvent{
				Type:   "task.notify",
				TaskID: task.ID,
				Data:   map[string]interface{}{"reason": "wait predicate fired"},
			})
		}
	default: // "review" and any unexpected value
		if err := store.TransitionTask(task.ID, "review"); err != nil {
			return err
		}
	}
	if bus != nil {
		bus.Publish(SchedulerEvent{
			Type:   "wait.fired",
			TaskID: task.ID,
		})
	}
	return nil
}
