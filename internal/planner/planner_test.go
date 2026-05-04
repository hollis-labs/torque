package planner_test

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/planner"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupPlannerStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

// AC2 / AC3: BuildTask produces the canonical kind=internal planner
// task with all V0 stamps in place. The orchestrator inserts this and
// waits on `done` before reading the refinement.
func TestPlanner_BuildTaskShape(t *testing.T) {
	plan := &sqlstore.TaskRecord{
		ID:           "CW-PLAN-001",
		WorkingDir:   "/tmp/plan",
		ProjectID:    sql.NullString{String: "PRJ-1", Valid: true},
		SprintID:     sql.NullString{String: "SP-1", Valid: true},
		EpicID:       sql.NullString{String: "EP-1", Valid: true},
	}

	rec, err := planner.BuildTask(planner.BuildOptions{
		TargetPlanID: plan.ID,
		IDFn:         func() (string, error) { return "CW-PLANNER-001", nil },
		Now:          func() time.Time { return time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC) },
		Inherit:      plan,
		Template:     "STUB TEMPLATE",
	})
	require.NoError(t, err)

	assert.Equal(t, "CW-PLANNER-001", rec.ID)
	assert.Equal(t, "internal", rec.Kind)
	assert.Equal(t, "agent", rec.SourceType)
	assert.False(t, rec.Manual, "planner must auto-dispatch")
	assert.Equal(t, "cli", rec.Executor)
	assert.Equal(t, planner.Profile, rec.AgentProfile)
	assert.Equal(t, "STUB TEMPLATE", rec.SystemPrompt)
	assert.Equal(t, "close", rec.OnDone)
	assert.Equal(t, "block", rec.OnFail)
	assert.Equal(t, 0, rec.MaxRetries, "AC: no retry in V0")
	assert.True(t, rec.CostBudget.Valid)
	assert.Greater(t, rec.CostBudget.Float64, 0.0)
	assert.Equal(t, plan.ID, rec.ParentID.String)

	// Inherits scope IDs from the target plan.
	assert.Equal(t, plan.ProjectID, rec.ProjectID)
	assert.Equal(t, plan.SprintID, rec.SprintID)
	assert.Equal(t, plan.EpicID, rec.EpicID)
	assert.Equal(t, plan.WorkingDir, rec.WorkingDir)

	// metadata.planner.target_plan_id round-trips.
	require.True(t, rec.Metadata.Valid)
	var meta map[string]any
	require.NoError(t, json.Unmarshal([]byte(rec.Metadata.String), &meta))
	plannerMeta, ok := meta["planner"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, plan.ID, plannerMeta["target_plan_id"])
}

func TestPlanner_BuildTaskRequiresTarget(t *testing.T) {
	_, err := planner.BuildTask(planner.BuildOptions{
		IDFn: func() (string, error) { return "x", nil },
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TargetPlanID")
}

// AC4: refinement persists in metadata.plan.planner_refinement +
// metadata.plan.planner_refined_at — no new tables. Round-trip via
// Write + Read confirms the schema survives JSON marshal/unmarshal.
func TestPlanner_RefinementRoundTrip(t *testing.T) {
	store := setupPlannerStore(t)

	plan := &sqlstore.TaskRecord{
		ID: "CW-PLAN-002", Title: "test plan", Kind: "plan", Status: "todo", Executor: "cli",
		Metadata: sql.NullString{
			// Pre-existing metadata.plan blob — Write must preserve
			// keys it didn't author (here, plan.phases).
			String: `{"plan":{"phases":[{"id":"P1"}]}}`,
			Valid:  true,
		},
	}
	require.NoError(t, store.CreateTask(plan))

	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	ref := &planner.Refinement{
		PerTask: []planner.TaskHint{
			{
				TaskID:            "CW-CHILD-001",
				SuggestedPriority: 1,
				EstTokenBudget:    8000,
				ContextToInject:   "see ADR-0042",
				BlockersFlagged:   []string{"depends-on-CW-CHILD-002"},
			},
		},
		PlanLevel: &planner.PlanLevelChanges{
			Ordering:       []string{"CHILD-002 before CHILD-001"},
			AcceptanceGaps: []string{"phase 2 missing rollback steps"},
		},
		Confidence: "high",
		Notes:      "single-pass refinement",
	}

	require.NoError(t, planner.Write(store, plan.ID, ref, now))

	got, err := planner.Read(store, plan.ID)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, "v0", got.Version, "Write should default Version to v0")
	require.Len(t, got.PerTask, 1)
	assert.Equal(t, "CW-CHILD-001", got.PerTask[0].TaskID)
	assert.Equal(t, 8000, got.PerTask[0].EstTokenBudget)
	assert.Equal(t, []string{"depends-on-CW-CHILD-002"}, got.PerTask[0].BlockersFlagged)
	require.NotNil(t, got.PlanLevel)
	assert.Equal(t, []string{"CHILD-002 before CHILD-001"}, got.PlanLevel.Ordering)
	assert.Equal(t, "high", got.Confidence)

	// The pre-existing plan.phases blob must survive.
	post, err := store.GetTask(plan.ID)
	require.NoError(t, err)
	require.True(t, post.Metadata.Valid)
	var root map[string]any
	require.NoError(t, json.Unmarshal([]byte(post.Metadata.String), &root))
	plan2, ok := root["plan"].(map[string]any)
	require.True(t, ok)
	phases, ok := plan2["phases"].([]any)
	require.True(t, ok, "Write must preserve unrelated metadata.plan keys")
	assert.Len(t, phases, 1)

	// planner_refined_at is the RFC3339 timestamp Write stamped.
	stamped, ok := plan2[planner.MetadataTimestampKey].(string)
	require.True(t, ok)
	parsed, err := time.Parse(time.RFC3339, stamped)
	require.NoError(t, err)
	assert.True(t, parsed.Equal(now.UTC()))
}

// Read returns (nil, nil) when no refinement has been written. The
// orchestrator falls back to the as-authored plan in that case.
func TestPlanner_ReadEmpty(t *testing.T) {
	store := setupPlannerStore(t)
	plan := &sqlstore.TaskRecord{
		ID: "CW-PLAN-003", Title: "no metadata", Kind: "plan", Status: "todo", Executor: "cli",
	}
	require.NoError(t, store.CreateTask(plan))

	got, err := planner.Read(store, plan.ID)
	require.NoError(t, err)
	assert.Nil(t, got, "no metadata → nil refinement, no error")
}

// AC: embedded template fallback resolves when the user has no
// override directory. Substrate ships the template; fresh installs
// just work.
func TestPlanner_LoadTemplateEmbeddedFallback(t *testing.T) {
	t.Setenv(planner.TemplateEnvVar, "/nonexistent/path/that/should/not/exist")
	t.Setenv("HOME", "/nonexistent/home/that/should/not/exist")

	content, path := planner.LoadTemplate()
	assert.NotEmpty(t, content, "embedded fallback must not be empty")
	assert.Contains(t, content, "Planner", "embedded template must mention Planner")
	assert.Equal(t, "<embedded>", path)
}
