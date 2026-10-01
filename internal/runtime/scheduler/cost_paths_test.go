package scheduler_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/queue"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every path that ends a run keeps the usage the executor accumulated, so the
// tokens that were spent are recorded and runs.cost agrees with the ledger
// (CW-20260912-0003). The paths: daemon-shutdown interruption, an executor
// that fails with an error after running up usage, and the normal completion
// (TestSchedulerRunCostIsOneFigure).

// partialUsage is what an executor had accumulated when it was cut short.
var partialUsage = &executor.ExecutionResult{
	Status: "failed", Reason: "execution canceled: context canceled",
	Cost:   0.25,
	Tokens: executor.TokenUsage{PromptTokens: 1000, CompletionTokens: 500, CacheReadTokens: 2000, CacheWriteTokens: 300},
}

func requireCostAgrees(t *testing.T, store *sqlstore.Store, run sqlstore.RunRecord, wantRows int) {
	t.Helper()
	var n int
	var sum float64
	require.NoError(t, store.DB().QueryRow(`SELECT COUNT(*), COALESCE(SUM(cost), 0) FROM cost_ledger WHERE run_id = ?`, run.ID).Scan(&n, &sum))
	assert.Equal(t, wantRows, n, "ledger rows for the run")
	assert.InDelta(t, run.Cost, sum, 1e-9, "runs.cost == the run's ledger sum")
}

func TestSchedulerStopDaemonInterruptionKeepsAccumulatedUsage(t *testing.T) {
	sched, store, exec, cleanup := setupShutdownCauseScheduler(t, partialUsage)
	defer cleanup()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-SHUTDOWN-USAGE", Title: "interrupted", Status: "todo", Priority: 1,
		Executor: "cause", AgentProfile: "mock", OnFail: "retry", MaxRetries: 3,
	}))
	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool { return exec.RunCount() == 1 }, time.Second, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, sched.Stop(ctx))

	run := requireSingleRun(t, store, "CW-SHUTDOWN-USAGE")
	assert.Equal(t, sqlstore.RunStatusKilled, run.Status)
	assert.Equal(t, 1000, run.PromptTokens)
	assert.Equal(t, 500, run.CompletionTokens)
	assert.Equal(t, 2000, run.CacheReadTokens)
	assert.Equal(t, 300, run.CacheWriteTokens)
	assert.InDelta(t, 0.25, run.Cost, 1e-9)
	assert.Equal(t, "provider", run.CostSource)
	requireCostAgrees(t, store, run, 1)
}

// A run interrupted with no usage records none: nothing is invented, and the
// two agree at zero.
func TestSchedulerStopDaemonInterruptionWithoutUsageRecordsNoCost(t *testing.T) {
	sched, store, exec, cleanup := setupShutdownCauseScheduler(t, &executor.ExecutionResult{Status: "failed", Reason: "canceled"})
	defer cleanup()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-SHUTDOWN-NOUSAGE", Title: "interrupted", Status: "todo", Priority: 1,
		Executor: "cause", AgentProfile: "mock", OnFail: "retry", MaxRetries: 3,
	}))
	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool { return exec.RunCount() == 1 }, time.Second, 10*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, sched.Stop(ctx))

	run := requireSingleRun(t, store, "CW-SHUTDOWN-NOUSAGE")
	assert.Equal(t, sqlstore.RunStatusKilled, run.Status)
	assert.Zero(t, run.Cost)
	assert.Equal(t, "none", run.CostSource, "a run that ended without usage is none, like one completed without tokens")
	requireCostAgrees(t, store, run, 0)
}

// resultErrExecutor returns a partial result together with an error, as an
// executor does when it fails after running up usage.
type resultErrExecutor struct {
	result *executor.ExecutionResult
	err    error
}

func (e *resultErrExecutor) Name() string { return "resulterr" }
func (e *resultErrExecutor) Run(context.Context, *executor.ExecutionJob, executor.EventCallback) (*executor.ExecutionResult, error) {
	cp := *e.result
	return &cp, e.err
}
func (e *resultErrExecutor) Validate(*executor.ExecutionJob) error { return nil }
func (e *resultErrExecutor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{}
}

func TestSchedulerRunFailedWithAnErrorKeepsAccumulatedUsage(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	q, err := queue.Open(context.Background(), filepath.Join(t.TempDir(), "queue.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close(); _ = store.Close() })
	registry := executor.NewRegistry()
	registry.Register(&resultErrExecutor{result: partialUsage, err: errors.New("the CLI died")})
	sched := scheduler.New(store, q, registry, nil, &config.SchedulerConfig{Workers: 1, IntervalSeconds: 1, RetryBudget: 3, Enabled: true, StaleSeconds: 300, HeartbeatProgressSeconds: 1})
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-ERR-USAGE", Title: "fails with usage", Status: "todo", Priority: 1,
		Executor: "resulterr", AgentProfile: "mock", OnFail: "block", MaxRetries: 0,
	}))
	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool {
		runs, err := store.ListRuns("CW-ERR-USAGE")
		return err == nil && len(runs) == 1 && runs[0].Status != "running"
	}, 5*time.Second, 10*time.Millisecond)
	sched.DrainResults()

	run := requireSingleRun(t, store, "CW-ERR-USAGE")
	assert.Equal(t, "failed", run.Status)
	assert.Equal(t, 1000, run.PromptTokens)
	assert.InDelta(t, 0.25, run.Cost, 1e-9)
	assert.Equal(t, "provider", run.CostSource)
	requireCostAgrees(t, store, run, 1)
}

// A negative provider cost is not a credit: it reads as no provider figure, so
// the run is priced from its tokens and labelled an estimate.
func TestResolveCost_NegativeProviderCostIsIgnored(t *testing.T) {
	estimate := func(string, string, modelcatalog.UsageTokens) (float64, bool, bool) { return 0.5, false, true }
	resolve := func(scheduler.TaskProfile) (string, string, bool) { return "claude-code", "claude-sonnet-4-5", true }
	rc := scheduler.ResolveCost(scheduler.TaskProfile{AgentProfile: "claude"},
		&executor.ExecutionResult{Cost: -3, Tokens: executor.TokenUsage{PromptTokens: 10}}, estimate, resolve, true)
	assert.Zero(t, rc.ProviderCost)
	assert.InDelta(t, 0.5, rc.Cost, 1e-9)
	assert.Equal(t, scheduler.CostSourceEstimate, rc.Source)

	rc = scheduler.ResolveCost(scheduler.TaskProfile{AgentProfile: "claude"}, &executor.ExecutionResult{Cost: -3}, estimate, resolve, true)
	assert.Equal(t, scheduler.ResolvedCost{Source: scheduler.CostSourceNone}, rc, "a negative cost alone is no figure at all")
}

// A cache price the catalog lacks falls back to the input price, and the
// fallback is reported so the figure can be told apart.
func TestResolveCost_ReportsACacheFallback(t *testing.T) {
	estimate := func(string, string, modelcatalog.UsageTokens) (float64, bool, bool) { return 1, true, true }
	resolve := func(scheduler.TaskProfile) (string, string, bool) { return "claude-code", "claude-sonnet-4-5", true }
	rc := scheduler.ResolveCost(scheduler.TaskProfile{AgentProfile: "claude"},
		&executor.ExecutionResult{Tokens: executor.TokenUsage{PromptTokens: 10, CacheWriteTokens: 5}}, estimate, resolve, true)
	assert.True(t, rc.CacheFallback)
	assert.Equal(t, scheduler.CostSourceEstimate, rc.Source)
}

// finishFirstWriter lets a run finish for real just before the submit named op
// runs: a run that completed between the scheduler's decision and its write.
type finishFirstWriter struct {
	writeq.Writer
	op     string
	finish func()
}

func (w *finishFirstWriter) Submit(ctx context.Context, name string, fn func(*sqlstore.WriteTx) error) error {
	if name == w.op {
		w.finish()
	}
	return w.Writer.Submit(ctx, name, fn)
}

// A daemon-shutdown kill only writes a run that is still running. One that
// finished first keeps its status, tokens and cost (so it still matches its
// ledger row), and the kill records no interruption and does not block the
// task.
func TestSchedulerStopDaemonInterruptionLeavesARunThatFinishedFirst(t *testing.T) {
	sched, store, exec, cleanup := setupShutdownCauseScheduler(t, partialUsage)
	defer cleanup()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-SHUTDOWN-RACE", Title: "finishes as the daemon stops", Status: "todo", Priority: 1,
		Executor: "cause", AgentProfile: "mock", OnFail: "retry", MaxRetries: 3,
	}))
	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool { return exec.RunCount() == 1 }, time.Second, 10*time.Millisecond)
	run := requireSingleRun(t, store, "CW-SHUTDOWN-RACE")
	sched.SetStateWriter(&finishFirstWriter{Writer: writeq.NewDirect(store), op: "scheduler_run_interrupted", finish: func() {
		require.NoError(t, store.CompleteRun(run.ID, sqlstore.RunCompletion{Status: "done", PromptTokens: 10, Cost: 0.5, CostSource: "provider"}))
		_, err := store.AppendCostLedger(&sqlstore.CostLedgerRecord{TaskID: "CW-SHUTDOWN-RACE", RunID: run.ID, Cost: 0.5, CostSource: "provider", ProviderCost: 0.5})
		require.NoError(t, err)
	}})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, sched.Stop(ctx))

	after := requireSingleRun(t, store, "CW-SHUTDOWN-RACE")
	assert.Equal(t, "done", after.Status, "the run that finished first is not killed")
	assert.InDelta(t, 0.5, after.Cost, 1e-9)
	requireCostAgrees(t, store, after, 1)
	events, err := store.ListRunEvents(sqlstore.RunEventFilter{TaskID: "CW-SHUTDOWN-RACE", Types: []string{"run_interrupted"}})
	require.NoError(t, err)
	assert.Empty(t, events)
	task, err := store.GetTask("CW-SHUTDOWN-RACE")
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status, "and its task is not blocked as interrupted")
}

// blockUntilCancelExecutor blocks until its context is cancelled, then returns
// the partial usage it had accumulated, with no status: what an executor does
// when its task is transitioned out of doing under it.
type blockUntilCancelExecutor struct {
	result  *executor.ExecutionResult
	started chan struct{}
}

func (e *blockUntilCancelExecutor) Name() string { return "blockcancel" }
func (e *blockUntilCancelExecutor) Run(ctx context.Context, _ *executor.ExecutionJob, _ executor.EventCallback) (*executor.ExecutionResult, error) {
	close(e.started)
	<-ctx.Done()
	cp := *e.result
	return &cp, nil
}
func (e *blockUntilCancelExecutor) Validate(*executor.ExecutionJob) error { return nil }
func (e *blockUntilCancelExecutor) Capabilities() executor.ExecutorCapabilities {
	return executor.ExecutorCapabilities{}
}

// A run cancelled by its task leaving `doing` keeps the usage the executor had
// accumulated, like every other way a run ends.
func TestSchedulerRunCanceledOutOfDoingKeepsAccumulatedUsage(t *testing.T) {
	store := sqlitetest.OpenStore(t)
	q, err := queue.Open(context.Background(), filepath.Join(t.TempDir(), "queue.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close(); _ = store.Close() })
	exec := &blockUntilCancelExecutor{
		result:  &executor.ExecutionResult{Cost: 0.25, Tokens: executor.TokenUsage{PromptTokens: 1000, CompletionTokens: 500, CacheReadTokens: 2000, CacheWriteTokens: 300}},
		started: make(chan struct{}),
	}
	registry := executor.NewRegistry()
	registry.Register(exec)
	sched := scheduler.New(store, q, registry, nil, &config.SchedulerConfig{Workers: 1, IntervalSeconds: 1, RetryBudget: 3, Enabled: true, StaleSeconds: 300, HeartbeatProgressSeconds: 1})
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-CANCEL-USAGE", Title: "cancelled with usage", Status: "todo", Priority: 1,
		Executor: "blockcancel", AgentProfile: "mock", OnFail: "block", MaxRetries: 0,
	}))
	require.NoError(t, sched.Tick(context.Background()))
	select {
	case <-exec.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the executor never started")
	}
	require.NoError(t, store.TransitionTask("CW-CANCEL-USAGE", "review"))
	require.Eventually(t, func() bool {
		runs, err := store.ListRuns("CW-CANCEL-USAGE")
		return err == nil && len(runs) == 1 && runs[0].Status == "canceled"
	}, 5*time.Second, 25*time.Millisecond)
	sched.DrainResults()

	run := requireSingleRun(t, store, "CW-CANCEL-USAGE")
	assert.Equal(t, "canceled", run.Status)
	assert.Equal(t, 1000, run.PromptTokens)
	assert.Equal(t, 2000, run.CacheReadTokens)
	assert.InDelta(t, 0.25, run.Cost, 1e-9)
	assert.Equal(t, "provider", run.CostSource)
	requireCostAgrees(t, store, run, 1)
}
