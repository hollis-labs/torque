package sqlstore_test

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTaskAndRun is a small helper that creates a task and a run for it,
// returning the run's ID. Used by run_events tests that need a valid FK.
func createTaskAndRun(t *testing.T, store *sqlstore.Store, taskID string) int64 {
	t.Helper()
	require.NoError(t, store.CreateTask(sampleTask(taskID)))
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID:   taskID,
		Executor: "cli",
	})
	require.NoError(t, err)
	return runID
}

func TestAppendAndListRunEvents(t *testing.T) {
	store := setupTestStore(t)

	taskID := "CW-20260411-0001"
	runID := createTaskAndRun(t, store, taskID)

	id1, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
		RunID:   sql.NullInt64{Int64: runID, Valid: true},
		TaskID:  taskID,
		Type:    "run_started",
		Payload: `{"executor":"cli"}`,
	})
	require.NoError(t, err)
	assert.Greater(t, id1, int64(0))

	id2, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
		RunID:   sql.NullInt64{Int64: runID, Valid: true},
		TaskID:  taskID,
		Type:    "log",
		Payload: "line 1",
	})
	require.NoError(t, err)
	assert.Greater(t, id2, id1)

	id3, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
		RunID:   sql.NullInt64{Int64: runID, Valid: true},
		TaskID:  taskID,
		Type:    "run_completed",
		Payload: `{"status":"done"}`,
	})
	require.NoError(t, err)
	assert.Greater(t, id3, id2)

	events, err := store.ListRunEvents(sqlstore.RunEventFilter{RunID: runID})
	require.NoError(t, err)
	require.Len(t, events, 3)

	// id ASC ordering
	assert.Equal(t, id1, events[0].ID)
	assert.Equal(t, id2, events[1].ID)
	assert.Equal(t, id3, events[2].ID)

	// RunID round-trip
	assert.True(t, events[0].RunID.Valid)
	assert.Equal(t, runID, events[0].RunID.Int64)

	// Types and payloads round-trip
	assert.Equal(t, "run_started", events[0].Type)
	assert.Equal(t, `{"executor":"cli"}`, events[0].Payload)
	assert.Equal(t, "log", events[1].Type)
	assert.Equal(t, "line 1", events[1].Payload)
	assert.Equal(t, "run_completed", events[2].Type)

	// CreatedAt should be set by the DB
	assert.False(t, events[0].CreatedAt.IsZero())
}

func TestAppendRunEventRequiresTaskID(t *testing.T) {
	store := setupTestStore(t)

	_, err := store.AppendRunEvent(&sqlstore.RunEventRecord{Type: "log"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task_id")
}

func TestAppendRunEventRequiresType(t *testing.T) {
	store := setupTestStore(t)

	_, err := store.AppendRunEvent(&sqlstore.RunEventRecord{TaskID: "CW-X"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "type")
}

func TestAppendRunEventWithNullableRunID(t *testing.T) {
	store := setupTestStore(t)

	taskID := "CW-20260411-0002"
	require.NoError(t, store.CreateTask(sampleTask(taskID)))

	// Append an event with no run context — the core Step 6 use case.
	id, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
		RunID:   sql.NullInt64{}, // not valid -> NULL
		TaskID:  taskID,
		Type:    "task_transitioned",
		Payload: `{"from":"todo","to":"doing"}`,
	})
	require.NoError(t, err)
	assert.Greater(t, id, int64(0))

	events, err := store.ListRunEvents(sqlstore.RunEventFilter{TaskID: taskID})
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.False(t, events[0].RunID.Valid, "RunID should be NULL for pre-run events")
	assert.Equal(t, "task_transitioned", events[0].Type)
}

func TestListRunEventsCursor(t *testing.T) {
	store := setupTestStore(t)

	taskID := "CW-20260411-0003"
	runID := createTaskAndRun(t, store, taskID)

	// Insert 5 events.
	ids := make([]int64, 0, 5)
	for i := 0; i < 5; i++ {
		id, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
			RunID:  sql.NullInt64{Int64: runID, Valid: true},
			TaskID: taskID,
			Type:   "log",
		})
		require.NoError(t, err)
		ids = append(ids, id)
	}

	// SinceID = ids[1] -> expect events with ids[2], ids[3], ids[4]
	events, err := store.ListRunEvents(sqlstore.RunEventFilter{
		RunID:   runID,
		SinceID: ids[1],
	})
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Equal(t, ids[2], events[0].ID)
	assert.Equal(t, ids[3], events[1].ID)
	assert.Equal(t, ids[4], events[2].ID)

	// SinceID = last id -> empty
	events, err = store.ListRunEvents(sqlstore.RunEventFilter{
		RunID:   runID,
		SinceID: ids[4],
	})
	require.NoError(t, err)
	assert.Len(t, events, 0)
}

func TestListRunEventsByType(t *testing.T) {
	store := setupTestStore(t)

	taskID := "CW-20260411-0004"
	runID := createTaskAndRun(t, store, taskID)

	types := []string{"log", "artifact", "log", "signal"}
	for _, typ := range types {
		_, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
			RunID:  sql.NullInt64{Int64: runID, Valid: true},
			TaskID: taskID,
			Type:   typ,
		})
		require.NoError(t, err)
	}

	// Filter for "log" only.
	events, err := store.ListRunEvents(sqlstore.RunEventFilter{
		RunID: runID,
		Types: []string{"log"},
	})
	require.NoError(t, err)
	assert.Len(t, events, 2)
	for _, e := range events {
		assert.Equal(t, "log", e.Type)
	}

	// Filter for "artifact" or "signal".
	events, err = store.ListRunEvents(sqlstore.RunEventFilter{
		RunID: runID,
		Types: []string{"artifact", "signal"},
	})
	require.NoError(t, err)
	assert.Len(t, events, 2)
	gotTypes := []string{events[0].Type, events[1].Type}
	assert.Contains(t, gotTypes, "artifact")
	assert.Contains(t, gotTypes, "signal")
}

func TestListRunEventsLimit(t *testing.T) {
	store := setupTestStore(t)

	taskID := "CW-20260411-0005"
	runID := createTaskAndRun(t, store, taskID)

	for i := 0; i < 10; i++ {
		_, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
			RunID:  sql.NullInt64{Int64: runID, Valid: true},
			TaskID: taskID,
			Type:   "log",
		})
		require.NoError(t, err)
	}

	events, err := store.ListRunEvents(sqlstore.RunEventFilter{
		RunID: runID,
		Limit: 3,
	})
	require.NoError(t, err)
	assert.Len(t, events, 3)
}

func TestListRunEventsByTaskID(t *testing.T) {
	store := setupTestStore(t)

	taskA := "CW-20260411-0006"
	taskB := "CW-20260411-0007"
	runA := createTaskAndRun(t, store, taskA)
	runB := createTaskAndRun(t, store, taskB)

	for i := 0; i < 3; i++ {
		_, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
			RunID:  sql.NullInt64{Int64: runA, Valid: true},
			TaskID: taskA,
			Type:   "log",
		})
		require.NoError(t, err)
	}
	for i := 0; i < 2; i++ {
		_, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
			RunID:  sql.NullInt64{Int64: runB, Valid: true},
			TaskID: taskB,
			Type:   "log",
		})
		require.NoError(t, err)
	}

	events, err := store.ListRunEvents(sqlstore.RunEventFilter{TaskID: taskA})
	require.NoError(t, err)
	assert.Len(t, events, 3)
	for _, e := range events {
		assert.Equal(t, taskA, e.TaskID)
	}

	events, err = store.ListRunEvents(sqlstore.RunEventFilter{TaskID: taskB})
	require.NoError(t, err)
	assert.Len(t, events, 2)
	for _, e := range events {
		assert.Equal(t, taskB, e.TaskID)
	}
}

func TestListRunEventsDefaultLimit(t *testing.T) {
	store := setupTestStore(t)

	taskID := "CW-20260411-0008"
	runID := createTaskAndRun(t, store, taskID)

	for i := 0; i < 150; i++ {
		_, err := store.AppendRunEvent(&sqlstore.RunEventRecord{
			RunID:   sql.NullInt64{Int64: runID, Valid: true},
			TaskID:  taskID,
			Type:    "log",
			Payload: fmt.Sprintf("line %d", i),
		})
		require.NoError(t, err)
	}

	events, err := store.ListRunEvents(sqlstore.RunEventFilter{
		RunID: runID,
		Limit: 0, // default -> 100
	})
	require.NoError(t, err)
	assert.Len(t, events, 100)
}
