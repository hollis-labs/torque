package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestModeForJob exercises the Kind→Mode dispatch matrix. The intent is to
// pin the choice that drives long-lived workers: kind=agent → ModeLongLived;
// everything else (planner/reviewer-end-agent, the empty default) stays on
// ModeOneShot. The selection logic is one-line but the consequences are
// big — adding a new Kind should produce a deliberate test update.
func TestModeForJob(t *testing.T) {
	cases := []struct {
		name string
		job  *executor.ExecutionJob
		want Mode
	}{
		{name: "kind agent → long-lived", job: &executor.ExecutionJob{Kind: "agent"}, want: ModeLongLived},
		{name: "kind internal → one-shot (planner / reviewer)", job: &executor.ExecutionJob{Kind: "internal"}, want: ModeOneShot},
		{name: "kind plan → one-shot (defensive — plans don't dispatch through cli)", job: &executor.ExecutionJob{Kind: "plan"}, want: ModeOneShot},
		{name: "empty kind → one-shot (safe default)", job: &executor.ExecutionJob{}, want: ModeOneShot},
		{name: "nil job → one-shot", job: nil, want: ModeOneShot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, modeForJob(tc.job))
		})
	}
}

// TestResolveInactivityThreshold covers the precedence ladder for the
// inactivity-reset clock that drives idle-reap on ModeLongLived workers.
// The threshold is consulted by awaitLongLivedCompletion; getting the
// precedence wrong silently kills producing workers or leaks zombie ones.
func TestResolveInactivityThreshold(t *testing.T) {
	t.Run("default 30m when nothing set", func(t *testing.T) {
		got := resolveInactivityThreshold(Options{})
		assert.Equal(t, defaultInactivityThreshold, got)
	})

	t.Run("metadata override within range wins", func(t *testing.T) {
		got := resolveInactivityThreshold(Options{Metadata: map[string]any{"inactivity_threshold_seconds": 600}})
		assert.Equal(t, 10*time.Minute, got)
	})

	t.Run("metadata override below minimum falls back to default", func(t *testing.T) {
		got := resolveInactivityThreshold(Options{Metadata: map[string]any{"inactivity_threshold_seconds": 30}})
		assert.Equal(t, defaultInactivityThreshold, got)
	})

	t.Run("metadata override above maximum falls back to default", func(t *testing.T) {
		got := resolveInactivityThreshold(Options{Metadata: map[string]any{"inactivity_threshold_seconds": 100000}})
		assert.Equal(t, defaultInactivityThreshold, got)
	})

	t.Run("metadata override accepts float64 (mcp wire shape)", func(t *testing.T) {
		got := resolveInactivityThreshold(Options{Metadata: map[string]any{"inactivity_threshold_seconds": float64(900)}})
		assert.Equal(t, 15*time.Minute, got)
	})
}

// TestResolveHardCeiling covers the absolute wall-clock ceiling for
// ModeLongLived workers. Hard ceiling is the safety-net path that fires
// regardless of activity — activity does NOT reset it.
func TestResolveHardCeiling(t *testing.T) {
	t.Run("default 12h", func(t *testing.T) {
		got := resolveHardCeiling(Options{})
		assert.Equal(t, defaultHardCeiling, got)
	})

	t.Run("metadata override within range wins", func(t *testing.T) {
		got := resolveHardCeiling(Options{Metadata: map[string]any{"hard_ceiling_seconds": 7200}})
		assert.Equal(t, 2*time.Hour, got)
	})

	t.Run("below minimum falls back to default", func(t *testing.T) {
		got := resolveHardCeiling(Options{Metadata: map[string]any{"hard_ceiling_seconds": 60}})
		assert.Equal(t, defaultHardCeiling, got)
	})
}

// TestLongLivedOutcome_ToExecutionResult exercises the wire shape Manager
// .HandleResult depends on. Each branch produces a distinct (Status,
// ExitCode, Reason) triple and the lifecycle manager keys off Status for
// downstream routing — review enqueues the reviewer end-agent, blocked
// parks the task with reason, failed runs the retry/escalate path. A
// regression here would silently misroute completed work.
func TestLongLivedOutcome_ToExecutionResult(t *testing.T) {
	t.Run("self-transition to review → Status=review (drives end-agent enqueue)", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeTransition, TaskStatus: "review"}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "review", got.Status)
		require.NotNil(t, got.ExitCode)
		assert.Equal(t, 0, *got.ExitCode)
	})

	t.Run("self-transition to done → Status=done", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeTransition, TaskStatus: "done"}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "done", got.Status)
		require.NotNil(t, got.ExitCode)
		assert.Equal(t, 0, *got.ExitCode)
	})

	t.Run("self-transition to blocked → Status=blocked with reason", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeTransition, TaskStatus: "blocked", BlockedReason: "needs creds"}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "blocked", got.Status)
		assert.Contains(t, got.Reason, "needs creds")
	})

	t.Run("self-transition to blocked without reason picks a default", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeTransition, TaskStatus: "blocked"}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "blocked", got.Status)
		assert.NotEmpty(t, got.Reason)
	})

	t.Run("idle reap → Status=blocked with threshold in reason", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeIdle, IdleFor: 30 * time.Minute}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "blocked", got.Status)
		assert.Contains(t, got.Reason, "30m")
	})

	t.Run("hard ceiling → Status=failed with ceiling in reason", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeHardCeiling, CeilingAt: 12 * time.Hour}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "failed", got.Status)
		assert.Contains(t, got.Reason, "12h")
	})

	t.Run("ctx cancelled → Status=failed with cause", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeCtxCanceled, CauseErr: context.Canceled}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "failed", got.Status)
		assert.Contains(t, got.Reason, "canceled")
	})

	t.Run("stream error is prepended to reason when present", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeIdle, IdleFor: 30 * time.Minute}
		got := out.toExecutionResult(&executor.ExecutionResult{}, errors.New("rate_limit"))
		assert.Contains(t, got.Reason, "rate_limit")
		assert.Contains(t, got.Reason, "idle")
	})

	t.Run("transition to unexpected status surfaces it in reason", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeTransition, TaskStatus: "paused"}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "failed", got.Status)
		assert.Contains(t, got.Reason, "paused")
	})
}

// TestAwaitLongLivedCompletion_SelfTransitionToReview drives the happy path
// end-to-end against an in-memory store: the wait loop is polling task
// status, an external party (simulating the worker's MCP-side
// torque_task_review) flips the row from doing→review, the next status
// poll fires within the test's deadline and the wait returns the right
// outcome.
//
// The test uses a tight statusPollInterval override so the wait latency
// is sub-second; production keeps the 5s default to keep SQLite WAL
// pressure low.
func TestAwaitLongLivedCompletion_SelfTransitionToReview(t *testing.T) {
	store := newTestStoreForLongLived(t)

	const taskID = "CW-TEST-LL-0001"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "long-lived worker test",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "test",
	}))

	deps := &Dependencies{Store: store}

	prevInterval := statusPollInterval
	statusPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { statusPollInterval = prevInterval })

	// External actor (simulating the worker self-transitioning via the
	// loopback) flips the row to review after ~150ms — the wait must
	// observe it on the next poll tick and return outcomeTransition.
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = store.TransitionTask(taskID, "review")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	activityCh := make(chan struct{}, 1)
	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, time.Hour, time.Hour)
	assert.Equal(t, outcomeTransition, out.Kind)
	assert.Equal(t, "review", out.TaskStatus)
}

// TestAwaitLongLivedCompletion_IdleReap verifies the inactivity timer
// fires when no executor events flow within the threshold. activityCh is
// never signaled — the timer should fire on schedule.
func TestAwaitLongLivedCompletion_IdleReap(t *testing.T) {
	store := newTestStoreForLongLived(t)

	const taskID = "CW-TEST-LL-IDLE"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "idle reap test",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "test",
	}))

	deps := &Dependencies{Store: store}

	prevInterval := statusPollInterval
	statusPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { statusPollInterval = prevInterval })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	activityCh := make(chan struct{}, 1)
	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, 200*time.Millisecond, time.Hour)
	assert.Equal(t, outcomeIdle, out.Kind)
	assert.Equal(t, 200*time.Millisecond, out.IdleFor)
}

// TestAwaitLongLivedCompletion_HardCeiling verifies the hard ceiling fires
// even when activity is continually resetting the inactivity timer — the
// hard ceiling does NOT reset.
func TestAwaitLongLivedCompletion_HardCeiling(t *testing.T) {
	store := newTestStoreForLongLived(t)

	const taskID = "CW-TEST-LL-CEILING"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "hard ceiling test",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "test",
	}))

	deps := &Dependencies{Store: store}

	prevInterval := statusPollInterval
	statusPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { statusPollInterval = prevInterval })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	activityCh := make(chan struct{}, 1)

	// Pump activity continuously — would reset inactivity forever, but
	// hard ceiling ignores it. Stop pumping when the wait returns.
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				select {
				case activityCh <- struct{}{}:
				default:
				}
			}
		}
	}()
	defer close(stop)

	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, time.Hour, 250*time.Millisecond)
	assert.Equal(t, outcomeHardCeiling, out.Kind)
	assert.Equal(t, 250*time.Millisecond, out.CeilingAt)
}

// TestAwaitLongLivedCompletion_CtxCancelWhileDoing covers the genuine
// external-cancel branch: ctx cancels while task.Status is still "doing"
// — that's a scheduler-shutdown / operator-cancel, not a self-transition.
// Should return outcomeCtxCanceled (NOT outcomeTransition), so the
// downstream lifecycle path handles it as canceled/failed rather than as
// worker completion.
func TestAwaitLongLivedCompletion_CtxCancelWhileDoing(t *testing.T) {
	store := newTestStoreForLongLived(t)

	const taskID = "CW-TEST-LL-CTXCANCEL"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "ctx cancel test",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "test",
	}))

	deps := &Dependencies{Store: store}

	prevInterval := statusPollInterval
	statusPollInterval = 50 * time.Millisecond
	t.Cleanup(func() { statusPollInterval = prevInterval })

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	activityCh := make(chan struct{}, 1)
	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, time.Hour, time.Hour)
	assert.Equal(t, outcomeCtxCanceled, out.Kind)
	assert.Error(t, out.CauseErr)
}

// TestAwaitLongLivedCompletion_CtxCancelAfterSelfTransition exercises the
// scheduler-cancel-hook race: the worker self-transitions to review, the
// scheduler's global TaskTransitionHook fires synchronously and cancels
// the dispatch ctx BEFORE our status poll tick observes the new row state.
// awaitLongLivedCompletion must read task.Status on ctx.Done and treat the
// non-doing case as outcomeTransition, not outcomeCtxCanceled — otherwise
// every long-lived completion would be misclassified as a cancel.
func TestAwaitLongLivedCompletion_CtxCancelAfterSelfTransition(t *testing.T) {
	store := newTestStoreForLongLived(t)

	const taskID = "CW-TEST-LL-RACE"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "ctx-cancel race test",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "test",
	}))

	deps := &Dependencies{Store: store}

	// Long poll interval so the race resolves via the ctx.Done branch,
	// not via the status ticker.
	prevInterval := statusPollInterval
	statusPollInterval = time.Hour
	t.Cleanup(func() { statusPollInterval = prevInterval })

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		require.NoError(t, store.TransitionTask(taskID, "review"))
		cancel()
	}()
	activityCh := make(chan struct{}, 1)
	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, time.Hour, time.Hour)
	assert.Equal(t, outcomeTransition, out.Kind)
	assert.Equal(t, "review", out.TaskStatus)
}

// newTestStoreForLongLived opens a temp-file-backed sqlite store with all
// migrations applied. Routes through the shared sqlitetest fixture so
// these tests exercise the same pooled-connection + busy-timeout shape
// production uses — the older ":memory:" path silently hid SQLite_BUSY
// behavior that bites under concurrent writer load.
func newTestStoreForLongLived(t *testing.T) *sqlstore.Store {
	t.Helper()
	return sqlitetest.OpenStore(t)
}
