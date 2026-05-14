package sqlstore_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSprint(t *testing.T) {
	store := setupTestStore(t)

	sprint := &sqlstore.SprintRecord{
		ID:           "SP-20260407-0001",
		Name:         "Sprint 1",
		Goal:         "Ship auth module",
		ApprovalMode: "approve_each",
	}

	err := store.CreateSprint(sprint)
	require.NoError(t, err)

	got, err := store.GetSprint("SP-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "Sprint 1", got.Name)
	assert.Equal(t, "active", got.Status)
	assert.Equal(t, "approve_each", got.ApprovalMode)
	assert.Equal(t, "Ship auth module", got.Goal)
}

func TestCreateSprintWithProjectID(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project"})

	sprint := &sqlstore.SprintRecord{
		ID:        "SP-20260407-0001",
		Name:      "Sprint 1",
		ProjectID: sql.NullString{String: "PRJ-20260407-0001", Valid: true},
	}

	err := store.CreateSprint(sprint)
	require.NoError(t, err)

	got, err := store.GetSprint("SP-20260407-0001")
	require.NoError(t, err)
	assert.True(t, got.ProjectID.Valid)
	assert.Equal(t, "PRJ-20260407-0001", got.ProjectID.String)
}

func TestListSprints(t *testing.T) {
	store := setupTestStore(t)

	store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0001", Name: "Sprint 1"})
	store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0002", Name: "Sprint 2"})

	sprints, err := store.ListSprints(sqlstore.SprintFilter{})
	require.NoError(t, err)
	assert.Len(t, sprints, 2)
}

func TestListSprintsFilterByStatus(t *testing.T) {
	store := setupTestStore(t)

	store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0001", Name: "Sprint 1", Status: "inactive"})
	store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0002", Name: "Sprint 2", Status: "active"})

	sprints, err := store.ListSprints(sqlstore.SprintFilter{Status: "active"})
	require.NoError(t, err)
	assert.Len(t, sprints, 1)
	assert.Equal(t, "Sprint 2", sprints[0].Name)
}

func TestListSprintsFilterByProjectID(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project A"})
	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0002", Name: "Project B"})

	store.CreateSprint(&sqlstore.SprintRecord{
		ID:        "SP-20260407-0001",
		Name:      "Sprint A",
		ProjectID: sql.NullString{String: "PRJ-20260407-0001", Valid: true},
	})
	store.CreateSprint(&sqlstore.SprintRecord{
		ID:        "SP-20260407-0002",
		Name:      "Sprint B",
		ProjectID: sql.NullString{String: "PRJ-20260407-0002", Valid: true},
	})

	sprints, err := store.ListSprints(sqlstore.SprintFilter{ProjectID: "PRJ-20260407-0001"})
	require.NoError(t, err)
	assert.Len(t, sprints, 1)
	assert.Equal(t, "Sprint A", sprints[0].Name)
}

func TestUpdateSprint(t *testing.T) {
	store := setupTestStore(t)

	store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0001", Name: "Sprint 1"})

	err := store.UpdateSprint("SP-20260407-0001", sqlstore.SprintUpdate{
		Name: strPtr("Sprint 1 — Revised"),
		Goal: strPtr("Updated goal"),
	})
	require.NoError(t, err)

	got, err := store.GetSprint("SP-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "Sprint 1 — Revised", got.Name)
	assert.Equal(t, "Updated goal", got.Goal)
}

func TestUpdateSprintProjectID(t *testing.T) {
	store := setupTestStore(t)

	store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-20260407-0001", Name: "Project"})
	store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0001", Name: "Sprint 1"})

	err := store.UpdateSprint("SP-20260407-0001", sqlstore.SprintUpdate{
		ProjectID: strPtr("PRJ-20260407-0001"),
	})
	require.NoError(t, err)

	got, err := store.GetSprint("SP-20260407-0001")
	require.NoError(t, err)
	assert.True(t, got.ProjectID.Valid)
	assert.Equal(t, "PRJ-20260407-0001", got.ProjectID.String)
}

func TestTransitionSprint(t *testing.T) {
	store := setupTestStore(t)

	store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0001", Name: "Sprint 1", Status: "inactive"})

	err := store.TransitionSprint("SP-20260407-0001", "active")
	require.NoError(t, err)

	got, err := store.GetSprint("SP-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "active", got.Status)
}

func TestDeleteSprint(t *testing.T) {
	store := setupTestStore(t)

	store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0001", Name: "Sprint 1"})

	err := store.DeleteSprint("SP-20260407-0001")
	require.NoError(t, err)

	_, err = store.GetSprint("SP-20260407-0001")
	assert.Error(t, err)
}

func TestSprintCostAccumulator(t *testing.T) {
	store := setupTestStore(t)

	budget := 25.0
	store.CreateSprint(&sqlstore.SprintRecord{
		ID:   "SP-20260407-0001",
		Name: "Budget sprint",
	})
	store.UpdateSprint("SP-20260407-0001", sqlstore.SprintUpdate{
		CostBudget: &budget,
	})

	got, err := store.GetSprint("SP-20260407-0001")
	require.NoError(t, err)
	assert.True(t, got.CostBudget.Valid)
	assert.Equal(t, 25.0, got.CostBudget.Float64)
}

func TestNextSprintID(t *testing.T) {
	store := setupTestStore(t)

	id1, err := store.NextSprintID()
	require.NoError(t, err)
	assert.Contains(t, id1, "SP-")

	store.CreateSprint(&sqlstore.SprintRecord{ID: id1, Name: "Sprint 1"})

	id2, err := store.NextSprintID()
	require.NoError(t, err)
	assert.NotEqual(t, id1, id2)
}
