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

// TestListTasksFilterByManual verifies the Manual tri-state filter — unset
// returns all, true returns manual-hold tasks, false returns auto-eligible.
func TestListTasksFilterByManual(t *testing.T) {
	store := setupTestStore(t)

	auto := sampleTask("CW-20260417-0001")
	auto.Manual = false
	manual := sampleTask("CW-20260417-0002")
	manual.Manual = true

	require.NoError(t, store.CreateTask(auto))
	require.NoError(t, store.CreateTask(manual))

	all, err := store.ListTasks(sqlstore.TaskFilter{})
	require.NoError(t, err)
	require.Len(t, all, 2)

	trueFlag := true
	got, err := store.ListTasks(sqlstore.TaskFilter{Manual: &trueFlag})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, manual.ID, got[0].ID)

	falseFlag := false
	got, err = store.ListTasks(sqlstore.TaskFilter{Manual: &falseFlag})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, auto.ID, got[0].ID)
}

// TestListTasksFilterBySearch verifies the Search filter matches on id, title,
// or description and combines (ANDs) with the other filter fields.
func TestListTasksFilterBySearch(t *testing.T) {
	store := setupTestStore(t)

	hitTitle := sampleTask("CW-20260421-0001")
	hitTitle.Title = "Implement scheduler idle fix"

	hitDesc := sampleTask("CW-20260421-0002")
	hitDesc.Title = "Unrelated title"
	hitDesc.Description = "Fix the scheduler picker to avoid starvation"

	miss := sampleTask("CW-20260421-0003")
	miss.Title = "Polish the task detail page"

	hitTitleDone := sampleTask("CW-20260421-0004")
	hitTitleDone.Title = "scheduler cleanup"
	hitTitleDone.Status = "done"

	require.NoError(t, store.CreateTask(hitTitle))
	require.NoError(t, store.CreateTask(hitDesc))
	require.NoError(t, store.CreateTask(miss))
	require.NoError(t, store.CreateTask(hitTitleDone))

	// Search alone: three of the four match "scheduler".
	got, err := store.ListTasks(sqlstore.TaskFilter{Search: "scheduler"})
	require.NoError(t, err)
	require.Len(t, got, 3)
	ids := []string{got[0].ID, got[1].ID, got[2].ID}
	assert.ElementsMatch(t, []string{hitTitle.ID, hitDesc.ID, hitTitleDone.ID}, ids)

	// Search combined with a status filter: only the todo-status match.
	got, err = store.ListTasks(sqlstore.TaskFilter{Search: "scheduler", Status: "todo"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	ids = []string{got[0].ID, got[1].ID}
	assert.ElementsMatch(t, []string{hitTitle.ID, hitDesc.ID}, ids)

	// Empty Search is a no-op — returns all.
	got, err = store.ListTasks(sqlstore.TaskFilter{Search: ""})
	require.NoError(t, err)
	require.Len(t, got, 4)

	// No matches.
	got, err = store.ListTasks(sqlstore.TaskFilter{Search: "xyzzy"})
	require.NoError(t, err)
	require.Len(t, got, 0)

	// Search by ID substring matches that one task.
	got, err = store.ListTasks(sqlstore.TaskFilter{Search: "0421-0003"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, miss.ID, got[0].ID)

	// Search by ID prefix matches all four.
	got, err = store.ListTasks(sqlstore.TaskFilter{Search: "CW-20260421"})
	require.NoError(t, err)
	require.Len(t, got, 4)
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

// TestSearchTasks verifies LIKE search on id, title, and description.
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

	// ID substring matches the right task.
	byID, err := store.SearchTasks("0407-0002")
	require.NoError(t, err)
	require.Len(t, byID, 1)
	assert.Equal(t, "CW-20260407-0002", byID[0].ID)

	// ID prefix matches all three.
	byPrefix, err := store.SearchTasks("CW-20260407")
	require.NoError(t, err)
	require.Len(t, byPrefix, 3)
}

// TestTaskRecord_FacetsRoundTrip verifies facet fields (migration 007) persist.
func TestTaskRecord_FacetsRoundTrip(t *testing.T) {
	store := setupTestStore(t)

	rec := sampleTask("CW-20260416-0001")
	rec.Kind = "agent"
	rec.SourceType = "user"
	rec.SourceRef = sql.NullString{String: "chrispian", Valid: true}
	rec.Trust = "normal"
	rec.CheckpointMode = "none"
	rec.OnCheckpointResponse = "resume"

	require.NoError(t, store.CreateTask(rec))

	got, err := store.GetTask(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "agent", got.Kind)
	assert.Equal(t, "user", got.SourceType)
	assert.Equal(t, "chrispian", got.SourceRef.String)
	assert.True(t, got.SourceRef.Valid)
	assert.Equal(t, "normal", got.Trust)
	assert.Equal(t, "none", got.CheckpointMode)
	assert.Equal(t, "resume", got.OnCheckpointResponse)
}

// TestTaskRecord_FacetDefaultsApplied verifies applyDefaults fills facet zeros.
func TestTaskRecord_FacetDefaultsApplied(t *testing.T) {
	store := setupTestStore(t)

	// Zero-value facets on input → defaults applied.
	rec := sampleTask("CW-20260416-0002")
	require.NoError(t, store.CreateTask(rec))

	got, err := store.GetTask(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "agent", got.Kind)
	assert.Equal(t, "user", got.SourceType)
	assert.False(t, got.SourceRef.Valid)
	assert.Equal(t, "normal", got.Trust)
	assert.Equal(t, "none", got.CheckpointMode)
	assert.Equal(t, "resume", got.OnCheckpointResponse)
}

// TestTaskUpdate_Facets verifies facet fields can be partially updated.
func TestTaskUpdate_Facets(t *testing.T) {
	store := setupTestStore(t)

	rec := sampleTask("CW-20260416-0003")
	require.NoError(t, store.CreateTask(rec))

	newKind := "wait"
	newMode := "blocking"
	newResp := "review"
	ref := sql.NullString{String: "github.com/foo/bar#123", Valid: true}
	err := store.UpdateTask(rec.ID, sqlstore.TaskUpdate{
		Kind:                 &newKind,
		CheckpointMode:       &newMode,
		OnCheckpointResponse: &newResp,
		SourceRef:            &ref,
	})
	require.NoError(t, err)

	got, err := store.GetTask(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "wait", got.Kind)
	assert.Equal(t, "blocking", got.CheckpointMode)
	assert.Equal(t, "review", got.OnCheckpointResponse)
	assert.Equal(t, "github.com/foo/bar#123", got.SourceRef.String)
	// Untouched defaults remain.
	assert.Equal(t, "user", got.SourceType)
	assert.Equal(t, "normal", got.Trust)
}

// TestListTasks_FilterByTagSlugs verifies filtering tasks by one or more
// tag slugs. Multi-slug is AND-match (task must carry all listed tags).
func TestListTasks_FilterByTagSlugs(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "bug", Name: "bug"}))
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "ui", Name: "ui"}))
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "backend", Name: "backend"}))

	t1 := sampleTask("CW-TAG-0001")
	t2 := sampleTask("CW-TAG-0002")
	t3 := sampleTask("CW-TAG-0003")
	require.NoError(t, store.CreateTask(t1))
	require.NoError(t, store.CreateTask(t2))
	require.NoError(t, store.CreateTask(t3))

	require.NoError(t, store.SetTaskTags(t1.ID, []string{"bug", "ui"}))
	require.NoError(t, store.SetTaskTags(t2.ID, []string{"bug", "backend"}))
	require.NoError(t, store.SetTaskTags(t3.ID, []string{"ui"}))

	// single tag: bug → t1, t2
	got, err := store.ListTasks(sqlstore.TaskFilter{TagSlugs: []string{"bug"}})
	require.NoError(t, err)
	ids := []string{}
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	assert.ElementsMatch(t, []string{t1.ID, t2.ID}, ids)

	// AND-match: bug + ui → only t1
	got, err = store.ListTasks(sqlstore.TaskFilter{TagSlugs: []string{"bug", "ui"}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, t1.ID, got[0].ID)

	// no matching tag
	got, err = store.ListTasks(sqlstore.TaskFilter{TagSlugs: []string{"nonexistent"}})
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestListTasks_FilterByKind verifies the Kind filter narrows results.
func TestListTasks_FilterByKind(t *testing.T) {
	store := setupTestStore(t)

	t1 := sampleTask("CW-20260416-0010")
	t1.Kind = "wait"
	t2 := sampleTask("CW-20260416-0011")
	t2.Kind = "agent"
	require.NoError(t, store.CreateTask(t1))
	require.NoError(t, store.CreateTask(t2))

	waits, err := store.ListTasks(sqlstore.TaskFilter{Kind: "wait"})
	require.NoError(t, err)
	require.Len(t, waits, 1)
	assert.Equal(t, "CW-20260416-0010", waits[0].ID)
}

// ParkTaskOnCheckpoint is the conditional UPDATE behind
// CheckpointService.Emit's park. It must only fire for tasks currently
// in status=doing AND checkpoint_mode=blocking — protecting callers
// from clobbering a BlockedReason set by another actor between the
// caller's GetTask and this write.
func TestParkTaskOnCheckpoint_EligibleDoingBlocking(t *testing.T) {
	store := setupTestStore(t)

	rec := sampleTask("CW-PARK-1")
	rec.CheckpointMode = "blocking"
	rec.Status = "doing"
	require.NoError(t, store.CreateTask(rec))

	ok, err := store.ParkTaskOnCheckpoint(rec.ID, "awaiting checkpoint CORR-1")
	require.NoError(t, err)
	assert.True(t, ok, "eligible task should be parked")

	got, err := store.GetTask(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "review", got.Status)
	assert.Equal(t, "awaiting checkpoint CORR-1", got.BlockedReason)
}

func TestParkTaskOnCheckpoint_NotDoing_NoOp(t *testing.T) {
	store := setupTestStore(t)

	rec := sampleTask("CW-PARK-2")
	rec.CheckpointMode = "blocking"
	rec.Status = "review"
	rec.BlockedReason = "pre-existing reason"
	require.NoError(t, store.CreateTask(rec))

	ok, err := store.ParkTaskOnCheckpoint(rec.ID, "awaiting checkpoint CORR-2")
	require.NoError(t, err)
	assert.False(t, ok, "task not in doing should not be parked")

	got, err := store.GetTask(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "review", got.Status, "status unchanged")
	assert.Equal(t, "pre-existing reason", got.BlockedReason, "blocked_reason unchanged")
}

func TestParkTaskOnCheckpoint_NonBlockingMode_NoOp(t *testing.T) {
	store := setupTestStore(t)

	rec := sampleTask("CW-PARK-3")
	rec.CheckpointMode = "non_blocking"
	rec.Status = "doing"
	require.NoError(t, store.CreateTask(rec))

	ok, err := store.ParkTaskOnCheckpoint(rec.ID, "awaiting checkpoint CORR-3")
	require.NoError(t, err)
	assert.False(t, ok, "non_blocking task should not be parked")

	got, err := store.GetTask(rec.ID)
	require.NoError(t, err)
	assert.Equal(t, "doing", got.Status, "status unchanged")
	assert.Equal(t, "", got.BlockedReason)
}

func TestParkTaskOnCheckpoint_MissingTask_NoOp(t *testing.T) {
	store := setupTestStore(t)

	ok, err := store.ParkTaskOnCheckpoint("CW-DOESNT-EXIST", "awaiting checkpoint X")
	require.NoError(t, err)
	assert.False(t, ok, "missing task id should not be a hard error — just no-op")
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
