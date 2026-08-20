package sqlstore_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
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

// TestAddComment_PopulatesUpdatedAt is ENT-COMMENT's migration 031
// acceptance check: a freshly inserted comment's UpdatedAt is populated
// (DEFAULT CURRENT_TIMESTAMP), not the zero time.Time.
func TestAddComment_PopulatesUpdatedAt(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	c := &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   task.ID,
		Author:     "alice",
		Content:    "hello",
	}
	require.NoError(t, store.AddComment(c))
	assert.False(t, c.UpdatedAt.IsZero())
	assert.WithinDuration(t, c.CreatedAt, c.UpdatedAt, time.Second)
}

// TestGetUpdateDeleteComment exercises ENT-COMMENT's new correction-path
// primitives at the store layer.
func TestGetUpdateDeleteComment(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	c := &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   task.ID,
		Author:     "alice",
		Content:    "original",
	}
	require.NoError(t, store.AddComment(c))

	t.Run("GetComment round-trips", func(t *testing.T) {
		got, err := store.GetComment(c.ID)
		require.NoError(t, err)
		assert.Equal(t, "original", got.Content)
		assert.Equal(t, "alice", got.Author)
	})

	t.Run("GetComment not found", func(t *testing.T) {
		_, err := store.GetComment(999999)
		require.Error(t, err)
		assert.ErrorIs(t, err, sqlstore.ErrCommentNotFound)
	})

	t.Run("UpdateComment changes content and bumps updated_at", func(t *testing.T) {
		before, err := store.GetComment(c.ID)
		require.NoError(t, err)

		require.NoError(t, store.UpdateComment(c.ID, "edited content"))

		after, err := store.GetComment(c.ID)
		require.NoError(t, err)
		assert.Equal(t, "edited content", after.Content)
		assert.Equal(t, "alice", after.Author, "author is not changed by UpdateComment")
		assert.Equal(t, before.CreatedAt, after.CreatedAt, "created_at is immutable")
		assert.True(t, !after.UpdatedAt.Before(before.UpdatedAt), "updated_at must not go backwards")
	})

	t.Run("UpdateComment not found", func(t *testing.T) {
		err := store.UpdateComment(999999, "x")
		require.Error(t, err)
		assert.ErrorIs(t, err, sqlstore.ErrCommentNotFound)
	})

	t.Run("DeleteComment removes the row", func(t *testing.T) {
		victim := &sqlstore.CommentRecord{
			EntityType: sqlstore.EntityTypeTask,
			EntityID:   task.ID,
			Author:     "bob",
			Content:    "delete me",
		}
		require.NoError(t, store.AddComment(victim))

		require.NoError(t, store.DeleteComment(victim.ID))

		_, err := store.GetComment(victim.ID)
		require.Error(t, err)
		assert.ErrorIs(t, err, sqlstore.ErrCommentNotFound)
	})

	t.Run("DeleteComment not found", func(t *testing.T) {
		err := store.DeleteComment(999999)
		require.Error(t, err)
		assert.ErrorIs(t, err, sqlstore.ErrCommentNotFound)
	})
}

// TestSearchComments_EntityIDsFilter covers ENT-COMMENT's multi-entity
// EntityID filter (item 6 in the task's scope): OR-matching entity_id
// against a set, paired with a single EntityType — the primitive that
// answers "every comment across every task in sprint X" once the caller
// has resolved that sprint's task ids.
func TestSearchComments_EntityIDsFilter(t *testing.T) {
	store := setupTestStore(t)
	taskA := sampleTask("CW-20260407-0001")
	taskB := sampleTask("CW-20260407-0002")
	taskC := sampleTask("CW-20260407-0003")
	require.NoError(t, store.CreateTask(taskA))
	require.NoError(t, store.CreateTask(taskB))
	require.NoError(t, store.CreateTask(taskC))

	for _, tc := range []struct {
		taskID, content string
	}{
		{taskA.ID, "on A"},
		{taskB.ID, "on B"},
		{taskC.ID, "on C"},
	} {
		require.NoError(t, store.AddComment(&sqlstore.CommentRecord{
			EntityType: sqlstore.EntityTypeTask,
			EntityID:   tc.taskID,
			Author:     "alice",
			Content:    tc.content,
		}))
	}

	got, err := store.SearchComments(sqlstore.CommentFilter{
		EntityType: sqlstore.EntityTypeTask,
		EntityIDs:  []string{taskA.ID, taskB.ID},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	contents := []string{got[0].Content, got[1].Content}
	assert.Contains(t, contents, "on A")
	assert.Contains(t, contents, "on B")
	assert.NotContains(t, contents, "on C")
}

// TestSearchComments_CreatedAtRangeFilter covers the date-range filter
// added on CommentRecord.CreatedAt (previously unfiltered per the task).
func TestSearchComments_CreatedAtRangeFilter(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	c := &sqlstore.CommentRecord{
		EntityType: sqlstore.EntityTypeTask,
		EntityID:   task.ID,
		Author:     "alice",
		Content:    "in range",
	}
	require.NoError(t, store.AddComment(c))

	createdText := c.CreatedAt.UTC().Format(sqlstore.CommentDatetimeLayout)

	t.Run("after excludes future window", func(t *testing.T) {
		future := c.CreatedAt.Add(time.Hour).UTC().Format(sqlstore.CommentDatetimeLayout)
		got, err := store.SearchComments(sqlstore.CommentFilter{CreatedAfter: future})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("after includes exact match (inclusive)", func(t *testing.T) {
		got, err := store.SearchComments(sqlstore.CommentFilter{CreatedAfter: createdText})
		require.NoError(t, err)
		require.Len(t, got, 1)
	})

	t.Run("before excludes past window", func(t *testing.T) {
		past := c.CreatedAt.Add(-time.Hour).UTC().Format(sqlstore.CommentDatetimeLayout)
		got, err := store.SearchComments(sqlstore.CommentFilter{CreatedBefore: past})
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("before includes exact match (inclusive)", func(t *testing.T) {
		got, err := store.SearchComments(sqlstore.CommentFilter{CreatedBefore: createdText})
		require.NoError(t, err)
		require.Len(t, got, 1)
	})
}

// TestSearchComments_CursorPagination is PRIM-001/PRIM-002's acceptance
// criterion applied to Comment: paging via sort_by=created_at + cursor must
// not skip or duplicate rows, and must terminate.
func TestSearchComments_CursorPagination(t *testing.T) {
	store := setupTestStore(t)
	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	const total = 9
	ids := make(map[int64]bool, total)
	for i := 0; i < total; i++ {
		c := &sqlstore.CommentRecord{
			EntityType: sqlstore.EntityTypeTask,
			EntityID:   task.ID,
			Author:     "alice",
			Content:    fmt.Sprintf("comment %d", i),
		}
		require.NoError(t, store.AddComment(c))
		ids[c.ID] = true
	}

	seen := make(map[int64]bool, total)
	var afterSortValue, afterID string
	pages := 0
	for {
		pages++
		require.LessOrEqual(t, pages, total, "too many pages — likely an infinite loop from a broken cursor")

		got, err := store.SearchComments(sqlstore.CommentFilter{
			EntityType:     sqlstore.EntityTypeTask,
			EntityID:       task.ID,
			SortBy:         "created_at",
			SortDir:        "asc",
			AfterSortValue: afterSortValue,
			AfterID:        afterID,
			Limit:          3,
		})
		require.NoError(t, err)
		if len(got) == 0 {
			break
		}
		for _, c := range got {
			assert.False(t, seen[c.ID], "duplicate row across pages: id %d", c.ID)
			seen[c.ID] = true
		}
		last := got[len(got)-1]
		afterSortValue = last.CreatedAt.UTC().Format(sqlstore.CommentDatetimeLayout)
		afterID = fmt.Sprintf("%d", last.ID)
		if len(got) < 3 {
			break
		}
	}

	require.Len(t, seen, total, "every row must be visited exactly once")
	for id := range ids {
		assert.True(t, seen[id], "id %d never visited", id)
	}
}
