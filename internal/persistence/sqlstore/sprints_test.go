package sqlstore_test

import (
	"database/sql"
	"fmt"
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

// TestDeleteSprintRollsBackOnTaskCleanupFailure forces the reference-nulling
// UPDATE inside DeleteSprint to fail (via a trigger that RAISE(ABORT)s on any
// UPDATE touching tasks.sprint_id) and verifies the sprint's own DELETE never
// runs: the sprint row still exists and the task's sprint_id is untouched.
func TestDeleteSprintRollsBackOnTaskCleanupFailure(t *testing.T) {
	store := setupTestStore(t)

	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0001", Name: "Sprint 1"}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-20260407-0001",
		Title:    "Task in sprint",
		Executor: "cli",
	}))
	sprintRef := sql.NullString{String: "SP-20260407-0001", Valid: true}
	require.NoError(t, store.UpdateTask("CW-20260407-0001", sqlstore.TaskUpdate{SprintID: &sprintRef}))

	_, err := store.DB().Exec(`
		CREATE TRIGGER fail_sprint_cleanup
		BEFORE UPDATE OF sprint_id ON tasks
		BEGIN
			SELECT RAISE(ABORT, 'forced failure for test');
		END;
	`)
	require.NoError(t, err)

	err = store.DeleteSprint("SP-20260407-0001")
	require.Error(t, err)

	// Sprint row must still exist — the DELETE must not have proceeded.
	got, err := store.GetSprint("SP-20260407-0001")
	require.NoError(t, err)
	assert.Equal(t, "SP-20260407-0001", got.ID)

	// Task's sprint_id must remain untouched — no partial state.
	task, err := store.GetTask("CW-20260407-0001")
	require.NoError(t, err)
	assert.True(t, task.SprintID.Valid)
	assert.Equal(t, "SP-20260407-0001", task.SprintID.String)
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

// TestListSprintsCostBudgetRange covers ENT-SPRINT's budget-range filter:
// cost_budget_min/max only match sprints whose cost_budget falls in range —
// a NULL cost_budget row never matches either bound (SQL NULL comparison).
func TestListSprintsCostBudgetRange(t *testing.T) {
	store := setupTestStore(t)

	cheap, mid, pricey := 5.0, 25.0, 100.0
	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0001", Name: "Cheap", CostBudget: sql.NullFloat64{Float64: cheap, Valid: true}}))
	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0002", Name: "Mid", CostBudget: sql.NullFloat64{Float64: mid, Valid: true}}))
	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0003", Name: "Pricey", CostBudget: sql.NullFloat64{Float64: pricey, Valid: true}}))
	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0004", Name: "NoBudget"}))

	min, max := 10.0, 50.0
	sprints, err := store.ListSprints(sqlstore.SprintFilter{CostBudgetMin: &min, CostBudgetMax: &max})
	require.NoError(t, err)
	require.Len(t, sprints, 1)
	assert.Equal(t, "Mid", sprints[0].Name)

	// Min only: Mid and Pricey (NoBudget still excluded).
	minOnly, err := store.ListSprints(sqlstore.SprintFilter{CostBudgetMin: &min})
	require.NoError(t, err)
	assert.Len(t, minOnly, 2)
}

// TestListSprintsOverBudget covers the audit's "no filter by ... 'over
// budget'" gap: OverBudget restricts results to sprints whose total run
// cost (SprintCostUsed's same join) exceeds their cost_budget.
func TestListSprintsOverBudget(t *testing.T) {
	store := setupTestStore(t)

	budget := 10.0
	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0001", Name: "Over", CostBudget: sql.NullFloat64{Float64: budget, Valid: true}}))
	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0002", Name: "Under", CostBudget: sql.NullFloat64{Float64: budget, Valid: true}}))
	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0003", Name: "NoBudget"}))

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-20260407-0001", Title: "t1", Executor: "cli"}))
	overRef := sql.NullString{String: "SP-20260407-0001", Valid: true}
	require.NoError(t, store.UpdateTask("CW-20260407-0001", sqlstore.TaskUpdate{SprintID: &overRef}))
	_, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-20260407-0001", Executor: "cli", Cost: 15.0})
	require.NoError(t, err)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-20260407-0002", Title: "t2", Executor: "cli"}))
	underRef := sql.NullString{String: "SP-20260407-0002", Valid: true}
	require.NoError(t, store.UpdateTask("CW-20260407-0002", sqlstore.TaskUpdate{SprintID: &underRef}))
	_, err = store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-20260407-0002", Executor: "cli", Cost: 5.0})
	require.NoError(t, err)

	sprints, err := store.ListSprints(sqlstore.SprintFilter{OverBudget: true})
	require.NoError(t, err)
	require.Len(t, sprints, 1)
	assert.Equal(t, "Over", sprints[0].Name)
}

// TestListSprints_SortByName_CursorTiebreakOnID is PRIM-001/PRIM-002's
// acceptance criterion at the store layer, applied to Sprint: with
// SortBy="name" and many rows sharing the same name, cursor pagination
// (AfterSortValue + AfterID) must walk every row exactly once via the id
// tiebreak. Mirrors tasks_test.go's TestListTasks_SortByPriority_CursorTiebreakOnID.
func TestListSprints_SortByName_CursorTiebreakOnID(t *testing.T) {
	store := setupTestStore(t)

	const total = 5
	var ids []string
	for i := 0; i < total; i++ {
		id := fmt.Sprintf("SP-20260407-%04d", i+1)
		require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: id, Name: "Same Name"}))
		ids = append(ids, id)
	}

	var seen []string
	filter := sqlstore.SprintFilter{SortBy: "name", SortDir: "asc", Limit: 2}
	for {
		page, err := store.ListSprints(filter)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		for _, sp := range page {
			seen = append(seen, sp.ID)
		}
		last := page[len(page)-1]
		filter.AfterSortValue = last.Name
		filter.AfterID = last.ID
		if len(page) < filter.Limit {
			break
		}
	}

	assert.Equal(t, ids, seen, "cursor paging over duplicate name values must visit every row exactly once, in id order")
}

// TestListSprints_InvalidCursorSortValue verifies a malformed cursor sort
// value (non-date for the "updated_at" column) surfaces as
// sqlstore.ErrInvalidCursor, which mcpadapter.mapServiceError maps to
// arg_invalid rather than an internal error.
func TestListSprints_InvalidCursorSortValue(t *testing.T) {
	store := setupTestStore(t)
	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-20260407-0001", Name: "Sprint"}))

	_, err := store.ListSprints(sqlstore.SprintFilter{
		SortBy:         "updated_at",
		SortDir:        "asc",
		AfterSortValue: "not-a-date",
		AfterID:        "SP-20260407-0001",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, sqlstore.ErrInvalidCursor)
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
