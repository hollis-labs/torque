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
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	_ "modernc.org/sqlite"
)

// TestWorkerPoolNoGoroutineLeak is a regression guard for the original ticket
// intent (CW-20260417-0192): goroutines spawned by the worker pool must exit
// cleanly on Shutdown. The current architecture (internal/runtime/scheduler)
// wires each Submit()'d worker to the pool's context; Shutdown cancels it and
// WaitGroups ensure all workers return before Shutdown returns. This test
// locks that behavior in so future refactors cannot silently leak.
//
// goleak.IgnoreCurrent() is captured at the start of the test to exclude
// goroutines the test runtime itself (or previously-imported packages like
// modernc.org/sqlite) started before we did.
func TestWorkerPoolNoGoroutineLeak(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	pool := scheduler.NewWorkerPool(3)

	results := make(chan scheduler.WorkerResult, 5)
	pool.OnResult(func(r scheduler.WorkerResult) {
		results <- r
	})

	// Mix of fast work and work that observes ctx cancellation — exercises
	// both the natural-return path (fn finishes) and the pool-context-cancel
	// path (fn returns because of <-ctx.Done()).
	for i := 0; i < 3; i++ {
		pool.Submit("fast", int64(i), func(ctx context.Context) (*executor.ExecutionResult, error) {
			return &executor.ExecutionResult{Status: "done"}, nil
		})
	}
	pool.Submit("slow", 99, func(ctx context.Context) (*executor.ExecutionResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return &executor.ExecutionResult{Status: "done"}, nil
		}
	})

	// Drain the three fast results so the semaphore isn't saturated when we
	// shut down. The slow worker is still running; Shutdown must cancel it.
	for i := 0; i < 3; i++ {
		<-results
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, pool.Shutdown(shutdownCtx))

	// Drain the cancelled slow result so the OnResult callback goroutine
	// doesn't block on a full buffer (defensive — results is buffered 5).
	select {
	case <-results:
	case <-time.After(time.Second):
	}
}

// TestSchedulerNoGoroutineLeak exercises the full scheduler path: New(),
// Tick() dispatching a mock-executed task, DrainResults(), and Stop(). The
// dispatched task path exercises progress_heartbeat (one goroutine per run,
// cancelled in the worker closure's defer) and the WorkerPool. A goroutine
// leaking from any of these tear-downs fails the test.
func TestSchedulerNoGoroutineLeak(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	defer store.Close()

	dir := t.TempDir()
	q, err := queue.Open(filepath.Join(dir, "queue.db"))
	require.NoError(t, err)
	defer q.Close()

	mock := executor.NewMockExecutor()
	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:                  2,
		IntervalSeconds:          1,
		RetryBudget:              3,
		CostCeiling:              0,
		HeartbeatSeconds:         15,
		StaleSeconds:             300,
		HeartbeatProgressSeconds: 1,
		Enabled:                  true,
	}

	sched := scheduler.New(store, q, registry, nil, cfg)

	mock.SetResult(&executor.ExecutionResult{
		Status: "done",
		Cost:   0.01,
		Tokens: executor.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
	})

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-GOLEAK-1", Title: "leak guard", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnDone: "close",
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-GOLEAK-2", Title: "leak guard 2", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnDone: "close",
	}))

	require.NoError(t, sched.Tick(context.Background()))

	// Give workers time to complete and progress_heartbeat a chance to tick
	// (interval is 1s; we wait long enough for at least one publish). If the
	// heartbeat goroutine isn't correctly torn down when the worker returns,
	// goleak will catch it after Stop().
	time.Sleep(1200 * time.Millisecond)
	sched.DrainResults()

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, sched.Stop(stopCtx))
}
