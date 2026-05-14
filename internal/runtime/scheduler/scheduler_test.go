package scheduler_test

import (
	"bytes"
	"context"
	"log"
	"path/filepath"
	"regexp"
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
