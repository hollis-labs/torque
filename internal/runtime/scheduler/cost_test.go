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

func setupCostStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestCostTrackerRecord(t *testing.T) {
	store := setupCostStore(t)
	tracker := scheduler.NewCostTracker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	err := tracker.Record(scheduler.CostEntry{
		TaskID:           "CW-0001",
		RunID:            1,
		Cost:             0.05,
		PromptTokens:     1000,
		CompletionTokens: 500,
	})
	require.NoError(t, err)
}

func TestCostTrackerTaskTotal(t *testing.T) {
	store := setupCostStore(t)
	tracker := scheduler.NewCostTracker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, Cost: 0.05})
	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, Cost: 0.03})

	total, err := tracker.TaskTotal("CW-0001")
	require.NoError(t, err)
	assert.InDelta(t, 0.08, total, 0.001)
}

func TestCostTrackerSprintTotal(t *testing.T) {
	store := setupCostStore(t)
	tracker := scheduler.NewCostTracker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli",
		SprintID: sql.NullString{String: "sprint-1", Valid: true}})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, SprintID: "sprint-1", Cost: 0.10})
	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, SprintID: "sprint-1", Cost: 0.15})

	total, err := tracker.SprintTotal("sprint-1")
	require.NoError(t, err)
	assert.InDelta(t, 0.25, total, 0.001)
}

func TestCostTrackerGlobalTotal(t *testing.T) {
	store := setupCostStore(t)
	tracker := scheduler.NewCostTracker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "A", Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "B", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0002", Executor: "cli", Status: "running"})

	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, Cost: 0.10})
	tracker.Record(scheduler.CostEntry{TaskID: "CW-0002", RunID: 2, Cost: 0.20})

	total, err := tracker.GlobalTotal()
	require.NoError(t, err)
	assert.InDelta(t, 0.30, total, 0.001)
}

func TestCostTrackerWithinBudget(t *testing.T) {
	store := setupCostStore(t)
	tracker := scheduler.NewCostTracker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, Cost: 0.10})

	// Within budget
	ok, err := tracker.WithinGlobalBudget(1.00)
	require.NoError(t, err)
	assert.True(t, ok)

	// Over budget
	ok, err = tracker.WithinGlobalBudget(0.05)
	require.NoError(t, err)
	assert.False(t, ok)

	// Zero ceiling means no limit
	ok, err = tracker.WithinGlobalBudget(0)
	require.NoError(t, err)
	assert.True(t, ok)
}
