package scheduler

import (
	"encoding/json"
	"fmt"
	"log"

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
	task, err := lm.store.GetTask(taskID)
	if err != nil {
		return fmt.Errorf("lifecycle: get task %s: %w", taskID, err)
	}

	switch result.Status {
	case "done":
		return lm.handleDone(task, runID, result)
	case "failed":
		return lm.handleFailed(task, runID, result)
	case "blocked":
		return lm.handleBlocked(task, runID, result)
	case "review":
		return lm.transition(task, "review", "")
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
				return lm.retryOrBlock(task, "missing required deliverables")
			}
		}
	}

	// Apply OnDone rule
	switch task.OnDone {
	case "review":
		return lm.transition(task, "review", "")
	case "close":
		return lm.transition(task, "done", "")
	case "notify":
		// Transition to done and emit a notify event
		if err := lm.transition(task, "done", ""); err != nil {
			return err
		}
		lm.bus.Publish(SchedulerEvent{
			Type:   "task.notify",
			TaskID: task.ID,
			Data:   map[string]interface{}{"reason": "task completed"},
		})
		return nil
	default:
		return lm.transition(task, "review", "")
	}
}

func (lm *LifecycleManager) handleFailed(task *sqlstore.TaskRecord, runID int64, result *executor.ExecutionResult) error {
	switch task.OnFail {
	case "retry":
		return lm.retryOrBlock(task, result.Reason)
	case "block":
		return lm.transition(task, "blocked", result.Reason)
	case "escalate":
		return lm.handleEscalation(task, result)
	case "notify":
		if err := lm.transition(task, "blocked", result.Reason); err != nil {
			return err
		}
		lm.bus.Publish(SchedulerEvent{
			Type:   "task.notify",
			TaskID: task.ID,
			Data:   map[string]interface{}{"reason": result.Reason},
		})
		return nil
	default:
		return lm.retryOrBlock(task, result.Reason)
	}
}

func (lm *LifecycleManager) handleBlocked(task *sqlstore.TaskRecord, runID int64, result *executor.ExecutionResult) error {
	return lm.transition(task, "blocked", result.Reason)
}

func (lm *LifecycleManager) handleEscalation(task *sqlstore.TaskRecord, result *executor.ExecutionResult) error {
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
		return lm.transition(task, resolution.NewStatus, resolution.BlockedReason)
	}

	return lm.transition(task, resolution.NewStatus, "")
}

func (lm *LifecycleManager) retryOrBlock(task *sqlstore.TaskRecord, reason string) error {
	// Read current retry count from DB (uses 002 migration column)
	var retryCount int
	lm.store.DB().QueryRow("SELECT retry_count FROM tasks WHERE id = ?", task.ID).Scan(&retryCount)

	if retryCount < task.MaxRetries {
		// Increment retry count
		lm.store.DB().Exec("UPDATE tasks SET retry_count = retry_count + 1 WHERE id = ?", task.ID)
		return lm.transition(task, "todo", "")
	}

	return lm.transition(task, "blocked", fmt.Sprintf("retries exhausted (%d/%d): %s", retryCount, task.MaxRetries, reason))
}

func (lm *LifecycleManager) transition(task *sqlstore.TaskRecord, newStatus, blockedReason string) error {
	oldStatus := task.Status

	if err := lm.store.TransitionTask(task.ID, newStatus); err != nil {
		return fmt.Errorf("lifecycle: transition %s -> %s: %w", task.ID, newStatus, err)
	}

	if blockedReason != "" {
		lm.store.UpdateTask(task.ID, sqlstore.TaskUpdate{BlockedReason: &blockedReason})
	}

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

func missingTypes(missing []executor.Deliverable) []string {
	var types []string
	for _, d := range missing {
		types = append(types, d.Type)
	}
	return types
}
