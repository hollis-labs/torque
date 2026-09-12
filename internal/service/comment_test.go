package service_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/service"
)

func TestCommentAdd_EntityTypeValidation(t *testing.T) {
	svc := setupService(t)

	task, err := svc.Task.Create(service.TaskCreateInput{Title: "T1"})
	require.NoError(t, err)

	t.Run("task is valid", func(t *testing.T) {
		c, err := svc.Comment.Add("task", task.ID, "alice", "hi")
		require.NoError(t, err)
		assert.Equal(t, "task", c.EntityType)
	})

	t.Run("empty entity_type defaults to task", func(t *testing.T) {
		c, err := svc.Comment.Add("", task.ID, "alice", "hi")
		require.NoError(t, err)
		assert.Equal(t, "task", c.EntityType)
	})

	t.Run("project/epic/sprint are valid", func(t *testing.T) {
		for _, et := range []string{"project", "epic", "sprint"} {
			c, err := svc.Comment.Add(et, "some-id", "alice", "hi")
			require.NoError(t, err, "entity_type=%s should be accepted", et)
			assert.Equal(t, et, c.EntityType)
		}
	})

	t.Run("unsupported entity_type is rejected", func(t *testing.T) {
		_, err := svc.Comment.Add("collection", "some-id", "alice", "hi")
		require.Error(t, err)
		assert.IsType(t, &service.ValidationError{}, err)
	})
}

func TestCommentUpdate_AuthorScoped(t *testing.T) {
	svc := setupService(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "T1"})
	require.NoError(t, err)

	c, err := svc.Comment.Add("task", task.ID, "alice", "original")
	require.NoError(t, err)

	t.Run("original author can edit", func(t *testing.T) {
		updated, err := svc.Comment.Update(c.ID, "alice", "edited")
		require.NoError(t, err)
		assert.Equal(t, "edited", updated.Content)
	})

	t.Run("different author is rejected", func(t *testing.T) {
		_, err := svc.Comment.Update(c.ID, "bob", "hijacked")
		require.Error(t, err)
		assert.IsType(t, &service.PermissionError{}, err)
	})

	t.Run("nonexistent comment is not_found", func(t *testing.T) {
		_, err := svc.Comment.Update(999999, "alice", "x")
		require.Error(t, err)
	})
}

func TestCommentDelete_AuthorScoped(t *testing.T) {
	svc := setupService(t)
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "T1"})
	require.NoError(t, err)
	exists := func(id int64) bool {
		t.Helper()
		comments, err := svc.Comment.ListForTask(task.ID)
		require.NoError(t, err)
		for _, c := range comments {
			if c.ID == id {
				return true
			}
		}
		return false
	}

	t.Run("different author cannot delete without force", func(t *testing.T) {
		c, err := svc.Comment.Add("task", task.ID, "alice", "content")
		require.NoError(t, err)
		err = svc.Comment.Delete(c.ID, "bob", false)
		require.Error(t, err)
		assert.IsType(t, &service.PermissionError{}, err)
		assert.True(t, exists(c.ID))
	})

	t.Run("original author can delete", func(t *testing.T) {
		c, err := svc.Comment.Add("task", task.ID, "alice", "content")
		require.NoError(t, err)
		err = svc.Comment.Delete(c.ID, "alice", false)
		require.NoError(t, err)
		assert.False(t, exists(c.ID))
	})

	t.Run("force bypasses only author match", func(t *testing.T) {
		c, err := svc.Comment.Add("task", task.ID, "alice", "content")
		require.NoError(t, err)
		err = svc.Comment.Delete(c.ID, "bob", true)
		require.NoError(t, err)
		assert.False(t, exists(c.ID))
	})

	t.Run("force still returns missing comment errors", func(t *testing.T) {
		err := svc.Comment.Delete(999999, "bob", true)
		require.Error(t, err)
	})
}

func TestCommentBulkAdd(t *testing.T) {
	svc := setupService(t)
	taskA, err := svc.Task.Create(service.TaskCreateInput{Title: "A"})
	require.NoError(t, err)
	taskB, err := svc.Task.Create(service.TaskCreateInput{Title: "B"})
	require.NoError(t, err)

	targets := []service.CommentTarget{
		{EntityType: "task", EntityID: taskA.ID},
		{EntityType: "task", EntityID: taskB.ID},
		{EntityType: "collection", EntityID: "bad"}, // unsupported entity_type
	}

	created, succeededKeys, failed := svc.Comment.BulkAdd(targets, "alice", "broadcast note")

	require.Len(t, created, 2)
	assert.Len(t, succeededKeys, 2)
	require.Len(t, failed, 1)
	assert.IsType(t, &service.ValidationError{}, failed[0].Err)

	commentsA, err := svc.Comment.ListForTask(taskA.ID)
	require.NoError(t, err)
	require.Len(t, commentsA, 1)
	assert.Equal(t, "broadcast note", commentsA[0].Content)

	commentsB, err := svc.Comment.ListForTask(taskB.ID)
	require.NoError(t, err)
	require.Len(t, commentsB, 1)
}
