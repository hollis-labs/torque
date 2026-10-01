package scheduler_test

import (
	"database/sql"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupCostStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	// TestCostTrackerSprintTotal uses an ad hoc sprint_id string as a pure
	// grouping key (CostTracker.SprintTotal only filters cost_ledger rows by
	// the string value, it never joins against sprints) — no need for a real
	// sprints row. tasks.sprint_id carries a real FK since FK-002 (migration
	// 028_task_fk_constraints.sql); disable enforcement here rather than
	// seeding one.
	_, err = store.DB().Exec("PRAGMA foreign_keys = OFF")
	require.NoError(t, err)

	return store
}

func TestCostTrackerRecord(t *testing.T) {
	store := setupCostStore(t)
	tracker := scheduler.NewCostTracker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	err := tracker.Record(scheduler.CostEntry{
		TaskID:           "CW-0001",
		RunID:            1,
		Cost:             0.05,
		PromptTokens:     1000,
		CompletionTokens: 500,
	})
	require.NoError(t, err)
}

func TestCostTrackerTaskTotal(t *testing.T) {
	store := setupCostStore(t)
	tracker := scheduler.NewCostTracker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, Cost: 0.05})
	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, Cost: 0.03})

	total, err := tracker.TaskTotal("CW-0001")
	require.NoError(t, err)
	assert.InDelta(t, 0.08, total, 0.001)
}

func TestCostTrackerSprintTotal(t *testing.T) {
	store := setupCostStore(t)
	tracker := scheduler.NewCostTracker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli",
		SprintID: sql.NullString{String: "sprint-1", Valid: true}})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, SprintID: "sprint-1", Cost: 0.10})
	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, SprintID: "sprint-1", Cost: 0.15})

	total, err := tracker.SprintTotal("sprint-1")
	require.NoError(t, err)
	assert.InDelta(t, 0.25, total, 0.001)
}

func TestCostTrackerGlobalTotal(t *testing.T) {
	store := setupCostStore(t)
	tracker := scheduler.NewCostTracker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "A", Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "B", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0002", Executor: "cli", Status: "running"})

	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, Cost: 0.10})
	tracker.Record(scheduler.CostEntry{TaskID: "CW-0002", RunID: 2, Cost: 0.20})

	total, err := tracker.GlobalTotal()
	require.NoError(t, err)
	assert.InDelta(t, 0.30, total, 0.001)
}

func TestCostTrackerWithinBudget(t *testing.T) {
	store := setupCostStore(t)
	tracker := scheduler.NewCostTracker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	tracker.Record(scheduler.CostEntry{TaskID: "CW-0001", RunID: 1, Cost: 0.10})

	// Within budget
	ok, err := tracker.WithinGlobalBudget(1.00)
	require.NoError(t, err)
	assert.True(t, ok)

	// Over budget
	ok, err = tracker.WithinGlobalBudget(0.05)
	require.NoError(t, err)
	assert.False(t, ok)

	// Zero ceiling means no limit
	ok, err = tracker.WithinGlobalBudget(0)
	require.NoError(t, err)
	assert.True(t, ok)
}

// TestResolveCost covers the cost-decision policy (CW-20260912-0003):
// provider cost first, a cache-aware estimate for the tokens that came
// without one, and the provenance of the result. Lifted out of
// scheduler.Scheduler so the policy can be exercised without a full
// scheduler.
func TestResolveCost(t *testing.T) {
	// A flat 3/M input, 15/M output, 0.3/M cache read, 3.75/M cache write.
	estimateOK := func(_, _ string, u modelcatalog.UsageTokens) (float64, bool, bool) {
		return (float64(u.Input)*3 + float64(u.Output)*15 + float64(u.CacheRead)*0.3 + float64(u.CacheWrite)*3.75) / 1_000_000, false, true
	}
	estimateMiss := func(string, string, modelcatalog.UsageTokens) (float64, bool, bool) { return 0, false, false }
	resolveProfile := func(tp scheduler.TaskProfile) (string, string, bool) {
		if tp.AgentProfile == "claude" {
			return "claude-code", "claude-sonnet-4-5", true
		}
		return "", "", false
	}
	tokens := executor.TokenUsage{PromptTokens: 1000, CompletionTokens: 500}

	t.Run("provider-reported for every event", func(t *testing.T) {
		rc := scheduler.ResolveCost(scheduler.TaskProfile{AgentProfile: "claude"}, &executor.ExecutionResult{Cost: 0.42, Tokens: tokens}, estimateOK, resolveProfile, true)
		assert.Equal(t, scheduler.ResolvedCost{Cost: 0.42, ProviderCost: 0.42, Source: scheduler.CostSourceProvider}, rc)
	})

	t.Run("part provider-reported, part estimated", func(t *testing.T) {
		// A resumed Claude session: its first turn reports no cost and is
		// estimated; the others carry Claude's own figure.
		rc := scheduler.ResolveCost(scheduler.TaskProfile{AgentProfile: "claude"}, &executor.ExecutionResult{
			Cost:           0.42,
			Tokens:         executor.TokenUsage{PromptTokens: 2000, CompletionTokens: 1000},
			UnpricedTokens: tokens,
		}, estimateOK, resolveProfile, true)
		assert.Equal(t, scheduler.CostSourceMixed, rc.Source)
		assert.InDelta(t, 0.42, rc.ProviderCost, 1e-9)
		assert.InDelta(t, 0.0105, rc.EstimatedCost, 1e-9, "only the unpriced tokens are estimated")
		assert.InDelta(t, 0.4305, rc.Cost, 1e-9)
	})

	t.Run("estimated when no event carried a cost", func(t *testing.T) {
		rc := scheduler.ResolveCost(scheduler.TaskProfile{AgentProfile: "claude"}, &executor.ExecutionResult{Tokens: tokens, UnpricedTokens: tokens}, estimateOK, resolveProfile, true)
		assert.Equal(t, scheduler.CostSourceEstimate, rc.Source)
		assert.InDelta(t, 0.0105, rc.Cost, 1e-9)
		assert.Equal(t, rc.Cost, rc.EstimatedCost)
	})

	t.Run("an executor that does not split has all its tokens estimated", func(t *testing.T) {
		rc := scheduler.ResolveCost(scheduler.TaskProfile{AgentProfile: "claude"}, &executor.ExecutionResult{Tokens: tokens}, estimateOK, resolveProfile, true)
		assert.Equal(t, scheduler.CostSourceEstimate, rc.Source)
		assert.InDelta(t, 0.0105, rc.Cost, 1e-9)
	})

	t.Run("cache tokens are priced at their own rates", func(t *testing.T) {
		u := executor.TokenUsage{PromptTokens: 36, CompletionTokens: 1804, CacheReadTokens: 132711, CacheWriteTokens: 20565}
		rc := scheduler.ResolveCost(scheduler.TaskProfile{AgentProfile: "claude"}, &executor.ExecutionResult{Tokens: u, UnpricedTokens: u}, estimateOK, resolveProfile, true)
		assert.InDelta(t, 0.14410005, rc.Cost, 1e-9)
	})

	none := []struct {
		name     string
		profile  string
		result   *executor.ExecutionResult
		estimate scheduler.EstimateUsageFn
		backfill bool
	}{
		{"backfill off", "claude", &executor.ExecutionResult{Tokens: tokens}, estimateOK, false},
		{"no tokens", "claude", &executor.ExecutionResult{}, estimateOK, true},
		{"unknown profile", "not-a-profile", &executor.ExecutionResult{Tokens: tokens}, estimateOK, true},
		{"model not in the catalog", "claude", &executor.ExecutionResult{Tokens: tokens}, estimateMiss, true},
		{"no catalog", "claude", &executor.ExecutionResult{Tokens: tokens}, nil, true},
		{"no result", "claude", nil, estimateOK, true},
	}
	for _, tc := range none {
		t.Run("none: "+tc.name, func(t *testing.T) {
			rc := scheduler.ResolveCost(scheduler.TaskProfile{AgentProfile: tc.profile}, tc.result, tc.estimate, resolveProfile, tc.backfill)
			assert.Equal(t, scheduler.ResolvedCost{Source: scheduler.CostSourceNone}, rc, "0 here means unknown, not free")
		})
	}

	t.Run("provider cost stands when the rest cannot be priced", func(t *testing.T) {
		rc := scheduler.ResolveCost(scheduler.TaskProfile{AgentProfile: "claude"}, &executor.ExecutionResult{Cost: 0.42, Tokens: tokens, UnpricedTokens: tokens}, estimateMiss, resolveProfile, true)
		assert.Equal(t, scheduler.CostSourceProvider, rc.Source)
		assert.InDelta(t, 0.42, rc.Cost, 1e-9)
	})
}

// The estimate is asked under the catalog's provider id, with the runtime's
// own input convention: claude-code is Anthropic's and reports cache reads
// on top of input (CW-20261001-0182, which left every Claude run unpriced);
// codex is OpenAI's and counts them in input; opencode keeps its id and its
// "<provider>/<model>" model, which the catalog splits.
func TestResolveCost_CatalogProviderAndInputConvention(t *testing.T) {
	for _, tc := range []struct {
		provider, model  string
		wantProvider     string
		wantIncludesRead bool
	}{
		{"claude-code", "claude-sonnet-4-5", "anthropic", false},
		{"claude", "claude-sonnet-4-5", "anthropic", false},
		{"codex", "gpt-5.5", "openai", true},
		{"opencode", "opencode/claude-sonnet-4-5", "opencode", false},
	} {
		t.Run(tc.provider, func(t *testing.T) {
			var gotProvider, gotModel string
			var gotUsage modelcatalog.UsageTokens
			estimate := func(p, m string, u modelcatalog.UsageTokens) (float64, bool, bool) {
				gotProvider, gotModel, gotUsage = p, m, u
				return 0.01, false, true
			}
			resolveProfile := func(scheduler.TaskProfile) (string, string, bool) { return tc.provider, tc.model, true }
			rc := scheduler.ResolveCost(scheduler.TaskProfile{AgentProfile: "worker"}, &executor.ExecutionResult{Tokens: executor.TokenUsage{PromptTokens: 10, CacheReadTokens: 5}}, estimate, resolveProfile, true)
			assert.Equal(t, scheduler.CostSourceEstimate, rc.Source)
			assert.Equal(t, tc.wantProvider, gotProvider)
			assert.Equal(t, tc.model, gotModel)
			assert.Equal(t, tc.wantIncludesRead, gotUsage.InputIncludesCacheRead)
			assert.Equal(t, 5, gotUsage.CacheRead)
		})
	}
}

// TestCostTracker_RecordsSource verifies the cost_source column round-trips.
// The Source field on CostEntry is the only handle the UI / ops have for
// distinguishing measured cost from a models.dev estimate, so a regression
// here would silently degrade audit accuracy.
func TestCostTracker_RecordsSource(t *testing.T) {
	store := setupCostStore(t)
	tracker := scheduler.NewCostTracker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-S-001", Title: "T", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-S-001", Executor: "cli", Status: "running"})

	require.NoError(t, tracker.Record(scheduler.CostEntry{
		TaskID: "CW-S-001",
		RunID:  1,
		Cost:   0.42,
		Source: scheduler.CostSourceModelsDev,
	}))

	var src string
	require.NoError(t, store.DB().QueryRow(
		`SELECT cost_source FROM cost_ledger WHERE task_id = ?`, "CW-S-001",
	).Scan(&src))
	assert.Equal(t, "models_dev", src)

	// Empty Source defaults to 'unknown' so the column constraint is always met.
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-S-002", Title: "T2", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-S-002", Executor: "cli", Status: "running"})
	require.NoError(t, tracker.Record(scheduler.CostEntry{
		TaskID: "CW-S-002",
		RunID:  2,
		Cost:   0,
	}))
	require.NoError(t, store.DB().QueryRow(
		`SELECT cost_source FROM cost_ledger WHERE task_id = ?`, "CW-S-002",
	).Scan(&src))
	assert.Equal(t, "unknown", src)
}
