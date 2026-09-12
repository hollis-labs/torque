package scheduler_test

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/queue"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupScheduler(t *testing.T) (*scheduler.Scheduler, *sqlstore.Store, *executor.MockExecutor) {
	t.Helper()

	store := sqlitetest.OpenStore(t)

	dir := t.TempDir()
	q, err := queue.Open(context.Background(), filepath.Join(dir, "queue.db"))
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

	sched := scheduler.New(store, q, registry, nil, cfg)

	t.Cleanup(func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	})

	return sched, store, mock
}

type cancelResultExecutor struct {
	mu       sync.Mutex
	result   *executor.ExecutionResult
	runCount int
}

func (e *cancelResultExecutor) Name() string { return "cause" }

func (e *cancelResultExecutor) Run(ctx context.Context, _ *executor.ExecutionJob, _ executor.EventCallback) (*executor.ExecutionResult, error) {
	e.mu.Lock()
	e.runCount++
	e.mu.Unlock()
	<-ctx.Done()
	if e.result == nil {
		return nil, nil
	}
	cp := *e.result
	return &cp, nil
}

func (e *cancelResultExecutor) Validate(*executor.ExecutionJob) error { return nil }

func (e *cancelResultExecutor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{SupportsStreaming: true}
}

func (e *cancelResultExecutor) RunCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.runCount
}

func setupShutdownCauseScheduler(t *testing.T, result *executor.ExecutionResult) (*scheduler.Scheduler, *sqlstore.Store, *cancelResultExecutor, func()) {
	t.Helper()
	store := sqlitetest.OpenStore(t)
	dir := t.TempDir()
	q, err := queue.Open(context.Background(), filepath.Join(dir, "queue.db"))
	require.NoError(t, err)
	exec := &cancelResultExecutor{result: result}
	registry := executor.NewRegistry()
	registry.Register(exec)
	cfg := &config.SchedulerConfig{
		Workers: 1, IntervalSeconds: 1, RetryBudget: 3,
		Enabled: true, StaleSeconds: 300, HeartbeatProgressSeconds: 1,
	}
	sched := scheduler.New(store, q, registry, nil, cfg)
	cleanup := func() {
		_ = q.Close()
		_ = store.Close()
	}
	return sched, store, exec, cleanup
}

func requireSingleRun(t *testing.T, store *sqlstore.Store, taskID string) sqlstore.RunRecord {
	t.Helper()
	runs, err := store.ListRuns(taskID)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	return runs[0]
}

func retryCount(t *testing.T, store *sqlstore.Store, taskID string) int {
	t.Helper()
	var retryCount int
	require.NoError(t, store.DB().QueryRow(
		"SELECT retry_count FROM tasks WHERE id = ?", taskID,
	).Scan(&retryCount))
	return retryCount
}

func TestSchedulerStopDaemonInterruptionBlocksWithoutRetry(t *testing.T) {
	sched, store, exec, cleanup := setupShutdownCauseScheduler(t, &executor.ExecutionResult{Status: "failed", Reason: "execution canceled: context canceled"})
	defer cleanup()

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-SHUTDOWN-FAILED", Title: "interrupted", Status: "todo", Priority: 1,
		Executor: "cause", AgentProfile: "mock", OnFail: "retry", MaxRetries: 3,
	}))
	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool { return exec.RunCount() == 1 }, time.Second, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, sched.Stop(ctx))

	run := requireSingleRun(t, store, "CW-SHUTDOWN-FAILED")
	assert.Equal(t, sqlstore.RunStatusKilled, run.Status)
	assert.Contains(t, run.ErrorMessage, "daemon shutdown interrupted")
	task, err := store.GetTask("CW-SHUTDOWN-FAILED")
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status)
	assert.Contains(t, task.BlockedReason, "resume or repair manually")
	assert.Equal(t, 0, retryCount(t, store, "CW-SHUTDOWN-FAILED"))
}

func TestSchedulerStopDaemonInterruptionNilResultBlocks(t *testing.T) {
	sched, store, exec, cleanup := setupShutdownCauseScheduler(t, nil)
	defer cleanup()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-SHUTDOWN-NIL", Title: "interrupted nil", Status: "todo", Priority: 1,
		Executor: "cause", AgentProfile: "mock", OnFail: "retry", MaxRetries: 3,
	}))
	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool { return exec.RunCount() == 1 }, time.Second, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, sched.Stop(ctx))

	run := requireSingleRun(t, store, "CW-SHUTDOWN-NIL")
	assert.Equal(t, sqlstore.RunStatusKilled, run.Status)
	task, err := store.GetTask("CW-SHUTDOWN-NIL")
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status)
	assert.Equal(t, 0, retryCount(t, store, "CW-SHUTDOWN-NIL"))
}

func TestSchedulerStopDaemonInterruptionPreservesManualDoingTask(t *testing.T) {
	sched, store, exec, cleanup := setupShutdownCauseScheduler(t, nil)
	defer cleanup()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-SHUTDOWN-MANUAL", Title: "manual", Status: "todo", Priority: 1,
		Executor: "cause", AgentProfile: "mock", OnFail: "retry", MaxRetries: 3,
	}))
	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool { return exec.RunCount() == 1 }, time.Second, 10*time.Millisecond)
	_, err := store.DB().Exec(`UPDATE tasks SET manual = 1 WHERE id = ?`, "CW-SHUTDOWN-MANUAL")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, sched.Stop(ctx))

	run := requireSingleRun(t, store, "CW-SHUTDOWN-MANUAL")
	assert.Equal(t, sqlstore.RunStatusKilled, run.Status)
	task, err := store.GetTask("CW-SHUTDOWN-MANUAL")
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status)
	assert.Equal(t, 0, retryCount(t, store, "CW-SHUTDOWN-MANUAL"))
}

func TestSchedulerStopDaemonInterruptionPreservesTaskWithNewerRun(t *testing.T) {
	sched, store, exec, cleanup := setupShutdownCauseScheduler(t, nil)
	defer cleanup()
	const taskID = "CW-SHUTDOWN-NEWER"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: taskID, Title: "newer", Status: "todo", Priority: 1,
		Executor: "cause", AgentProfile: "mock", OnFail: "retry", MaxRetries: 3,
	}))
	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool { return exec.RunCount() == 1 }, time.Second, 10*time.Millisecond)
	_, err := store.CreateRun(&sqlstore.RunRecord{TaskID: taskID, Executor: "cause", Status: sqlstore.RunStatusDone})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, sched.Stop(ctx))

	runs, err := store.ListRuns(taskID)
	require.NoError(t, err)
	require.Len(t, runs, 2)
	assert.Equal(t, sqlstore.RunStatusKilled, runs[1].Status)
	task, err := store.GetTask(taskID)
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status)
	assert.Equal(t, 0, retryCount(t, store, taskID))
}

func TestSchedulerStopPreservesDefinitiveReviewResult(t *testing.T) {
	sched, store, exec, cleanup := setupShutdownCauseScheduler(t, &executor.ExecutionResult{Status: "review", Reason: "worker requested review"})
	defer cleanup()
	const taskID = "CW-SHUTDOWN-REVIEW"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: taskID, Title: "review race", Status: "todo", Priority: 1,
		Executor: "cause", AgentProfile: "mock", OnDone: "review", OnFail: "retry", MaxRetries: 3,
	}))
	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool { return exec.RunCount() == 1 }, time.Second, 10*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, sched.Stop(ctx))

	run := requireSingleRun(t, store, taskID)
	assert.Equal(t, "review", run.Status)
	assert.Equal(t, "worker requested review", run.ErrorMessage)
	task, err := store.GetTask(taskID)
	require.NoError(t, err)
	assert.Equal(t, "review", task.Status)
}

func TestSchedulerPicksAndExecutesTasks(t *testing.T) {
	sched, store, mock := setupScheduler(t)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Test task", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnDone: "close",
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
		Executor: "mock", AgentProfile: "mock", Manual: true,
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
		Executor: "mock", AgentProfile: "mock", OnFail: "retry", MaxRetries: 3,
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

func TestSchedulerPersistsResultReasonOnCompletedRun(t *testing.T) {
	sched, store, mock := setupScheduler(t)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-REASON-0001", Title: "reason", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnFail: "block", MaxRetries: 3,
	})
	mock.SetResult(&executor.ExecutionResult{
		Status: "failed",
		Reason: "provider terminal failure",
	})

	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool {
		runs, err := store.ListRuns("CW-REASON-0001")
		return err == nil && len(runs) == 1 && runs[0].Status == "failed"
	}, 2*time.Second, 25*time.Millisecond)
	sched.DrainResults()

	run := requireSingleRun(t, store, "CW-REASON-0001")
	assert.Equal(t, "provider terminal failure", run.ErrorMessage)
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
		Executor: "mock", AgentProfile: "mock",
	})

	sched.SetEnabled(false)
	sched.Tick(context.Background())
	time.Sleep(100 * time.Millisecond)
	sched.DrainResults()

	assert.Len(t, mock.RecordedJobs(), 0, "disabled scheduler should not pick tasks")
}

// TestSchedulerStatusSurfacesStaleThreshold verifies that the configured
// StaleSeconds is visible in SchedulerStatus.StaleHeartbeatThresholdSeconds
// (CW-20260418-0018). The HTTP handler serves status verbatim so this is the
// contract the GUI and MCP tool see.
func TestSchedulerStatusSurfacesStaleThreshold(t *testing.T) {
	sched, _, _ := setupScheduler(t)
	status := sched.Status()
	assert.Equal(t, 300, status.StaleHeartbeatThresholdSeconds,
		"setupScheduler uses default 300s; Status must expose it")
}

// TestSchedulerTickEmitsHeartbeatGaugeLog verifies the per-tick info-level
// gauge line from CW-20260418-0018. Format is load-bearing: operators grep
// `heartbeats live=` to watch zombie accumulation, and the literal
// `threshold=<N>s` is what distinguishes this line from noise. If the
// format changes, update docs and downstream log-processors in the same PR.
func TestSchedulerTickEmitsHeartbeatGaugeLog(t *testing.T) {
	sched, _, _ := setupScheduler(t) // StaleSeconds=300 from helper

	// Redirect the default logger the scheduler writes to, then restore.
	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	}()

	require.NoError(t, sched.Tick(context.Background()))

	// Exact format: `[scheduler] heartbeats live=N stale=M threshold=Ts`
	// live/stale digits and threshold digits are checked via regex so
	// whitespace changes stay brittle-on-purpose while counts stay flex.
	re := regexp.MustCompile(`\[scheduler\] heartbeats live=\d+ stale=\d+ threshold=300s`)
	assert.Regexp(t, re, buf.String(),
		"Tick must emit the gauge line with the documented format")
}

// TestSchedulerTickCleansStaleHeartbeatAndReclaimsSlot is the integration
// test required by CW-20260418-0018: a stale heartbeat row pre-registered
// before Tick should be cleaned up by that tick, and a pending task should
// still get dispatched in the same tick (the zombie row does not hold a
// pool slot, so the picker's AvailableSlots is not gated by it — this test
// locks in that invariant).
func TestSchedulerTickCleansStaleHeartbeatAndReclaimsSlot(t *testing.T) {
	// Build a scheduler with a very short stale threshold so "backdated
	// by 2 seconds" is already stale. Using a sub-second threshold risks
	// flake under -race; 1s is the floor that stays deterministic.
	store := sqlitetest.OpenStore(t)

	dir := t.TempDir()
	q, err := queue.Open(context.Background(), filepath.Join(dir, "queue.db"))
	require.NoError(t, err)

	mock := executor.NewMockExecutor()
	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:          2,
		IntervalSeconds:  1,
		RetryBudget:      3,
		HeartbeatSeconds: 15,
		StaleSeconds:     1, // aggressive so -2m backdate is firmly past it
		Enabled:          true,
	}
	sched := scheduler.New(store, q, registry, nil, cfg)
	t.Cleanup(func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	})

	// Pre-plant a zombie heartbeat row for a non-existent worker — the
	// would-be owner task and run still need to exist because the table
	// has FK constraints on task_id and run_id.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-ZOMBIE", Title: "owner of zombie row", Status: "done",
		Executor: "mock", AgentProfile: "mock",
	}))
	zombieRunID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-ZOMBIE", Executor: "mock", Status: "failed",
	})
	require.NoError(t, err)
	_, err = store.DB().Exec(
		`INSERT INTO worker_heartbeats (worker_id, task_id, run_id, executor, started_at, last_heartbeat)
		 VALUES (?, ?, ?, ?, datetime('now', '-2 minutes'), datetime('now', '-2 minutes'))`,
		"worker-zombie", "CW-ZOMBIE", zombieRunID, "mock",
	)
	require.NoError(t, err)

	// Create a pending task the picker should dispatch.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-PENDING", Title: "next task", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnDone: "close",
	}))
	mock.SetResult(&executor.ExecutionResult{Status: "done"})

	// Run one tick — cleanup path runs AFTER dispatch, so the tick both
	// picks CW-PENDING and prunes the zombie row.
	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(200 * time.Millisecond)
	sched.DrainResults()

	// Zombie row gone.
	row := store.DB().QueryRow(
		`SELECT COUNT(*) FROM worker_heartbeats WHERE worker_id = ?`, "worker-zombie")
	var n int
	require.NoError(t, row.Scan(&n))
	assert.Equal(t, 0, n, "stale zombie row should be deleted by the tick")

	// Pending task picked and completed in the same tick.
	task, err := store.GetTask("CW-PENDING")
	require.NoError(t, err)
	assert.Equal(t, "done", task.Status,
		"picker should dispatch the pending task regardless of zombie row presence")
}

func TestSchedulerEventBus(t *testing.T) {
	sched, store, mock := setupScheduler(t)

	sub := sched.EventBus().Subscribe()
	defer sched.EventBus().Unsubscribe(sub)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnDone: "close",
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
