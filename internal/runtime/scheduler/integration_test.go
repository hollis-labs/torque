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

func TestIntegrationFullRoundTrip(t *testing.T) {
	// Setup
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
	mock.SetResult(&executor.ExecutionResult{
		Status: "done",
		Cost:   0.05,
		Tokens: executor.TokenUsage{PromptTokens: 500, CompletionTokens: 200},
		Artifacts: []executor.Artifact{
			{Type: "diff", Content: "--- a/auth.go\n+++ b/auth.go\n@@ -1 +1 @@"},
			{Type: "test-results", Content: "PASS: TestAuth (0.5s)"},
		},
	})

	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:         2,
		IntervalSeconds: 1,
		RetryBudget:     3,
		Enabled:         true,
		StaleSeconds:    300,
	}

	sched := scheduler.New(store, q, registry, cfg)

	// Subscribe to events
	sub := sched.EventBus().Subscribe()

	// Create a task with deliverables
	store.CreateTask(&sqlstore.TaskRecord{
		ID:          "CW-20260407-0001",
		Title:       "Fix auth endpoint",
		Description: "The login endpoint returns 500 on invalid tokens. Fix the validation logic.",
		Status:      "todo",
		Priority:    1,
		Executor:    "mock",
		OnDone:      "close",
		MaxRetries:  3,
		Deliverables: sql.NullString{
			String: `[{"type":"diff","required":true},{"type":"test-results","required":true}]`,
			Valid:  true,
		},
	})

	// Run scheduler tick
	err = sched.Tick(context.Background())
	require.NoError(t, err)

	// Wait for execution to complete
	time.Sleep(300 * time.Millisecond)

	// Process results
	sched.DrainResults()

	// Verify task reached done
	task, err := store.GetTask("CW-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "done", task.Status, "task should be done — all deliverables present")

	// Verify run was created and completed
	runs, err := store.ListRuns("CW-20260407-0001")
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(runs), 1)
	assert.Equal(t, "done", runs[0].Status)
	// The lifecycle transition to "done" must have been keyed on a real run ID,
	// not the historical hardcoded 0. See fix(scheduler): thread RunID through WorkerResult.
	assert.Greater(t, runs[0].ID, int64(0), "run should have a non-zero ID threaded through the worker pool")

	// Verify executor received the job
	jobs := mock.RecordedJobs()
	assert.Len(t, jobs, 1)
	assert.Equal(t, "CW-20260407-0001", jobs[0].TaskID)
	assert.Contains(t, jobs[0].Description, "login endpoint")

	// Verify events were emitted
	var events []scheduler.SchedulerEvent
	timeout := time.After(time.Second)
	for {
		select {
		case e := <-sub:
			events = append(events, e)
		case <-timeout:
			goto checkEvents
		}
	}
checkEvents:
	eventTypes := make(map[string]bool)
	for _, e := range events {
		eventTypes[e.Type] = true
	}
	assert.True(t, eventTypes["task.transitioned"], "should have task.transitioned event")
	assert.True(t, eventTypes["run.started"], "should have run.started event")

	// Verify run events were persisted to the run_events table.
	// Expect at least: the initial task_transitioned (todo → doing) and
	// run_started written from dispatchTask, run_completed written after the
	// worker returns, and another task_transitioned from the lifecycle
	// manager's final doing → done transition.
	persistedEvents, err := store.ListRunEvents(sqlstore.RunEventFilter{RunID: runs[0].ID, Limit: 100})
	require.NoError(t, err)
	persistedTypes := make(map[string]int)
	var runStartedIdx, runCompletedIdx = -1, -1
	for i, e := range persistedEvents {
		persistedTypes[e.Type]++
		if e.Type == "run_started" && runStartedIdx == -1 {
			runStartedIdx = i
		}
		if e.Type == "run_completed" && runCompletedIdx == -1 {
			runCompletedIdx = i
		}
	}
	assert.GreaterOrEqual(t, persistedTypes["task_transitioned"], 1, "should have task_transitioned run events")
	assert.Equal(t, 1, persistedTypes["run_started"], "should have exactly one run_started run event")
	assert.Equal(t, 1, persistedTypes["run_completed"], "should have exactly one run_completed run event")
	assert.NotEqual(t, -1, runStartedIdx, "run_started should be present")
	assert.NotEqual(t, -1, runCompletedIdx, "run_completed should be present")
	assert.Less(t, runStartedIdx, runCompletedIdx, "run_started should come before run_completed")

	// Stop scheduler before closing store to avoid use-after-close
	sched.EventBus().Unsubscribe(sub)
	sched.Stop(context.Background())
	q.Close()
	store.Close()
}

func TestIntegrationEscalationChain(t *testing.T) {
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
	mock.SetResult(&executor.ExecutionResult{
		Status: "failed",
		Reason: "could not resolve issue",
	})

	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:         1,
		IntervalSeconds: 1,
		RetryBudget:     3,
		Enabled:         true,
		StaleSeconds:    300,
	}

	sched := scheduler.New(store, q, registry, cfg)

	// Create task with escalation chain
	store.CreateTask(&sqlstore.TaskRecord{
		ID:              "CW-20260407-0001",
		Title:           "Hard problem",
		Description:     "This requires multiple attempts",
		Status:          "todo",
		Priority:        1,
		Executor:        "mock",
		OnFail:          "escalate",
		EscalationChain: sql.NullString{String: `["retry","human"]`, Valid: true},
	})

	// First tick: task gets picked, executor fails, escalation step 0 = retry -> back to todo
	sched.Tick(context.Background())
	time.Sleep(200 * time.Millisecond)
	sched.DrainResults()

	task, _ := store.GetTask("CW-20260407-0001")
	assert.Equal(t, "todo", task.Status, "first escalation step (retry) should go back to todo")

	// Second tick: task gets picked again, fails again, escalation step 1 = human -> blocked
	sched.Tick(context.Background())
	time.Sleep(200 * time.Millisecond)
	sched.DrainResults()

	task, _ = store.GetTask("CW-20260407-0001")
	assert.Equal(t, "blocked", task.Status, "second escalation step (human) should block")

	// Stop scheduler before closing store
	sched.Stop(context.Background())
	q.Close()
	store.Close()
}

func TestIntegrationCostCeiling(t *testing.T) {
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
	mock.SetResult(&executor.ExecutionResult{
		Status: "done",
		Cost:   5.00,
	})

	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:         1,
		IntervalSeconds: 1,
		RetryBudget:     3,
		CostCeiling:     4.00, // Set ceiling below what the task costs
		Enabled:         true,
		StaleSeconds:    300,
	}

	sched := scheduler.New(store, q, registry, cfg)

	// Run first task
	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Expensive task", Status: "todo",
		Priority: 1, Executor: "mock", OnDone: "close",
	})

	sched.Tick(context.Background())
	time.Sleep(200 * time.Millisecond)
	sched.DrainResults()

	// Create second task
	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0002", Title: "Second task", Status: "todo",
		Priority: 1, Executor: "mock", OnDone: "close",
	})

	// Second tick should skip because cost ceiling exceeded
	sched.Tick(context.Background())
	time.Sleep(200 * time.Millisecond)
	sched.DrainResults()

	task2, _ := store.GetTask("CW-0002")
	assert.Equal(t, "todo", task2.Status, "second task should not be picked — cost ceiling exceeded")

	// Stop scheduler before closing store to avoid use-after-close
	sched.Stop(context.Background())
	q.Close()
	store.Close()
}
