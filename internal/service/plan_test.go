package service_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/service"
)

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
	var vErr *service.ValidationError
	require.ErrorAs(t, err, &vErr)
	require.Equal(t, "phase_id", vErr.Field)

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
	require.NoError(t, svc.Task.Transition(c1.ID, "doing"))
	require.NoError(t, svc.Task.Transition(c1.ID, "done"))

	prog, err := svc.Plan.Progress(plan.ID)
	require.NoError(t, err)
	require.Equal(t, 2, prog.TotalChildren)
	require.Equal(t, 1, prog.Done)
	require.Equal(t, 0, prog.Blocked)
	roll := prog.ByPhase["ph-1"]
	require.Equal(t, 2, roll.Total)
	require.Equal(t, 1, roll.Done)
}
