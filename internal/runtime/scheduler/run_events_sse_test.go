package scheduler_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/config"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/queue"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/hollis-labs/clockwork-manifold/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRunEventsScheduler(t *testing.T, mock *executor.MockExecutor) (*scheduler.Scheduler, *sqlstore.Store, *queue.Queue) {
	t.Helper()
	store := sqlitetest.OpenStore(t)

	q, err := queue.Open(context.Background(), filepath.Join(t.TempDir(), "queue.db"))
	require.NoError(t, err)

	registry := executor.NewRegistry()
	registry.Register(mock)

	cfg := &config.SchedulerConfig{
		Workers:         1,
		IntervalSeconds: 1,
		RetryBudget:     3,
		Enabled:         true,
		StaleSeconds:    300,
	}
	sched := scheduler.New(store, q, registry, nil, cfg)

	t.Cleanup(func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	})

	return sched, store, q
}

// TestRunStartedEventCarriesPayload verifies that run.started fires on the
// SSE bus with executor and started_at populated, alongside task_id and the
// DB-issued run_id.
func TestRunStartedEventCarriesPayload(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.SetResult(&executor.ExecutionResult{Status: "done"})

	sched, store, _ := setupRunEventsScheduler(t, mock)

	sub := sched.EventBus().Subscribe()
	defer sched.EventBus().Unsubscribe(sub)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-R-0001", Title: "T", Status: "todo",
		Priority: 1, Executor: "mock", AgentProfile: "mock", OnDone: "close",
	}))

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(200 * time.Millisecond)
	sched.DrainResults()

	events := drainEvents(sub, 200*time.Millisecond)

	var started *scheduler.SchedulerEvent
	for i := range events {
		if events[i].Type == "run.started" {
			started = &events[i]
			break
		}
	}
	require.NotNil(t, started, "expected run.started event on bus")

	assert.Equal(t, "CW-R-0001", started.TaskID)
	assert.Greater(t, started.RunID, int64(0), "run_id should be the DB-issued id")

	data, ok := started.Data.(map[string]interface{})
	require.True(t, ok, "data should be map[string]interface{}, got %T", started.Data)
	assert.Equal(t, "mock", data["executor"])
	assert.NotEmpty(t, data["started_at"], "started_at should be an RFC3339 string")
}

// TestRunProgressArtifact verifies that an artifact executor event surfaces
// as a run.progress SSE event with kind=artifact. Note progress (CLOCKWORK_NOTE
// stdout signal) was retired in Phase E along with the rest of the
// CLOCKWORK_* protocol — agents now post notes via clockwork_comment_add over
// MCP, which doesn't ride the run.progress SSE channel.
func TestRunProgressArtifact(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.SetResult(&executor.ExecutionResult{Status: "done"})
	mock.SetEvents([]executor.ExecutionEvent{
		executor.ArtifactEvent(executor.Artifact{Type: "diff", Content: "patch"}),
	})

	sched, store, _ := setupRunEventsScheduler(t, mock)

	sub := sched.EventBus().Subscribe()
	defer sched.EventBus().Unsubscribe(sub)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-R-0002", Title: "T", Status: "todo",
		Priority: 1, Executor: "mock", AgentProfile: "mock", OnDone: "close",
	}))

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(200 * time.Millisecond)
	sched.DrainResults()

	events := drainEvents(sub, 200*time.Millisecond)

	var sawArtifact bool
	for _, e := range events {
		if e.Type != "run.progress" {
			continue
		}
		data, _ := e.Data.(map[string]interface{})
		if data["kind"] == "artifact" {
			assert.Equal(t, "diff", data["artifact_type"])
			assert.Equal(t, "patch", data["content"])
			sawArtifact = true
		}
	}
	assert.True(t, sawArtifact, "expected a run.progress kind=artifact event")
}

// TestRunProgressTokensThrottled verifies that two token events fired in
// quick succession produce only one run.progress emission. Rate-limit window
// is 2s, so the second emission inside ~100ms must be dropped.
func TestRunProgressTokensThrottled(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.SetResult(&executor.ExecutionResult{Status: "done"})
	mock.SetEvents([]executor.ExecutionEvent{
		executor.TokenEvent(100, 50, 0.01),
		executor.TokenEvent(200, 100, 0.02),
	})

	sched, store, _ := setupRunEventsScheduler(t, mock)

	sub := sched.EventBus().Subscribe()
	defer sched.EventBus().Unsubscribe(sub)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-R-0003", Title: "T", Status: "todo",
		Priority: 1, Executor: "mock", AgentProfile: "mock", OnDone: "close",
	}))

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(200 * time.Millisecond)
	sched.DrainResults()

	events := drainEvents(sub, 200*time.Millisecond)

	var tokenProgressEvents int
	for _, e := range events {
		if e.Type != "run.progress" {
			continue
		}
		if data, ok := e.Data.(map[string]interface{}); ok && data["kind"] == "tokens" {
			tokenProgressEvents++
		}
	}
	assert.Equal(t, 1, tokenProgressEvents, "tokens events should be throttled to one per 2s window")
}

// TestRunFinishedEventEmittedWithDuration verifies that a completed run
// produces a run.finished event with the final status and a non-negative
// duration_ms.
func TestRunFinishedEventEmittedWithDuration(t *testing.T) {
	mock := executor.NewMockExecutor()
	mock.SetDelay(50 * time.Millisecond)
	mock.SetResult(&executor.ExecutionResult{Status: "done"})

	sched, store, _ := setupRunEventsScheduler(t, mock)

	sub := sched.EventBus().Subscribe()
	defer sched.EventBus().Unsubscribe(sub)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-R-0004", Title: "T", Status: "todo",
		Priority: 1, Executor: "mock", AgentProfile: "mock", OnDone: "close",
	}))

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(400 * time.Millisecond)
	sched.DrainResults()

	events := drainEvents(sub, 200*time.Millisecond)

	var finished *scheduler.SchedulerEvent
	for i := range events {
		if events[i].Type == "run.finished" {
			finished = &events[i]
			break
		}
	}
	require.NotNil(t, finished, "expected run.finished event on bus")

	assert.Equal(t, "CW-R-0004", finished.TaskID)
	assert.Greater(t, finished.RunID, int64(0))

	data, ok := finished.Data.(map[string]interface{})
	require.True(t, ok, "data should be map[string]interface{}, got %T", finished.Data)
	assert.Equal(t, "done", data["status"])

	durationMs, _ := data["duration_ms"].(int64)
	assert.GreaterOrEqual(t, durationMs, int64(0), "duration_ms should be non-negative")
}

// TestRunFinishedEventOnBlockedResult verifies run.finished fires even when
// the lifecycle transitions the task to blocked (the UI needs to stop the
// pulse regardless of final outcome).
func TestRunFinishedEventOnBlockedResult(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-R-0005", Title: "T", Status: "doing", Executor: "cli",
	}))
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-R-0005", Executor: "cli", Status: "running",
	})
	require.NoError(t, err)

	result := &executor.ExecutionResult{Status: "blocked", Reason: "external dependency"}
	require.NoError(t, lm.HandleResult("CW-R-0005", runID, result))

	events := drainEvents(sub, 100*time.Millisecond)

	var finished *scheduler.SchedulerEvent
	for i := range events {
		if events[i].Type == "run.finished" {
			finished = &events[i]
			break
		}
	}
	require.NotNil(t, finished, "run.finished must fire on blocked terminal transition")

	data, _ := finished.Data.(map[string]interface{})
	assert.Equal(t, "blocked", data["status"])
	assert.Equal(t, "external dependency", data["reason"])
}
