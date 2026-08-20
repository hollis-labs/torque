package scheduler_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// CW-20260417-0378 regression guard.
//
// The ticket reported Pick() returning empty in a tick where the top-of-queue
// candidates were dep-blocked and a lower-priority sibling in an already-
// reserved project bucket should have been visible. CW-20260418-0003 (d742b12)
// resolved the starvation class by moving the per-project slot reservation to
// run AFTER the dep check (picker.go:186-189). These tests encode the expected
// behavior so a future refactor that moves reservation back above the dep
// check fails CI immediately.

// TestPicker_ReturnsLowPriEligibleWhenHighPriDepsUnmet reproduces the ticket
// scenario exactly. Ordering is priority ASC, so the scan order is:
//
//	CW-0152 (prio 6, dep unmet)       -> skip dep_unmet
//	CW-0098 (prio 7, deps unmet)      -> skip dep_unmet
//	CW-0079 (prio 20, no deps)        -> picked, reserves project "proj-A"
//	CW-0080 (prio 20, project proj-A) -> skip project_contention
//	CW-0128 (prio 22, project proj-A) -> skip project_contention
//
// CW-0151 is status=review so it's not a picker candidate at all — it only
// exists as a dependency target. Pick(2) yields exactly one picked task
// (CW-0079) even though the limit is 2, because everything else is either
// dep-blocked or gated by the already-reserved project slot.
func TestPicker_ReturnsLowPriEligibleWhenHighPriDepsUnmet(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	// CW-0151: not-done dependency target (status=review means "not done").
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0151", Title: "blocker in review", Status: "review", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile",
	}))

	// CW-0152: depends on CW-0151, higher priority (lower Priority int means
	// higher logical priority in the queue-order sense).
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0152", Title: "depends on blocker", Status: "todo", Priority: 6,
		Executor: "cli", AgentProfile: "cli-profile",
	}))
	require.NoError(t, store.SetTaskDependencies("CW-0152", []string{"CW-0151"}))

	// CW-0098: depends on both CW-0151 and CW-0152 (neither done).
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0098", Title: "depends on two blockers", Status: "todo", Priority: 7,
		Executor: "cli", AgentProfile: "cli-profile",
	}))
	require.NoError(t, store.SetTaskDependencies("CW-0098", []string{"CW-0151", "CW-0152"}))

	// CW-0079: eligible, sits in project "proj-A". This is the task that
	// should surface when the higher-priority candidates are dep-blocked.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0079", Title: "eligible low-pri", Status: "todo", Priority: 20,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "proj-A", Valid: true},
	}))

	// CW-0080: same project as CW-0079, will be skipped by the per-project
	// gate once CW-0079 reserves the slot.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0080", Title: "same project as 0079", Status: "todo", Priority: 20,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "proj-A", Valid: true},
	}))

	// CW-0128: also same project, scanned last due to higher Priority int.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-0128", Title: "same project, lower prio", Status: "todo", Priority: 22,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "proj-A", Valid: true},
	}))

	picked, decisions, err := picker.Pick(2)
	require.NoError(t, err)

	// The ticket-reported bug was "picked is empty". The post-fix invariant
	// is "picked contains exactly CW-0079".
	require.Len(t, picked, 1, "one task should be picked; dep-blocked high-pri tasks must not starve eligible low-pri siblings")
	assert.Equal(t, "CW-0079", picked[0].ID)

	// Reason tally. CW-0151 is status=review so is never a candidate.
	assert.Equal(t, 5, decisions.Candidates, "5 todo rows are picker candidates (0151 is review, not counted)")
	assert.Equal(t, 2, decisions.Counts[scheduler.SkipReasonDepUnmet], "CW-0152 and CW-0098 skip dep_unmet")
	assert.Equal(t, 2, decisions.Counts[scheduler.SkipReasonProjectContention], "CW-0080 and CW-0128 skip project_contention after 0079 reserves proj-A")

	// Per-task detail: reasons align with task IDs.
	reasonByID := map[string]string{}
	for _, sd := range decisions.Skipped {
		reasonByID[sd.TaskID] = sd.Reason
	}
	assert.Equal(t, scheduler.SkipReasonDepUnmet, reasonByID["CW-0152"])
	assert.Equal(t, scheduler.SkipReasonDepUnmet, reasonByID["CW-0098"])
	assert.Equal(t, scheduler.SkipReasonProjectContention, reasonByID["CW-0080"])
	assert.Equal(t, scheduler.SkipReasonProjectContention, reasonByID["CW-0128"])
}

// TestPicker_AllCandidatesSkippedReturnsEmpty guards the "every candidate is
// skipped" path. Pick must return (empty, decisions, nil) — not an error, and
// the reason counters must tally every skip. A regression that returned a
// non-nil error here would stall the scheduler loop.
func TestPicker_AllCandidatesSkippedReturnsEmpty(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	// Blocker in review — not a picker candidate, but its presence (not
	// status=done) makes dependents dep_unmet.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-BLOCKER", Title: "not done", Status: "review", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile",
	}))

	// Two dep-unmet candidates.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-DEP-1", Title: "dep unmet 1", Status: "todo", Priority: 5,
		Executor: "cli", AgentProfile: "cli-profile",
	}))
	require.NoError(t, store.SetTaskDependencies("CW-DEP-1", []string{"CW-BLOCKER"}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-DEP-2", Title: "dep unmet 2", Status: "todo", Priority: 6,
		Executor: "cli", AgentProfile: "cli-profile",
	}))
	require.NoError(t, store.SetTaskDependencies("CW-DEP-2", []string{"CW-BLOCKER"}))

	// Two project-contention candidates in the same project with a third
	// eligible sibling that reserves the slot first. Then we add ONE more
	// task that, because it's also in the same project and scanned after
	// the reserver, gets skipped. The reserver itself is eligible though —
	// so to make ALL candidates skipped, we put the reserver under a manual
	// flag so it's filtered out before it can reserve, and leave its two
	// same-project siblings also in the project — but now nothing reserves
	// the slot. Simpler path: add a doing task in that project so busyProjects
	// already holds the key, then same-project todo rows skip project_busy.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-INFLIGHT", Title: "already running", Status: "doing", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "proj-X", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-BUSY-1", Title: "project busy 1", Status: "todo", Priority: 10,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "proj-X", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-BUSY-2", Title: "project busy 2", Status: "todo", Priority: 11,
		Executor: "cli", AgentProfile: "cli-profile",
		ProjectID: sql.NullString{String: "proj-X", Valid: true},
	}))

	picked, decisions, err := picker.Pick(2)
	require.NoError(t, err, "all-candidates-skipped is NOT an error")
	assert.Len(t, picked, 0, "no task is eligible")

	// Candidates = todo rows only. 2 dep-unmet + 2 project-busy = 4.
	assert.Equal(t, 4, decisions.Candidates)
	assert.Equal(t, 2, decisions.Counts[scheduler.SkipReasonDepUnmet])
	assert.Equal(t, 2, decisions.Counts[scheduler.SkipReasonProjectBusy])

	// Sum-of-counts must equal len(Skipped) — same invariant as the existing
	// multi-state tick test.
	var totalCounts int
	for _, v := range decisions.Counts {
		totalCounts += v
	}
	assert.Equal(t, totalCounts, len(decisions.Skipped), "Counts map and Skipped slice must agree")
	assert.Equal(t, 4, len(decisions.Skipped))
}

// TestPicker_Limit1FirstSkippedReturnsNext guards the "limit=1, first-scanned
// candidate is not eligible" path. The picker must keep scanning until it
// finds an eligible task or exhausts candidates — returning early because the
// first candidate was skipped would regress CW-20260417-0378 at limit=1.
func TestPicker_Limit1FirstSkippedReturnsNext(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	// Blocker for the dep_unmet candidate.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-HEAD-BLOCKER", Title: "not done", Status: "review", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile",
	}))

	// Priority 5 dep-unmet candidate — scanned first.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-HEAD-DEP", Title: "dep unmet, scanned first", Status: "todo", Priority: 5,
		Executor: "cli", AgentProfile: "cli-profile",
	}))
	require.NoError(t, store.SetTaskDependencies("CW-HEAD-DEP", []string{"CW-HEAD-BLOCKER"}))

	// Priority 10 eligible candidate — scanned second.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-ELIGIBLE", Title: "eligible, lower priority", Status: "todo", Priority: 10,
		Executor: "cli", AgentProfile: "cli-profile",
	}))

	picked, decisions, err := picker.Pick(1)
	require.NoError(t, err)
	require.Len(t, picked, 1, "picker must keep scanning past a skipped head until it finds an eligible row")
	assert.Equal(t, "CW-ELIGIBLE", picked[0].ID, "eligible task, not the first-scanned dep-blocked one")

	assert.Equal(t, 1, decisions.Counts[scheduler.SkipReasonDepUnmet])
	assert.Equal(t, 2, decisions.Candidates, "both todo rows are candidates")
}
