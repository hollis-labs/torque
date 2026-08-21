package scheduler_test

import (
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPicker_DependencyCheck_BatchedMatchesPerTask covers Pick()'s batched
// dependency check (picker.go's bulk fetch ahead of the main loop) against a
// mix of zero-dependency, met-dependency, and unmet-dependency candidates in
// one tick. The eligible set and skip reasons must be identical to what the
// original per-task ListTaskDependencyIDs/GetTask loop produced — the
// batching is a pure round-trip optimization, not a behavior change.
func TestPicker_DependencyCheck_BatchedMatchesPerTask(t *testing.T) {
	store := setupPickerStore(t)
	picker := scheduler.NewPicker(store)

	// Dependency targets.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-BATCH-DONE-DEP", Title: "done dependency", Status: "done", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile", Manual: true,
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-BATCH-NOTDONE-DEP", Title: "not-done dependency", Status: "review", Priority: 1,
		Executor: "cli", AgentProfile: "cli-profile", Manual: true,
	}))

	// Zero-dependency candidate — must never be affected by the dependency
	// batch machinery at all.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-BATCH-NODEPS", Title: "no dependencies", Status: "todo", Priority: 10,
		Executor: "cli", AgentProfile: "cli-profile",
	}))

	// Met-dependency candidate — depends only on the done task.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-BATCH-MET", Title: "dependency satisfied", Status: "todo", Priority: 11,
		Executor: "cli", AgentProfile: "cli-profile",
	}))
	require.NoError(t, store.SetTaskDependencies("CW-BATCH-MET", []string{"CW-BATCH-DONE-DEP"}))

	// Unmet-dependency candidate — depends on both; one isn't done.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-BATCH-UNMET", Title: "dependency outstanding", Status: "todo", Priority: 12,
		Executor: "cli", AgentProfile: "cli-profile",
	}))
	require.NoError(t, store.SetTaskDependencies("CW-BATCH-UNMET", []string{"CW-BATCH-DONE-DEP", "CW-BATCH-NOTDONE-DEP"}))

	picked, decisions, err := picker.Pick(10)
	require.NoError(t, err)

	pickedIDs := make(map[string]bool, len(picked))
	for _, p := range picked {
		pickedIDs[p.ID] = true
	}
	assert.True(t, pickedIDs["CW-BATCH-NODEPS"], "zero-dependency candidate must be picked")
	assert.True(t, pickedIDs["CW-BATCH-MET"], "candidate whose only dependency is done must be picked")
	assert.False(t, pickedIDs["CW-BATCH-UNMET"], "candidate with an outstanding dependency must not be picked")

	reasonByID := map[string]string{}
	for _, sd := range decisions.Skipped {
		reasonByID[sd.TaskID] = sd.Reason
	}
	assert.Equal(t, scheduler.SkipReasonDepUnmet, reasonByID["CW-BATCH-UNMET"])
	_, nodepsSkipped := reasonByID["CW-BATCH-NODEPS"]
	assert.False(t, nodepsSkipped)
	_, metSkipped := reasonByID["CW-BATCH-MET"]
	assert.False(t, metSkipped)
}
