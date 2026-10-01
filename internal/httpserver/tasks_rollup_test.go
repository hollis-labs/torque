package httpserver_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedScopedTasks gives two projects a mix of statuses plus one unscoped
// task, all with a description long enough to notice in a list response.
func seedScopedTasks(t *testing.T, store *sqlstore.Store) {
	t.Helper()
	for _, id := range []string{"PRJ-A", "PRJ-B"} {
		require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: id, Name: id}))
	}
	for _, tc := range []struct{ id, status, project string }{
		{"CW-20261001-9101", "todo", "PRJ-A"},
		{"CW-20261001-9102", "done", "PRJ-A"},
		{"CW-20261001-9103", "archived", "PRJ-A"},
		{"CW-20261001-9104", "blocked", "PRJ-B"},
		{"CW-20261001-9105", "todo", ""},
	} {
		task := &sqlstore.TaskRecord{
			ID:           tc.id,
			Title:        "rollup " + tc.id,
			Description:  "a long body the list views never render",
			SystemPrompt: "prompt",
			Priority:     2,
			Status:       tc.status,
			ProjectID:    sql.NullString{String: tc.project, Valid: tc.project != ""},
		}
		require.NoError(t, store.CreateTask(task))
	}
}

func getJSON(t *testing.T, url string, wantStatus int, out any) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, wantStatus, resp.StatusCode)
	require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
}

func TestHTTP_TaskRollup_GroupsByScope(t *testing.T) {
	ts, store := setupRunsTestServer(t)
	seedScopedTasks(t, store)

	type rollup struct {
		GroupBy string `json:"group_by"`
		Total   int    `json:"total"`
		Scopes  []struct {
			ScopeID string         `json:"scope_id"`
			Total   int            `json:"total"`
			Counts  map[string]int `json:"counts"`
		} `json:"scopes"`
	}

	var all rollup
	getJSON(t, ts.URL+"/api/v1/tasks/rollup?group_by=project_id", http.StatusOK, &all)
	assert.Equal(t, "project_id", all.GroupBy)
	assert.Equal(t, 4, all.Total, "every status counts toward the scoped total; the unscoped task does not")
	require.Len(t, all.Scopes, 2)
	assert.Equal(t, "PRJ-A", all.Scopes[0].ScopeID)
	assert.Equal(t, 3, all.Scopes[0].Total)
	assert.Equal(t, map[string]int{"todo": 1, "done": 1, "archived": 1}, all.Scopes[0].Counts)
	assert.Equal(t, "PRJ-B", all.Scopes[1].ScopeID)
	assert.Equal(t, map[string]int{"blocked": 1}, all.Scopes[1].Counts)

	var one rollup
	getJSON(t, ts.URL+"/api/v1/tasks/rollup?group_by=project_id&project_id=PRJ-B", http.StatusOK, &one)
	require.Len(t, one.Scopes, 1)
	assert.Equal(t, "PRJ-B", one.Scopes[0].ScopeID)

	var empty rollup
	getJSON(t, ts.URL+"/api/v1/tasks/rollup?group_by=sprint_id", http.StatusOK, &empty)
	assert.Equal(t, 0, empty.Total)
	assert.NotNil(t, empty.Scopes, "scopes is an empty array, not null")

	for _, tc := range []struct{ query, field string }{
		{"", "group_by"},
		{"group_by=parent_id", "group_by"},
		{"group_by=project_id&limit=10", "limit"},
		{"group_by=project_id&sort_by=priority", "sort_by"},
		{"group_by=project_id&dimensions=status", "dimensions"},
	} {
		var errBody struct {
			Field string `json:"field"`
		}
		getJSON(t, ts.URL+"/api/v1/tasks/rollup?"+tc.query, http.StatusBadRequest, &errBody)
		assert.Equal(t, tc.field, errBody.Field, tc.query)
	}
}

func TestHTTP_TaskList_SummaryFieldsOmitBodies(t *testing.T) {
	ts, store := setupRunsTestServer(t)
	seedScopedTasks(t, store)

	full := decodeHTTPTaskList(t, ts.URL+"/api/v1/tasks?project_id=PRJ-A")
	require.Len(t, full.tasks, 3)
	assert.Contains(t, full.tasks[0], "description", "the default list shape is unchanged")
	assert.Contains(t, full.tasks[0], "system_prompt")

	summary := decodeHTTPTaskList(t, ts.URL+"/api/v1/tasks?project_id=PRJ-A&fields=summary")
	require.Len(t, summary.tasks, 3)
	assert.Equal(t, full.total, summary.total)
	for _, task := range summary.tasks {
		assert.NotContains(t, task, "description")
		assert.NotContains(t, task, "system_prompt")
		assert.Contains(t, task, "title")
		assert.Contains(t, task, "status")
		assert.Contains(t, task, "subtodos")
	}

	var errBody struct {
		Field string `json:"field"`
	}
	getJSON(t, ts.URL+"/api/v1/tasks?fields=full", http.StatusBadRequest, &errBody)
	assert.Equal(t, "fields", errBody.Field)
}
