package sqlstore_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateProject(t *testing.T) {
	store := setupTestStore(t)

	proj := &sqlstore.ProjectRecord{
		ID:       "PRJ-20260407-0001",
		Name:     "Clockwork Manifold",
		RepoPath: "~/Projects-apps/clockwork-manifold",
	}

	err := store.CreateProject(proj)
	require.NoError(t, err)

	got, err := store.GetProject("PRJ-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "Clockwork Manifold", got.Name)
	assert.Equal(t, "~/Projects-apps/clockwork-manifold", got.RepoPath)
}

func TestListProjects(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project A"})
	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0002", Name: "Project B"})

	projects, err := store.ListProjects()
	require.NoError(t, err)
	assert.Len(t, projects, 2)
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
