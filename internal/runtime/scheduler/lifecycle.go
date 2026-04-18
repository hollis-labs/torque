package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
)

// LifecycleManager applies OnDone/OnFail/OnReview rules, checks deliverables,
// and triggers escalation. It is the central decision engine for task state transitions
// after an execution run completes.
type LifecycleManager struct {
	store      *sqlstore.Store
	bus        *EventBus
	checker    *DeliverableChecker
	escalation *EscalationEngine
}

// NewLifecycleManager creates a new lifecycle manager.
func NewLifecycleManager(store *sqlstore.Store, bus *EventBus) *LifecycleManager {
	return &LifecycleManager{
		store:      store,
		bus:        bus,
		checker:    NewDeliverableChecker(),
		escalation: NewEscalationEngine(),
	}
}

// HandleResult processes an execution result and applies the appropriate lifecycle transition.
func (lm *LifecycleManager) HandleResult(taskID string, runID int64, result *executor.ExecutionResult) error {
	// run.finished always fires when lifecycle finishes processing — regardless
	// of which branch ran (done/failed/blocked/review/superseded). It is the
	// frontend's single signal that a run is over and its pulse/Activity panel
	// should stop. Uses a deferred closure so early-return paths still emit.
	defer lm.emitRunFinished(taskID, runID, result)

	// Run-status guard (CW-20260418-0015). If an operator/scheduler path
	// already stamped the run row as cancelled/superseded/killed while the
	// executor was still running, the late-arriving executor result must
	// NOT drive retry/block/escalate/notify. Operator actions are silent
	// by design — no on_fail hook should fire, no retry counter should
	// advance, and the run row stands as stamped. We still emit
	// run.finished (via the deferred closure) so the UI's active-run pulse
	// stops.
	if runID > 0 {
		if run, err := lm.store.GetRun(runID); err == nil && sqlstore.IsOperatorTerminalRunStatus(run.Status) {
			return nil
		}
	}

	task, err := lm.store.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("lifecycle: get task %s: %w", taskID, err)
	}

	// If the task has already reached a terminal status — e.g. an operator
	// side-channel marked it done, or a parent rollup archived it — any
	// in-flight run result must NOT flip the task back. Retry/block/escalate
	// paths would otherwise re-queue a done task (CW-20260417-0012 incident).
	// Record the run as superseded and exit without touching task.Status.
	if isTerminalTaskStatus(task.Status) {
		return lm.markRunSuperseded(task, runID, result)
	}

	switch result.Status {
	case "done":
		return lm.handleDone(task, runID, result)
	case "failed":
		return lm.handleFailed(task, runID, result)
	case "blocked":
		return lm.handleBlocked(task, runID, result)
	case "review":
		return lm.transition(task, runID, "review", "")
	case "canceled":
		// Cancellation path (CW-20260418-0005). The worker already
		// wrote status=canceled on the run and the task has already
		// been transitioned by the external actor (DB task_transition
		// or equivalent). We MUST NOT touch task.Status here — doing
		// so would clobber the user's intent — and we MUST NOT count
		// this against retry budget. The deferred emitRunFinished
		// still fires so SSE consumers see the terminal signal.
		return nil
	default:
		return fmt.Errorf("lifecycle: unknown result status %q", result.Status)
	}
}

func (lm *LifecycleManager) handleDone(task *sqlstore.TaskRecord, runID int64, result *executor.ExecutionResult) error {
	// Check deliverables before accepting "done"
	if task.Deliverables.Valid && task.Deliverables.String != "" {
		var required []executor.Deliverable
		if err := json.Unmarshal([]byte(task.Deliverables.String), &required); err != nil {
			log.Printf("[lifecycle] failed to parse deliverables for %s: %v", task.ID, err)
		} else {
			missing := lm.checker.Check(required, result.Artifacts)
			if len(missing) > 0 {
				log.Printf("[lifecycle] task %s missing deliverables: %v", task.ID, missingTypes(missing))
				return lm.retryOrBlock(task, runID, "missing required deliverables")
			}
		}
	}

	// Structural acceptance gate: required subtodos must be ticked off. Unlike
	// deliverables this is keyed by item_id + executor-provided evidence, so a
	// simple emission of CLOCKWORK_SUBTODO_DONE during the run is the only way
	// to clear it. Non-required items are ignored.
	if missing, err := lm.missingRequiredSubtodos(task.ID); err != nil {
		log.Printf("[lifecycle] subtodo check error for %s: %v", task.ID, err)
	} else if len(missing) > 0 {
		reason := "missing required subtodos: " + strings.Join(missing, ", ")
		log.Printf("[lifecycle] task %s blocked by subtodo gate (%s)", task.ID, reason)
		return lm.transition(task, runID, "blocked", reason)
	}

	// Apply OnDone rule
	switch task.OnDone {
	case "review":
		return lm.transition(task, runID, "review", "")
	case "close":
		return lm.transition(task, runID, "done", "")
	case "notify":
		// Transition to done and emit a notify event
		if err := lm.transition(task, runID, "done", ""); err != nil {
			return err
		}
		lm.bus.Publish(SchedulerEvent{
			Type:   "task.notify",
			TaskID: task.ID,
			Data:   map[string]interface{}{"reason": "task completed"},
		})
		return nil
	default:
		return lm.transition(task, runID, "review", "")
	}
}

func (lm *LifecycleManager) handleFailed(task *sqlstore.TaskRecord, runID int64, result *executor.ExecutionResult) error {
	switch task.OnFail {
	case "retry":
		return lm.retryOrBlock(task, runID, result.Reason)
	case "block":
		return lm.transition(task, runID, "blocked", result.Reason)
	case "escalate":
		return lm.handleEscalation(task, runID, result)
	case "notify":
		if err := lm.transition(task, runID, "blocked", result.Reason); err != nil {
			return err
		}
		lm.bus.Publish(SchedulerEvent{
			Type:   "task.notify",
			TaskID: task.ID,
			Data:   map[string]interface{}{"reason": result.Reason},
		})
		return nil
	default:
		return lm.retryOrBlock(task, runID, result.Reason)
	}
}

func (lm *LifecycleManager) handleBlocked(task *sqlstore.TaskRecord, runID int64, result *executor.ExecutionResult) error {
	return lm.transition(task, runID, "blocked", result.Reason)
}

func (lm *LifecycleManager) handleEscalation(task *sqlstore.TaskRecord, runID int64, result *executor.ExecutionResult) error {
	var chain []string
	if task.EscalationChain.Valid && task.EscalationChain.String != "" {
		if err := json.Unmarshal([]byte(task.EscalationChain.String), &chain); err != nil {
			return fmt.Errorf("lifecycle: parse escalation chain: %w", err)
		}
	}

	// Read current escalation step
	var currentStep int
	lm.store.DB().QueryRow("SELECT escalation_step FROM tasks WHERE id = ?", task.ID).Scan(&currentStep)

	action := lm.escalation.NextAction(chain, currentStep)
	resolution, err := lm.escalation.Resolve(action)
	if err != nil {
		return fmt.Errorf("lifecycle: resolve escalation: %w", err)
	}

	// Update escalation step
	lm.store.DB().Exec("UPDATE tasks SET escalation_step = ? WHERE id = ?", resolution.NewEscalationStep, task.ID)

	// Apply agent profile change if needed
	if resolution.ChangeAgentProfile {
		lm.store.UpdateTask(task.ID, sqlstore.TaskUpdate{AgentProfile: &resolution.AgentProfile})
	}

	if resolution.BlockedReason != "" {
		return lm.transition(task, runID, resolution.NewStatus, resolution.BlockedReason)
	}

	return lm.transition(task, runID, resolution.NewStatus, "")
}

func (lm *LifecycleManager) retryOrBlock(task *sqlstore.TaskRecord, runID int64, reason string) error {
	// Read current retry count from DB (uses 002 migration column)
	var retryCount int
	lm.store.DB().QueryRow("SELECT retry_count FROM tasks WHERE id = ?", task.ID).Scan(&retryCount)

	if retryCount < task.MaxRetries {
		// Increment retry count
		lm.store.DB().Exec("UPDATE tasks SET retry_count = retry_count + 1 WHERE id = ?", task.ID)
		return lm.transition(task, runID, "todo", "")
	}

	return lm.transition(task, runID, "blocked", fmt.Sprintf("retries exhausted (%d/%d): %s", retryCount, task.MaxRetries, reason))
}

func (lm *LifecycleManager) transition(task *sqlstore.TaskRecord, runID int64, newStatus, blockedReason string) error {
	oldStatus := task.Status

	if err := lm.store.TransitionTask(task.ID, newStatus); err != nil {
		return fmt.Errorf("lifecycle: transition %s -> %s: %w", task.ID, newStatus, err)
	}

	if blockedReason != "" {
		lm.store.UpdateTask(task.ID, sqlstore.TaskUpdate{BlockedReason: &blockedReason})
	}

	payload := map[string]interface{}{
		"from": oldStatus,
		"to":   newStatus,
	}
	if blockedReason != "" {
		payload["reason"] = blockedReason
	}
	writeRunEvent(lm.store, runID, task.ID, "task_transitioned", payload)

	lm.bus.Publish(SchedulerEvent{
		Type:   "task.transitioned",
		TaskID: task.ID,
		Data: map[string]interface{}{
			"from": oldStatus,
			"to":   newStatus,
		},
	})

	return nil
}

// emitRunFinished publishes the terminal run.finished event. duration_ms is
// computed from the run row's started_at; if the run can't be read back
// (unknown runID, store error) duration_ms is zero and the event still fires
// so the UI can reliably stop the active-run pulse.
func (lm *LifecycleManager) emitRunFinished(taskID string, runID int64, result *executor.ExecutionResult) {
	if result == nil {
		return
	}

	var durationMs int64
	if runID > 0 {
		if run, err := lm.store.GetRun(runID); err == nil && !run.StartedAt.IsZero() {
			end := time.Now().UTC()
			if run.EndedAt.Valid {
				end = run.EndedAt.Time
			}
			if d := end.Sub(run.StartedAt); d > 0 {
				durationMs = d.Milliseconds()
			}
		}
	}

	data := map[string]interface{}{
		"status":      result.Status,
		"duration_ms": durationMs,
	}
	if result.Reason != "" {
		data["reason"] = result.Reason
	}

	lm.bus.Publish(SchedulerEvent{
		Type:   "run.finished",
		TaskID: taskID,
		RunID:  runID,
		Data:   data,
	})
}

// missingRequiredSubtodos returns the ids of required subtodos that are not
// yet marked done. Returns a nil slice when the task has no subtodos or all
// required items are ticked off.
func (lm *LifecycleManager) missingRequiredSubtodos(taskID string) ([]string, error) {
	items, err := lm.store.GetSubtodos(taskID)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, it := range items {
		if it.Required && !it.Done {
			missing = append(missing, it.ID)
		}
	}
	return missing, nil
}

func missingTypes(missing []executor.Deliverable) []string {
	var types []string
	for _, d := range missing {
		types = append(types, d.Type)
	}
	return types
}

// isTerminalTaskStatus reports whether a task status is a sink in the task
// FSM — no outbound transitions should be driven by run results. See the
// canonical transitions in internal/service/task.go.
func isTerminalTaskStatus(status string) bool {
	return status == "done" || status == "archived"
}

// markRunSuperseded flags a run whose lifecycle result arrived after the
// task had already moved to a terminal status. The run row is updated to
// status="superseded" and a run_event is emitted for observability. The
// task record is left untouched.
func (lm *LifecycleManager) markRunSuperseded(task *sqlstore.TaskRecord, runID int64, result *executor.ExecutionResult) error {
	if runID > 0 {
		if _, err := lm.store.DB().Exec(
			`UPDATE runs SET status = ? WHERE id = ?`,
			"superseded", runID,
		); err != nil {
			log.Printf("[lifecycle] mark run %d superseded: %v", runID, err)
		}
	}

	payload := map[string]interface{}{
		"task_status":   task.Status,
		"result_status": result.Status,
		"result_reason": result.Reason,
	}
	writeRunEvent(lm.store, runID, task.ID, "run_superseded", payload)

	lm.bus.Publish(SchedulerEvent{
		Type:   "run.superseded",
		TaskID: task.ID,
		RunID:  runID,
		Data: map[string]interface{}{
			"task_status":   task.Status,
			"result_status": result.Status,
		},
	})

	return nil
}
