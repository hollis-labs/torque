package scheduler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/go-modelsdev/modelsdev"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticProfiles config.ProfileMap

func (p staticProfiles) CurrentProfiles() config.ProfileMap { return config.ProfileMap(p) }

// setupCostScheduler is a scheduler wired with a models.dev fixture catalog
// and two profiles, codex-worker (gpt-5.5) and claude-worker (sonnet 4.5).
func setupCostScheduler(t *testing.T) (*scheduler.Scheduler, *sqlstore.Store, *executor.MockExecutor) {
	t.Helper()
	sched, store, mock := setupScheduler(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]modelsdev.Provider{
			"openai":    {ID: "openai", Models: map[string]modelsdev.Model{"gpt-5.5": {ID: "gpt-5.5", Cost: modelsdev.Pricing{Input: 5, Output: 30, CacheRead: 0.5}}}},
			"anthropic": {ID: "anthropic", Models: map[string]modelsdev.Model{"claude-sonnet-4-5": {ID: "claude-sonnet-4-5", Cost: modelsdev.Pricing{Input: 3, Output: 15, CacheWrite: 3.75, CacheRead: 0.3}}}},
		})
	}))
	t.Cleanup(srv.Close)
	catalog := modelcatalog.New(modelsdev.WithURL(srv.URL), modelsdev.WithHTTPClient(srv.Client()), modelsdev.WithCacheDir(t.TempDir()), modelsdev.WithCacheTTL(24*time.Hour))
	require.NoError(t, catalog.Refresh(context.Background()))
	sched.Models = catalog
	sched.Profiles = staticProfiles{
		"codex-worker":  {Executor: "cli", Provider: "codex", Model: "gpt-5.5"},
		"claude-worker": {Executor: "cli", Provider: "claude-code", Model: "claude-sonnet-4-5"},
	}
	return sched, store, mock
}

// runToCompletion creates a mock task, runs it through the real completion
// path with result, and returns its single run.
func runToCompletion(t *testing.T, sched *scheduler.Scheduler, store *sqlstore.Store, mock *executor.MockExecutor, task sqlstore.TaskRecord, result executor.ExecutionResult) sqlstore.RunRecord {
	t.Helper()
	task.Status, task.Priority, task.Executor, task.OnDone = "todo", 1, "mock", "close"
	require.NoError(t, store.CreateTask(&task))
	mock.SetResult(&result)
	require.NoError(t, sched.Tick(context.Background()))
	require.Eventually(t, func() bool {
		runs, err := store.ListRuns(task.ID)
		return err == nil && len(runs) == 1 && runs[0].Status != "running"
	}, 5*time.Second, 10*time.Millisecond, "run for %s never completed", task.ID)
	sched.DrainResults()
	return requireSingleRun(t, store, task.ID)
}

// A task's profile resolves for pricing as the executor resolves it:
// launch_profile first, then agent_profile (launchprofile.Resolve). A task
// with only a launch_profile used to look up profile "" and cost 0, source
// none (CW-20260912-0003 review); one with both set to different profiles was
// priced against the wrong model.
func TestSchedulerPricesTheProfileTheExecutorResolves(t *testing.T) {
	sched, store, mock := setupCostScheduler(t)
	// Tonight's run 1140: codex gpt-5.5, input includes its 40,064 cached tokens.
	usage := executor.TokenUsage{PromptTokens: 58212, CompletionTokens: 830, CacheReadTokens: 40064}
	const codexPrice = 0.135672
	// The same tokens priced as a Claude run (input excludes the cache reads):
	// a different figure, so the wrong profile cannot pass by coincidence.
	const claudePrice = (58212*3 + 830*15 + 40064*0.3) / 1e6
	require.NotEqual(t, codexPrice, claudePrice)

	for _, tc := range []struct {
		name string
		task sqlstore.TaskRecord
		want float64
	}{
		{"launch_profile only", sqlstore.TaskRecord{ID: "CW-LP-ONLY", LaunchProfile: "codex-worker"}, codexPrice},
		{"agent_profile only", sqlstore.TaskRecord{ID: "CW-AP-ONLY", AgentProfile: "codex-worker"}, codexPrice},
		{"both set, to different profiles: the launch_profile wins", sqlstore.TaskRecord{ID: "CW-BOTH", LaunchProfile: "codex-worker", AgentProfile: "claude-worker"}, codexPrice},
		{"both set, the other way round", sqlstore.TaskRecord{ID: "CW-BOTH-2", LaunchProfile: "claude-worker", AgentProfile: "codex-worker"}, claudePrice},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := runToCompletion(t, sched, store, mock, tc.task, executor.ExecutionResult{Status: "done", Tokens: usage})
			assert.Equal(t, "estimate", run.CostSource)
			assert.InDelta(t, tc.want, run.Cost, 1e-9)
		})
	}
}

// A run's cost is resolved once and written to runs.cost and its
// cost_ledger row together, so the run, its task's aggregate and the
// scheduler's total_cost all read the same figures (CW-20260912-0003). Four
// runs through the real completion path, one per provenance, with a
// models.dev fixture and tonight's run 1140 as the codex estimate.
func TestSchedulerRunCostIsOneFigure(t *testing.T) {
	sched, store, mock := setupCostScheduler(t)

	resumed := executor.TokenUsage{PromptTokens: 12, CompletionTokens: 300, CacheReadTokens: 40000, CacheWriteTokens: 5000}
	runs := []struct {
		taskID, profile string
		result          executor.ExecutionResult
		wantSource      string
		wantCost        float64
	}{
		{"CW-COST-PROVIDER", "claude-worker", executor.ExecutionResult{
			Status: "done", Cost: 0.1904,
			Tokens: executor.TokenUsage{PromptTokens: 36, CompletionTokens: 1804, CacheReadTokens: 132711, CacheWriteTokens: 20565},
		}, "provider", 0.1904},
		{"CW-COST-MIXED", "claude-worker", executor.ExecutionResult{
			Status: "done", Cost: 0.14,
			Tokens:         executor.TokenUsage{PromptTokens: 36, CompletionTokens: 1804, CacheReadTokens: 132711, CacheWriteTokens: 20565},
			UnpricedTokens: resumed,
		}, "mixed", 0.14 + (12*3+300*15+40000*0.3+5000*3.75)/1e6},
		{"CW-COST-ESTIMATE", "codex-worker", executor.ExecutionResult{
			Status: "done",
			Tokens: executor.TokenUsage{PromptTokens: 58212, CompletionTokens: 830, CacheReadTokens: 40064},
		}, "estimate", 0.135672},
		{"CW-COST-NONE", "not-a-profile", executor.ExecutionResult{
			Status: "done",
			Tokens: executor.TokenUsage{PromptTokens: 100, CompletionTokens: 10},
		}, "none", 0},
	}
	for _, r := range runs {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: r.taskID, Title: r.taskID, Status: "todo", Priority: 1,
			Executor: "mock", AgentProfile: r.profile, OnDone: "close",
		}))
		res := r.result
		mock.SetResult(&res)
		require.NoError(t, sched.Tick(context.Background()))
		require.Eventually(t, func() bool {
			runs, err := store.ListRuns(r.taskID)
			return err == nil && len(runs) == 1 && runs[0].Status != "running"
		}, 5*time.Second, 10*time.Millisecond, "run for %s never completed", r.taskID)
		sched.DrainResults()
	}

	var sum float64
	for _, r := range runs {
		run := requireSingleRun(t, store, r.taskID)
		assert.Equal(t, r.wantSource, run.CostSource, r.taskID)
		assert.InDelta(t, r.wantCost, run.Cost, 1e-9, r.taskID)
		assert.Equal(t, r.result.Tokens.CacheReadTokens, run.CacheReadTokens, r.taskID)
		assert.Equal(t, r.result.Tokens.CacheWriteTokens, run.CacheWriteTokens, r.taskID)

		var ledgerCost, provider, estimated float64
		var source string
		var cacheRead int
		require.NoError(t, store.DB().QueryRow(`SELECT cost, cost_source, provider_cost, estimated_cost, cache_read_tokens FROM cost_ledger WHERE run_id = ?`, run.ID).
			Scan(&ledgerCost, &source, &provider, &estimated, &cacheRead))
		assert.Equal(t, run.Cost, ledgerCost, "%s: runs.cost == the run's ledger figure", r.taskID)
		assert.Equal(t, run.CostSource, source, r.taskID)
		assert.InDelta(t, ledgerCost, provider+estimated, 1e-9, "%s: cost is its provider and estimated parts", r.taskID)
		assert.Equal(t, run.CacheReadTokens, cacheRead, r.taskID)

		agg, err := store.GetTaskRunAggregate(r.taskID)
		require.NoError(t, err)
		assert.InDelta(t, run.Cost, agg.Cost, 1e-9, "%s: the task aggregate is its ledger", r.taskID)
		sum += run.Cost
	}

	status := sched.Status()
	assert.InDelta(t, sum, status.TotalCost, 1e-9, "scheduler total_cost == the sum of the ledger")
	var bySource float64
	for _, v := range status.TotalCostBySource {
		bySource += v
	}
	assert.InDelta(t, status.TotalCost, bySource, 1e-9, "the by-source split adds up to total_cost")
	assert.InDelta(t, 0.135672, status.TotalCostBySource["estimate"], 1e-9)
	assert.Contains(t, status.TotalCostBySource, "none")
}

// A task whose profile is not in the registry resolves to an empty profile,
// and config logs a warning for it. Cost resolution adds none of its own on
// each completion: across several completions of such tasks the warning from
// cost resolution appears once (the executor, which launched with the same
// profile, logs its own).
func TestSchedulerLogsAnUnresolvedProfileOnceWhilePricing(t *testing.T) {
	sched, store, mock := setupCostScheduler(t)
	// A registry with no "default" profile, so an unknown name resolves to empty.
	sched.Profiles = staticProfiles{"codex-worker": {Executor: "cli", Provider: "codex", Model: "gpt-5.5"}}

	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	usage := executor.TokenUsage{PromptTokens: 100, CompletionTokens: 10}
	for i, id := range []string{"CW-UNRES-1", "CW-UNRES-2", "CW-UNRES-3"} {
		run := runToCompletion(t, sched, store, mock, sqlstore.TaskRecord{ID: id, AgentProfile: "not-in-the-registry"}, executor.ExecutionResult{Status: "done", Tokens: usage})
		assert.Equal(t, "none", run.CostSource, "run %d: nothing to price against", i)
	}
	warnings := strings.Count(buf.String(), `agent_profile "not-in-the-registry" not found`)
	assert.Equal(t, 1, warnings, "the not-found warning is logged once, not on every completion")
}
