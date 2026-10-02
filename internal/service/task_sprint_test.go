package service_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
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

func TestTaskCreateSprintStatus(t *testing.T) {
	for _, status := range []string{"active", "inactive", "completed"} {
		t.Run(status, func(t *testing.T) {
			svc := setupService(t)
			require.NoError(t, svc.Feature.Enable("sprints"))
			sprint, err := svc.Sprint.Create(service.SprintCreateInput{Name: "Sprint"})
			require.NoError(t, err)
			if status != "active" {
				require.NoError(t, svc.Sprint.Transition(sprint.ID, status))
			}
			task, err := svc.Task.Create(service.TaskCreateInput{
				Title: "Task", Description: "Sprint insertion", SprintID: sprint.ID, Manual: true,
			})
			if status == "completed" {
				require.Nil(t, task)
				var validation *service.ValidationError
				require.ErrorAs(t, err, &validation)
				assert.Equal(t, "sprint_id", validation.Field)
				assert.Equal(t, "cannot add tasks to a completed sprint", validation.Message)
				tasks, listErr := svc.Task.List(sqlstore.TaskFilter{SprintID: sprint.ID})
				require.NoError(t, listErr)
				assert.Empty(t, tasks)
				return
			}
			require.NoError(t, err)
			persisted, err := svc.Task.Get(task.ID)
			require.NoError(t, err)
			assert.True(t, persisted.SprintID.Valid)
			assert.Equal(t, sprint.ID, persisted.SprintID.String)
		})
	}
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
	project, _ := svc.Project.Create(service.ProjectCreateInput{Name: "Project", RepoPath: t.TempDir()})
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
