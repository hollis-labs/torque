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

// TestLifecycleRunErrorsWhileTaskTerminalDone reproduces the 2026-04-17
// CW-20260417-0012 dogfood incident: the task was side-channel marked
// status=done while a run was in flight, the subprocess was killed, and
// the lifecycle manager re-queued the task to "todo" via retryOrBlock,
// causing the scheduler to re-dispatch on the next tick. After the fix,
// a terminal task must never be transitioned by HandleResult and the
// run should be recorded as superseded.
func TestLifecycleRunErrorsWhileTaskTerminalDone(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "done", Executor: "cli",
		OnFail: "retry", MaxRetries: 3,
	})
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-0001", Executor: "cli", Status: "failed",
	})
	require.NoError(t, err)

	// Worker errored (e.g. external SIGTERM to the subprocess).
	result := &executor.ExecutionResult{Status: "failed", Reason: "signal: killed"}

	require.NoError(t, lm.HandleResult("CW-0001", runID, result))

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "done", task.Status, "terminal task must not be re-queued")

	var retryCount int
	store.DB().QueryRow("SELECT retry_count FROM tasks WHERE id = ?", "CW-0001").Scan(&retryCount)
	assert.Equal(t, 0, retryCount, "retry counter must not increment for terminal tasks")

	run, _ := store.GetRun(runID)
	assert.Equal(t, "superseded", run.Status, "run should be marked superseded when task was already terminal")
}

func TestLifecycleRunErrorsWhileTaskTerminalArchived(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "archived", Executor: "cli",
		OnFail: "retry", MaxRetries: 3,
	})
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-0001", Executor: "cli", Status: "failed",
	})
	require.NoError(t, err)

	result := &executor.ExecutionResult{Status: "failed", Reason: "signal: killed"}
	require.NoError(t, lm.HandleResult("CW-0001", runID, result))

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "archived", task.Status, "archived task must not be re-queued")

	run, _ := store.GetRun(runID)
	assert.Equal(t, "superseded", run.Status)
}

// TestLifecycleRunErrorsWhileTaskTerminalBlockPath verifies the guard also
// applies when OnFail=block — the task had been side-channel marked done,
// a run errored, and without the guard transition() would flip done→blocked.
func TestLifecycleRunErrorsWhileTaskTerminalBlockPath(t *testing.T) {
	store := setupLifecycleStore(t)
	bus := scheduler.NewEventBus()
	defer bus.Close()
	lm := scheduler.NewLifecycleManager(store, bus)

	store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0001", Title: "Task", Status: "done", Executor: "cli",
		OnFail: "block",
	})
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-0001", Executor: "cli", Status: "failed",
	})
	require.NoError(t, err)

	result := &executor.ExecutionResult{Status: "failed", Reason: "boom"}
	require.NoError(t, lm.HandleResult("CW-0001", runID, result))

	task, _ := store.GetTask("CW-0001")
	assert.Equal(t, "done", task.Status)
	assert.Empty(t, task.BlockedReason, "blocked_reason should not be set on a done task")
}

// TestLifecycleShortCircuitsOperatorTerminalRun proves the CW-20260418-0015
// retry-path guard: if the run row has been stamped with an
// operator-terminal status (cancelled/superseded/killed) while the
// executor was in-flight, a late-arriving "failed" result MUST NOT
// trigger retryOrBlock, MUST NOT increment retry_count, MUST NOT flip
// the task out of doing, and MUST NOT fire on_fail hooks. The run's
// operator status and reason stand unchanged.
func TestLifecycleShortCircuitsOperatorTerminalRun(t *testing.T) {
	cases := []struct {
		name     string
		opStatus string
		opReason string
	}{
		{"cancelled short-circuits retry", "cancelled", "operator halted for triage"},
		{"killed short-circuits retry", "killed", "live-validation cleanup"},
		{"superseded short-circuits retry", "superseded", "accepted via run 42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := setupLifecycleStore(t)
			bus := scheduler.NewEventBus()
			defer bus.Close()
			lm := scheduler.NewLifecycleManager(store, bus)

			require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
				ID: "CW-SHORT-0001", Title: "t", Status: "doing", Executor: "cli",
				OnFail: "retry", MaxRetries: 3,
			}))

			runID, err := store.CreateRun(&sqlstore.RunRecord{
				TaskID: "CW-SHORT-0001", Executor: "cli",
			})
			require.NoError(t, err)

			// Operator stamps the run while the executor is still running.
			require.NoError(t, store.SetRunOperatorStatus(runID, tc.opStatus, tc.opReason))

			// Subscribe AFTER the operator stamp so we only see events
			// emitted by the lifecycle path under test.
			sub := bus.Subscribe()
			defer bus.Unsubscribe(sub)

			// Late-arriving executor result. In production this would come
			// from the executor returning with context-cancelled or
			// signal-killed.
			result := &executor.ExecutionResult{
				Status: "failed",
				Reason: "context canceled",
			}

			require.NoError(t, lm.HandleResult("CW-SHORT-0001", runID, result))

			// Run row is unchanged — operator status/reason stand.
			got, err := store.GetRun(runID)
			require.NoError(t, err)
			assert.Equal(t, tc.opStatus, got.Status, "run status must stay operator-terminal")
			assert.Equal(t, tc.opReason, got.ErrorMessage, "operator reason must not be clobbered")

			// Task did NOT retry: still in doing, retry_count unchanged.
			task, err := store.GetTask("CW-SHORT-0001")
			require.NoError(t, err)
			assert.Equal(t, "doing", task.Status, "task must not be re-queued via retryOrBlock")

			var retryCount int
			store.DB().QueryRow("SELECT retry_count FROM tasks WHERE id = ?", "CW-SHORT-0001").Scan(&retryCount)
			assert.Equal(t, 0, retryCount, "retry_count must not advance on operator-terminal run")

			// No task.transitioned event — the on_fail retry path would
			// emit one (doing -> todo). run.finished is allowed (UI pulse
			// signal) but task.transitioned MUST be absent.
			var events []scheduler.SchedulerEvent
		drain:
			for {
				select {
				case e := <-sub:
					events = append(events, e)
				default:
					break drain
				}
			}
			for _, e := range events {
				assert.NotEqual(t, "task.transitioned", e.Type, "operator actions must be silent on task FSM: saw %#v", e)
				assert.NotEqual(t, "task.notify", e.Type, "on_fail=notify must not fire: saw %#v", e)
			}
		})
	}
}

// TestLifecycleShortCircuitHonorsBlockAndEscalate confirms the guard is
// not keyed to OnFail=retry. Regardless of OnFail mode, an operator-stamped
// run must short-circuit every downstream lifecycle branch.
func TestLifecycleShortCircuitHonorsBlockAndEscalate(t *testing.T) {
	modes := []string{"retry", "block", "escalate", "notify"}
	for _, mode := range modes {
		t.Run("on_fail="+mode, func(t *testing.T) {
			store := setupLifecycleStore(t)
			bus := scheduler.NewEventBus()
			defer bus.Close()
			lm := scheduler.NewLifecycleManager(store, bus)

			require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
				ID: "CW-MODE-0001", Title: "t", Status: "doing", Executor: "cli",
				OnFail: mode, MaxRetries: 3,
				EscalationChain: sql.NullString{String: `["notify_owner"]`, Valid: true},
			}))

			runID, err := store.CreateRun(&sqlstore.RunRecord{
				TaskID: "CW-MODE-0001", Executor: "cli",
			})
			require.NoError(t, err)

			require.NoError(t, store.SetRunOperatorStatus(runID, "cancelled", "operator halted"))

			result := &executor.ExecutionResult{Status: "failed", Reason: "boom"}
			require.NoError(t, lm.HandleResult("CW-MODE-0001", runID, result))

			task, _ := store.GetTask("CW-MODE-0001")
			assert.Equal(t, "doing", task.Status, "task must not move for OnFail=%s when run is operator-terminal", mode)
			assert.Empty(t, task.BlockedReason, "no blocked reason stamped")
		})
	}
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
