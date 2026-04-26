package service_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskService_AddAndListSubtodos(t *testing.T) {
	svc := setupService(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "t1"})
	require.NoError(t, err)

	got, err := svc.Task.AddSubtodo(task.ID, sqlstore.Subtodo{ID: "a", Text: "first", Required: true})
	require.NoError(t, err)
	require.Len(t, got, 1)

	got, err = svc.Task.AddSubtodo(task.ID, sqlstore.Subtodo{ID: "b", Text: "second"})
	require.NoError(t, err)
	require.Len(t, got, 2)

	all, err := svc.Task.ListSubtodos(task.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, []string{all[0].ID, all[1].ID})
}

func TestTaskService_AddSubtodoRejectsDuplicateID(t *testing.T) {
	svc := setupService(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "t1"})
	require.NoError(t, err)

	_, err = svc.Task.AddSubtodo(task.ID, sqlstore.Subtodo{ID: "x", Text: "one"})
	require.NoError(t, err)

	_, err = svc.Task.AddSubtodo(task.ID, sqlstore.Subtodo{ID: "x", Text: "dup"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestTaskService_AddSubtodoRejectsMissingFields(t *testing.T) {
	svc := setupService(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "t1"})
	require.NoError(t, err)

	_, err = svc.Task.AddSubtodo(task.ID, sqlstore.Subtodo{Text: "no id"})
	require.Error(t, err)
	_, err = svc.Task.AddSubtodo(task.ID, sqlstore.Subtodo{ID: "a"})
	require.Error(t, err)
}

func TestTaskService_MarkSubtodoDone(t *testing.T) {
	svc := setupService(t)
	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:    "t1",
		Subtodos: []sqlstore.Subtodo{{ID: "a", Text: "do", Required: true}},
	})
	require.NoError(t, err)

	got, err := svc.Task.MarkSubtodoDone(task.ID, "a", "commit-abc")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, got[0].Done)
	assert.Equal(t, "commit-abc", got[0].Evidence)
}

func TestTaskService_UpdateSubtodo(t *testing.T) {
	svc := setupService(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "t1"})
	require.NoError(t, err)
	_, err = svc.Task.AddSubtodo(task.ID, sqlstore.Subtodo{ID: "a", Text: "old", Required: false})
	require.NoError(t, err)

	// Update both fields.
	newText := "new text"
	required := true
	got, err := svc.Task.UpdateSubtodo(task.ID, "a", &newText, &required)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "new text", got[0].Text)
	assert.True(t, got[0].Required)

	// Partial: leave text untouched, flip required back.
	required = false
	got, err = svc.Task.UpdateSubtodo(task.ID, "a", nil, &required)
	require.NoError(t, err)
	assert.Equal(t, "new text", got[0].Text)
	assert.False(t, got[0].Required)

	// Empty text rejected.
	empty := ""
	_, err = svc.Task.UpdateSubtodo(task.ID, "a", &empty, nil)
	require.Error(t, err)

	// Unknown id rejected.
	_, err = svc.Task.UpdateSubtodo(task.ID, "missing", &newText, nil)
	require.Error(t, err)
}

func TestTaskService_DeleteSubtodo(t *testing.T) {
	svc := setupService(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "t1"})
	require.NoError(t, err)
	_, err = svc.Task.AddSubtodo(task.ID, sqlstore.Subtodo{ID: "a", Text: "first"})
	require.NoError(t, err)
	_, err = svc.Task.AddSubtodo(task.ID, sqlstore.Subtodo{ID: "b", Text: "second"})
	require.NoError(t, err)

	got, err := svc.Task.DeleteSubtodo(task.ID, "a")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].ID)

	// Deleting last item leaves empty list.
	got, err = svc.Task.DeleteSubtodo(task.ID, "b")
	require.NoError(t, err)
	assert.Empty(t, got)

	// Unknown id rejected.
	_, err = svc.Task.DeleteSubtodo(task.ID, "missing")
	require.Error(t, err)
}
