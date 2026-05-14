package sqlstore_test

import (
	"errors"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateAndListArtifacts(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	a1 := &sqlstore.ArtifactRecord{
		TaskID:  task.ID,
		Type:    "log",
		Content: "first artifact",
	}
	require.NoError(t, store.CreateArtifact(a1))
	assert.Greater(t, a1.ID, int64(0))

	a2 := &sqlstore.ArtifactRecord{
		TaskID:   task.ID,
		Type:     "file",
		Content:  "second artifact",
		FilePath: "/tmp/output.txt",
	}
	require.NoError(t, store.CreateArtifact(a2))
	assert.Greater(t, a2.ID, a1.ID)

	artifacts, err := store.ListArtifacts(task.ID)
	require.NoError(t, err)
	require.Len(t, artifacts, 2)
	// oldest first
	assert.Equal(t, a1.ID, artifacts[0].ID)
	assert.Equal(t, "log", artifacts[0].Type)
	assert.Equal(t, a2.ID, artifacts[1].ID)
	assert.Equal(t, "file", artifacts[1].Type)
	assert.Equal(t, "/tmp/output.txt", artifacts[1].FilePath)
}

func TestListArtifacts_Empty(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	artifacts, err := store.ListArtifacts(task.ID)
	require.NoError(t, err)
	assert.Empty(t, artifacts)
}

func TestGetArtifact(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	a := &sqlstore.ArtifactRecord{
		TaskID:   task.ID,
		Type:     "file",
		Content:  "body",
		FilePath: "/tmp/a.txt",
	}
	require.NoError(t, store.CreateArtifact(a))

	got, err := store.GetArtifact(a.ID)
	require.NoError(t, err)
	assert.Equal(t, a.ID, got.ID)
	assert.Equal(t, "file", got.Type)
	assert.Equal(t, "/tmp/a.txt", got.FilePath)
	assert.Equal(t, task.ID, got.TaskID)
}

func TestGetArtifact_NotFound(t *testing.T) {
	store := setupTestStore(t)

	_, err := store.GetArtifact(999999)
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrArtifactNotFound))
}

func TestDeleteArtifact(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	a := &sqlstore.ArtifactRecord{TaskID: task.ID, Type: "file"}
	require.NoError(t, store.CreateArtifact(a))

	require.NoError(t, store.DeleteArtifact(a.ID))

	// Second delete should report not-found.
	err := store.DeleteArtifact(a.ID)
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrArtifactNotFound))

	// Get confirms the row is gone.
	_, getErr := store.GetArtifact(a.ID)
	assert.True(t, errors.Is(getErr, sqlstore.ErrArtifactNotFound))
}

func TestDeleteArtifact_NotFound(t *testing.T) {
	store := setupTestStore(t)

	err := store.DeleteArtifact(888888)
	require.Error(t, err)
	assert.True(t, errors.Is(err, sqlstore.ErrArtifactNotFound))
}
