package sqlstore_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestListTaskDependencyIDsBulk verifies the batched form returns the same
// per-task edges (in sort_order) as ListTaskDependencyIDs, for a mix of
// tasks with zero, one, and several dependencies, plus an id with no rows at
// all — the picker.go fast path this method exists for relies on a missing
// key meaning "no dependencies", not an error.
func TestListTaskDependencyIDsBulk(t *testing.T) {
	store := setupTestStore(t)
	_, err := store.DB().Exec("PRAGMA foreign_keys = OFF")
	require.NoError(t, err)

	for _, id := range []string{"CW-BULK-A", "CW-BULK-B", "CW-BULK-C", "CW-BULK-D", "CW-BULK-NODEPS"} {
		require.NoError(t, store.CreateTask(sampleTask(id)))
	}
	require.NoError(t, store.SetTaskDependencies("CW-BULK-A", []string{"CW-BULK-B", "CW-BULK-C"}))
	require.NoError(t, store.SetTaskDependencies("CW-BULK-D", []string{"CW-BULK-B"}))
	// CW-BULK-NODEPS has no dependency edges at all.

	got, err := store.ListTaskDependencyIDsBulk([]string{"CW-BULK-A", "CW-BULK-D", "CW-BULK-NODEPS"})
	require.NoError(t, err)

	assert.Equal(t, []string{"CW-BULK-B", "CW-BULK-C"}, got["CW-BULK-A"])
	assert.Equal(t, []string{"CW-BULK-B"}, got["CW-BULK-D"])
	_, present := got["CW-BULK-NODEPS"]
	assert.False(t, present, "a task with no dependency edges must be absent from the map, not an empty-slice entry")
}

// TestListTaskDependencyIDsBulk_EmptyInput confirms the empty-input
// short-circuit returns an empty map with no error (picker.go relies on
// this when a tick has zero candidates).
func TestListTaskDependencyIDsBulk_EmptyInput(t *testing.T) {
	store := setupTestStore(t)
	got, err := store.ListTaskDependencyIDsBulk(nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestGetTaskStatuses verifies the batched status lookup matches each task's
// real status and that a nonexistent id is simply absent from the map
// (picker.go treats an absent id the same as a GetTask not-found error: dep
// not met).
func TestGetTaskStatuses(t *testing.T) {
	store := setupTestStore(t)

	done := sampleTask("CW-STATUS-DONE")
	done.Status = "done"
	require.NoError(t, store.CreateTask(done))

	todo := sampleTask("CW-STATUS-TODO")
	todo.Status = "todo"
	require.NoError(t, store.CreateTask(todo))

	got, err := store.GetTaskStatuses([]string{"CW-STATUS-DONE", "CW-STATUS-TODO", "CW-STATUS-MISSING"})
	require.NoError(t, err)

	assert.Equal(t, "done", got["CW-STATUS-DONE"])
	assert.Equal(t, "todo", got["CW-STATUS-TODO"])
	_, present := got["CW-STATUS-MISSING"]
	assert.False(t, present, "a nonexistent id must be absent from the map, not a zero-value entry")
}

// TestGetTaskStatuses_EmptyInput confirms the empty-input short-circuit
// returns an empty map with no error.
func TestGetTaskStatuses_EmptyInput(t *testing.T) {
	store := setupTestStore(t)
	got, err := store.GetTaskStatuses(nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}
