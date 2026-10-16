package service_test

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
)

// A removed phase keeps its row, so its id is never issued again.
func TestPlanPhaseIDsAreNeverReused(t *testing.T) {
	svc := setupService(t)
	plan, err := svc.Plan.Create(service.PlanCreateInput{Title: "p", Phases: []service.PlanPhaseInput{{Name: "A"}, {Name: "B"}}})
	require.NoError(t, err)

	require.NoError(t, svc.Plan.RemovePhase(plan.ID, "ph-2"))
	id, err := svc.Plan.AddPhase(plan.ID, "C", "")
	require.NoError(t, err)
	require.Equal(t, "ph-3", id, "ph-2 was issued once and is not handed out again")

	// Removing the highest id and adding again still moves forward.
	require.NoError(t, svc.Plan.RemovePhase(plan.ID, "ph-3"))
	id, err = svc.Plan.AddPhase(plan.ID, "D", "")
	require.NoError(t, err)
	require.Equal(t, "ph-4", id)

	// A tombstoned phase is gone from the plan and cannot be removed twice.
	detail, err := svc.Plan.Get(plan.ID)
	require.NoError(t, err)
	require.Len(t, detail.Plan.Phases, 2)
	err = svc.Plan.RemovePhase(plan.ID, "ph-2")
	var vErr *service.ValidationError
	require.ErrorAs(t, err, &vErr)
}

// The compatibility projection shows record state, and Task.Metadata agrees
// with Plan so a reader of either sees the same phases.
func TestPlanGetProjectsPhaseRecords(t *testing.T) {
	svc := setupService(t)
	plan, err := svc.Plan.Create(service.PlanCreateInput{Title: "p", Phases: []service.PlanPhaseInput{{Name: "A", Acceptance: "ok"}}})
	require.NoError(t, err)

	detail, err := svc.Plan.Get(plan.ID)
	require.NoError(t, err)
	require.Equal(t, service.PlanPhase{ID: "ph-1", Name: "A", Order: 1, Acceptance: "ok", State: "pending", Generation: 1}, detail.Plan.Phases[0])

	var meta struct {
		Plan service.PlanMetadata `json:"plan"`
	}
	require.NoError(t, json.Unmarshal([]byte(detail.Task.Metadata.String), &meta))
	require.Equal(t, detail.Plan.Phases, meta.Plan.Phases)
}

// A child names its phase in metadata.phase_id (the established convention);
// the store records it in phase_id and holds it to the parent plan's phases.
func TestTaskPhaseFromMetadata(t *testing.T) {
	svc := setupService(t)
	plan, err := svc.Plan.Create(service.PlanCreateInput{Title: "p", Phases: []service.PlanPhaseInput{{Name: "A"}, {Name: "B"}}})
	require.NoError(t, err)
	other, err := svc.Plan.Create(service.PlanCreateInput{Title: "other", Phases: []service.PlanPhaseInput{{Name: "X"}}})
	require.NoError(t, err)

	child, err := svc.Task.Create(service.TaskCreateInput{Title: "c", ParentID: plan.ID, Metadata: map[string]any{"phase_id": "ph-1"}})
	require.NoError(t, err)
	require.Equal(t, "ph-1", child.PhaseID.String)

	// A phase the plan does not have is rejected, not recorded.
	_, err = svc.Task.Create(service.TaskCreateInput{Title: "bad", ParentID: plan.ID, Metadata: map[string]any{"phase_id": "ph-9"}})
	var vErr *service.ValidationError
	require.ErrorAs(t, err, &vErr)
	require.Equal(t, "phase_id", vErr.Field)

	// Under a non-plan parent the metadata means whatever the caller meant.
	plain, err := svc.Task.Create(service.TaskCreateInput{Title: "plain"})
	require.NoError(t, err)
	got, err := svc.Task.Create(service.TaskCreateInput{Title: "g", ParentID: plain.ID, Metadata: map[string]any{"phase_id": "ph-1"}})
	require.NoError(t, err)
	require.False(t, got.PhaseID.Valid)

	// Re-pointing metadata.phase_id moves the task; an empty value clears it.
	setMeta := func(m string) error {
		v := sql.NullString{String: m, Valid: true}
		return svc.Task.Update(child.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{Metadata: &v}})
	}
	require.NoError(t, setMeta(`{"phase_id":"ph-2"}`))
	rec, err := svc.Task.Get(child.ID)
	require.NoError(t, err)
	require.Equal(t, "ph-2", rec.PhaseID.String)
	require.ErrorAs(t, setMeta(`{"phase_id":"ph-9"}`), &vErr)
	require.NoError(t, setMeta(`{"phase_id":""}`))
	rec, err = svc.Task.Get(child.ID)
	require.NoError(t, err)
	require.False(t, rec.PhaseID.Valid)

	// Moving a member to another plan drops the old phase instead of failing.
	require.NoError(t, setMeta(`{"phase_id":"ph-1"}`))
	moved := sql.NullString{String: other.ID, Valid: true}
	require.NoError(t, svc.Task.Update(child.ID, service.TaskUpdateInput{TaskUpdate: sqlstore.TaskUpdate{ParentID: &moved}}))
	rec, err = svc.Task.Get(child.ID)
	require.NoError(t, err)
	require.Equal(t, other.ID, rec.ParentID.String)
	require.False(t, rec.PhaseID.Valid)

	// A removed phase takes no new members.
	require.NoError(t, svc.Plan.RemovePhase(plan.ID, "ph-2"))
	_, err = svc.Task.Create(service.TaskCreateInput{Title: "late", ParentID: plan.ID, Metadata: map[string]any{"phase_id": "ph-2"}})
	require.ErrorAs(t, err, &vErr)
}

// Progress and the children filter read the phase_id column.
func TestPlanProgressUsesPhaseColumn(t *testing.T) {
	svc := setupService(t)
	plan, err := svc.Plan.Create(service.PlanCreateInput{Title: "p", Phases: []service.PlanPhaseInput{{Name: "A"}}})
	require.NoError(t, err)
	_, err = svc.Task.Create(service.TaskCreateInput{Title: "in", ParentID: plan.ID, Metadata: map[string]any{"phase_id": "ph-1"}})
	require.NoError(t, err)
	_, err = svc.Task.Create(service.TaskCreateInput{Title: "out", ParentID: plan.ID})
	require.NoError(t, err)

	p, err := svc.Plan.Progress(plan.ID)
	require.NoError(t, err)
	require.Equal(t, 1, p.ByPhase["ph-1"].Total)
	require.Equal(t, 1, p.ByPhase[""].Total)

	kids, err := svc.Plan.ListChildren(plan.ID, "ph-1")
	require.NoError(t, err)
	require.Len(t, kids, 1)
	require.Equal(t, "in", kids[0].Title)
}
