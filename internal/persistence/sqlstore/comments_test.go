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
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   task.ID,
		Author:     "alice",
		Content:    "First comment",
	}
	require.NoError(t, store.AddComment(c1))
	assert.Greater(t, c1.ID, int64(0))

	c2 := &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   task.ID,
		Author:     "bob",
		Content:    "Second comment",
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
	assert.Equal(t, sqlstore.EntityTypeTask, comments[0].EntityType)
	assert.Equal(t, task.ID, comments[0].EntityID)
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

// TestPolymorphicComments_NonTaskEntity exercises the entity_type axis at
// the persistence layer with a non-task entity, validating that the schema
// supports comments on arbitrary entities (collections, epics, etc.) without
// requiring those entities to exist as FKs.
func TestPolymorphicComments_NonTaskEntity(t *testing.T) {
	store := setupTestStore(t)

	// A task comment and a collection comment with the same EntityID — the
	// composite (entity_type, entity_id) key must keep them disjoint.
	task := sampleTask("CW-20260503-0001")
	require.NoError(t, store.CreateTask(task))

	taskC := &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   task.ID,
		Author:     "alice",
		Content:    "task-side comment",
	}
	require.NoError(t, store.AddComment(taskC))

	collectionID := task.ID // deliberately reuse the string to prove the type axis matters
	collC := &sqlstore.CommentRecord{
		EntityType: "collection",
		EntityID:   collectionID,
		Author:     "bob",
		Content:    "collection-side comment",
	}
	require.NoError(t, store.AddComment(collC))

	// ListCommentsForEntity returns only the task-side comment for the task.
	taskComments, err := store.ListCommentsForEntity(sqlstore.EntityTypeTask, task.ID)
	require.NoError(t, err)
	require.Len(t, taskComments, 1)
	assert.Equal(t, "task-side comment", taskComments[0].Content)
	assert.Equal(t, sqlstore.EntityTypeTask, taskComments[0].EntityType)

	// ListCommentsForEntity returns only the collection-side comment for the collection.
	collComments, err := store.ListCommentsForEntity("collection", collectionID)
	require.NoError(t, err)
	require.Len(t, collComments, 1)
	assert.Equal(t, "collection-side comment", collComments[0].Content)
	assert.Equal(t, "collection", collComments[0].EntityType)

	// SearchComments scoped by entity_type isolates the slice as expected.
	got, err := store.SearchComments(sqlstore.CommentFilter{EntityType: "collection"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "collection", got[0].EntityType)
}

func TestSearchComments(t *testing.T) {
	store := setupTestStore(t)

	taskA := sampleTask("CW-20260407-0001")
	taskB := sampleTask("CW-20260407-0002")
	require.NoError(t, store.CreateTask(taskA))
	require.NoError(t, store.CreateTask(taskB))

	// Seed comments across two tasks and two authors.
	comments := []sqlstore.CommentRecord{
		{EntityType: sqlstore.EntityTypeTask, EntityID: taskA.ID, Author: "alice", Content: "Fix the login handler"},
		{EntityType: sqlstore.EntityTypeTask, EntityID: taskA.ID, Author: "bob", Content: "Agreed, login is broken"},
		{EntityType: sqlstore.EntityTypeTask, EntityID: taskB.ID, Author: "alice", Content: "Unrelated work item"},
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

	t.Run("entity_id filter", func(t *testing.T) {
		got, err := store.SearchComments(sqlstore.CommentFilter{
			EntityType: sqlstore.EntityTypeTask,
			EntityID:   taskB.ID,
		})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, taskB.ID, got[0].EntityID)
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
			Search:     "login",
			EntityType: sqlstore.EntityTypeTask,
			EntityID:   taskA.ID,
			Author:     "alice",
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
		extra := sqlstore.CommentRecord{
			EntityType: sqlstore.EntityTypeTask,
			EntityID:   taskA.ID,
			Author:     "carol",
			Content:    "Extra comment",
		}
		require.NoError(t, store.AddComment(&extra))

		got, err := store.SearchComments(sqlstore.CommentFilter{Limit: 2})
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})
}
