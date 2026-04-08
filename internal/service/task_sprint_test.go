package service_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskCreateValidatesSprintExists(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:       "Task",
		Description: "In a nonexistent sprint",
		SprintID:    "SP-99999999-9999",
	})
	assert.Error(t, err)
}

func TestTaskCreateRejectsCompletedSprint(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint"})
	svc.Sprint.Transition(sprint.ID, "active")
	svc.Sprint.Transition(sprint.ID, "completed")

	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:       "Task",
		Description: "In a completed sprint",
		SprintID:    sprint.ID,
	})
	assert.Error(t, err)
	assert.IsType(t, &service.ValidationError{}, err)
}

func TestTaskCreateValidatesProjectExists(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("projects")

	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:       "Task",
		Description: "In a nonexistent project",
		ProjectID:   "PRJ-99999999-9999",
	})
	assert.Error(t, err)
}

func TestTaskCreateValidatesEpicExists(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("epics")

	_, err := svc.Task.Create(service.TaskCreateInput{
		Title:       "Task",
		Description: "In a nonexistent epic",
		EpicID:      "EP-99999999-9999",
	})
	assert.Error(t, err)
}

func TestTaskCreateInActiveSprint(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Active Sprint"})
	svc.Sprint.Transition(sprint.ID, "active")

	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:       "Task",
		Description: "In an active sprint",
		SprintID:    sprint.ID,
	})
	require.NoError(t, err)
	assert.True(t, task.SprintID.Valid)
	assert.Equal(t, sprint.ID, task.SprintID.String)
}

func TestTaskCreateWithAllAssociations(t *testing.T) {
	svc := setupService(t)
	svc.Feature.Enable("sprints")
	svc.Feature.Enable("projects")
	svc.Feature.Enable("epics")

	sprint, _ := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint"})
	project, _ := svc.Project.Create(service.ProjectCreateInput{Name: "Project"})
	epic, _ := svc.Epic.Create(service.EpicCreateInput{Name: "Epic"})

	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:       "Fully associated task",
		Description: "Has sprint, project, and epic",
		SprintID:    sprint.ID,
		ProjectID:   project.ID,
		EpicID:      epic.ID,
	})
	require.NoError(t, err)
	assert.True(t, task.SprintID.Valid)
	assert.True(t, task.ProjectID.Valid)
	assert.True(t, task.EpicID.Valid)
}

func TestTaskCreateSprintIdIgnoredWhenFeatureDisabled(t *testing.T) {
	svc := setupService(t)
	// sprints NOT enabled

	// Providing a sprint_id when feature is disabled should silently ignore it
	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:       "Task",
		Description: "Sprint ID ignored",
		SprintID:    "SP-20260407-0001",
	})
	require.NoError(t, err)
	// sprint_id should be cleared since feature is not enabled
	assert.False(t, task.SprintID.Valid)
}
