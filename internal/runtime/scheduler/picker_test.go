package scheduler_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupPickerStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestPickerEligibleTasks(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task A", Status: "todo", Priority: 2, Executor: "cli", AgentProfile: "cli-profile"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "Task B", Status: "todo", Priority: 1, Executor: "cli", AgentProfile: "cli-profile"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0003", Title: "Task C", Status: "doing", Priority: 1, Executor: "cli", AgentProfile: "cli-profile"})

	tasks, _, err := picker.Pick(3)
	require.NoError(t, err)
	assert.Len(t, tasks, 2, "only todo tasks are eligible")
	assert.Equal(t, "CW-0002", tasks[0].ID, "P1 comes before P2")
	assert.Equal(t, "CW-0001", tasks[1].ID)
}

func TestPickerSkipsManualTasks(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Auto", Status: "todo", Priority: 2, Executor: "cli", AgentProfile: "cli-profile"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "Manual", Status: "todo", Priority: 1, Manual: true, Executor: "cli", AgentProfile: "cli-profile"})

	tasks, _, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "CW-0001", tasks[0].ID)
}

func TestPickerRespectsLimit(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "A", Status: "todo", Priority: 1, Executor: "cli", AgentProfile: "cli-profile"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "B", Status: "todo", Priority: 1, Executor: "cli", AgentProfile: "cli-profile"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0003", Title: "C", Status: "todo", Priority: 1, Executor: "cli", AgentProfile: "cli-profile"})

	tasks, _, err := picker.Pick(2)
	require.NoError(t, err)
	assert.Len(t, tasks, 2)
}

func TestPickerSkipsBlockedDependencies(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Prerequisite", Status: "todo", Priority: 1, Executor: "cli", AgentProfile: "cli-profile"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "Dependent", Status: "todo", Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
		DependsOn: sql.NullString{String: `["CW-0001"]`, Valid: true}})

	tasks, _, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, tasks, 1, "dependent task should not be picked until dependency is done")
	assert.Equal(t, "CW-0001", tasks[0].ID)
}

func TestPickerAllowsDependencyMet(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Prerequisite", Status: "done", Priority: 1, Executor: "cli", AgentProfile: "cli-profile"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "Dependent", Status: "todo", Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
		DependsOn: sql.NullString{String: `["CW-0001"]`, Valid: true}})

	tasks, _, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, tasks, 1)
	assert.Equal(t, "CW-0002", tasks[0].ID)
}

func TestPickerEmptyStore(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	tasks, _, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, tasks, 0)
}

// Observability: multi-state tick should produce a PickDecisions with
// counters keyed by canonical skip-reason constants. Mirrors the tick
// scenario from CW-20260418-0016: 3 empty-profile, 2 dep-unmet, 2 manual,
// 1 retry-exhausted (status=blocked — not picker-visible), 2 eligible.
// Pick(3) must return only the 2 eligible rows; Pick honours the limit
// but must still tally ALL skipped candidates it saw so operators can
// diagnose a silent scheduler.
func TestPickerDecisionsMultiStateTick(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	// 3 empty-profile (agent kind, no AgentProfile set). These are todo
	// but rejected at the picker per the CW-20260418-0010 guard.
	for i := 1; i <= 3; i++ {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: "CW-EMPTY-" + string(rune('0'+i)), Title: "empty", Status: "todo",
			Priority: 3, Executor: "cli", Kind: "agent",
			// AgentProfile intentionally omitted.
		}))
	}

	// 2 dep-unmet: dependency on a todo task (not done). Manual=true on
	// the dep so the dep itself doesn't get picked and steal a slot.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-DEP-BLOCKER", Title: "blocker", Status: "todo",
		Priority: 5, Executor: "cli", AgentProfile: "cli-profile", Manual: true,
	}))
	for i := 1; i <= 2; i++ {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: "CW-DEPUNMET-" + string(rune('0'+i)), Title: "depunmet", Status: "todo",
			Priority: 3, Executor: "cli", AgentProfile: "cli-profile",
			DependsOn: sql.NullString{String: `["CW-DEP-BLOCKER"]`, Valid: true},
		}))
	}

	// 2 manual.
	for i := 1; i <= 2; i++ {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: "CW-MANUAL-" + string(rune('0'+i)), Title: "manual", Status: "todo",
			Priority: 3, Executor: "cli", AgentProfile: "cli-profile", Manual: true,
		}))
	}

	// 1 retry-exhausted: modelled as status=blocked, which is how the
	// lifecycle transitions tasks whose retry budget is spent. The
	// picker only consults status=todo rows, so this must NEVER surface
	// in decisions.Counts nor in the picked slice. Asserting that
	// documents the "blocked tasks are out-of-scope for picker
	// observability" contract.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-RETRY-EXHAUSTED", Title: "retry exhausted", Status: "blocked",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
	}))

	// 2 eligible.
	for i := 1; i <= 2; i++ {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: "CW-ELIGIBLE-" + string(rune('0'+i)), Title: "eligible", Status: "todo",
			Priority: 2, Executor: "cli", AgentProfile: "cli-profile",
		}))
	}

	picked, decisions, err := picker.Pick(3)
	require.NoError(t, err)

	// Eligible rows picked. Only 2 eligible exist even though limit=3.
	assert.Len(t, picked, 2, "only the 2 eligible tasks are picked")
	pickedIDs := []string{picked[0].ID, picked[1].ID}
	assert.ElementsMatch(t, []string{"CW-ELIGIBLE-1", "CW-ELIGIBLE-2"}, pickedIDs)

	// Candidates = all todo rows the picker saw (10: 3 empty + 1 dep-blocker
	// + 2 depunmet + 2 manual + 2 eligible). The blocked row is NOT counted.
	assert.Equal(t, 10, decisions.Candidates, "picker sees all todo rows")

	// Per-reason counters. Note the dep-blocker (manual) adds to the
	// manual count, bringing the total manual skips to 3.
	assert.Equal(t, 3, decisions.Counts[scheduler.SkipReasonEmptyProfile])
	assert.Equal(t, 2, decisions.Counts[scheduler.SkipReasonDepUnmet])
	assert.Equal(t, 3, decisions.Counts[scheduler.SkipReasonManual],
		"2 manual + 1 manual dep-blocker")
	assert.Zero(t, decisions.Counts["retry_exhausted"],
		"retry-exhausted (status=blocked) tasks are invisible to the picker")

	// Sum of all counter values must equal the number of per-task
	// SkipDecision entries — the two views are derived from the same
	// increment site and must never drift.
	var totalCounts int
	for _, v := range decisions.Counts {
		totalCounts += v
	}
	assert.Equal(t, totalCounts, len(decisions.Skipped),
		"Counts map and Skipped slice must agree")
	assert.Equal(t, 8, len(decisions.Skipped), "3 empty + 2 dep_unmet + 3 manual skipped")
}

// CW-20260503-0011 (S1.1): kind=internal tasks (Reviewer end-agents and
// other automation primitives) participate in scheduling but bypass the
// per-project concurrency gate — they are meta-work that should always
// flow alongside agent dispatches without holding or being held by
// project slots. Five internal todos plus one agent todo, all in the
// same project, must all become eligible in a single tick.
func TestPickerKindInternalBypassesProjectConcurrency(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	for i := 1; i <= 5; i++ {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: "CW-INTERNAL-" + string(rune('0'+i)), Title: "review", Status: "todo",
			Priority: 2, Kind: "internal", Executor: "cli", AgentProfile: "reviewer",
			ProjectID: sql.NullString{String: "PRJ-A", Valid: true},
		}))
	}
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-AGENT-1", Title: "agent", Status: "todo",
		Priority: 2, Kind: "agent", Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-A", Valid: true},
	}))

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, picked, 6, "5 internal + 1 agent all dispatch despite project max=1")
	assert.Zero(t, decisions.Counts[scheduler.SkipReasonProjectBusy])
	assert.Zero(t, decisions.Counts[scheduler.SkipReasonProjectContention])
}

// A kind=internal task already in `doing` for a project must NOT block
// an agent task from the same project from being picked.
func TestPickerDoingInternalDoesNotBlockProject(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-INTERNAL-DOING", Title: "review running", Status: "doing",
		Priority: 2, Kind: "internal", Executor: "cli", AgentProfile: "reviewer",
		ProjectID: sql.NullString{String: "PRJ-A", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-AGENT-WAIT", Title: "agent ready", Status: "todo",
		Priority: 2, Kind: "agent", Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "PRJ-A", Valid: true},
	}))

	picked, _, err := picker.Pick(10)
	require.NoError(t, err)
	require.Len(t, picked, 1)
	assert.Equal(t, "CW-AGENT-WAIT", picked[0].ID)
}

// kind=internal tasks need an agent_profile too — the empty-profile
// guard applies symmetrically to agent and internal so Reviewer / future
// System / PM agents that lack a profile are skipped at the picker
// (CW-20260418-0010 defense-in-depth extended in CW-20260503-0011).
func TestPickerInternalRequiresAgentProfile(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-INTERNAL-NOPROF", Title: "no profile", Status: "todo",
		Priority: 2, Kind: "internal", Executor: "cli", AgentProfile: "",
	}))

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, picked, 0)
	assert.Equal(t, 1, decisions.Counts[scheduler.SkipReasonEmptyProfile])
}
