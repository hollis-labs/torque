package service_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskCreate_AutoExtractsCheckboxSubtodos(t *testing.T) {
	svc, store := setupServiceWithStore(t)

	task, err := svc.Task.Create(service.TaskCreateInput{
		Title: "Has checklist",
		Description: "Intro.\n\n" +
			"- [ ] do first\n" +
			"- [x] already done\n",
	})
	require.NoError(t, err)

	got, err := store.GetSubtodos(task.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "do first", got[0].Text)
	assert.False(t, got[0].Done)
	assert.True(t, got[1].Done)
}

func TestTaskCreate_NoCheckboxesNoSubtodos(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:       "Prose only",
		Description: "nothing structural here",
	})
	require.NoError(t, err)

	got, err := store.GetSubtodos(task.ID)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestTaskCreate_ExplicitSubtodosOverrideExtraction(t *testing.T) {
	svc, store := setupServiceWithStore(t)
	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:       "explicit wins",
		Description: "- [ ] extracted\n", // would auto-extract
		Subtodos: []sqlstore.Subtodo{
			{ID: "explicit", Text: "caller-provided", Required: true},
		},
	})
	require.NoError(t, err)

	got, err := store.GetSubtodos(task.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "explicit", got[0].ID)
	assert.True(t, got[0].Required)
}
