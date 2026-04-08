package sqlstore_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupTestStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }

func sampleTask(id string) *sqlstore.TaskRecord {
	return &sqlstore.TaskRecord{
		ID:       id,
		Title:    "Test task " + id,
		Priority: 2,
	}
}

// TestCreateTask verifies a task can be inserted and retrieved.
func TestCreateTask(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	task.Title = "Hello world"
	task.Description = "A test task"

	require.NoError(t, store.CreateTask(task))

	got, err := store.GetTask(task.ID)
	require.NoError(t, err)

	assert.Equal(t, task.ID, got.ID)
	assert.Equal(t, "Hello world", got.Title)
	assert.Equal(t, "A test task", got.Description)
	// defaults applied
	assert.Equal(t, "todo", got.Status)
	assert.Equal(t, "review", got.OnDone)
	assert.Equal(t, "retry", got.OnFail)
	assert.Equal(t, "pause", got.OnReview)
	assert.Equal(t, "none", got.OnDoneMerge)
	assert.Equal(t, "[]", got.Tags)
	assert.Equal(t, 3, got.MaxRetries)
}

// TestListTasks verifies basic list returns all tasks ordered correctly.
func TestListTasks(t *testing.T) {
	store := setupTestStore(t)

	t1 := sampleTask("CW-20260407-0001")
	t1.Priority = 3
	t2 := sampleTask("CW-20260407-0002")
	t2.Priority = 1

	require.NoError(t, store.CreateTask(t1))
	require.NoError(t, store.CreateTask(t2))

	tasks, err := store.ListTasks(sqlstore.TaskFilter{})
	require.NoError(t, err)
	require.Len(t, tasks, 2)

	// lower priority value = higher priority, should come first
	assert.Equal(t, "CW-20260407-0002", tasks[0].ID)
	assert.Equal(t, "CW-20260407-0001", tasks[1].ID)
}

// TestListTasksFilterByStatus verifies status filtering works.
func TestListTasksFilterByStatus(t *testing.T) {
	store := setupTestStore(t)

	t1 := sampleTask("CW-20260407-0001")
	t1.Status = "done"
	t2 := sampleTask("CW-20260407-0002")
	// t2.Status defaults to "todo"

	require.NoError(t, store.CreateTask(t1))
	require.NoError(t, store.CreateTask(t2))

	done, err := store.ListTasks(sqlstore.TaskFilter{Status: "done"})
	require.NoError(t, err)
	require.Len(t, done, 1)
	assert.Equal(t, "CW-20260407-0001", done[0].ID)

	todo, err := store.ListTasks(sqlstore.TaskFilter{Status: "todo"})
	require.NoError(t, err)
	require.Len(t, todo, 1)
	assert.Equal(t, "CW-20260407-0002", todo[0].ID)
}

// TestUpdateTask verifies partial updates apply correctly.
func TestUpdateTask(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	err := store.UpdateTask(task.ID, sqlstore.TaskUpdate{
		Title:    strPtr("Updated title"),
		Priority: intPtr(1),
	})
	require.NoError(t, err)

	got, err := store.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated title", got.Title)
	assert.Equal(t, 1, got.Priority)
	// unchanged
	assert.Equal(t, "todo", got.Status)
}

// TestUpdateTask_NotFound verifies a helpful error for missing tasks.
func TestUpdateTask_NotFound(t *testing.T) {
	store := setupTestStore(t)
	err := store.UpdateTask("nope", sqlstore.TaskUpdate{Title: strPtr("x")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestTransitionTask verifies status transitions.
func TestTransitionTask(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	require.NoError(t, store.TransitionTask(task.ID, "in_progress"))

	got, err := store.GetTask(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "in_progress", got.Status)
}

// TestTransitionTask_NotFound verifies a helpful error for missing tasks.
func TestTransitionTask_NotFound(t *testing.T) {
	store := setupTestStore(t)
	err := store.TransitionTask("nope", "done")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestDeleteTask verifies deletion and not-found handling.
func TestDeleteTask(t *testing.T) {
	store := setupTestStore(t)

	task := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(task))

	require.NoError(t, store.DeleteTask(task.ID))

	_, err := store.GetTask(task.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// second delete should also return not found
	err = store.DeleteTask(task.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// TestSearchTasks verifies LIKE search on title and description.
func TestSearchTasks(t *testing.T) {
	store := setupTestStore(t)

	t1 := sampleTask("CW-20260407-0001")
	t1.Title = "Deploy the rocket"
	t1.Description = "Send it to space"

	t2 := sampleTask("CW-20260407-0002")
	t2.Title = "Fix the login bug"
	t2.Description = "Users cannot log in with rocket email"

	t3 := sampleTask("CW-20260407-0003")
	t3.Title = "Write docs"
	t3.Description = "Documentation for the API"

	require.NoError(t, store.CreateTask(t1))
	require.NoError(t, store.CreateTask(t2))
	require.NoError(t, store.CreateTask(t3))

	results, err := store.SearchTasks("rocket")
	require.NoError(t, err)
	require.Len(t, results, 2)

	ids := []string{results[0].ID, results[1].ID}
	assert.Contains(t, ids, "CW-20260407-0001")
	assert.Contains(t, ids, "CW-20260407-0002")

	results2, err := store.SearchTasks("docs")
	require.NoError(t, err)
	require.Len(t, results2, 1)
	assert.Equal(t, "CW-20260407-0003", results2[0].ID)

	empty, err := store.SearchTasks("zzznomatch")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

// TestNextTaskID verifies sequential ID generation for today.
func TestNextTaskID(t *testing.T) {
	store := setupTestStore(t)

	id1, err := store.NextTaskID()
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(id1, "-0001"), "expected suffix -0001, got %s", id1)

	// Insert a task with that ID so the counter advances.
	task := sampleTask(id1)
	require.NoError(t, store.CreateTask(task))

	id2, err := store.NextTaskID()
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(id2, "-0002"), "expected suffix -0002, got %s", id2)
}
