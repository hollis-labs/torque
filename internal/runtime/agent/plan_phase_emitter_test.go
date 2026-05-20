package agent

import (
	"context"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/service"
)

func TestPlanPhaseEmitter_PublishesAddedAndRemoved(t *testing.T) {
	bus := scheduler.NewEventBus()
	defer bus.Close()
	sub := bus.Subscribe()

	emitter := NewPlanPhaseEmitter(bus)
	if emitter == nil {
		t.Fatal("NewPlanPhaseEmitter returned nil for non-nil bus")
	}

	emitter.ObservePlanPhase(context.Background(), service.PlanPhaseEvent{
		Kind:      "added",
		PlanID:    "PLAN-1",
		PhaseID:   "ph-2",
		PhaseName: "Integration",
		Order:     2,
	})
	emitter.ObservePlanPhase(context.Background(), service.PlanPhaseEvent{
		Kind:    "removed",
		PlanID:  "PLAN-1",
		PhaseID: "ph-2",
	})

	added := receiveEvent(t, sub)
	if added.Type != "plan.phase_added" {
		t.Errorf("Type = %q, want plan.phase_added", added.Type)
	}
	if added.TaskID != "PLAN-1" {
		t.Errorf("TaskID = %q, want PLAN-1", added.TaskID)
	}
	addedPayload, ok := added.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Data not a map: %T", added.Data)
	}
	if addedPayload["phase_id"] != "ph-2" {
		t.Errorf("phase_id = %v", addedPayload["phase_id"])
	}
	if addedPayload["phase_name"] != "Integration" {
		t.Errorf("phase_name = %v", addedPayload["phase_name"])
	}
	if addedPayload["order"] != 2 {
		t.Errorf("order = %v", addedPayload["order"])
	}

	removed := receiveEvent(t, sub)
	if removed.Type != "plan.phase_removed" {
		t.Errorf("Type = %q, want plan.phase_removed", removed.Type)
	}
	if removed.TaskID != "PLAN-1" {
		t.Errorf("TaskID = %q, want PLAN-1", removed.TaskID)
	}
	removedPayload, ok := removed.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Data not a map: %T", removed.Data)
	}
	if removedPayload["phase_id"] != "ph-2" {
		t.Errorf("phase_id = %v", removedPayload["phase_id"])
	}
	if _, present := removedPayload["phase_name"]; present {
		t.Errorf("phase_name should be omitted on removed; got %v", removedPayload["phase_name"])
	}
}

func TestPlanPhaseEmitter_DropsUnknownKind(t *testing.T) {
	bus := scheduler.NewEventBus()
	defer bus.Close()
	sub := bus.Subscribe()

	emitter := NewPlanPhaseEmitter(bus)
	emitter.ObservePlanPhase(context.Background(), service.PlanPhaseEvent{
		Kind:    "renamed",
		PlanID:  "PLAN-1",
		PhaseID: "ph-2",
	})

	select {
	case ev := <-sub:
		t.Errorf("expected no event for unknown kind, got %s", ev.Type)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPlanPhaseEmitter_DropsMissingIDs(t *testing.T) {
	bus := scheduler.NewEventBus()
	defer bus.Close()
	sub := bus.Subscribe()

	emitter := NewPlanPhaseEmitter(bus)
	emitter.ObservePlanPhase(context.Background(), service.PlanPhaseEvent{
		Kind:    "added",
		PlanID:  "", // missing
		PhaseID: "ph-1",
	})
	emitter.ObservePlanPhase(context.Background(), service.PlanPhaseEvent{
		Kind:    "added",
		PlanID:  "PLAN-1",
		PhaseID: "", // missing
	})

	select {
	case ev := <-sub:
		t.Errorf("expected no event for missing ids, got %s", ev.Type)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPlanPhaseEmitter_NilBusReturnsNilEmitter(t *testing.T) {
	if NewPlanPhaseEmitter(nil) != nil {
		t.Error("expected nil emitter for nil bus")
	}
}

func receiveEvent(t *testing.T, sub <-chan scheduler.SchedulerEvent) scheduler.SchedulerEvent {
	t.Helper()
	select {
	case ev := <-sub:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SchedulerEvent")
		return scheduler.SchedulerEvent{}
	}
}
