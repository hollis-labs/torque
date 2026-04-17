package scheduler_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
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
		Priority: 1, Executor: "cli",
		ProjectID: sql.NullString{String: "PRJ-A", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-A-TODO", Title: "A waiting", Status: "todo",
		Priority: 1, Executor: "cli",
		ProjectID: sql.NullString{String: "PRJ-A", Valid: true},
	}))
	// Project B: todo, no doing — eligible.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-B-TODO", Title: "B waiting", Status: "todo",
		Priority: 2, Executor: "cli",
		ProjectID: sql.NullString{String: "PRJ-B", Valid: true},
	}))

	picked, err := picker.Pick(10)
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
		Priority: 1, Executor: "cli",
		ProjectID: sql.NullString{String: "PRJ-X", Valid: true},
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-X-2", Title: "Second", Status: "todo",
		Priority: 2, Executor: "cli",
		ProjectID: sql.NullString{String: "PRJ-X", Valid: true},
	}))

	picked, err := picker.Pick(10)
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
			Priority: 1, Executor: "cli",
			ProjectID: sql.NullString{String: proj, Valid: true},
		}))
	}

	picked, err := picker.Pick(10)
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
		Priority: 1, Executor: "cli",
	}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-ANON-2", Title: "anon 2", Status: "todo",
		Priority: 2, Executor: "cli",
	}))

	picked, err := picker.Pick(10)
	require.NoError(t, err)
	assert.Len(t, picked, 2, "project-less tasks are not gated by the per-project rule")
}
