package sqlstore_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateEpic(t *testing.T) {
	store := setupTestStore(t)

	epic := &sqlstore.EpicRecord{
		ID:          "EP-20260407-0001",
		Name:        "Auth Overhaul",
		Description: "Replace entire auth stack",
	}

	err := store.CreateEpic(epic)
	require.NoError(t, err)

	got, err := store.GetEpic("EP-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "Auth Overhaul", got.Name)
	assert.Equal(t, "active", got.Status)
}

func TestCreateEpicWithPriorityAndProjectID(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project"})

	epic := &sqlstore.EpicRecord{
		ID:        "EP-20260407-0001",
		Name:      "Big Feature",
		Priority:  sql.NullInt64{Int64: 1, Valid: true},
		ProjectID: sql.NullString{String: "PRJ-20260407-0001", Valid: true},
	}

	err := store.CreateEpic(epic)
	require.NoError(t, err)

	got, err := store.GetEpic("EP-20260407-0001")
	require.NoError(t, err)
	assert.True(t, got.Priority.Valid)
	assert.Equal(t, int64(1), got.Priority.Int64)
	assert.True(t, got.ProjectID.Valid)
	assert.Equal(t, "PRJ-20260407-0001", got.ProjectID.String)
}

func TestListEpics(t *testing.T) {
	store := setupTestStore(t)

	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0001", Name: "Epic A"})
	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0002", Name: "Epic B"})

	epics, err := store.ListEpics(sqlstore.EpicFilter{})
	require.NoError(t, err)
	assert.Len(t, epics, 2)
}

func TestListEpicsFilterByStatus(t *testing.T) {
	store := setupTestStore(t)

	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0001", Name: "Active", Status: "active"})
	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0002", Name: "Inactive", Status: "inactive"})

	epics, err := store.ListEpics(sqlstore.EpicFilter{Status: "active"})
	require.NoError(t, err)
	assert.Len(t, epics, 1)
	assert.Equal(t, "Active", epics[0].Name)
}

func TestListEpicsFilterByProjectID(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project A"})
	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0002", Name: "Project B"})

	store.CreateEpic(&sqlstore.EpicRecord{
		ID:        "EP-20260407-0001",
		Name:      "Epic A",
		ProjectID: sql.NullString{String: "PRJ-20260407-0001", Valid: true},
	})
	store.CreateEpic(&sqlstore.EpicRecord{
		ID:        "EP-20260407-0002",
		Name:      "Epic B",
		ProjectID: sql.NullString{String: "PRJ-20260407-0002", Valid: true},
	})

	epics, err := store.ListEpics(sqlstore.EpicFilter{ProjectID: "PRJ-20260407-0001"})
	require.NoError(t, err)
	assert.Len(t, epics, 1)
	assert.Equal(t, "Epic A", epics[0].Name)
}

func TestUpdateEpic(t *testing.T) {
	store := setupTestStore(t)

	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0001", Name: "Old Name"})

	err := store.UpdateEpic("EP-20260407-0001", sqlstore.EpicUpdate{
		Name:        strPtr("New Name"),
		Description: strPtr("Updated description"),
	})
	require.NoError(t, err)

	got, err := store.GetEpic("EP-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "New Name", got.Name)
	assert.Equal(t, "Updated description", got.Description)
}

func TestUpdateEpicStatus(t *testing.T) {
	store := setupTestStore(t)

	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0001", Name: "Epic"})

	closed := "inactive"
	err := store.UpdateEpic("EP-20260407-0001", sqlstore.EpicUpdate{Status: &closed})
	require.NoError(t, err)

	got, err := store.GetEpic("EP-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "inactive", got.Status)
}

func TestUpdateEpicPriorityAndProjectID(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project"})
	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0001", Name: "Epic"})

	prio := int64(2)
	proj := "PRJ-20260407-0001"
	err := store.UpdateEpic("EP-20260407-0001", sqlstore.EpicUpdate{
		Priority:  &prio,
		ProjectID: &proj,
	})
	require.NoError(t, err)

	got, err := store.GetEpic("EP-20260407-0001")
	require.NoError(t, err)
	assert.True(t, got.Priority.Valid)
	assert.Equal(t, int64(2), got.Priority.Int64)
	assert.True(t, got.ProjectID.Valid)
	assert.Equal(t, "PRJ-20260407-0001", got.ProjectID.String)
}

func TestDeleteEpic(t *testing.T) {
	store := setupTestStore(t)

	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0001", Name: "Epic"})

	err := store.DeleteEpic("EP-20260407-0001")
	require.NoError(t, err)

	_, err = store.GetEpic("EP-20260407-0001")
	assert.Error(t, err)
}

func TestDeleteEpicClearsTaskFK(t *testing.T) {
	store := setupTestStore(t)

	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0001", Name: "Epic"})
	store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-20260407-0001",
		Title:    "Task in epic",
		Executor: "cli",
	})
	epicID := "EP-20260407-0001"
	store.UpdateTask("CW-20260407-0001", sqlstore.TaskUpdate{EpicID: &sql.NullString{String: epicID, Valid: true}})

	store.DeleteEpic("EP-20260407-0001")

	task, err := store.GetTask("CW-20260407-0001")
	require.NoError(t, err)
	assert.False(t, task.EpicID.Valid)
}

// TestDeleteEpicRollsBackOnTaskCleanupFailure forces the reference-nulling
// UPDATE inside DeleteEpic to fail (via a trigger that RAISE(ABORT)s on any
// UPDATE touching tasks.epic_id) and verifies the epic's own DELETE never
// runs: the epic row still exists and the task's epic_id is untouched.
func TestDeleteEpicRollsBackOnTaskCleanupFailure(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0001", Name: "Epic"}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-20260407-0001",
		Title:    "Task in epic",
		Executor: "cli",
	}))
	epicRef := sql.NullString{String: "EP-20260407-0001", Valid: true}
	require.NoError(t, store.UpdateTask("CW-20260407-0001", sqlstore.TaskUpdate{EpicID: &epicRef}))

	_, err := store.DB().Exec(`
		CREATE TRIGGER fail_epic_cleanup
		BEFORE UPDATE OF epic_id ON tasks
		BEGIN
			SELECT RAISE(ABORT, 'forced failure for test');
		END;
	`)
	require.NoError(t, err)

	err = store.DeleteEpic("EP-20260407-0001")
	require.Error(t, err)

	// Epic row must still exist — the DELETE must not have proceeded.
	got, err := store.GetEpic("EP-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "EP-20260407-0001", got.ID)

	// Task's epic_id must remain untouched — no partial state.
	task, err := store.GetTask("CW-20260407-0001")
	require.NoError(t, err)
	assert.True(t, task.EpicID.Valid)
	assert.Equal(t, "EP-20260407-0001", task.EpicID.String)
}

func TestNextEpicID(t *testing.T) {
	store := setupTestStore(t)

	id1, err := store.NextEpicID()
	require.NoError(t, err)
	assert.Contains(t, id1, "EP-")

	store.CreateEpic(&sqlstore.EpicRecord{ID: id1, Name: "Epic 1"})

	id2, err := store.NextEpicID()
	require.NoError(t, err)
	assert.NotEqual(t, id1, id2)
}
