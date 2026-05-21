package agent

import (
	"context"

	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/service"
)

// PlanPhaseEmitter bridges service.PlanPhaseObserver onto the
// scheduler.EventBus. Each observed plan-phase event becomes one
// SchedulerEvent published as `plan.phase_added` or `plan.phase_removed`
// so any in-process subscriber (SSE bridge, session-lifecycle-hook,
// future supervisors) sees plan structural changes through the same fan-
// out as task transitions / run events.
//
// Bridge motivation (CW-20260519-0126): the 2026-05-19 orchestrator
// stand-down was partly caused by no event telling the orchestrator
// that ph-1 had been removed. Routing AddPhase / RemovePhase through
// the EventBus closes that gap without requiring durable agents to poll
// torque_plan_get every turn. Mirrors the SessionLifecycleHook bridge
// pattern (TaskTransitionObserver → EventBus consumer).
type PlanPhaseEmitter struct {
	bus *scheduler.EventBus
}

// NewPlanPhaseEmitter returns an emitter bound to the given bus. Returns
// nil if bus is nil — callers wire unconditionally; nil-tolerance lets a
// test harness skip the bridge without panicking.
func NewPlanPhaseEmitter(bus *scheduler.EventBus) *PlanPhaseEmitter {
	if bus == nil {
		return nil
	}
	return &PlanPhaseEmitter{bus: bus}
}

// ObservePlanPhase implements service.PlanPhaseObserver. Translates a
// PlanPhaseEvent into a SchedulerEvent. The event kind is namespaced
// `plan.phase_added` / `plan.phase_removed` (period-separated, matching
// the existing `task.transitioned` / `run.started` convention); the
// payload carries phase_id (+ name + order for added).
//
// Non-blocking: scheduler.EventBus.Publish never blocks (drops on full
// for slow subscribers), so this method is safe to call from the
// service-layer request path.
func (e *PlanPhaseEmitter) ObservePlanPhase(_ context.Context, ev service.PlanPhaseEvent) {
	if e == nil || e.bus == nil {
		return
	}
	if ev.PlanID == "" || ev.PhaseID == "" {
		return
	}

	var kind string
	payload := map[string]interface{}{"phase_id": ev.PhaseID}
	switch ev.Kind {
	case "added":
		kind = "plan.phase_added"
		if ev.PhaseName != "" {
			payload["phase_name"] = ev.PhaseName
		}
		if ev.Order > 0 {
			payload["order"] = ev.Order
		}
	case "removed":
		kind = "plan.phase_removed"
	default:
		// Defensive: an unknown Kind from a future PlanPhaseEvent expansion
		// is dropped rather than emitted as a malformed event. Service-layer
		// is the source of truth for the legal Kind set.
		return
	}

	e.bus.Publish(scheduler.SchedulerEvent{
		Type:   kind,
		TaskID: ev.PlanID,
		Data:   payload,
	})
}
