package sqlstore_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	return sqlitetest.OpenStore(t)
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

func TestListTasksPrioritySetDistinguishesOmittedAndZero(t *testing.T) {
	store := setupTestStore(t)

	zero := sampleTask("CW-20260911-0000")
	zero.Priority = 0
	one := sampleTask("CW-20260911-0001")
	one.Priority = 1
	two := sampleTask("CW-20260911-0002")
	two.Priority = 2

	require.NoError(t, store.CreateTask(zero))
	require.NoError(t, store.CreateTask(one))
	require.NoError(t, store.CreateTask(two))

	all, err := store.ListTasks(sqlstore.TaskFilter{})
	require.NoError(t, err)
	require.Len(t, all, 3, "omitted priority remains unfiltered")

	legacy, err := store.ListTasks(sqlstore.TaskFilter{Priority: 1})
	require.NoError(t, err)
	require.Len(t, legacy, 1, "legacy scalar Priority callers keep working")
	assert.Equal(t, one.ID, legacy[0].ID)

	explicitZero, err := store.ListTasks(sqlstore.TaskFilter{Priorities: []int{0}})
	require.NoError(t, err)
	require.Len(t, explicitZero, 1, "explicit zero in the exact set is a real filter")
	assert.Equal(t, zero.ID, explicitZero[0].ID)

	set, err := store.ListTasks(sqlstore.TaskFilter{Priorities: []int{2, 0, 2}})
	require.NoError(t, err)
	require.Len(t, set, 2, "duplicates are deduped and values OR-match")
	assert.Equal(t, []string{zero.ID, two.ID}, []string{set[0].ID, set[1].ID})
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

// CW-20260503-0011 (S1.1): kind=internal is the substrate primitive for
// automation/system tasks. It must round-trip through the SQLite CHECK
// constraint widened in migration 021, and the ExcludeInternal filter
// flag must hide internal rows by default while leaving an explicit
// Kind="internal" filter intact.
func TestListTasksExcludeInternal(t *testing.T) {
	store := setupTestStore(t)

	agent := sampleTask("CW-20260503-1001")
	agent.Kind = "agent"
	agent.Executor = "cli"
	agent.AgentProfile = "cli-profile"

	internal := sampleTask("CW-20260503-1002")
	internal.Kind = "internal"
	internal.Executor = "cli"
	internal.AgentProfile = "reviewer"

	require.NoError(t, store.CreateTask(agent))
	require.NoError(t, store.CreateTask(internal))

	// Default behaviour (ExcludeInternal=false) preserves prior semantics:
	// callers see every kind. Picker / scheduler internals depend on this.
	all, err := store.ListTasks(sqlstore.TaskFilter{})
	require.NoError(t, err)
	require.Len(t, all, 2)

	// ExcludeInternal=true hides the internal row.
	visible, err := store.ListTasks(sqlstore.TaskFilter{ExcludeInternal: true})
	require.NoError(t, err)
	require.Len(t, visible, 1)
	assert.Equal(t, agent.ID, visible[0].ID)

	// Explicit Kind filter wins over the exclusion — operators can still
	// surface internal rows by asking for them specifically.
	onlyInternal, err := store.ListTasks(sqlstore.TaskFilter{
		Kind:            "internal",
		ExcludeInternal: true,
	})
	require.NoError(t, err)
	require.Len(t, onlyInternal, 1)
	assert.Equal(t, internal.ID, onlyInternal[0].ID)
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

// TestListTasks_RepeatedTagPredicates verifies that duplicate tag slugs in
// the filter are normalized (CW-20260911-0081). Repeated distinct requested
// tags must produce the same results regardless of repetition.
func TestListTasks_RepeatedTagPredicates(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "torque", Name: "torque"}))
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "api", Name: "api"}))
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "mcp", Name: "mcp"}))

	t1 := sampleTask("CW-REP-0001")
	t2 := sampleTask("CW-REP-0002")
	t3 := sampleTask("CW-REP-0003")
	require.NoError(t, store.CreateTask(t1))
	require.NoError(t, store.CreateTask(t2))
	require.NoError(t, store.CreateTask(t3))

	require.NoError(t, store.SetTaskTags(t1.ID, []string{"torque"}))
	require.NoError(t, store.SetTaskTags(t2.ID, []string{"torque", "api"}))
	require.NoError(t, store.SetTaskTags(t3.ID, []string{"mcp"}))

	// Duplicate single tag: ["torque", "torque"] should match same as ["torque"]
	got, err := store.ListTasks(sqlstore.TaskFilter{TagSlugs: []string{"torque", "torque"}})
	require.NoError(t, err)
	ids := []string{}
	for _, r := range got {
		ids = append(ids, r.ID)
	}
	assert.ElementsMatch(t, []string{t1.ID, t2.ID}, ids, "duplicate single tag should match same as single instance")

	// Duplicate tags with genuinely distinct tags: ["torque", "api", "torque"] should match same as ["torque", "api"]
	got, err = store.ListTasks(sqlstore.TaskFilter{TagSlugs: []string{"torque", "api", "torque"}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, t2.ID, got[0].ID, "duplicate mixed with distinct tags should still apply AND-match correctly")

	// Empty/whitespace-only tags mixed with valid tags: ["torque", "", "  ", "api"] should match same as ["torque", "api"]
	got, err = store.ListTasks(sqlstore.TaskFilter{TagSlugs: []string{"torque", "", "  ", "api"}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, t2.ID, got[0].ID, "empty/whitespace tags should be filtered out")

	// Leading/trailing whitespace: [" torque ", "api"] should match same as ["torque", "api"]
	got, err = store.ListTasks(sqlstore.TaskFilter{TagSlugs: []string{" torque ", "api"}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, t2.ID, got[0].ID, "whitespace should be trimmed from slugs")

	// All empty/whitespace tags: ["", "  "] should be normalized to empty filter
	got, err = store.ListTasks(sqlstore.TaskFilter{TagSlugs: []string{"", "  "}})
	require.NoError(t, err)
	// Should return all tasks since the normalized filter is empty
	assert.Len(t, got, 3, "all-empty tag filter should not restrict results")
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

// TestListTasks_FilterByStatuses verifies the ENT-TASK Statuses OR-filter:
// multiple statuses match any task in the set, and it takes precedence over
// the single Status field when both are set.
func TestListTasks_FilterByStatuses(t *testing.T) {
	store := setupTestStore(t)

	t1 := sampleTask("CW-20260601-0001")
	t1.Status = "done"
	t2 := sampleTask("CW-20260601-0002")
	t2.Status = "doing"
	t3 := sampleTask("CW-20260601-0003")
	// t3.Status defaults to "todo"
	require.NoError(t, store.CreateTask(t1))
	require.NoError(t, store.CreateTask(t2))
	require.NoError(t, store.CreateTask(t3))

	got, err := store.ListTasks(sqlstore.TaskFilter{Statuses: []string{"done", "doing"}})
	require.NoError(t, err)
	require.Len(t, got, 2)
	ids := []string{got[0].ID, got[1].ID}
	assert.Contains(t, ids, "CW-20260601-0001")
	assert.Contains(t, ids, "CW-20260601-0002")
	assert.NotContains(t, ids, "CW-20260601-0003")

	// Statuses takes precedence over Status when both are set.
	got, err = store.ListTasks(sqlstore.TaskFilter{Status: "todo", Statuses: []string{"done"}})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "CW-20260601-0001", got[0].ID)
}

// TestListTasks_FilterByAgentAndLaunchProfile verifies the ENT-TASK
// agent_profile/launch_profile exact-match filters.
func TestListTasks_FilterByAgentAndLaunchProfile(t *testing.T) {
	store := setupTestStore(t)

	t1 := sampleTask("CW-20260601-0010")
	t1.AgentProfile = "reviewer"
	t1.LaunchProfile = "claude-code"
	t2 := sampleTask("CW-20260601-0011")
	t2.AgentProfile = "builder"
	t2.LaunchProfile = "codex"
	require.NoError(t, store.CreateTask(t1))
	require.NoError(t, store.CreateTask(t2))

	byAgent, err := store.ListTasks(sqlstore.TaskFilter{AgentProfile: "reviewer"})
	require.NoError(t, err)
	require.Len(t, byAgent, 1)
	assert.Equal(t, "CW-20260601-0010", byAgent[0].ID)

	byLaunch, err := store.ListTasks(sqlstore.TaskFilter{LaunchProfile: "codex"})
	require.NoError(t, err)
	require.Len(t, byLaunch, 1)
	assert.Equal(t, "CW-20260601-0011", byLaunch[0].ID)
}

// TestListTasks_FilterByCreatedUpdatedRange verifies the ENT-TASK
// created_at/updated_at range filters. Timestamps are whole-second
// precision (see updatedAtNow's doc comment), so the test sleeps past a
// full second between creates — same technique
// TestListTasks_SortByUpdatedAtDesc_CursorRoundTrips uses — to get two
// rows in distinct seconds without relying on sub-second precision.
func TestListTasks_FilterByCreatedUpdatedRange(t *testing.T) {
	store := setupTestStore(t)

	t1 := sampleTask("CW-20260601-0020")
	require.NoError(t, store.CreateTask(t1))
	time.Sleep(1100 * time.Millisecond)
	t2 := sampleTask("CW-20260601-0021")
	require.NoError(t, store.CreateTask(t2))

	got1, err := store.GetTask(t1.ID)
	require.NoError(t, err)
	got2, err := store.GetTask(t2.ID)
	require.NoError(t, err)

	// Bounds use each row's own stored (whole-second) timestamp directly,
	// rather than an arbitrary sub-second offset: SQLiteDatetimeLayout has no
	// fractional-second component, so Format() would silently collapse an
	// offset smaller than 1s back onto the same whole-second string and
	// defeat the boundary. The two rows are guaranteed >=1s apart by the
	// sleep above, so >= t2's own timestamp excludes t1, and <= t1's own
	// timestamp excludes t2.
	createdAfter := got2.CreatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout)
	onlyLater, err := store.ListTasks(sqlstore.TaskFilter{CreatedAfter: createdAfter})
	require.NoError(t, err)
	require.Len(t, onlyLater, 1)
	assert.Equal(t, t2.ID, onlyLater[0].ID)

	createdBefore := got1.CreatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout)
	onlyEarlier, err := store.ListTasks(sqlstore.TaskFilter{CreatedBefore: createdBefore})
	require.NoError(t, err)
	require.Len(t, onlyEarlier, 1)
	assert.Equal(t, t1.ID, onlyEarlier[0].ID)

	// updated_at mirrors created_at for freshly-created, untouched rows.
	updatedAfter := got2.UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout)
	onlyLaterUpdated, err := store.ListTasks(sqlstore.TaskFilter{UpdatedAfter: updatedAfter})
	require.NoError(t, err)
	require.Len(t, onlyLaterUpdated, 1)
	assert.Equal(t, t2.ID, onlyLaterUpdated[0].ID)

	updatedBefore := got1.UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout)
	onlyEarlierUpdated, err := store.ListTasks(sqlstore.TaskFilter{UpdatedBefore: updatedBefore})
	require.NoError(t, err)
	require.Len(t, onlyEarlierUpdated, 1)
	assert.Equal(t, t1.ID, onlyEarlierUpdated[0].ID)
}

// TestListTasks_FilterByBudgetRange verifies the ENT-TASK budget/duration
// Gte/Lte filters, including that a NULL column (budget never set) never
// matches either bound.
func TestListTasks_FilterByBudgetRange(t *testing.T) {
	store := setupTestStore(t)

	cheap := sampleTask("CW-20260601-0030")
	cheap.CostBudget = sql.NullFloat64{Float64: 10, Valid: true}
	cheap.TokenBudget = sql.NullInt64{Int64: 1000, Valid: true}
	cheap.MaxDurationMs = sql.NullInt64{Int64: 60000, Valid: true}
	cheap.MaxRetries = 1

	pricey := sampleTask("CW-20260601-0031")
	pricey.CostBudget = sql.NullFloat64{Float64: 500, Valid: true}
	pricey.TokenBudget = sql.NullInt64{Int64: 500000, Valid: true}
	pricey.MaxDurationMs = sql.NullInt64{Int64: 3600000, Valid: true}
	pricey.MaxRetries = 5

	unset := sampleTask("CW-20260601-0032")
	// cheap/pricey's budget fields left at their zero value (Valid: false —
	// NULL in the DB); MaxRetries left at 0, which applyDefaults rewrites to 3.

	require.NoError(t, store.CreateTask(cheap))
	require.NoError(t, store.CreateTask(pricey))
	require.NoError(t, store.CreateTask(unset))

	gte := 100.0
	overBudget, err := store.ListTasks(sqlstore.TaskFilter{CostBudgetGte: &gte})
	require.NoError(t, err)
	require.Len(t, overBudget, 1)
	assert.Equal(t, pricey.ID, overBudget[0].ID)

	lte := 100.0
	underBudget, err := store.ListTasks(sqlstore.TaskFilter{CostBudgetLte: &lte})
	require.NoError(t, err)
	require.Len(t, underBudget, 1)
	assert.Equal(t, cheap.ID, underBudget[0].ID)

	tokenGte := int64(10000)
	highToken, err := store.ListTasks(sqlstore.TaskFilter{TokenBudgetGte: &tokenGte})
	require.NoError(t, err)
	require.Len(t, highToken, 1)
	assert.Equal(t, pricey.ID, highToken[0].ID)

	durLte := int64(120000)
	shortDur, err := store.ListTasks(sqlstore.TaskFilter{MaxDurationMsLte: &durLte})
	require.NoError(t, err)
	require.Len(t, shortDur, 1)
	assert.Equal(t, cheap.ID, shortDur[0].ID)

	retriesGte := 3
	highRetries, err := store.ListTasks(sqlstore.TaskFilter{MaxRetriesGte: &retriesGte})
	require.NoError(t, err)
	// pricey (5) and unset (defaulted to 3) both qualify.
	require.Len(t, highRetries, 2)
	gotIDs := []string{highRetries[0].ID, highRetries[1].ID}
	assert.Contains(t, gotIDs, pricey.ID)
	assert.Contains(t, gotIDs, unset.ID)
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

// TestNextTaskID_AboveLexCeiling guards against the lex-MAX(id) ceiling at
// suffix 9999. Before the CAST-to-INTEGER fix, MAX(id) over the string id
// kept returning "...-9999" because lexicographic comparison sorts "9999"
// greater than "10000". This froze allocation at -10000 and produced
// UNIQUE-constraint failures on every subsequent insert. Both the
// auto-commit and the in-transaction allocators must now step past 9999.
func TestNextTaskID_AboveLexCeiling(t *testing.T) {
	store := setupTestStore(t)

	// Skip if we're inside a UTC-midnight rollover window. Both the test
	// seeds and the allocators under test derive "today" from time.Now(),
	// so if the day flips between the two calls, the prefixes diverge and
	// the assertion fails for reasons unrelated to the lex-MAX fix.
	// 30s gives the rest of the test plenty of headroom on slow CI.
	if untilMidnight := timeUntilUTCMidnight(); untilMidnight < 30*time.Second {
		t.Skipf("skipping near UTC midnight (in %s) to avoid date-rollover flake", untilMidnight)
	}

	today := time.Now().UTC().Format("20060102")
	prefix := "CW-" + today + "-"

	// Seed the highest 4-digit row, a 5-digit row, and an 8-digit row
	// (10000000) that lexicographically sorts below "9999" but is numerically
	// the true max. A lex-MAX implementation would return "9999" and
	// re-allocate "10000"; the index-friendly width-bucketed MAX returns
	// 10000000 and allocates 10000001.
	seed := []string{
		prefix + "9999",
		prefix + "10000",
		prefix + "10000000",
	}
	for _, id := range seed {
		require.NoError(t, store.CreateTask(sampleTask(id)))
	}

	want := fmt.Sprintf("%s%04d", prefix, 10000001)

	id, err := store.NextTaskID()
	require.NoError(t, err)
	assert.Equal(t, want, id, "Store.NextTaskID must use numeric MAX, not lex MAX")

	// Same contract for the in-transaction variant used by the writeq path.
	tx, err := store.BeginWriteTx(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })

	txID, err := tx.NextTaskID()
	require.NoError(t, err)
	assert.Equal(t, want, txID, "WriteTx.NextTaskID must use numeric MAX, not lex MAX")
}

// TestListTasks_DefaultOrderUnchangedWithoutSortBy pins down that leaving
// SortBy empty (every caller that hasn't adopted PRIM-002 — HTTP
// /api/v1/tasks, scheduler internals) preserves the exact original
// `priority ASC, created_at ASC` order.
func TestListTasks_DefaultOrderUnchangedWithoutSortBy(t *testing.T) {
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
	assert.Equal(t, "CW-20260407-0002", tasks[0].ID)
	assert.Equal(t, "CW-20260407-0001", tasks[1].ID)
}

// TestListTasks_SortByPriority_CursorTiebreakOnID is PRIM-001/PRIM-002's
// acceptance criterion at the store layer: with SortBy="priority" and many
// rows sharing the same priority value, cursor pagination (AfterSortValue +
// AfterID) must walk every row exactly once via the id tiebreak.
func TestListTasks_SortByPriority_CursorTiebreakOnID(t *testing.T) {
	store := setupTestStore(t)

	const total = 7
	var ids []string
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("CW-20260407-%04d", i+1)
		task := sampleTask(id)
		task.Priority = 2 // identical for every row — forces the id tiebreak
		require.NoError(t, store.CreateTask(task))
		ids = append(ids, id)
	}

	var seen []string
	filter := sqlstore.TaskFilter{SortBy: "priority", SortDir: "asc", Limit: 3}
	for {
		page, err := store.ListTasks(filter)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		for _, t := range page {
			seen = append(seen, t.ID)
		}
		last := page[len(page)-1]
		filter.AfterSortValue = fmt.Sprintf("%d", last.Priority)
		filter.AfterID = last.ID
		if len(page) < filter.Limit {
			break
		}
	}

	assert.Equal(t, ids, seen, "cursor paging over duplicate priority values must visit every row exactly once, in id order")
}

// TestListTasks_SortByUpdatedAtDesc_CursorRoundTrips exercises a timestamp
// sort column end to end at the store layer, guarding the string-argument
// binding taskCursorArg relies on (binding a driver-reformatted time.Time
// instead would compare against a differently formatted stored value —
// see sqlstore.updatedAtNow's doc comment — and silently misorder).
func TestListTasks_SortByUpdatedAtDesc_CursorRoundTrips(t *testing.T) {
	store := setupTestStore(t)

	t1 := sampleTask("CW-20260407-0001")
	require.NoError(t, store.CreateTask(t1))
	// created_at/updated_at are whole-second precision (see updatedAtNow's
	// doc comment in tasks.go) — sleep past a full second so the two rows
	// land in different seconds and this test isn't relying on the id
	// tiebreak to (accidentally) produce the expected order.
	time.Sleep(1100 * time.Millisecond)
	t2 := sampleTask("CW-20260407-0002")
	require.NoError(t, store.CreateTask(t2))

	// First page: desc order should surface the more-recently-created (thus
	// more-recently-updated) row first.
	page1, err := store.ListTasks(sqlstore.TaskFilter{SortBy: "updated_at", SortDir: "desc", Limit: 1})
	require.NoError(t, err)
	require.Len(t, page1, 1)
	assert.Equal(t, "CW-20260407-0002", page1[0].ID)

	// Cursor into the next page using the encoded sqlstore.SQLiteDatetimeLayout
	// value a real caller (mcpadapter.taskSortValue) would produce.
	after := page1[0]
	page2, err := store.ListTasks(sqlstore.TaskFilter{
		SortBy:         "updated_at",
		SortDir:        "desc",
		Limit:          1,
		AfterSortValue: after.UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout),
		AfterID:        after.ID,
	})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Equal(t, "CW-20260407-0001", page2[0].ID)
}

// TestListTasks_InvalidCursorSortValue verifies a malformed cursor sort
// value (non-numeric for the "priority" column) surfaces as
// sqlstore.ErrInvalidCursor, which mcpadapter.mapServiceError maps to
// arg_invalid rather than an internal error.
func TestListTasks_InvalidCursorSortValue(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateTask(sampleTask("CW-20260407-0001")))

	_, err := store.ListTasks(sqlstore.TaskFilter{
		SortBy:         "priority",
		SortDir:        "asc",
		AfterSortValue: "not-a-number",
		AfterID:        "CW-20260407-0001",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, sqlstore.ErrInvalidCursor)
}

// timeUntilUTCMidnight returns the duration from now until the next UTC
// midnight. Used by tests that seed task IDs based on today's date so they
// can skip rather than flake when "today" might change mid-test.
func timeUntilUTCMidnight() time.Duration {
	now := time.Now().UTC()
	nextMidnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	return nextMidnight.Sub(now)
}
