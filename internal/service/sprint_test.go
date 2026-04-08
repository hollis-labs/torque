package service_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
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
		Goal:         "Ship auth",
		ApprovalMode: "approve_each",
		CostBudget:   floatPtr(50.0),
	})
	require.NoError(t, err)
	assert.Contains(t, sprint.ID, "SP-")
	assert.Equal(t, "planning", sprint.Status)
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

	// planning -> active
	err := svc.Sprint.Transition(sprint.ID, "active")
	require.NoError(t, err)
	got, _ := svc.Sprint.Get(sprint.ID)
	assert.Equal(t, "active", got.Status)

	// active -> completed
	err = svc.Sprint.Transition(sprint.ID, "completed")
	require.NoError(t, err)
	got, _ = svc.Sprint.Get(sprint.ID)
	assert.Equal(t, "completed", got.Status)
}

func TestSprintTransitionInvalid(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 1"})

	// planning -> completed (invalid — must go through active)
	err := svc.Sprint.Transition(sprint.ID, "completed")
	assert.Error(t, err)
	assert.IsType(t, &service.TransitionError{}, err)

	// planning -> planning (no-op but invalid)
	err = svc.Sprint.Transition(sprint.ID, "planning")
	assert.Error(t, err)
}

func TestSprintTransitionBackwardsInvalid(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint 1"})
	svc.Sprint.Transition(sprint.ID, "active")

	// active -> planning (backwards, invalid)
	err := svc.Sprint.Transition(sprint.ID, "planning")
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
	svc.Sprint.Transition(sprint.ID, "active")

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
	svc.Sprint.Transition(sprint.ID, "active")

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
	svc.Sprint.Transition(sprint.ID, "active")

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

	sprints, err := svc.Sprint.List("")
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

// Helper
func floatPtr(f float64) *float64 { return &f }
