package scheduler_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/queue"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupScheduler(t *testing.T) (*scheduler.Scheduler, *sqlstore.Store, *executor.MockExecutor) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1) // in-memory SQLite is per-connection; force single conn
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	dir := t.TempDir()
	q, err := queue.Open(filepath.Join(dir, "queue.db"))
	require.NoError(t, err)

	mock := executor.NewMockExecutor()
	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:          2,
		IntervalSeconds:  1,
		RetryBudget:      3,
		CostCeiling:      0,
		HeartbeatSeconds: 15,
		StaleSeconds:     300,
		Enabled:          true,
	}

	sched := scheduler.New(store, q, registry, cfg)

	t.Cleanup(func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	})

	return sched, store, mock
}

func TestSchedulerPicksAndExecutesTasks(t *testing.T) {
	sched, store, mock := setupScheduler(t)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Test task", Status: "todo", Priority: 1,
		Executor: "mock", OnDone: "close",
	})

	mock.SetResult(&executor.ExecutionResult{
		Status: "done",
		Cost:   0.05,
		Tokens: executor.TokenUsage{PromptTokens: 100, CompletionTokens: 50},
	})

	// Run one tick
	err := sched.Tick(context.Background())
	require.NoError(t, err)

	// Wait for worker to complete
	time.Sleep(200 * time.Millisecond)

	// Process results
	sched.DrainResults()

	task, err := store.GetTask("CW-0001")
	require.NoError(t, err)
	assert.Equal(t, "done", task.Status)

	jobs := mock.RecordedJobs()
	assert.Len(t, jobs, 1)
	assert.Equal(t, "CW-0001", jobs[0].TaskID)
}

func TestSchedulerSkipsManualTasks(t *testing.T) {
	sched, store, mock := setupScheduler(t)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Manual task", Status: "todo", Priority: 1,
		Executor: "mock", Manual: true,
	})

	sched.Tick(context.Background())
	time.Sleep(100 * time.Millisecond)
	sched.DrainResults()

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "todo", task.Status, "manual task should not be picked")
	assert.Len(t, mock.RecordedJobs(), 0)
}

func TestSchedulerHandlesFailure(t *testing.T) {
	sched, store, mock := setupScheduler(t)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Failing task", Status: "todo", Priority: 1,
		Executor: "mock", OnFail: "retry", MaxRetries: 3,
	})

	mock.SetResult(&executor.ExecutionResult{
		Status: "failed",
		Reason: "something went wrong",
	})

	sched.Tick(context.Background())
	time.Sleep(200 * time.Millisecond)
	sched.DrainResults()

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "todo", task.Status, "failed task with retry should go back to todo")
}

func TestSchedulerStatus(t *testing.T) {
	sched, _, _ := setupScheduler(t)

	status := sched.Status()
	assert.Equal(t, 2, status.MaxWorkers)
	assert.Equal(t, 0, status.ActiveWorkers)
	assert.True(t, status.Enabled)
}

func TestSchedulerToggle(t *testing.T) {
	sched, _, _ := setupScheduler(t)

	sched.SetEnabled(false)
	status := sched.Status()
	assert.False(t, status.Enabled)

	sched.SetEnabled(true)
	status = sched.Status()
	assert.True(t, status.Enabled)
}

func TestSchedulerRespectsDisabled(t *testing.T) {
	sched, store, mock := setupScheduler(t)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "todo", Priority: 1,
		Executor: "mock",
	})

	sched.SetEnabled(false)
	sched.Tick(context.Background())
	time.Sleep(100 * time.Millisecond)
	sched.DrainResults()

	assert.Len(t, mock.RecordedJobs(), 0, "disabled scheduler should not pick tasks")
}

func TestSchedulerEventBus(t *testing.T) {
	sched, store, mock := setupScheduler(t)

	sub := sched.EventBus().Subscribe()
	defer sched.EventBus().Unsubscribe(sub)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "todo", Priority: 1,
		Executor: "mock", OnDone: "close",
	})

	mock.SetResult(&executor.ExecutionResult{Status: "done"})

	sched.Tick(context.Background())
	time.Sleep(200 * time.Millisecond)
	sched.DrainResults()

	var events []scheduler.SchedulerEvent
	timeout := time.After(time.Second)
	for {
		select {
		case e := <-sub:
			events = append(events, e)
		case <-timeout:
			goto done
		}
	}
done:
	assert.GreaterOrEqual(t, len(events), 1, "should emit at least one event")
}
