package service_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"

	_ "modernc.org/sqlite"
)

func setupService(t *testing.T) *service.Service {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return service.New(store)
}

func TestTaskCreate(t *testing.T) {
	svc := setupService(t)

	task, err := svc.Task.Create(service.TaskCreateInput{
		Title:    "My first task",
		Priority: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, task)
	require.True(t, strings.Contains(task.ID, "CW-"), "ID should contain CW-")
	require.Equal(t, "todo", task.Status)
	require.Equal(t, 1, task.Priority)
}

func TestTaskCreateValidation(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Task.Create(service.TaskCreateInput{Title: ""})
	require.Error(t, err)

	var ve *service.ValidationError
	require.ErrorAs(t, err, &ve)
}

func TestTaskTransitionValid(t *testing.T) {
	svc := setupService(t)

	task, err := svc.Task.Create(service.TaskCreateInput{Title: "Transition test"})
	require.NoError(t, err)

	err = svc.Task.Transition(task.ID, "doing")
	require.NoError(t, err)

	updated, err := svc.Task.Get(task.ID)
	require.NoError(t, err)
	require.Equal(t, "doing", updated.Status)
}

func TestTaskTransitionInvalid(t *testing.T) {
	svc := setupService(t)

	task, err := svc.Task.Create(service.TaskCreateInput{Title: "Invalid transition test"})
	require.NoError(t, err)

	// todo → done is not a valid transition
	err = svc.Task.Transition(task.ID, "done")
	require.Error(t, err)

	var te *service.TransitionError
	require.ErrorAs(t, err, &te)
}

func TestTaskSearch(t *testing.T) {
	svc := setupService(t)

	_, err := svc.Task.Create(service.TaskCreateInput{Title: "Alpha task about widgets"})
	require.NoError(t, err)

	_, err = svc.Task.Create(service.TaskCreateInput{Title: "Beta task about gadgets"})
	require.NoError(t, err)

	results, err := svc.Task.Search("widgets")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Contains(t, results[0].Title, "widgets")
}
