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

func TestSearchComments(t *testing.T) {
	store := setupTestStore(t)

	taskA := sampleTask("CW-20260407-0001")
	taskB := sampleTask("CW-20260407-0002")
	require.NoError(t, store.CreateTask(taskA))
	require.NoError(t, store.CreateTask(taskB))

	// Seed comments across two tasks and two authors.
	comments := []sqlstore.CommentRecord{
		{TaskID: taskA.ID, Author: "alice", Content: "Fix the login handler"},
		{TaskID: taskA.ID, Author: "bob", Content: "Agreed, login is broken"},
		{TaskID: taskB.ID, Author: "alice", Content: "Unrelated work item"},
	}
	for i := range comments {
		require.NoError(t, store.AddComment(&comments[i]))
	}

	t.Run("content match", func(t *testing.T) {
		got, err := store.SearchComments(sqlstore.CommentFilter{Search: "login"})
		require.NoError(t, err)
		require.Len(t, got, 2)
		// Both login-matching comments must be present; ORDER BY created_at DESC
		// but don't assert on position since timestamps may be identical in tests.
		contents := []string{got[0].Content, got[1].Content}
		assert.Contains(t, contents, "Fix the login handler")
		assert.Contains(t, contents, "Agreed, login is broken")
	})

	t.Run("task_id filter", func(t *testing.T) {
		got, err := store.SearchComments(sqlstore.CommentFilter{TaskID: taskB.ID})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, taskB.ID, got[0].TaskID)
	})

	t.Run("author filter", func(t *testing.T) {
		got, err := store.SearchComments(sqlstore.CommentFilter{Author: "alice"})
		require.NoError(t, err)
		require.Len(t, got, 2)
		for _, c := range got {
			assert.Equal(t, "alice", c.Author)
		}
	})

	t.Run("combined filters", func(t *testing.T) {
		got, err := store.SearchComments(sqlstore.CommentFilter{
			Search: "login",
			TaskID: taskA.ID,
			Author: "alice",
		})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "Fix the login handler", got[0].Content)
		assert.Equal(t, "alice", got[0].Author)
	})

	t.Run("no match returns empty slice", func(t *testing.T) {
		got, err := store.SearchComments(sqlstore.CommentFilter{Search: "nonexistent-xyz"})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("Limit truncates", func(t *testing.T) {
		// Insert one more so there are 3 comments matching no filter, limit to 2.
		extra := sqlstore.CommentRecord{TaskID: taskA.ID, Author: "carol", Content: "Extra comment"}
		require.NoError(t, store.AddComment(&extra))

		got, err := store.SearchComments(sqlstore.CommentFilter{Limit: 2})
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})
}
