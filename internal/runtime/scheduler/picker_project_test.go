package scheduler_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Per-project max-concurrent=1: two todo tasks in same project, one already
// doing in that project, picker returns 0 (project_id is busy).
func TestPickerProjectConcurrencyGate(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	// Project A: one doing, one todo — todo must be gated out.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-A-DOING", Title: "A running", Status: "doing",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-A", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-A-TODO", Title: "A waiting", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-A", Valid: true},
	}))
	// Project B: todo, no doing — eligible.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-B-TODO", Title: "B waiting", Status: "todo",
		Priority: 2, Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-B", Valid: true},
	}))

	picked, _, err := picker.Pick(10)
	require.NoError(t, err)
	require.Len(t, picked, 1, "only B should be picked; A is gated by same-project in-flight")
	assert.Equal(t, "CW-B-TODO", picked[0].ID)
}

// Two tasks in same project — picker returns only the first, gating the
// second tentatively allocated slot.
func TestPickerSameProjectInSingleTick(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-X-1", Title: "First", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-X", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-X-2", Title: "Second", Status: "todo",
		Priority: 2, Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-X", Valid: true},
	}))

	picked, _, err := picker.Pick(10)
	require.NoError(t, err)
	require.Len(t, picked, 1, "only one of two same-project tasks picks in a single tick")
	assert.Equal(t, "CW-X-1", picked[0].ID, "higher-priority one wins")
}

// Different projects can all pick up to `limit` — no gate between projects.
func TestPickerDifferentProjectsAllEligible(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	for _, proj := range []string{"PRJ-1", "PRJ-2", "PRJ-3"} {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: "CW-" + proj, Title: proj, Status: "todo",
			Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
			ProjectID: sql.NullString{String: proj, Valid: true},
		}))
	}

	picked, _, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, picked, 3, "three different projects all eligible")
}

// Tasks without a project_id are NOT gated by the per-project rule —
// the gate only applies when project_id is set. Project-less tasks
// serialize via worker count alone.
func TestPickerAnonymousProjectNotGated(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-ANON-1", Title: "anon 1", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-ANON-2", Title: "anon 2", Status: "todo",
		Priority: 2, Executor: "cli", AgentProfile: "cli-profile",
	}))

	picked, _, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, picked, 2, "project-less tasks are not gated by the per-project rule")
}

// Regression: the project-allocation slot must not be consumed by a candidate
// that then fails its dependency check. Previously the picker reserved the
// project bucket before verifying deps, so a higher-priority dep-blocked task
// silently starved every other task in the same project — producing an
// "idle scheduler despite eligible tasks" state with no error logs.
// (CW-20260418-0003)
func TestPickerDoesNotConsumeProjectSlotOnDepBlockedTask(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	// Unmet dependency: CW-DEP is still todo. Marked manual so it cannot
	// itself be picked — this isolates the per-project-allocation bug from
	// the project-less-eligible case.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-DEP", Title: "blocker", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile", Manual: true,
	}))
	// Higher-priority task in project P with CW-DEP as an unmet dep.
	// Ordering (priority ASC, created_at ASC) places this first.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-P-BLOCKED", Title: "dep-blocked", Status: "todo",
		Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-P", Valid: true},
		DependsOn: sql.NullString{String: `["CW-DEP"]`, Valid: true},
	}))
	// Lower-priority task in the same project with NO deps — this must be
	// picked once the dep-blocked task is skipped.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-P-READY", Title: "ready", Status: "todo",
		Priority: 2,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-P", Valid: true},
	}))

	picked, _, err := picker.Pick(10)
	require.NoError(t, err)
	require.Len(t, picked, 1, "dep-blocked task must not starve same-project siblings")
	assert.Equal(t, "CW-P-READY", picked[0].ID)
}

// CW-20260509-0002: a doing kind=plan task (orchestrator's host) must NOT
// block dispatch of agent children in the same project. The orchestrator
// drives plan execution by promoting children to manual=false and waiting
// for the scheduler to pick them up; if its own kind=plan task counted as
// busy, the children would skip with project_busy and the orchestrator
// would deadlock forever (the smoke-blocker observed 2026-05-08).
func TestPickerKindPlanDoesNotBlockSameProjectChildren(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	// Orchestrator's host: kind=plan, status=doing on project P.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-PLAN-DOING", Title: "orchestrator host", Status: "doing",
		Kind: "plan", Priority: 1,
		ProjectID: sql.NullString{String: "PRJ-COORD", Valid: true},
	}))
	// Agent child the orchestrator just promoted: same project, todo,
	// manual=false. Must be eligible despite the plan task being doing.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-CHILD-1", Title: "promoted child", Status: "todo",
		Kind: "agent", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-COORD", Valid: true},
	}))

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	require.Len(t, picked, 1, "kind=plan task must not block same-project agent children")
	assert.Equal(t, "CW-CHILD-1", picked[0].ID)
	assert.Equal(t, 0, decisions.Counts[scheduler.SkipReasonProjectBusy],
		"no candidate should be skipped with project_busy when only a kind=plan task is in flight")
}

// Sister case: a doing kind=agent worker task DOES block same-project
// children (busy semantics preserved for actual workers). Guards against
// over-broadening the coordination-role exclusion.
func TestPickerKindAgentDoingStillBlocksSameProject(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	// Real worker: kind=agent, status=doing.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-WORKER-DOING", Title: "active worker", Status: "doing",
		Kind: "agent", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-WORKER", Valid: true},
	}))
	// Same-project sibling — must be gated by project_busy.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-SIBLING", Title: "waiting sibling", Status: "todo",
		Kind: "agent", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-WORKER", Valid: true},
	}))

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, picked, 0, "same-project sibling must be gated while a worker is doing")
	assert.Equal(t, 1, decisions.Counts[scheduler.SkipReasonProjectBusy])
}

// Mixed case: a kind=plan coordinator AND a kind=agent worker both running
// in the same project. The worker still triggers the busy gate; the plan
// task alone does not. Confirms the exclusion is additive, not subtractive.
func TestPickerMixedCoordinatorAndWorkerBlocks(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-PLAN-MIX", Title: "orchestrator", Status: "doing",
		Kind: "plan", Priority: 1,
		ProjectID: sql.NullString{String: "PRJ-MIX", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-WORKER-MIX", Title: "active worker", Status: "doing",
		Kind: "agent", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-MIX", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-PENDING-MIX", Title: "queued sibling", Status: "todo",
		Kind: "agent", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-MIX", Valid: true},
	}))

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, picked, 0, "worker doing still gates the project regardless of plan presence")
	assert.Equal(t, 1, decisions.Counts[scheduler.SkipReasonProjectBusy])
}

// Same shape as TestPickerKindPlanDoesNotBlockSameProjectChildren, but for
// kind=parent. Parents are status-derived by ParentRollupTick (never
// dispatched by the picker — SkipReasonParentKind), so a parent stuck in
// `doing` (legacy row, manual transition, or transient rollup state)
// shouldn't gate dispatch of its own children. Mirrors the plan-task
// exclusion in the busyProjects accumulator.
func TestPickerKindParentDoesNotBlockSameProjectChildren(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	// Parent task: kind=parent, status=doing on project P.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-PARENT-DOING", Title: "parent host", Status: "doing",
		Kind: "parent", Priority: 1,
		ProjectID: sql.NullString{String: "PRJ-PARENT", Valid: true},
	}))
	// Agent child of that parent: same project, todo, manual=false.
	// Must be eligible despite the parent being doing.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-PARENT-CHILD", Title: "child of parent", Status: "todo",
		Kind: "agent", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-PARENT", Valid: true},
	}))

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	require.Len(t, picked, 1, "kind=parent task must not block same-project agent children")
	assert.Equal(t, "CW-PARENT-CHILD", picked[0].ID)
	assert.Equal(t, 0, decisions.Counts[scheduler.SkipReasonProjectBusy],
		"no candidate should be skipped with project_busy when only a kind=parent task is in flight")
}
