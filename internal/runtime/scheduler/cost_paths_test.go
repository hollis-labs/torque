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
	assert.Empty(t, run.CostSource)
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
