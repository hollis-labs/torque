package sqlstore_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddAndListComments(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	c1 := &sqlstore.CommentRecord{
		TaskID:  task.ID,
		Author:  "alice",
		Content: "First comment",
	}
	require.NoError(t, store.AddComment(c1))
	assert.Greater(t, c1.ID, int64(0))

	c2 := &sqlstore.CommentRecord{
		TaskID:  task.ID,
		Author:  "bob",
		Content: "Second comment",
	}
	require.NoError(t, store.AddComment(c2))
	assert.Greater(t, c2.ID, c1.ID)

	comments, err := store.ListComments(task.ID)
	require.NoError(t, err)
	require.Len(t, comments, 2)
	// oldest first
	assert.Equal(t, c1.ID, comments[0].ID)
	assert.Equal(t, "alice", comments[0].Author)
	assert.Equal(t, "First comment", comments[0].Content)
	assert.Equal(t, c2.ID, comments[1].ID)
	assert.Equal(t, "bob", comments[1].Author)
}

func TestListComments_Empty(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	comments, err := store.ListComments(task.ID)
	require.NoError(t, err)
	assert.Empty(t, comments)
}
