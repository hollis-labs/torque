package scheduler_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupPickerStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestPickerEligibleTasks(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task A", Status: "todo", Priority: 2, Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "Task B", Status: "todo", Priority: 1, Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0003", Title: "Task C", Status: "doing", Priority: 1, Executor: "cli"})

	tasks, err := picker.Pick(3)
	require.NoError(t, err)
	assert.Len(t, tasks, 2, "only todo tasks are eligible")
	assert.Equal(t, "CW-0002", tasks[0].ID, "P1 comes before P2")
	assert.Equal(t, "CW-0001", tasks[1].ID)
}

func TestPickerSkipsManualTasks(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Auto", Status: "todo", Priority: 2, Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "Manual", Status: "todo", Priority: 1, Manual: true, Executor: "cli"})

	tasks, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "CW-0001", tasks[0].ID)
}

func TestPickerRespectsLimit(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "A", Status: "todo", Priority: 1, Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "B", Status: "todo", Priority: 1, Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0003", Title: "C", Status: "todo", Priority: 1, Executor: "cli"})

	tasks, err := picker.Pick(2)
	require.NoError(t, err)
	assert.Len(t, tasks, 2)
}

func TestPickerSkipsBlockedDependencies(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Prerequisite", Status: "todo", Priority: 1, Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "Dependent", Status: "todo", Priority: 1, Executor: "cli",
		DependsOn: sql.NullString{String: `["CW-0001"]`, Valid: true}})

	tasks, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, tasks, 1, "dependent task should not be picked until dependency is done")
	assert.Equal(t, "CW-0001", tasks[0].ID)
}

func TestPickerAllowsDependencyMet(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Prerequisite", Status: "done", Priority: 1, Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "Dependent", Status: "todo", Priority: 1, Executor: "cli",
		DependsOn: sql.NullString{String: `["CW-0001"]`, Valid: true}})

	tasks, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "CW-0002", tasks[0].ID)
}

func TestPickerEmptyStore(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	tasks, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, tasks, 0)
}
