package sqlstore_test

import (
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateProject(t *testing.T) {
	store := setupTestStore(t)

	proj := &sqlstore.ProjectRecord{
		ID:       "PRJ-20260407-0001",
		Name:     "Torque",
		RepoPath: "~/Projects-apps/torque",
	}

	err := store.CreateProject(proj)
	require.NoError(t, err)

	got, err := store.GetProject("PRJ-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "Torque", got.Name)
	assert.Equal(t, "~/Projects-apps/torque", got.RepoPath)
	assert.Equal(t, "active", got.Status)
	assert.Equal(t, "", got.Icon)
}

func TestCreateProjectWithStatusAndIcon(t *testing.T) {
	store := setupTestStore(t)

	proj := &sqlstore.ProjectRecord{
		ID:     "PRJ-20260407-0001",
		Name:   "Manifold",
		Status: "inactive",
		Icon:   "rocket",
	}

	err := store.CreateProject(proj)
	require.NoError(t, err)

	got, err := store.GetProject("PRJ-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "inactive", got.Status)
	assert.Equal(t, "rocket", got.Icon)
}

func TestListProjects(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project A"})
	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0002", Name: "Project B"})

	projects, err := store.ListProjects(sqlstore.ProjectFilter{})
	require.NoError(t, err)
	assert.Len(t, projects, 2)
}

func TestListProjectsFilterByStatus(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Active Project", Status: "active"})
	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0002", Name: "Inactive Project", Status: "inactive"})

	active, err := store.ListProjects(sqlstore.ProjectFilter{Status: "active"})
	require.NoError(t, err)
	assert.Len(t, active, 1)
	assert.Equal(t, "Active Project", active[0].Name)

	inactive, err := store.ListProjects(sqlstore.ProjectFilter{Status: "inactive"})
	require.NoError(t, err)
	assert.Len(t, inactive, 1)
	assert.Equal(t, "Inactive Project", inactive[0].Name)
}

func TestUpdateProject(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Old Name"})

	err := store.UpdateProject("PRJ-20260407-0001", sqlstore.ProjectUpdate{
		Name:   strPtr("New Name"),
		Status: strPtr("inactive"),
		Icon:   strPtr("star"),
	})
	require.NoError(t, err)

	got, err := store.GetProject("PRJ-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "New Name", got.Name)
	assert.Equal(t, "inactive", got.Status)
	assert.Equal(t, "star", got.Icon)
}

func TestUpdateProject_NotFound(t *testing.T) {
	store := setupTestStore(t)
	err := store.UpdateProject("nope", sqlstore.ProjectUpdate{Name: strPtr("x")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestDeleteProject(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project"})

	err := store.DeleteProject("PRJ-20260407-0001")
	require.NoError(t, err)

	_, err = store.GetProject("PRJ-20260407-0001")
	assert.Error(t, err)
}

func TestDeleteProjectClearsTaskFK(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project"})
	store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-20260407-0001",
		Title:    "Task in project",
		Executor: "cli",
	})
	projNS := sql.NullString{String: "PRJ-20260407-0001", Valid: true}
	store.UpdateTask("CW-20260407-0001", sqlstore.TaskUpdate{ProjectID: &projNS})

	store.DeleteProject("PRJ-20260407-0001")

	task, err := store.GetTask("CW-20260407-0001")
	require.NoError(t, err)
	assert.False(t, task.ProjectID.Valid)
}

func TestDeleteProjectClearsSprintAndEpicFK(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project"})
	store.CreateSprint(&sqlstore.SprintRecord{
		ID:        "SP-20260407-0001",
		Name:      "Sprint",
		ProjectID: sql.NullString{String: "PRJ-20260407-0001", Valid: true},
	})
	store.CreateEpic(&sqlstore.EpicRecord{
		ID:        "EP-20260407-0001",
		Name:      "Epic",
		ProjectID: sql.NullString{String: "PRJ-20260407-0001", Valid: true},
	})

	store.DeleteProject("PRJ-20260407-0001")

	sprint, err := store.GetSprint("SP-20260407-0001")
	require.NoError(t, err)
	assert.False(t, sprint.ProjectID.Valid)

	epic, err := store.GetEpic("EP-20260407-0001")
	require.NoError(t, err)
	assert.False(t, epic.ProjectID.Valid)
}

// TestDeleteProjectRollsBackOnPartialCleanupFailure forces the sprints
// reference-nulling UPDATE — which runs after the tasks UPDATE has already
// succeeded inside the same transaction — to fail (via a trigger that
// RAISE(ABORT)s on any UPDATE touching sprints.project_id). It verifies the
// whole chain rolls back: the project row still exists, the earlier tasks
// UPDATE is undone, and the sprint's project_id is untouched. This proves
// atomicity across the full cleanup sequence, not just the first statement.
func TestDeleteProjectRollsBackOnPartialCleanupFailure(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project"}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-20260407-0001",
		Title:    "Task in project",
		Executor: "cli",
	}))
	projRef := sql.NullString{String: "PRJ-20260407-0001", Valid: true}
	require.NoError(t, store.UpdateTask("CW-20260407-0001", sqlstore.TaskUpdate{ProjectID: &projRef}))

	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{
		ID:        "SP-20260407-0001",
		Name:      "Sprint",
		ProjectID: projRef,
	}))

	_, err := store.DB().Exec(`
		CREATE TRIGGER fail_project_sprint_cleanup
		BEFORE UPDATE OF project_id ON sprints
		BEGIN
			SELECT RAISE(ABORT, 'forced failure for test');
		END;
	`)
	require.NoError(t, err)

	err = store.DeleteProject("PRJ-20260407-0001")
	require.Error(t, err)

	// Project row must still exist — the DELETE must not have proceeded.
	got, err := store.GetProject("PRJ-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "PRJ-20260407-0001", got.ID)

	// The tasks UPDATE that succeeded earlier in the same transaction must
	// have been rolled back too — no partial state left behind.
	task, err := store.GetTask("CW-20260407-0001")
	require.NoError(t, err)
	assert.True(t, task.ProjectID.Valid)
	assert.Equal(t, "PRJ-20260407-0001", task.ProjectID.String)

	// Sprint's project_id must remain untouched.
	sprint, err := store.GetSprint("SP-20260407-0001")
	require.NoError(t, err)
	assert.True(t, sprint.ProjectID.Valid)
	assert.Equal(t, "PRJ-20260407-0001", sprint.ProjectID.String)
}

// TestListProjects_DefaultOrderUnchangedWithoutSortBy pins down that
// leaving SortBy empty (every caller that hasn't adopted PRIM-002 — HTTP
// /api/v1/projects) preserves the exact original `name ASC` order.
func TestListProjects_DefaultOrderUnchangedWithoutSortBy(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Zeta"}))
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0002", Name: "Alpha"}))

	projects, err := store.ListProjects(sqlstore.ProjectFilter{})
	require.NoError(t, err)
	require.Len(t, projects, 2)
	assert.Equal(t, "Alpha", projects[0].Name)
	assert.Equal(t, "Zeta", projects[1].Name)
}

// TestListProjects_SortByStatus_CursorTiebreakOnID is ENT-PROJECT's
// PRIM-001/PRIM-002 acceptance criterion at the store layer: with
// SortBy="status" and many rows sharing the same status value, cursor
// pagination (AfterSortValue + AfterID) must walk every row exactly once
// via the id tiebreak.
func TestListProjects_SortByStatus_CursorTiebreakOnID(t *testing.T) {
	store := setupTestStore(t)

	const total = 7
	var ids []string
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("PRJ-20260407-%04d", i+1)
		require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: id, Name: id, Status: "active"}))
		ids = append(ids, id)
	}

	var seen []string
	filter := sqlstore.ProjectFilter{SortBy: "status", SortDir: "asc", Limit: 3}
	for {
		page, err := store.ListProjects(filter)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		for _, p := range page {
			seen = append(seen, p.ID)
		}
		last := page[len(page)-1]
		filter.AfterSortValue = last.Status
		filter.AfterID = last.ID
		if len(page) < filter.Limit {
			break
		}
	}

	assert.Equal(t, ids, seen, "cursor paging over duplicate status values must visit every row exactly once, in id order")
}

// TestListProjects_SortByUpdatedAtDesc_CursorRoundTrips exercises a
// timestamp sort column end to end at the store layer, guarding the
// string-argument binding projectCursorArg relies on (see
// TestListTasks_SortByUpdatedAtDesc_CursorRoundTrips for the full
// rationale — binding a driver-reformatted time.Time instead of the
// original string would compare against a differently formatted stored
// value and silently misorder).
func TestListProjects_SortByUpdatedAtDesc_CursorRoundTrips(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "First"}))
	time.Sleep(1100 * time.Millisecond)
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0002", Name: "Second"}))

	page1, err := store.ListProjects(sqlstore.ProjectFilter{SortBy: "updated_at", SortDir: "desc", Limit: 1})
	require.NoError(t, err)
	require.Len(t, page1, 1)
	assert.Equal(t, "PRJ-20260407-0002", page1[0].ID)

	after := page1[0]
	page2, err := store.ListProjects(sqlstore.ProjectFilter{
		SortBy:         "updated_at",
		SortDir:        "desc",
		Limit:          1,
		AfterSortValue: after.UpdatedAt.UTC().Format(sqlstore.SQLiteDatetimeLayout),
		AfterID:        after.ID,
	})
	require.NoError(t, err)
	require.Len(t, page2, 1)
	assert.Equal(t, "PRJ-20260407-0001", page2[0].ID)
}

// TestListProjects_InvalidCursorSortValue verifies a malformed cursor
// sort value (an unparseable timestamp for the "updated_at" column)
// surfaces as sqlstore.ErrInvalidCursor, which mcpadapter.mapServiceError
// maps to arg_invalid rather than an internal error.
func TestListProjects_InvalidCursorSortValue(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project"}))

	_, err := store.ListProjects(sqlstore.ProjectFilter{
		SortBy:         "updated_at",
		SortDir:        "asc",
		AfterSortValue: "not-a-timestamp",
		AfterID:        "PRJ-20260407-0001",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, sqlstore.ErrInvalidCursor)
}

// TestListProjects_ArchivedExcludedFromSortedPage covers the interaction
// between PRIM-004's archive filter and PRIM-002's sort — archived rows
// must stay excluded from a sorted/cursor-paginated page by default, same
// as the unsorted default-order path already covered by
// TestProjectListExcludesArchivedByDefault (service layer).
func TestListProjects_ArchivedExcludedFromSortedPage(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Alpha"}))
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0002", Name: "Beta"}))
	require.NoError(t, store.ArchiveProject("PRJ-20260407-0002"))

	projects, err := store.ListProjects(sqlstore.ProjectFilter{SortBy: "name", SortDir: "asc"})
	require.NoError(t, err)
	require.Len(t, projects, 1)
	assert.Equal(t, "PRJ-20260407-0001", projects[0].ID)

	all, err := store.ListProjects(sqlstore.ProjectFilter{SortBy: "name", SortDir: "asc", IncludeArchived: true})
	require.NoError(t, err)
	assert.Len(t, all, 2)
}

func TestNextProjectID(t *testing.T) {
	store := setupTestStore(t)

	id1, err := store.NextProjectID()
	require.NoError(t, err)
	assert.Contains(t, id1, "PRJ-")

	store.CreateProject(&sqlstore.ProjectRecord{ID: id1, Name: "Project 1"})

	id2, err := store.NextProjectID()
	require.NoError(t, err)
	assert.NotEqual(t, id1, id2)
}
