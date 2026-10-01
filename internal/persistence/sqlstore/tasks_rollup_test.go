package sqlstore_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskScopeRollup_CountsPerScopeAndStatus(t *testing.T) {
	store := setupTestStore(t)
	for _, id := range []string{"PRJ-A", "PRJ-B", ""} {
		require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: id, Name: "project " + id}))
	}
	require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-1", Name: "epic"}))

	create := func(id, status, kind string, project, epic sql.NullString) {
		t.Helper()
		task := sampleTask(id)
		task.Status = status
		task.Kind = kind
		task.ProjectID = project
		task.EpicID = epic
		require.NoError(t, store.CreateTask(task))
	}
	prj := func(id string) sql.NullString { return sql.NullString{String: id, Valid: true} }
	none := sql.NullString{}
	create("CW-20261001-9001", "todo", "agent", prj("PRJ-A"), prj("EP-1"))
	create("CW-20261001-9002", "todo", "agent", prj("PRJ-A"), none)
	create("CW-20261001-9003", "done", "agent", prj("PRJ-A"), prj("EP-1"))
	create("CW-20261001-9004", "archived", "agent", prj("PRJ-A"), none)
	create("CW-20261001-9005", "doing", "agent", prj("PRJ-B"), none)
	// Empty and NULL scopes belong to no project; internal tasks are hidden
	// by the list's default filter and must be hidden here too.
	create("CW-20261001-9006", "todo", "agent", prj(""), none)
	create("CW-20261001-9007", "todo", "agent", none, none)
	create("CW-20261001-9008", "todo", "internal", prj("PRJ-B"), none)

	cells, err := store.TaskScopeRollup(sqlstore.TaskFilter{ExcludeInternal: true}, "project_id")
	require.NoError(t, err)
	assert.Equal(t, []sqlstore.TaskScopeStatusCount{
		{ScopeID: "PRJ-A", Status: "archived", Count: 1},
		{ScopeID: "PRJ-A", Status: "done", Count: 1},
		{ScopeID: "PRJ-A", Status: "todo", Count: 2},
		{ScopeID: "PRJ-B", Status: "doing", Count: 1},
	}, cells)

	withInternal, err := store.TaskScopeRollup(sqlstore.TaskFilter{}, "project_id")
	require.NoError(t, err)
	assert.Contains(t, withInternal, sqlstore.TaskScopeStatusCount{ScopeID: "PRJ-B", Status: "todo", Count: 1})

	byEpic, err := store.TaskScopeRollup(sqlstore.TaskFilter{ExcludeInternal: true}, "epic_id")
	require.NoError(t, err)
	assert.Equal(t, []sqlstore.TaskScopeStatusCount{
		{ScopeID: "EP-1", Status: "done", Count: 1},
		{ScopeID: "EP-1", Status: "todo", Count: 1},
	}, byEpic)

	// The list filter narrows the cohort, and paging fields are ignored.
	filtered, err := store.TaskScopeRollup(sqlstore.TaskFilter{ProjectID: "PRJ-A", Statuses: []string{"todo", "done"}, Limit: 1, Offset: 3}, "project_id")
	require.NoError(t, err)
	assert.Equal(t, []sqlstore.TaskScopeStatusCount{
		{ScopeID: "PRJ-A", Status: "done", Count: 1},
		{ScopeID: "PRJ-A", Status: "todo", Count: 2},
	}, filtered)

	_, err = store.TaskScopeRollup(sqlstore.TaskFilter{}, "parent_id")
	assert.Error(t, err)
}
