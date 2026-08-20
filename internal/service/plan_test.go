package service_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/service"
)

// recordingPlanPhaseObserver is a test fake that captures every
// ObservePlanPhase call. Used to verify the observer hook fires on
// AddPhase / RemovePhase with the expected event shape.
type recordingPlanPhaseObserver struct {
	mu     sync.Mutex
	events []service.PlanPhaseEvent
}

func (r *recordingPlanPhaseObserver) ObservePlanPhase(_ context.Context, ev service.PlanPhaseEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingPlanPhaseObserver) snapshot() []service.PlanPhaseEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]service.PlanPhaseEvent, len(r.events))
	copy(out, r.events)
	return out
}

// TestPlanCreate_ShapeAndMetadata verifies that CreatePlan builds a
// kind=plan task whose metadata.plan contains the expected phase list
// with auto-assigned ids + monotonic orders.
func TestPlanCreate_ShapeAndMetadata(t *testing.T) {
	svc := setupService(t)

	plan, err := svc.Plan.Create(service.PlanCreateInput{
		Title:       "Ship feature X",
		Description: "Three-phase rollout",
		Phases: []service.PlanPhaseInput{
			{Name: "Foundation", Acceptance: "migration green"},
			{Name: "Integration"},
			{Name: "Rollout"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "plan", plan.Kind)
	require.True(t, plan.Manual, "plans must be manual — they never dispatch")

	var metadata map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(plan.Metadata.String), &metadata))

	var decoded service.PlanMetadata
	require.NoError(t, json.Unmarshal(metadata["plan"], &decoded))
	require.Equal(t, 1, decoded.Version)
	require.Len(t, decoded.Phases, 3)
	require.Equal(t, "ph-1", decoded.Phases[0].ID)
	require.Equal(t, "ph-2", decoded.Phases[1].ID)
	require.Equal(t, "ph-3", decoded.Phases[2].ID)
	require.Equal(t, 1, decoded.Phases[0].Order)
	require.Equal(t, 3, decoded.Phases[2].Order)
	require.Equal(t, "migration green", decoded.Phases[0].Acceptance)
}

// TestPlanAddPhase_UniqueIDMonotonicOrder verifies AddPhase never reuses a
// phase id and always appends at the end of the display order.
func TestPlanAddPhase_UniqueIDMonotonicOrder(t *testing.T) {
	svc := setupService(t)

	plan, err := svc.Plan.Create(service.PlanCreateInput{
		Title:  "p",
		Phases: []service.PlanPhaseInput{{Name: "A"}},
	})
	require.NoError(t, err)

	id1, err := svc.Plan.AddPhase(plan.ID, "B", "accept-B")
	require.NoError(t, err)
	id2, err := svc.Plan.AddPhase(plan.ID, "C", "")
	require.NoError(t, err)
	require.NotEqual(t, id1, id2)

	detail, err := svc.Plan.Get(plan.ID)
	require.NoError(t, err)
	require.Len(t, detail.Plan.Phases, 3)
	require.Equal(t, 1, detail.Plan.Phases[0].Order)
	require.Equal(t, 2, detail.Plan.Phases[1].Order)
	require.Equal(t, 3, detail.Plan.Phases[2].Order)
}

// TestPlanRemovePhase_BlocksWhenChildrenReferencePhase verifies that a phase
// cannot be deleted while child tasks still carry metadata.phase_id pointing
// at it — child reassignment is a caller responsibility.
func TestPlanRemovePhase_BlocksWhenChildrenReferencePhase(t *testing.T) {
	svc := setupService(t)

	plan, err := svc.Plan.Create(service.PlanCreateInput{
		Title:  "p",
		Phases: []service.PlanPhaseInput{{Name: "A"}, {Name: "B"}},
	})
	require.NoError(t, err)

	_, err = svc.Task.Create(service.TaskCreateInput{
		Title:    "child of A",
		ParentID: plan.ID,
		Metadata: map[string]any{"phase_id": "ph-1"},
	})
	require.NoError(t, err)

	err = svc.Plan.RemovePhase(plan.ID, "ph-1")
	require.Error(t, err)
	// ConflictError, not ValidationError: this is a state-dependent
	// rejection (children still referencing the phase), not a malformed
	// argument. Matches torque_plan_remove_phase's documented
	// error.code=conflict contract (mcpadapter.mapServiceError maps
	// ConflictError -> conflict, ValidationError -> arg_invalid).
	var cErr *service.ConflictError
	require.ErrorAs(t, err, &cErr)

	// Phase with no children removes cleanly.
	require.NoError(t, svc.Plan.RemovePhase(plan.ID, "ph-2"))
	detail, err := svc.Plan.Get(plan.ID)
	require.NoError(t, err)
	require.Len(t, detail.Plan.Phases, 1)
}

// TestPlanListChildren_PhaseFilter verifies that ListChildren partitions
// children by metadata.phase_id when a filter is provided.
func TestPlanListChildren_PhaseFilter(t *testing.T) {
	svc := setupService(t)

	plan, err := svc.Plan.Create(service.PlanCreateInput{
		Title:  "p",
		Phases: []service.PlanPhaseInput{{Name: "A"}, {Name: "B"}},
	})
	require.NoError(t, err)

	_, err = svc.Task.Create(service.TaskCreateInput{
		Title:    "c1",
		ParentID: plan.ID,
		Metadata: map[string]any{"phase_id": "ph-1"},
	})
	require.NoError(t, err)
	_, err = svc.Task.Create(service.TaskCreateInput{
		Title:    "c2",
		ParentID: plan.ID,
		Metadata: map[string]any{"phase_id": "ph-2"},
	})
	require.NoError(t, err)

	all, err := svc.Plan.ListChildren(plan.ID, "")
	require.NoError(t, err)
	require.Len(t, all, 2)

	ph1, err := svc.Plan.ListChildren(plan.ID, "ph-1")
	require.NoError(t, err)
	require.Len(t, ph1, 1)
	require.Equal(t, "c1", ph1[0].Title)
}

// TestPlanProgress_Rollup covers the aggregate counts exposed to the GUI.
func TestPlanProgress_Rollup(t *testing.T) {
	svc := setupService(t)

	plan, err := svc.Plan.Create(service.PlanCreateInput{
		Title:  "p",
		Phases: []service.PlanPhaseInput{{Name: "A"}, {Name: "B"}},
	})
	require.NoError(t, err)

	c1, err := svc.Task.Create(service.TaskCreateInput{
		Title:    "c1",
		ParentID: plan.ID,
		Metadata: map[string]any{"phase_id": "ph-1"},
	})
	require.NoError(t, err)
	_, err = svc.Task.Create(service.TaskCreateInput{
		Title:    "c2",
		ParentID: plan.ID,
		Metadata: map[string]any{"phase_id": "ph-1"},
	})
	require.NoError(t, err)

	// Progress → c1→doing→done, c2 stays todo.
	require.NoError(t, svc.Task.Transition(context.Background(), c1.ID, "doing"))
	require.NoError(t, svc.Task.Transition(context.Background(), c1.ID, "done"))

	prog, err := svc.Plan.Progress(plan.ID)
	require.NoError(t, err)
	require.Equal(t, 2, prog.TotalChildren)
	require.Equal(t, 1, prog.Done)
	require.Equal(t, 0, prog.Blocked)
	roll := prog.ByPhase["ph-1"]
	require.Equal(t, 2, roll.Total)
	require.Equal(t, 1, roll.Done)
}

// TestPlanPhaseObserver_FiresOnAddAndRemove verifies that an installed
// PlanPhaseObserver is invoked once per successful AddPhase / RemovePhase
// with the expected event kind, plan id, phase id, and (for added) name +
// order. Powers the daemon-side bridge that publishes plan.phase_added /
// plan.phase_removed onto the scheduler.EventBus (CW-20260519-0126).
func TestPlanPhaseObserver_FiresOnAddAndRemove(t *testing.T) {
	svc := setupService(t)

	rec := &recordingPlanPhaseObserver{}
	svc.Plan.SetPhaseObserver(rec)

	plan, err := svc.Plan.Create(service.PlanCreateInput{
		Title:  "p",
		Phases: []service.PlanPhaseInput{{Name: "Foundation"}},
	})
	require.NoError(t, err)

	// Adding a phase fires "added" with the new phase id + name + order.
	id, err := svc.Plan.AddPhase(plan.ID, "Integration", "")
	require.NoError(t, err)

	events := rec.snapshot()
	require.Len(t, events, 1, "AddPhase should fire one observer event")
	require.Equal(t, "added", events[0].Kind)
	require.Equal(t, plan.ID, events[0].PlanID)
	require.Equal(t, id, events[0].PhaseID)
	require.Equal(t, "Integration", events[0].PhaseName)
	require.Equal(t, 2, events[0].Order, "second phase appends with order=2")

	// Removing a phase fires "removed" with the phase id; name+order are zero.
	require.NoError(t, svc.Plan.RemovePhase(plan.ID, id))

	events = rec.snapshot()
	require.Len(t, events, 2, "RemovePhase should fire a second observer event")
	require.Equal(t, "removed", events[1].Kind)
	require.Equal(t, plan.ID, events[1].PlanID)
	require.Equal(t, id, events[1].PhaseID)
	require.Equal(t, "", events[1].PhaseName)
	require.Equal(t, 0, events[1].Order)
}

// TestPlanPhaseObserver_NotFiredOnFailedAdd verifies that the observer
// does NOT fire when AddPhase fails validation. The observer hook runs
// only after the metadata write succeeds.
func TestPlanPhaseObserver_NotFiredOnFailedAdd(t *testing.T) {
	svc := setupService(t)

	rec := &recordingPlanPhaseObserver{}
	svc.Plan.SetPhaseObserver(rec)

	plan, err := svc.Plan.Create(service.PlanCreateInput{Title: "p"})
	require.NoError(t, err)

	// Empty name → validation error → observer must not fire.
	_, err = svc.Plan.AddPhase(plan.ID, "", "")
	require.Error(t, err)
	require.Empty(t, rec.snapshot())
}
