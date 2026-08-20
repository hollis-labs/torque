package sqlstore_test

import (
	"database/sql"
	"testing"

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
