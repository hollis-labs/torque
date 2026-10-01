package mcpadapter_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
)

func TestTaskEligibilityHTTPMCPSharedCohort(t *testing.T) {
	a, ts, db := setupTaskQueryParitySurfaces(t)
	defer ts.Close()
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-eligible", Name: "Eligibility", RepoPath: t.TempDir()}))
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "eligible-tag", Name: "Eligible"}))
	for _, id := range []string{"a", "b", "manual", "missing-profile", "wrong-tag", "wrong-search", "wrong-project", "doing", "dep-blocked", "prerequisite"} {
		task := &sqlstore.TaskRecord{ID: id, Title: "needle " + id, Status: "todo", AgentProfile: "profile", ProjectID: sql.NullString{String: "PRJ-eligible", Valid: true}}
		if id == "manual" {
			task.Manual = true
		}
		if id == "missing-profile" {
			task.AgentProfile = ""
		}
		if id == "wrong-search" {
			task.Title = "other"
		}
		if id == "wrong-project" {
			task.ProjectID = sql.NullString{}
		}
		if id == "doing" {
			task.Status = "doing"
		}
		if id == "prerequisite" {
			task.Status = "blocked"
		}
		require.NoError(t, store.CreateTask(task))
		if id != "wrong-tag" {
			require.NoError(t, store.SetTaskTags(id, []string{"eligible-tag"}))
		}
	}
	require.NoError(t, store.SetTaskDependencies("dep-blocked", []string{"prerequisite"}))
	filters := url.Values{"eligible": {"true"}, "status": {"todo"}, "project_id": {"PRJ-eligible"}, "tags": {"eligible-tag"}, "search": {"needle"}}
	args := map[string]interface{}{"eligible": true, "status": "todo", "project_id": "PRJ-eligible", "tags": []string{"eligible-tag"}, "search": "needle", "limit": "1", "include_total": true, "sort_by": "priority", "sort_dir": "asc"}
	var got []string
	for {
		q := url.Values{}
		for key, values := range filters {
			q[key] = append([]string(nil), values...)
		}
		q.Set("limit", "1")
		q.Set("include_total", "true")
		q.Set("sort_by", "priority")
		q.Set("sort_dir", "asc")
		if c, ok := args["cursor"]; ok {
			q.Set("cursor", c.(string))
		}
		hp := httpTaskPage(t, ts.URL+"/api/v1/tasks?"+q.Encode())
		text, isErr := callTool(t, a, "torque_task_list", args)
		require.False(t, isErr, text)
		var mp taskListCursorEnvelope
		parseData(t, text, &mp)
		meta := hp["meta"].(map[string]interface{})
		require.EqualValues(t, 2, meta["total"])
		require.Equal(t, 2, *mp.Meta.Total)
		hi := hp["items"].([]interface{})
		require.Len(t, hi, 1)
		require.Len(t, mp.Items, 1)
		id := hi[0].(map[string]interface{})["id"].(string)
		require.Equal(t, id, mp.Items[0]["id"])
		got = append(got, id)
		require.Equal(t, meta["has_more"], mp.Meta.HasMore)
		if !mp.Meta.HasMore {
			break
		}
		require.Equal(t, meta["next_cursor"], *mp.Meta.NextCursor)
		args["cursor"] = *mp.Meta.NextCursor
	}
	require.Equal(t, []string{"a", "b"}, got)
	facets := decodeHTTPTaskFacets(t, ts.URL+"/api/v1/tasks/facets?"+filters.Encode())
	facetArgs := map[string]interface{}{"eligible": "true", "status": "todo", "project_id": "PRJ-eligible", "tags": []string{"eligible-tag"}, "search": "needle"}
	text, isErr := callTool(t, a, "torque_task_facets", facetArgs)
	require.False(t, isErr, text)
	var mf decodedTaskFacets
	parseData(t, text, &mf)
	require.Equal(t, 2, facets.MatchingCount)
	require.Equal(t, facets, mf)
	filters.Set("group_by", "project_id")
	resp, err := http.Get(ts.URL + "/api/v1/tasks/rollup?" + filters.Encode())
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var rollup service.TaskScopeRollupResult
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&rollup))
	require.Equal(t, 2, rollup.Total)
	require.Len(t, rollup.Scopes, 1)
	require.Equal(t, 2, rollup.Scopes[0].Counts["todo"])
	// Combining an incompatible status with eligible is an intersection.
	require.Empty(t, httpTaskIDs(t, ts.URL+"/api/v1/tasks?eligible=true&status=doing"))
	require.Empty(t, mcpTaskIDs(t, a, map[string]interface{}{"eligible": true, "manual": "manual"}))
	require.Len(t, httpTaskIDs(t, ts.URL+"/api/v1/tasks?eligible=false"), 10)
}

func TestTaskEligibilityMalformedBoolean(t *testing.T) {
	a, ts, _ := setupTaskQueryParitySurfaces(t)
	defer ts.Close()
	for _, path := range []string{"tasks", "tasks/facets", "tasks/rollup"} {
		for _, value := range []string{"", "maybe", "2"} {
			resp, err := http.Get(ts.URL + "/api/v1/" + path + "?eligible=" + url.QueryEscape(value))
			require.NoError(t, err)
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			resp.Body.Close()
		}
	}
	for _, tool := range []string{"torque_task_list", "torque_task_facets"} {
		for _, value := range []interface{}{"", "maybe", "2", nil, 1, []string{"true"}} {
			text, isErr := callTool(t, a, tool, map[string]interface{}{"eligible": value})
			require.True(t, isErr, text)
			code, _, field := parseError(t, text)
			require.Equal(t, "arg_invalid", code)
			require.Equal(t, "eligible", field)
		}
	}
}
