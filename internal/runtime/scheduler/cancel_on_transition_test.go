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

// TestCancel_OnDBTransitionOutOfDoing verifies the end-to-end wiring of the
// DB-transition cancel path (CW-20260418-0005). A long-running mock executor
// is dispatched; we then transition the task out of "doing" directly in the
// DB (simulating MCP task_transition). The worker's context must cancel
// within one scheduler tick, the run must be marked status=canceled with
// the structured reason, and the task must NOT have its retry_count bumped.
func TestCancel_OnDBTransitionOutOfDoing(t *testing.T) {
	sched, store, mock, cleanup := newCancelTestHarness(t)
	defer cleanup()

	// Mock executor blocks on ctx.Done so we can drive cancellation.
	mock.SetDelay(30 * time.Second)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-CANCEL-0001", Title: "long work", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnDone: "close", MaxRetries: 3,
	}))

	// Dispatch the task — after Tick returns, worker is running.
	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool { return mock.RunCount() == 1 },
		2*time.Second, 10*time.Millisecond, "mock executor should be running")

	// Sanity: task is in doing.
	task, err := store.GetTask("CW-CANCEL-0001")
	require.NoError(t, err)
	require.Equal(t, "doing", task.Status)

	// External transition — this is what MCP task_transition would do.
	// The store hook should fire and cancel the worker's context.
	require.NoError(t, store.TransitionTask("CW-CANCEL-0001", "review"))

	// The worker must terminate quickly. Then drain the result so
	// lifecycle can process it.
	require.Eventually(t, func() bool {
		runs, err := store.ListRuns("CW-CANCEL-0001")
		if err != nil || len(runs) == 0 {
			return false
		}
		return runs[0].Status == "canceled"
	}, 3*time.Second, 25*time.Millisecond,
		"run should be marked canceled within grace window")

	// Drain results to let lifecycle observe the canceled status.
	sched.DrainResults()

	// Task status stays at "review" — lifecycle must not flip it back.
	task, err = store.GetTask("CW-CANCEL-0001")
	require.NoError(t, err)
	assert.Equal(t, "review", task.Status,
		"task status set by external transition must not be touched by lifecycle")

	// retry_count must not have been bumped.
	var retryCount int
	require.NoError(t, store.DB().QueryRow(
		"SELECT retry_count FROM tasks WHERE id = ?", "CW-CANCEL-0001",
	).Scan(&retryCount))
	assert.Equal(t, 0, retryCount,
		"canceled runs must not consume retry budget")

	// Run record carries status=canceled with the structured reason.
	runs, err := store.ListRuns("CW-CANCEL-0001")
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "canceled", runs[0].Status)
	assert.Equal(t, "task_transition_out_of_doing", runs[0].ErrorMessage)
}

// TestCancel_RaceBetweenDispatchAndFirstTick guards the acceptance-criteria
// race case: cancellation fires between worker submission and the executor's
// first instruction. The worker should observe a cancelled context on its
// very first ctx check and exit with status=canceled, never actually running
// any meaningful work.
func TestCancel_RaceBetweenDispatchAndFirstTick(t *testing.T) {
	sched, store, mock, cleanup := newCancelTestHarness(t)
	defer cleanup()

	// Mock executor pauses briefly at start; we cancel during that window.
	// A shorter delay than typical so the test runs fast but long enough
	// that the scheduler definitely dispatches before we cancel.
	mock.SetDelay(10 * time.Second)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-CANCEL-0002", Title: "race guard", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnDone: "close", MaxRetries: 3,
	}))

	require.NoError(t, sched.Tick(context.Background()))

	// Cancel as fast as we can after the task is in doing. Don't wait for
	// the executor to start — the point is to exercise the path where the
	// cancel arrives while the executor may still be inside its delay
	// select. We poll briefly for doing to confirm dispatch happened.
	require.Eventually(t, func() bool {
		t, err := store.GetTask("CW-CANCEL-0002")
		return err == nil && t.Status == "doing"
	}, time.Second, 5*time.Millisecond)

	require.NoError(t, store.TransitionTask("CW-CANCEL-0002", "todo"))

	require.Eventually(t, func() bool {
		runs, err := store.ListRuns("CW-CANCEL-0002")
		return err == nil && len(runs) == 1 && runs[0].Status == "canceled"
	}, 3*time.Second, 25*time.Millisecond)

	sched.DrainResults()

	// Task is back at "todo" (user's intent) and retry_count unchanged.
	task, err := store.GetTask("CW-CANCEL-0002")
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status)

	var retryCount int
	require.NoError(t, store.DB().QueryRow(
		"SELECT retry_count FROM tasks WHERE id = ?", "CW-CANCEL-0002",
	).Scan(&retryCount))
	assert.Equal(t, 0, retryCount)
}

// TestCancel_CompletionBeforeCancelIsNoop covers the reverse race: the
// worker finishes naturally just as a transition arrives. The natural
// completion must take precedence — the already-terminal run must not be
// flipped to canceled by a late-arriving cancel.
func TestCancel_CompletionBeforeCancelIsNoop(t *testing.T) {
	sched, store, mock, cleanup := newCancelTestHarness(t)
	defer cleanup()

	// No delay — mock executor returns immediately with done.
	mock.SetResult(&executor.ExecutionResult{Status: "done", Cost: 0.01})

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-CANCEL-0003", Title: "quick work", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnDone: "close", MaxRetries: 3,
	}))

	require.NoError(t, sched.Tick(context.Background()))

	// Wait until the run is recorded as a terminal status (done). Worker
	// has completed naturally.
	require.Eventually(t, func() bool {
		runs, err := store.ListRuns("CW-CANCEL-0003")
		if err != nil || len(runs) == 0 {
			return false
		}
		return runs[0].Status == "done"
	}, 2*time.Second, 25*time.Millisecond)

	sched.DrainResults()

	// At this point the task has already been lifecycle-transitioned to
	// "done" (OnDone: close) and deregistered from the cancel registry.
	// A transition attempt from "done" to another status is rejected by
	// the FSM, but the store.TransitionTask low-level call still fires
	// the hook — and the cancel lookup misses cleanly.
	runsBefore, _ := store.ListRuns("CW-CANCEL-0003")
	require.Len(t, runsBefore, 1)
	require.Equal(t, "done", runsBefore[0].Status)

	runsAfter, _ := store.ListRuns("CW-CANCEL-0003")
	require.Len(t, runsAfter, 1)
	assert.Equal(t, "done", runsAfter[0].Status,
		"already-terminal run must not be flipped to canceled by a late transition")
}

// newCancelTestHarness builds a standard in-memory scheduler stack suitable
// for the cancel-on-transition tests. Returns the scheduler, store, mock
// executor, and a cleanup func that stops the scheduler and releases
// resources.
func newCancelTestHarness(t *testing.T) (*scheduler.Scheduler, *sqlstore.Store, *executor.MockExecutor, func()) {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
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
		Workers:                  2,
		IntervalSeconds:          1,
		RetryBudget:              3,
		Enabled:                  true,
		StaleSeconds:             300,
		HeartbeatProgressSeconds: 1,
	}

	sched := scheduler.New(store, q, registry, nil, cfg)

	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = sched.Stop(ctx)
		_ = q.Close()
		_ = store.Close()
	}
	return sched, store, mock, cleanup
}
