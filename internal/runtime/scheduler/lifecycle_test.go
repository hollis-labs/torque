package scheduler_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/executor"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupLifecycleStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestLifecycleDoneWithReview(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "doing", Executor: "cli",
		OnDone: "review",
	})

	result := &executor.ExecutionResult{
		Status:    "done",
		Artifacts: []executor.Artifact{},
	}

	err := lm.HandleResult("CW-0001", 1, result)
	require.NoError(t, err)

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "review", task.Status)
}

func TestLifecycleDoneWithClose(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "doing", Executor: "cli",
		OnDone: "close",
	})

	result := &executor.ExecutionResult{Status: "done"}

	err := lm.HandleResult("CW-0001", 1, result)
	require.NoError(t, err)

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "done", task.Status)
}

func TestLifecycleDoneWithNotify(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "doing", Executor: "cli",
		OnDone: "notify",
	})

	result := &executor.ExecutionResult{Status: "done"}

	err := lm.HandleResult("CW-0001", 1, result)
	require.NoError(t, err)

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "done", task.Status)
}

func TestLifecycleFailedWithRetry(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "doing", Executor: "cli",
		OnFail: "retry", MaxRetries: 3,
	})

	result := &executor.ExecutionResult{Status: "failed", Reason: "timeout"}

	err := lm.HandleResult("CW-0001", 1, result)
	require.NoError(t, err)

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "todo", task.Status, "should re-queue for retry")
}

func TestLifecycleFailedRetryExhausted(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "doing", Executor: "cli",
		OnFail: "retry", MaxRetries: 1,
	})

	// Simulate retry count already at max by updating directly
	store.DB().Exec("UPDATE tasks SET retry_count = 1 WHERE id = ?", "CW-0001")

	result := &executor.ExecutionResult{Status: "failed", Reason: "still failing"}

	err := lm.HandleResult("CW-0001", 1, result)
	require.NoError(t, err)

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "blocked", task.Status, "should block when retries exhausted")
}

func TestLifecycleFailedWithBlock(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "doing", Executor: "cli",
		OnFail: "block",
	})

	result := &executor.ExecutionResult{Status: "failed", Reason: "critical error"}

	err := lm.HandleResult("CW-0001", 1, result)
	require.NoError(t, err)

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "blocked", task.Status)
}

func TestLifecycleFailedWithEscalate(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "doing", Executor: "cli",
		OnFail:          "escalate",
		EscalationChain: sql.NullString{String: `["retry","senior-agent","human"]`, Valid: true},
	})

	result := &executor.ExecutionResult{Status: "failed", Reason: "needs escalation"}

	err := lm.HandleResult("CW-0001", 1, result)
	require.NoError(t, err)

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "todo", task.Status, "first escalation step is retry -> todo")
}

func TestLifecycleBlockedResult(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "doing", Executor: "cli",
	})

	result := &executor.ExecutionResult{Status: "blocked", Reason: "waiting on external API"}

	err := lm.HandleResult("CW-0001", 1, result)
	require.NoError(t, err)

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "blocked", task.Status)
	assert.Equal(t, "waiting on external API", task.BlockedReason)
}

func TestLifecycleReviewResult(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "doing", Executor: "cli",
	})

	result := &executor.ExecutionResult{Status: "review"}

	err := lm.HandleResult("CW-0001", 1, result)
	require.NoError(t, err)

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "review", task.Status)
}

func TestLifecycleMissingDeliverablesCountsAsRetry(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "doing", Executor: "cli",
		OnDone:       "close",
		MaxRetries:   3,
		Deliverables: sql.NullString{String: `[{"type":"diff","required":true},{"type":"test-results","required":true}]`, Valid: true},
	})

	// Result says done but only has one artifact
	result := &executor.ExecutionResult{
		Status: "done",
		Artifacts: []executor.Artifact{
			{Type: "diff", Content: "some diff"},
		},
	}

	err := lm.HandleResult("CW-0001", 1, result)
	require.NoError(t, err)

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "todo", task.Status, "missing deliverables should re-queue")
}

func TestLifecycleEmitsEvents(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	sub := bus.Subscribe()
	defer bus.Unsubscribe(sub)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "doing", Executor: "cli",
		OnDone: "close",
	})

	result := &executor.ExecutionResult{Status: "done"}
	lm.HandleResult("CW-0001", 1, result)

	// Drain events — should have at least a transition event
	var events []scheduler.SchedulerEvent
	for {
		select {
		case e := <-sub:
			events = append(events, e)
		default:
			goto done
		}
	}
done:
	assert.GreaterOrEqual(t, len(events), 1, "should emit at least one event")

	found := false
	for _, e := range events {
		if e.Type == "task.transitioned" {
			found = true
		}
	}
	assert.True(t, found, "should emit task.transitioned event")
}
