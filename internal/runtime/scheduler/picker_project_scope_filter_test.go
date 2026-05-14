package scheduler_test

// DEPRECATED: remove when CW-20260417-0129 (workspace support) ships.
// These tests cover the stopgap TORQUE_PROJECT_ID / TORQUE_PROJECT_IDS
// scheduler allowlist (CW-20260417-0130). Delete this file when the
// workspace feature lands and the SkipReasonProjectScopeFilter constant is
// removed from the picker.

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Allowlist = [PRJ-A]. Candidate set spans PRJ-A (eligible) and PRJ-B
// (filtered). Pick must return only PRJ-A tasks, and PRJ-B candidates must
// be tallied under SkipReasonProjectScopeFilter so the per-tick counters
// line up.
func TestPickerProjectScopeFilter_OnlyAllowlistedPicked(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)
	picker.SetProjectAllowlist([]string{"PRJ-A"})

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-A-1", Title: "A one", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-A", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-B-1", Title: "B one", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-B", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-B-2", Title: "B two", Status: "todo",
		Priority: 2, Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-B", Valid: true},
	}))

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	require.Len(t, picked, 1, "only PRJ-A should be picked")
	assert.Equal(t, "CW-A-1", picked[0].ID)

	assert.Equal(t, 2, decisions.Counts[scheduler.SkipReasonProjectScopeFilter],
		"both PRJ-B rows must count under project_scope_filter")
	assert.Zero(t, decisions.Counts[scheduler.SkipReasonProjectBusy],
		"filtered tasks must not consume concurrency accounting")
	assert.Zero(t, decisions.Counts[scheduler.SkipReasonProjectContention],
		"filtered tasks must not consume concurrency accounting")
}

// Back-compat: no allowlist set → all projects eligible (current behavior).
func TestPickerProjectScopeFilter_UnsetAllowsAllProjects(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)
	// Deliberately no SetProjectAllowlist call.

	for _, proj := range []string{"PRJ-A", "PRJ-B", "PRJ-C"} {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: "CW-" + proj, Title: proj, Status: "todo",
			Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
			ProjectID: sql.NullString{String: proj, Valid: true},
		}))
	}

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, picked, 3, "unset allowlist = no filter")
	assert.Zero(t, decisions.Counts[scheduler.SkipReasonProjectScopeFilter],
		"no scope-filter skips when allowlist is empty")
}

// Empty allowlist (explicitly set to nil/empty) behaves like unset.
func TestPickerProjectScopeFilter_EmptyAllowlistNoFilter(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)
	picker.SetProjectAllowlist(nil)
	picker.SetProjectAllowlist([]string{}) // belt-and-braces both forms

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-A-1", Title: "A", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-A", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-ANON", Title: "anon", Status: "todo",
		Priority: 2, Executor: "cli", AgentProfile: "cli-profile",
	}))

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, picked, 2)
	assert.Zero(t, decisions.Counts[scheduler.SkipReasonProjectScopeFilter])
}

// Allowlist pointed at a project with no eligible tasks — picker returns
// empty even though other-project tasks exist.
func TestPickerProjectScopeFilter_EmptyMatchReturnsNothing(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)
	picker.SetProjectAllowlist([]string{"PRJ-GHOST"})

	for _, proj := range []string{"PRJ-A", "PRJ-B"} {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: "CW-" + proj, Title: proj, Status: "todo",
			Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
			ProjectID: sql.NullString{String: proj, Valid: true},
		}))
	}

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Empty(t, picked, "allowlist hits no project → no picks")
	assert.Equal(t, 2, decisions.Counts[scheduler.SkipReasonProjectScopeFilter])
}

// Allowlist with 3 IDs, candidates spread across 5 projects — only the
// three allowlisted projects surface.
func TestPickerProjectScopeFilter_SubsetFromLargerCandidateSet(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)
	picker.SetProjectAllowlist([]string{"PRJ-1", "PRJ-3", "PRJ-5"})

	for _, proj := range []string{"PRJ-1", "PRJ-2", "PRJ-3", "PRJ-4", "PRJ-5"} {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: "CW-" + proj, Title: proj, Status: "todo",
			Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
			ProjectID: sql.NullString{String: proj, Valid: true},
		}))
	}

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)

	gotIDs := make([]string, 0, len(picked))
	for _, t := range picked {
		gotIDs = append(gotIDs, t.ID)
	}
	assert.ElementsMatch(t,
		[]string{"CW-PRJ-1", "CW-PRJ-3", "CW-PRJ-5"}, gotIDs,
		"only the three allowlisted projects should pick")
	assert.Equal(t, 2, decisions.Counts[scheduler.SkipReasonProjectScopeFilter],
		"PRJ-2 and PRJ-4 must count as project_scope_filter skips")
}

// When an allowlist is active, project-less tasks (no project_id) are
// filtered out too. The operator has asked for a named-project scope; an
// anonymous task isn't part of that scope.
func TestPickerProjectScopeFilter_FiltersProjectLessTasks(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)
	picker.SetProjectAllowlist([]string{"PRJ-A"})

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-A-1", Title: "A", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-A", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-ANON", Title: "anon", Status: "todo",
		Priority: 2, Executor: "cli", AgentProfile: "cli-profile",
	}))

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	require.Len(t, picked, 1)
	assert.Equal(t, "CW-A-1", picked[0].ID)
	assert.Equal(t, 1, decisions.Counts[scheduler.SkipReasonProjectScopeFilter],
		"project-less task must count as a scope-filter skip when allowlist active")
}
