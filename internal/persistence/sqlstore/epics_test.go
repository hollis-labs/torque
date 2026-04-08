package sqlstore_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
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
	assert.Equal(t, "open", got.Status)
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

	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0001", Name: "Open", Status: "open"})
	store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-20260407-0002", Name: "Closed", Status: "closed"})

	epics, err := store.ListEpics(sqlstore.EpicFilter{Status: "open"})
	require.NoError(t, err)
	assert.Len(t, epics, 1)
	assert.Equal(t, "Open", epics[0].Name)
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

	closed := "closed"
	err := store.UpdateEpic("EP-20260407-0001", sqlstore.EpicUpdate{Status: &closed})
	require.NoError(t, err)

	got, err := store.GetEpic("EP-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "closed", got.Status)
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
