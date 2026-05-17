package service_test

import (
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
	svc.Task.Transition(task1.ID, "doing")
	svc.Task.Transition(task1.ID, "review")
	svc.Task.Transition(task2.ID, "doing")
	svc.Task.Transition(task2.ID, "review")

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
	svc.Task.Transition(task.ID, "doing")
	svc.Task.Transition(task.ID, "review")

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
	svc.Task.Transition(task.ID, "doing")
	svc.Task.Transition(task.ID, "review")

	err := svc.Sprint.ApproveTask(sprint.ID, task.ID)
	assert.Error(t, err)
}

func TestSprintList(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 1"})
	svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 2"})

	sprints, err := svc.Sprint.List("", "")
	require.NoError(t, err)
	assert.Len(t, sprints, 2)
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
