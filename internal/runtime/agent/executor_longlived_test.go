package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hollis-labs/agentkit/agentsessions"
	llmtypes "github.com/hollis-labs/go-llm-types"
	gomsg "github.com/hollis-labs/go-messaging"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/steering"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
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

	t.Run("task max duration wins", func(t *testing.T) {
		maxDuration := 45 * time.Second
		got := resolveHardCeiling(Options{
			Limits: executor.ExecutionLimits{MaxDuration: &maxDuration},
			Metadata: map[string]any{
				"hard_ceiling_seconds": 7200,
			},
		})
		assert.Equal(t, maxDuration, got)
		assert.True(t, hardCeilingIsTaskDeadline(Options{
			Limits: executor.ExecutionLimits{MaxDuration: &maxDuration},
		}, got))
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

	t.Run("task deadline → Status=failed with deadline in reason", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeHardCeiling, CeilingAt: 45 * time.Second, TaskDeadline: true}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "failed", got.Status)
		assert.Contains(t, got.Reason, "task deadline exceeded")
		assert.Contains(t, got.Reason, "45s")
	})

	t.Run("ctx cancelled → Status=failed with cause", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeCtxCanceled, CauseErr: context.Canceled}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "failed", got.Status)
		assert.Contains(t, got.Reason, "canceled")
	})

	t.Run("operator pause → Status=canceled with explicit reason", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeTransition, TaskStatus: "paused"}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "canceled", got.Status)
		assert.Equal(t, operatorPauseReason, got.Reason)
	})

	t.Run("stream error is prepended to reason when present", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeIdle, IdleFor: 30 * time.Minute}
		got := out.toExecutionResult(&executor.ExecutionResult{}, errors.New("rate_limit"))
		assert.Contains(t, got.Reason, "rate_limit")
		assert.Contains(t, got.Reason, "idle")
	})

	t.Run("transition to unexpected status surfaces it in reason", func(t *testing.T) {
		out := longLivedOutcome{Kind: outcomeTransition, TaskStatus: "todo"}
		got := out.toExecutionResult(&executor.ExecutionResult{}, nil)
		assert.Equal(t, "failed", got.Status)
		assert.Contains(t, got.Reason, "todo")
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
	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, nil, time.Hour, time.Hour, false)
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
	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, nil, 200*time.Millisecond, time.Hour, false)
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

	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, nil, time.Hour, 250*time.Millisecond, false)
	assert.Equal(t, outcomeHardCeiling, out.Kind)
	assert.Equal(t, 250*time.Millisecond, out.CeilingAt)
}

func TestOptsFromJob_CarriesExecutionLimits(t *testing.T) {
	maxDuration := 45 * time.Second
	job := &executor.ExecutionJob{
		TaskID:       "CW-LIMITS",
		AgentProfile: "test",
		Limits: executor.ExecutionLimits{
			MaxDuration: &maxDuration,
			MaxRetries:  0,
		},
	}

	opts := optsFromJob(job, t.TempDir())

	require.NotNil(t, opts.Limits.MaxDuration)
	assert.Equal(t, maxDuration, *opts.Limits.MaxDuration)
	assert.Equal(t, 0, opts.Limits.MaxRetries)
}

func TestRunLongLived_TaskDeadlineStopsSession(t *testing.T) {
	store := newTestStoreForLongLived(t)
	const taskID = "CW-TEST-LL-DEADLINE"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "deadline reap test",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "test",
		OnFail:       "retry",
		MaxRetries:   0,
	}))
	deadline := 120 * time.Millisecond
	fr := &deadlineFakeRuntime{}
	deps := &Dependencies{
		Store:       store,
		StateWriter: writeq.NewDirect(store),
		Profiles: config.ProfileMap{
			"test": {Executor: "cli", Provider: "claude-code", RuntimeKind: "streaming-stdio"},
		},
		RuntimeFactory: func(agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) {
			return fr, nil
		},
		Reminder: steering.NewReminderRegistry(),
	}
	deps.Sessions = NewManager(deps).WithIDFunc(func() string { return "SES-DEADLINE" })
	agentExec := NewExecutor(deps)

	result, err := agentExec.Run(context.Background(), &executor.ExecutionJob{
		TaskID:       taskID,
		Kind:         "agent",
		AgentProfile: "test",
		WorkingDir:   t.TempDir(),
		RunID:        1,
		Limits: executor.ExecutionLimits{
			MaxDuration: &deadline,
			MaxRetries:  0,
		},
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Reason, "task deadline exceeded")
	assert.Eventually(t, func() bool { return fr.stopCount.Load() == 1 }, time.Second, 10*time.Millisecond)

	rec, err := store.GetSession("SES-DEADLINE")
	require.NoError(t, err)
	assert.Equal(t, string(StatusFailed), rec.State)
	require.True(t, rec.ExitCode.Valid)
	assert.Equal(t, int64(-1), rec.ExitCode.Int64)
	assert.Equal(t, 0, deps.Sessions.LivePID("SES-DEADLINE"), "deadline cleanup must release the live manager slot")
}

func TestRunLongLived_TaskDeadlineCoversLaunch(t *testing.T) {
	store := newTestStoreForLongLived(t)
	const taskID = "CW-TEST-LL-LAUNCH-DEADLINE"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "launch deadline test",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "test",
		OnFail:       "retry",
		MaxRetries:   0,
	}))
	deadline := 75 * time.Millisecond
	deps := &Dependencies{
		Store:       store,
		StateWriter: writeq.NewDirect(store),
		Profiles: config.ProfileMap{
			"test": {Executor: "cli", Provider: "claude-code", RuntimeKind: "streaming-stdio"},
		},
		RuntimeFactory: func(agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) {
			return &deadlineFakeRuntime{blockStartUntilContextDone: true}, nil
		},
		Reminder: steering.NewReminderRegistry(),
	}
	deps.Sessions = NewManager(deps).WithIDFunc(func() string { return "SES-LAUNCH-DEADLINE" })
	agentExec := NewExecutor(deps)

	result, err := agentExec.Run(context.Background(), &executor.ExecutionJob{
		TaskID:       taskID,
		Kind:         "agent",
		AgentProfile: "test",
		WorkingDir:   t.TempDir(),
		RunID:        1,
		Limits: executor.ExecutionLimits{
			MaxDuration: &deadline,
			MaxRetries:  0,
		},
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "failed", result.Status)
	assert.Contains(t, result.Reason, "task deadline exceeded")

	rec, err := store.GetSession("SES-LAUNCH-DEADLINE")
	require.NoError(t, err)
	assert.Equal(t, string(StatusFailed), rec.State)
	assert.Equal(t, 0, deps.Sessions.LivePID("SES-LAUNCH-DEADLINE"), "a blocked launch must not leave a live manager slot")
}

func TestRunLongLived_UpstreamDeadlineIsNotTaskDeadline(t *testing.T) {
	store := newTestStoreForLongLived(t)
	const taskID = "CW-TEST-LL-UPSTREAM-DEADLINE"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "upstream deadline test",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "test",
		OnFail:       "retry",
		MaxRetries:   0,
	}))
	maxDuration := time.Second
	deps := &Dependencies{
		Store:       store,
		StateWriter: writeq.NewDirect(store),
		Profiles: config.ProfileMap{
			"test": {Executor: "cli", Provider: "claude-code", RuntimeKind: "streaming-stdio"},
		},
		RuntimeFactory: func(agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) {
			return &deadlineFakeRuntime{blockStartUntilContextDone: true}, nil
		},
		Reminder: steering.NewReminderRegistry(),
	}
	deps.Sessions = NewManager(deps).WithIDFunc(func() string { return "SES-UPSTREAM-DEADLINE" })
	agentExec := NewExecutor(deps)

	upstreamCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	result, err := agentExec.Run(upstreamCtx, &executor.ExecutionJob{
		TaskID:       taskID,
		Kind:         "agent",
		AgentProfile: "test",
		WorkingDir:   t.TempDir(),
		RunID:        1,
		Limits: executor.ExecutionLimits{
			MaxDuration: &maxDuration,
			MaxRetries:  0,
		},
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "failed", result.Status)
	assert.NotContains(t, result.Reason, "task deadline exceeded")
	assert.Contains(t, result.Reason, "context deadline")
}

func TestAwaitLongLivedCompletion_TaskDeadlinePreservesTransitionPrecedence(t *testing.T) {
	store := newTestStoreForLongLived(t)

	const taskID = "CW-TEST-LL-DEADLINE-RACE"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "deadline race test",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "test",
	}))

	deps := &Dependencies{Store: store}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	go func() {
		time.Sleep(50 * time.Millisecond)
		require.NoError(t, store.TransitionTask(taskID, "review"))
	}()
	activityCh := make(chan struct{}, 1)
	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, nil, time.Hour, 100*time.Millisecond, true)
	assert.Equal(t, outcomeTransition, out.Kind)
	assert.Equal(t, "review", out.TaskStatus)
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
	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, nil, time.Hour, time.Hour, false)
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
	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, nil, time.Hour, time.Hour, false)
	assert.Equal(t, outcomeTransition, out.Kind)
	assert.Equal(t, "review", out.TaskStatus)
}

func TestAwaitLongLivedCompletion_CtxCancelAfterOperatorPause(t *testing.T) {
	store := newTestStoreForLongLived(t)

	const taskID = "CW-TEST-LL-PAUSE-RACE"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "ctx-cancel pause race test",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "test",
	}))

	deps := &Dependencies{Store: store}

	prevInterval := statusPollInterval
	statusPollInterval = time.Hour
	t.Cleanup(func() { statusPollInterval = prevInterval })

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		require.NoError(t, store.TransitionTask(taskID, "paused"))
		cancel()
	}()
	activityCh := make(chan struct{}, 1)
	out := awaitLongLivedCompletion(ctx, deps, taskID, "SES-TEST", activityCh, nil, time.Hour, time.Hour, false)
	assert.Equal(t, outcomeTransition, out.Kind)
	assert.Equal(t, "paused", out.TaskStatus)

	res := out.toExecutionResult(&executor.ExecutionResult{}, nil)
	assert.Equal(t, "canceled", res.Status)
	assert.Equal(t, operatorPauseReason, res.Reason)
}

func TestRunLongLived_OperatorPauseCancelsRunAndSession(t *testing.T) {
	store := newTestStoreForLongLived(t)
	const taskID = "CW-TEST-LL-PAUSE"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           taskID,
		Title:        "operator pause test",
		Status:       "doing",
		Executor:     "cli",
		Kind:         "agent",
		AgentProfile: "test",
		OnFail:       "retry",
		MaxRetries:   3,
	}))
	fr := &deadlineFakeRuntime{}
	deps := &Dependencies{
		Store:       store,
		StateWriter: writeq.NewDirect(store),
		Profiles: config.ProfileMap{
			"test": {Executor: "cli", Provider: "claude-code", RuntimeKind: "streaming-stdio"},
		},
		RuntimeFactory: func(agentsessions.AdapterRuntimeConfig) (agentsessions.Runtime, error) {
			return fr, nil
		},
		Reminder: steering.NewReminderRegistry(),
	}
	deps.Sessions = NewManager(deps).WithIDFunc(func() string { return "SES-PAUSE" })
	agentExec := NewExecutor(deps)

	prevInterval := statusPollInterval
	statusPollInterval = 25 * time.Millisecond
	t.Cleanup(func() { statusPollInterval = prevInterval })

	resultCh := make(chan *executor.ExecutionResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := agentExec.Run(context.Background(), &executor.ExecutionJob{
			TaskID:       taskID,
			Kind:         "agent",
			AgentProfile: "test",
			WorkingDir:   t.TempDir(),
			RunID:        1,
		}, nil)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	require.Eventually(t, func() bool {
		rec, err := store.GetSession("SES-PAUSE")
		return err == nil && rec.State == string(StatusRunning)
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, store.TransitionTask(taskID, "paused"))

	var result *executor.ExecutionResult
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case result = <-resultCh:
	case <-time.After(3 * time.Second):
		t.Fatal("long-lived executor did not return after operator pause")
	}
	require.NotNil(t, result)
	assert.Equal(t, "canceled", result.Status)
	assert.Equal(t, operatorPauseReason, result.Reason)
	assert.Eventually(t, func() bool { return fr.stopCount.Load() == 1 }, time.Second, 10*time.Millisecond)

	rec, err := store.GetSession("SES-PAUSE")
	require.NoError(t, err)
	assert.Equal(t, string(StatusCanceled), rec.State)
	require.True(t, rec.ExitCode.Valid)
	assert.Equal(t, int64(-1), rec.ExitCode.Int64)
	assert.True(t, rec.EndedAt.Valid)
	meta := decodeMeta(rec.MetaJSON)
	assert.Equal(t, metaStopCauseOperator, meta[metaKeyStopCause])
	assert.Equal(t, operatorPauseReason, meta[metaKeyStopReason])
	assert.Equal(t, 0, deps.Sessions.LivePID("SES-PAUSE"), "pause cleanup must release the live manager slot")
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

// fakeTurnSender records SendTurn invocations for assertions on the
// reminder pump. send errors are injectable via SendErr; once exhausted
// the next call returns nil. Safe for concurrent calls.
type fakeTurnSender struct {
	mu      sync.Mutex
	sends   []string
	sendErr error
}

func (f *fakeTurnSender) SendTurn(_ context.Context, _ *Session, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		err := f.sendErr
		f.sendErr = nil
		return err
	}
	f.sends = append(f.sends, text)
	return nil
}

func (f *fakeTurnSender) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sends...)
}

func reminderTestEnvelope(id, body string) gomsg.Envelope {
	return gomsg.Envelope{
		ID:          id,
		Kind:        gomsg.MsgKindNotice,
		From:        gomsg.Address{Kind: gomsg.KindUser, Authority: "local", ID: "operator"},
		To:          gomsg.Address{Kind: gomsg.KindSession, Authority: "local", ID: "SES-PUMP"},
		Payload:     []byte(body),
		ContentType: "application/json",
	}
}

// TestRunReminderPump_DeliversReminderOnTurnDoneAfterInjection covers the
// happy path: an injection arrives, the turn ends, the pump runs the
// reminder. The pump's MarkReminded step is observed by feeding a second
// turn-done with no new injection — no further sends fire.
func TestRunReminderPump_DeliversReminderOnTurnDoneAfterInjection(t *testing.T) {
	reg := steering.NewReminderRegistry()
	const taskID = "CW-TEST-PUMP-1"
	reg.RecordDelivery(taskID, reminderTestEnvelope("ENV-1", `"hi"`))

	sender := &fakeTurnSender{}
	sess := &Session{ID: "SES-PUMP"}
	ch := make(chan struct{}, 4)
	var wg sync.WaitGroup
	wg.Add(1)
	go runReminderPump(context.Background(), sender, reg, taskID, sess, ch, &wg)

	ch <- struct{}{} // first turn boundary — has injection, should remind
	// Loop until the pump has acted; bounded by a 2s safety budget.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sender.all()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	close(ch)
	wg.Wait()

	sends := sender.all()
	require.Len(t, sends, 1, "first turn boundary with pending must produce exactly one reminder")
	assert.Contains(t, sends[0], "envelope=ENV-1")
	assert.Contains(t, sends[0], "1 unaddressed message")

	// And the envelope is now marked reminded — Snapshot reflects it.
	snap := reg.Snapshot(taskID)
	require.Len(t, snap, 1)
	assert.True(t, snap[0].Reminded)
}

// TestRunReminderPump_NoInjectionMeansNoReminder covers the spec's
// "if a message was injected during THIS turn" gate: a turn boundary
// without an intervening injection produces no reminder even if pending
// envelopes exist from prior turns. (The pending envelopes were already
// reminded once and the "stop nagging" rule keeps them silent.)
func TestRunReminderPump_NoInjectionMeansNoReminder(t *testing.T) {
	reg := steering.NewReminderRegistry()
	const taskID = "CW-TEST-PUMP-2"

	sender := &fakeTurnSender{}
	sess := &Session{ID: "SES-PUMP"}
	ch := make(chan struct{}, 4)
	var wg sync.WaitGroup
	wg.Add(1)
	go runReminderPump(context.Background(), sender, reg, taskID, sess, ch, &wg)

	ch <- struct{}{} // turn boundary, no prior injection
	time.Sleep(75 * time.Millisecond)
	close(ch)
	wg.Wait()

	assert.Empty(t, sender.all(), "turn boundary with no injection must produce no reminder")
}

// TestRunReminderPump_DismissedEnvelopeSilenced verifies the
// torque_steering_dismiss path: an envelope that has been dismissed by
// the agent must not surface in the reminder even when the turn had a
// fresh injection of a different envelope.
func TestRunReminderPump_DismissedEnvelopeSilenced(t *testing.T) {
	reg := steering.NewReminderRegistry()
	const taskID = "CW-TEST-PUMP-3"
	reg.RecordDelivery(taskID, reminderTestEnvelope("ENV-OLD", `"old"`))
	reg.Dismiss(taskID, "ENV-OLD")
	reg.RecordDelivery(taskID, reminderTestEnvelope("ENV-NEW", `"new"`))

	sender := &fakeTurnSender{}
	sess := &Session{ID: "SES-PUMP"}
	ch := make(chan struct{}, 4)
	var wg sync.WaitGroup
	wg.Add(1)
	go runReminderPump(context.Background(), sender, reg, taskID, sess, ch, &wg)

	ch <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(sender.all()) > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(ch)
	wg.Wait()

	sends := sender.all()
	require.Len(t, sends, 1)
	assert.NotContains(t, sends[0], "ENV-OLD")
	assert.Contains(t, sends[0], "ENV-NEW")
}

// TestRunReminderPump_NilRegistryDrainsCleanly is the degraded-wiring
// guarantee: with no reminder registry the pump must still consume
// turn-done signals and exit on close. Otherwise the drain goroutine
// would back up and we'd be worse off than before the feature.
func TestRunReminderPump_NilRegistryDrainsCleanly(t *testing.T) {
	sender := &fakeTurnSender{}
	sess := &Session{ID: "SES-PUMP"}
	ch := make(chan struct{}, 4)
	var wg sync.WaitGroup
	wg.Add(1)
	go runReminderPump(context.Background(), sender, nil, "CW-TEST-PUMP-4", sess, ch, &wg)

	for i := 0; i < 3; i++ {
		ch <- struct{}{}
	}
	close(ch)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// ok
	case <-time.After(2 * time.Second):
		t.Fatal("pump did not exit on channel close with nil registry")
	}

	assert.Empty(t, sender.all(), "nil registry must not send anything")
}

// TestRunReminderPump_SendFailureLeavesEnvelopeUnreminded covers the
// retry guarantee: a SendTurn error must NOT MarkReminded, so the next
// turn boundary that sees an injection retries the surface.
func TestRunReminderPump_SendFailureLeavesEnvelopeUnreminded(t *testing.T) {
	reg := steering.NewReminderRegistry()
	const taskID = "CW-TEST-PUMP-5"
	reg.RecordDelivery(taskID, reminderTestEnvelope("ENV-FAIL", `"x"`))

	sender := &fakeTurnSender{sendErr: errors.New("pipe closed")}
	sess := &Session{ID: "SES-PUMP"}
	ch := make(chan struct{}, 4)
	var wg sync.WaitGroup
	wg.Add(1)
	go runReminderPump(context.Background(), sender, reg, taskID, sess, ch, &wg)

	ch <- struct{}{}
	time.Sleep(75 * time.Millisecond)
	close(ch)
	wg.Wait()

	snap := reg.Snapshot(taskID)
	require.Len(t, snap, 1)
	assert.False(t, snap[0].Reminded, "send failure must leave the envelope unreminded so the next turn retries")
}

func TestAwaitLongLivedCompletion_TerminalFailureBlocks(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	defer store.Close()

	const taskID = "CW-LL-TERMINAL-FAIL"
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: taskID, Title: "terminal provider failure", Status: "doing",
		Executor: "cli", AgentProfile: "codex",
	}))
	deps := &Dependencies{Store: store}
	terminalFailureCh := make(chan string, 1)
	terminalFailureCh <- "codex terminal turn failed: unexpected status 401"

	out := awaitLongLivedCompletion(context.Background(), deps, taskID, "SES-TEST", nil, terminalFailureCh, time.Hour, time.Hour, false)
	require.Equal(t, outcomeTerminalFailure, out.Kind)
	res := out.toExecutionResult(&executor.ExecutionResult{}, nil)
	assert.Equal(t, "blocked", res.Status)
	assert.Contains(t, res.Reason, "unexpected status 401")
}

type deadlineFakeRuntime struct {
	stopCount                  atomic.Int32
	blockStartUntilContextDone bool
}

func (r *deadlineFakeRuntime) ID() string   { return "deadline-fake" }
func (r *deadlineFakeRuntime) Kind() string { return string(RuntimeKindStreamingStdio) }
func (r *deadlineFakeRuntime) Caps() agentsessions.Capabilities {
	return agentsessions.Capabilities{}
}
func (r *deadlineFakeRuntime) Prepare(context.Context) error { return nil }
func (r *deadlineFakeRuntime) Start(ctx context.Context, opts agentsessions.StartOptions) (agentsessions.Session, error) {
	if r.blockStartUntilContextDone {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return &deadlineFakeSession{
		done:      make(chan struct{}),
		stopCount: &r.stopCount,
		events:    opts.EventFanout,
	}, nil
}

type deadlineFakeSession struct {
	done      chan struct{}
	once      sync.Once
	stopCount *atomic.Int32
	events    chan<- llmtypes.StreamEvent
}

func (s *deadlineFakeSession) Wait() (int, error) {
	<-s.done
	return -1, nil
}

func (s *deadlineFakeSession) Stop(context.Context) error {
	s.stopCount.Add(1)
	s.once.Do(func() { close(s.done) })
	return nil
}

func (s *deadlineFakeSession) SendInput(context.Context, []byte) error {
	if s.events != nil {
		select {
		case s.events <- llmtypes.StreamEvent{Type: llmtypes.EventDelta, Content: "still running"}:
		default:
		}
	}
	return nil
}

func (s *deadlineFakeSession) Resize(context.Context, uint16, uint16) error { return nil }

func (s *deadlineFakeSession) Health() agentsessions.HealthStatus {
	return agentsessions.HealthStatus{Alive: true, PID: 1234}
}

func (s *deadlineFakeSession) CheckpointHints() (agentsessions.CheckpointHint, bool) {
	return agentsessions.CheckpointHint{}, false
}
