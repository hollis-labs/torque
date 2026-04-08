package sqlstore_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
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
