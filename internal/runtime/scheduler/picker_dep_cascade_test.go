package scheduler_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPickerDependencyCascadeOnDelete is the FK-003 / DEC-003 deadlock-fix
// regression guard, exercised end-to-end against a real store.
//
// Before migration 027, depends_on was a JSON array stored directly on the
// tasks row, and nothing ever pruned a stale ID when the referenced task was
// deleted. That meant: delete a dependency task, and the dependent task's
// depends_on array still listed it forever; the picker's per-dep GetTask
// lookup (picker.go) then failed on every tick, allMet never became true
// again, and the dependent task stayed SkipReasonDepUnmet permanently — a
// silent, unrecoverable scheduler deadlock with no operator-visible error.
//
// With task_dependencies + ON DELETE CASCADE on depends_on_task_id, deleting
// the dependency task removes the join row automatically, so the dependent
// task is eligible again on the very next Pick() call with no other
// intervention required.
func TestPickerDependencyCascadeOnDelete(t *testing.T) {
	store := setupPickerStore(t)
	// setupPickerStore disables FK enforcement package-wide (FK-002) so
	// other picker/cost tests can use synthetic, non-existent sprint/
	// project IDs without violating the new tasks.sprint_id/project_id/
	// epic_id constraints. This test needs the opposite: it's exercising
	// ON DELETE CASCADE on task_dependencies, which SQLite only enforces
	// when foreign_keys is ON. Both tasks here are real rows with no
	// synthetic sprint/project/epic references, so re-enabling FK
	// enforcement locally is safe.
	_, err := store.DB().Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)
	picker := scheduler.NewPicker(store)

	// The dependency task. Manual=true so it never becomes a picker
	// candidate itself — isolates the cascade behavior from "did the
	// dependency also get picked and finish" noise.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-DEL-DEP", Title: "will be deleted", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile", Manual: true,
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-DEL-DEPENDENT", Title: "depends on CW-DEL-DEP", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
	}))
	require.NoError(t, store.SetTaskDependencies("CW-DEL-DEPENDENT", []string{"CW-DEL-DEP"}))

	// Before delete: the dependent must be blocked — the dependency exists
	// and is not done.
	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, picked, 0, "dependent must not be eligible while its dependency is outstanding")
	var reason string
	for _, sd := range decisions.Skipped {
		if sd.TaskID == "CW-DEL-DEPENDENT" {
			reason = sd.Reason
		}
	}
	assert.Equal(t, scheduler.SkipReasonDepUnmet, reason)

	// Delete the dependency task. This is exactly the scenario that used to
	// deadlock under the old JSON-column implementation.
	require.NoError(t, store.DeleteTask("CW-DEL-DEP"))

	// Confirm the join row was actually pruned by ON DELETE CASCADE, not
	// just that Pick happens to pass for some other reason.
	deps, err := store.ListTaskDependencyIDs("CW-DEL-DEPENDENT")
	require.NoError(t, err)
	assert.Empty(t, deps, "ON DELETE CASCADE must prune the edge when the dependency task is deleted")

	// After delete: the dependent is immediately eligible on the very next
	// scheduling tick — no manual cleanup, no restart, no stale block.
	picked, _, err = picker.Pick(10)
	require.NoError(t, err)
	require.Len(t, picked, 1, "dependent must become eligible the moment its only dependency is deleted")
	assert.Equal(t, "CW-DEL-DEPENDENT", picked[0].ID)
}

// TestPickerDependencyCascadeOnDependentDelete confirms the second,
// smaller dangling-reference direction from FK-003's scope: deleting the
// DEPENDENT task itself must not leave an orphaned task_dependencies row
// (ON DELETE CASCADE on task_id, not just depends_on_task_id).
func TestPickerDependencyCascadeOnDependentDelete(t *testing.T) {
	store := setupPickerStore(t)
	// See TestPickerDependencyCascadeOnDelete's comment: re-enable FK
	// enforcement locally so ON DELETE CASCADE actually fires.
	_, err := store.DB().Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-KEEP-DEP", Title: "dependency", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-DOOMED-DEPENDENT", Title: "will be deleted", Status: "todo",
		Priority: 1, Executor: "cli", AgentProfile: "cli-profile",
	}))
	require.NoError(t, store.SetTaskDependencies("CW-DOOMED-DEPENDENT", []string{"CW-KEEP-DEP"}))

	require.NoError(t, store.DeleteTask("CW-DOOMED-DEPENDENT"))

	deps, err := store.ListTaskDependencyIDs("CW-DOOMED-DEPENDENT")
	require.NoError(t, err)
	assert.Empty(t, deps, "no orphaned task_dependencies row should survive the dependent task's own deletion")
}
