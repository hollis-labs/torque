package service_test

import (
	"context"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSprintCreateRequiresFeature(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 1"})
	assert.Error(t, err)
	assert.IsType(t, &service.FeatureDisabledError{}, err)
}

func TestSprintCreate(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, err := svc.Sprint.Create(service.SprintCreateInput{
		Name:         "Sprint 1",
		ApprovalMode: "approve_each",
		CostBudget:   floatPtr(50.0),
	})
	require.NoError(t, err)
	assert.Contains(t, sprint.ID, "SP-")
	assert.Equal(t, "active", sprint.Status)
	assert.Equal(t, "approve_each", sprint.ApprovalMode)
	assert.Equal(t, 50.0, sprint.CostBudget.Float64)
}

func TestSprintCreateValidation(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	_, err := svc.Sprint.Create(service.SprintCreateInput{})
	assert.Error(t, err)
	assert.IsType(t, &service.ValidationError{}, err)
}

func TestSprintCreateInvalidApprovalMode(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	_, err := svc.Sprint.Create(service.SprintCreateInput{
		Name:         "Sprint",
		ApprovalMode: "invalid",
	})
	assert.Error(t, err)
	assert.IsType(t, &service.ValidationError{}, err)
}

func TestSprintTransitionFSM(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 1"})
	// default is active
	assert.Equal(t, "active", sprint.Status)

	// active -> inactive
	err := svc.Sprint.Transition(sprint.ID, "inactive")
	require.NoError(t, err)
	got, _ := svc.Sprint.Get(sprint.ID)
	assert.Equal(t, "inactive", got.Status)

	// inactive -> active
	err = svc.Sprint.Transition(sprint.ID, "active")
	require.NoError(t, err)
	got, _ = svc.Sprint.Get(sprint.ID)
	assert.Equal(t, "active", got.Status)
}

func TestSprintTransitionInvalid(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 1"})

	// active -> active (no-op, invalid)
	err := svc.Sprint.Transition(sprint.ID, "active")
	assert.Error(t, err)
	assert.IsType(t, &service.TransitionError{}, err)

	// active -> bogus (invalid)
	err = svc.Sprint.Transition(sprint.ID, "bogus")
	assert.Error(t, err)
	assert.IsType(t, &service.TransitionError{}, err)
}

// TestSprintTransitionToCompleted verifies the direct active→completed and
// inactive→completed paths (CW-20260417-0010). Users expect a single
// transition after all child tasks reach done; previously only active↔inactive
// was allowed and closing a sprint required a workaround.
func TestSprintTransitionToCompleted(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	// active -> completed
	a, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Active→Completed"})
	require.NoError(t, svc.Sprint.Transition(a.ID, "completed"))
	got, _ := svc.Sprint.Get(a.ID)
	assert.Equal(t, "completed", got.Status)

	// inactive -> completed
	b, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Inactive→Completed"})
	require.NoError(t, svc.Sprint.Transition(b.ID, "inactive"))
	require.NoError(t, svc.Sprint.Transition(b.ID, "completed"))
	got, _ = svc.Sprint.Get(b.ID)
	assert.Equal(t, "completed", got.Status)

	// completed is terminal: completed -> active should fail
	err := svc.Sprint.Transition(a.ID, "active")
	assert.Error(t, err)
	assert.IsType(t, &service.TransitionError{}, err)
}

func TestSprintTransitionBackwardsInvalid(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 1"})
	svc.Sprint.Transition(sprint.ID, "inactive")

	// inactive -> inactive (no-op, invalid)
	err := svc.Sprint.Transition(sprint.ID, "inactive")
	assert.Error(t, err)
	assert.IsType(t, &service.TransitionError{}, err)
}

func TestSprintCostBudgetCheck(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{
		Name:       "Budget Sprint",
		CostBudget: floatPtr(10.0),
	})

	// No runs yet — budget not exceeded
	ok, remaining := svc.Sprint.CheckCostBudget(sprint.ID)
	assert.True(t, ok)
	assert.Equal(t, 10.0, remaining)
}

func TestSprintApproveAll(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{
		Name:         "Approve Sprint",
		ApprovalMode: "approve_sprint",
	})
	// Sprint starts as active by default

	// Create tasks in the sprint and move them to review
	task1, _ := svc.Task.Create(service.TaskCreateInput{
		Title: "Task 1", Description: "Do thing 1", SprintID: sprint.ID,
	})
	task2, _ := svc.Task.Create(service.TaskCreateInput{
		Title: "Task 2", Description: "Do thing 2", SprintID: sprint.ID,
	})
	svc.Task.Transition(context.Background(), task1.ID, "doing")
	svc.Task.Transition(context.Background(), task1.ID, "review")
	svc.Task.Transition(context.Background(), task2.ID, "doing")
	svc.Task.Transition(context.Background(), task2.ID, "review")

	// Approve all tasks in the sprint at once
	count, err := svc.Sprint.ApproveAll(sprint.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Verify tasks moved to done
	got1, _ := svc.Task.Get(task1.ID)
	got2, _ := svc.Task.Get(task2.ID)
	assert.Equal(t, "done", got1.Status)
	assert.Equal(t, "done", got2.Status)
}

func TestSprintApproveTask(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{
		Name:         "Approve Each Sprint",
		ApprovalMode: "approve_each",
	})
	// Sprint starts as active by default

	task, _ := svc.Task.Create(service.TaskCreateInput{
		Title: "Task 1", Description: "Do thing", SprintID: sprint.ID,
	})
	svc.Task.Transition(context.Background(), task.ID, "doing")
	svc.Task.Transition(context.Background(), task.ID, "review")

	// Approve single task
	err := svc.Sprint.ApproveTask(sprint.ID, task.ID)
	require.NoError(t, err)

	got, _ := svc.Task.Get(task.ID)
	assert.Equal(t, "done", got.Status)
}

func TestSprintApproveTaskWrongSprint(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint A"})
	// Sprint starts as active by default

	// Task not in this sprint
	task, _ := svc.Task.Create(service.TaskCreateInput{
		Title: "Unattached task", Description: "No sprint",
	})
	svc.Task.Transition(context.Background(), task.ID, "doing")
	svc.Task.Transition(context.Background(), task.ID, "review")

	err := svc.Sprint.ApproveTask(sprint.ID, task.ID)
	assert.Error(t, err)
}

func TestSprintList(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 1"})
	svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 2"})

	sprints, err := svc.Sprint.List(sqlstore.SprintFilter{})
	require.NoError(t, err)
	assert.Len(t, sprints, 2)
}

// TestSprintArchiveDoesNotChangeStatus covers PRIM-004 AC: archiving is
// independent of status.
func TestSprintArchiveDoesNotChangeStatus(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, err := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 1"})
	require.NoError(t, err)
	require.Equal(t, "active", sprint.Status)

	require.NoError(t, svc.Sprint.Archive(sprint.ID))

	got, err := svc.Sprint.Get(sprint.ID)
	require.NoError(t, err)
	assert.True(t, got.ArchivedAt.Valid)
	assert.Equal(t, "active", got.Status)

	require.NoError(t, svc.Sprint.Unarchive(sprint.ID))

	got, err = svc.Sprint.Get(sprint.ID)
	require.NoError(t, err)
	assert.False(t, got.ArchivedAt.Valid)
	assert.Equal(t, "active", got.Status)
}

// TestSprintListExcludesArchivedByDefault covers PRIM-004 AC: list filters
// default to excluding archived rows unless include_archived is passed.
func TestSprintListExcludesArchivedByDefault(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	s1, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 1"})
	s2, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 2"})
	require.NoError(t, svc.Sprint.Archive(s2.ID))

	sprints, err := svc.Sprint.List(sqlstore.SprintFilter{})
	require.NoError(t, err)
	require.Len(t, sprints, 1)
	assert.Equal(t, s1.ID, sprints[0].ID)

	all, err := svc.Sprint.List(sqlstore.SprintFilter{IncludeArchived: true})
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

// TestSprintBulkUpdate covers PRIM-003's pattern applied to Sprint: the same
// field update lands on every id, and a not-found id fails independently
// without blocking the others (partial success is not an error).
func TestSprintBulkUpdate(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	s1, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint A"})
	s2, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint B"})

	newGoal := "Shared goal"
	succeeded, failed := svc.Sprint.BulkUpdate(
		[]string{s1.ID, s2.ID, "SP-does-not-exist"},
		sqlstore.SprintUpdate{Goal: &newGoal},
		"",
	)
	assert.ElementsMatch(t, []string{s1.ID, s2.ID}, succeeded)
	require.Len(t, failed, 1)
	assert.Equal(t, "SP-does-not-exist", failed[0].ID)

	got1, _ := svc.Sprint.Get(s1.ID)
	got2, _ := svc.Sprint.Get(s2.ID)
	assert.Equal(t, "Shared goal", got1.Goal)
	assert.Equal(t, "Shared goal", got2.Goal)
}

// TestSprintUpdateNameCannotBeCleared verifies SprintService.Update rejects
// an explicit empty name with a clean ValidationError (SWEEP-001, mirroring
// TaskService.Update's title guard added for FIX-001) rather than silently
// persisting an empty name or falling through to a raw DB error.
func TestSprintUpdateNameCannotBeCleared(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	s1, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint A"})

	empty := ""
	err := svc.Sprint.Update(s1.ID, sqlstore.SprintUpdate{Name: &empty})
	require.Error(t, err)
	ve, ok := err.(*service.ValidationError)
	require.True(t, ok, "expected *service.ValidationError, got %T", err)
	assert.Equal(t, "name", ve.Field)

	got, _ := svc.Sprint.Get(s1.ID)
	assert.Equal(t, "Sprint A", got.Name, "name must be unchanged after rejected clear")
}

// TestSprintUpdateCostBudgetExplicitZero verifies cost_budget can be set to
// an explicit 0 via SprintService.Update (SWEEP-001: buildSprintUpdate used
// to treat any zero value as "not provided", so an explicit 0 was silently
// dropped — same bug class FIX-001 fixed for Task).
func TestSprintUpdateCostBudgetExplicitZero(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	budget := 50.0
	s1, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint A", CostBudget: &budget})

	zero := 0.0
	require.NoError(t, svc.Sprint.Update(s1.ID, sqlstore.SprintUpdate{CostBudget: &zero}))

	got, _ := svc.Sprint.Get(s1.ID)
	require.True(t, got.CostBudget.Valid)
	assert.Equal(t, 0.0, got.CostBudget.Float64)
}

// TestSprintBulkUpdateWithStatus covers the combined status-transition +
// field-update path handleSprintBulkUpdate exercises, mirroring
// handleSprintUpdate's single-item semantics (transition first, then field
// update).
func TestSprintBulkUpdateWithStatus(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	s1, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint A"})
	s2, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint B"})

	succeeded, failed := svc.Sprint.BulkUpdate(
		[]string{s1.ID, s2.ID},
		sqlstore.SprintUpdate{},
		"inactive",
	)
	assert.ElementsMatch(t, []string{s1.ID, s2.ID}, succeeded)
	assert.Empty(t, failed)

	got1, _ := svc.Sprint.Get(s1.ID)
	got2, _ := svc.Sprint.Get(s2.ID)
	assert.Equal(t, "inactive", got1.Status)
	assert.Equal(t, "inactive", got2.Status)
}

// TestSprintBulkUpdateRequiresFeature verifies a disabled "sprints" feature
// fails every id uniformly (BulkTag's shared-precondition precedent), rather
// than each id individually erroring out through Transition/Update.
func TestSprintBulkUpdateRequiresFeature(t *testing.T) {
	svc := setupService(t)

	succeeded, failed := svc.Sprint.BulkUpdate([]string{"SP-1", "SP-2"}, sqlstore.SprintUpdate{}, "")
	assert.Empty(t, succeeded)
	require.Len(t, failed, 2)
	assert.IsType(t, &service.FeatureDisabledError{}, failed[0].Err)
	assert.IsType(t, &service.FeatureDisabledError{}, failed[1].Err)
}

// TestSprintListCostBudgetRange and TestSprintListOverBudget exercise
// ENT-SPRINT's budget-range filter through the service layer (store-layer
// coverage lives in sqlstore/sprints_test.go).
func TestSprintListCostBudgetRange(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	svc.Sprint.Create(service.SprintCreateInput{Name: "Cheap", CostBudget: floatPtr(5.0)})
	svc.Sprint.Create(service.SprintCreateInput{Name: "Mid", CostBudget: floatPtr(25.0)})
	svc.Sprint.Create(service.SprintCreateInput{Name: "NoBudget"})

	min := 10.0
	sprints, err := svc.Sprint.List(sqlstore.SprintFilter{CostBudgetMin: &min})
	require.NoError(t, err)
	require.Len(t, sprints, 1)
	assert.Equal(t, "Mid", sprints[0].Name)
}

func TestSprintDelete(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 1"})

	err := svc.Sprint.Delete(sprint.ID)
	require.NoError(t, err)

	_, err = svc.Sprint.Get(sprint.ID)
	assert.Error(t, err)
}

// TestSprintStart covers CW-20260517-0011 edge 5: Start promotes the
// sprint's parked (manual=true) tasks to manual=false so the scheduler can
// dispatch them, and is idempotent on a second call.
func TestSprintStart(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{
		Name:         "Start Sprint",
		ApprovalMode: "approve_sprint",
	})

	// Three tasks in the sprint. Task.Create force-sets manual=true on every
	// create (CW-20260417-0133), so all three start parked; we promote one
	// back to manual=false via Update to exercise the AlreadyEligible path.
	parked1, _ := svc.Task.Create(service.TaskCreateInput{
		Title: "Parked 1", Description: "x", SprintID: sprint.ID,
	})
	parked2, _ := svc.Task.Create(service.TaskCreateInput{
		Title: "Parked 2", Description: "x", SprintID: sprint.ID,
	})
	eligible, _ := svc.Task.Create(service.TaskCreateInput{
		Title: "Eligible", Description: "x", SprintID: sprint.ID,
	})
	manualFalse := false
	require.NoError(t, svc.Task.Update(eligible.ID, service.TaskUpdateInput{
		TaskUpdate: sqlstore.TaskUpdate{Manual: &manualFalse},
	}))

	res, err := svc.Sprint.Start(sprint.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, res.Promoted)
	assert.Equal(t, 1, res.AlreadyEligible)
	assert.ElementsMatch(t, []string{parked1.ID, parked2.ID}, res.PromotedIDs)
	assert.Empty(t, res.Skipped)

	// Tasks are now manual=false and pickable.
	for _, id := range []string{parked1.ID, parked2.ID, eligible.ID} {
		got, _ := svc.Task.Get(id)
		assert.False(t, got.Manual, "task %s should be manual=false after start", id)
	}

	// Idempotent second call.
	res2, err := svc.Sprint.Start(sprint.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, res2.Promoted)
	assert.Equal(t, 3, res2.AlreadyEligible)
}

func TestSprintStartRequiresFeature(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Sprint.Start("SP-1")
	require.Error(t, err)
	assert.IsType(t, &service.FeatureDisabledError{}, err)
}

func TestSprintStartUnknownSprint(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	_, err := svc.Sprint.Start("SP-does-not-exist")
	require.Error(t, err)
}

// Helper
func floatPtr(f float64) *float64 { return &f }
