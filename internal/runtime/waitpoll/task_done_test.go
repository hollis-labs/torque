package waitpoll_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/waitpoll"
)

func setupPredicateStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestTaskDone_Evaluate_True(t *testing.T) {
	store := setupPredicateStore(t)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-T-DONE", Title: "done one", Status: "done",
	}))

	p := waitpoll.NewTaskDone(store)
	assert.Equal(t, "task_done", p.Type())
	ok, err := p.Evaluate(context.Background(), map[string]any{"task_id": "CW-T-DONE"})
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestTaskDone_Evaluate_False(t *testing.T) {
	store := setupPredicateStore(t)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-T-TODO", Title: "still todo", Status: "todo",
	}))

	p := waitpoll.NewTaskDone(store)
	ok, err := p.Evaluate(context.Background(), map[string]any{"task_id": "CW-T-TODO"})
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestTaskDone_Evaluate_MissingTask(t *testing.T) {
	store := setupPredicateStore(t)
	p := waitpoll.NewTaskDone(store)
	_, err := p.Evaluate(context.Background(), map[string]any{"task_id": "CW-NONE"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrTaskNotFound))
}

func TestTaskDone_Validate(t *testing.T) {
	p := waitpoll.NewTaskDone(nil)
	require.Error(t, p.Validate(map[string]any{}))
	require.Error(t, p.Validate(map[string]any{"task_id": ""}))
	require.NoError(t, p.Validate(map[string]any{"task_id": "CW-1"}))
}
