package mcpadapter_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

func setupAdjacentQueryParitySurfaces(t *testing.T) (*mcpadapter.Adapter, *httptest.Server, *service.Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	svc := service.New(store)
	require.NoError(t, svc.Feature.Enable("projects"))
	require.NoError(t, svc.Feature.Enable("sprints"))
	require.NoError(t, svc.Feature.Enable("epics"))
	ts := httptest.NewServer(httpserver.New(svc, nil))
	t.Cleanup(ts.Close)
	return mcpadapter.New(svc, nil), ts, svc
}

type adjacentEnvelope struct {
	Items []map[string]any `json:"items"`
	Meta  struct {
		Returned   int     `json:"returned"`
		Limit      int     `json:"limit"`
		HasMore    bool    `json:"has_more"`
		NextCursor *string `json:"next_cursor"`
		SortBy     string  `json:"sort_by"`
		SortDir    string  `json:"sort_dir"`
	} `json:"meta"`
}

func decodeHTTPAdjacentEnvelope(t *testing.T, rawURL string) adjacentEnvelope {
	t.Helper()
	resp, err := http.Get(rawURL)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var env adjacentEnvelope
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&env))
	return env
}

func idsOf(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if id, ok := item["id"].(string); ok {
			out = append(out, id)
			continue
		}
		if id, ok := item["ID"].(string); ok {
			out = append(out, id)
			continue
		}
		if id, ok := item["id"].(float64); ok {
			out = append(out, fmt.Sprintf("%.0f", id))
			continue
		}
		panic(fmt.Sprintf("item has no supported id shape: %#v", item))
	}
	return out
}

func mcpAdjacentPage(t *testing.T, a *mcpadapter.Adapter, tool string, args map[string]any) adjacentEnvelope {
	t.Helper()
	text, isErr := callTool(t, a, tool, args)
	require.False(t, isErr, "mcp %s: %s", tool, text)
	var env adjacentEnvelope
	parseData(t, text, &env)
	return env
}

func collectMCPAdjacentIDs(t *testing.T, a *mcpadapter.Adapter, tool string, args map[string]any) []string {
	t.Helper()
	var out []string
	seen := map[string]bool{}
	for page := 0; page < 20; page++ {
		env := mcpAdjacentPage(t, a, tool, args)
		require.NotZero(t, len(env.Items), "MCP cursor reported progress with an empty page")
		for _, id := range idsOf(env.Items) {
			require.False(t, seen[id], "duplicate MCP id %s", id)
			seen[id] = true
			out = append(out, id)
		}
		if !env.Meta.HasMore {
			require.Nil(t, env.Meta.NextCursor)
			return out
		}
		require.NotNil(t, env.Meta.NextCursor)
		args["cursor"] = *env.Meta.NextCursor
	}
	t.Fatalf("MCP pagination did not terminate for %s", tool)
	return nil
}

func collectHTTPAdjacentIDs(t *testing.T, base string, params url.Values) []string {
	t.Helper()
	var out []string
	seen := map[string]bool{}
	for page := 0; page < 20; page++ {
		env := decodeHTTPAdjacentEnvelope(t, base+"?"+params.Encode())
		require.NotZero(t, len(env.Items), "HTTP cursor reported progress with an empty page")
		for _, id := range idsOf(env.Items) {
			require.False(t, seen[id], "duplicate HTTP id %s", id)
			seen[id] = true
			out = append(out, id)
		}
		if !env.Meta.HasMore {
			require.Nil(t, env.Meta.NextCursor)
			return out
		}
		require.NotNil(t, env.Meta.NextCursor)
		params.Set("cursor", *env.Meta.NextCursor)
	}
	t.Fatalf("HTTP pagination did not terminate for %s", base)
	return nil
}

func TestFullStack_AdjacentHTTPMCPParity_ComposedQueriesAndCursors(t *testing.T) {
	a, ts, _ := setupAdjacentQueryParitySurfaces(t)
	repoPath := t.TempDir()

	projectIDs := map[string]string{}
	for _, name := range []string{"Alpha Query", "Beta Query", "Gamma Query", "Delta Query"} {
		text, isErr := callTool(t, a, "torque_project_create", map[string]any{"name": name, "repo_path": repoPath})
		require.False(t, isErr, "project create: %s", text)
		var p map[string]any
		parseData(t, text, &p)
		projectIDs[name] = p["ID"].(string)
	}
	_, isErr := callTool(t, a, "torque_project_update", map[string]any{"id": projectIDs["Delta Query"], "status": "inactive"})
	require.False(t, isErr)

	var sprintIDs []string
	for i := 0; i < 3; i++ {
		sprintText, isErr := callTool(t, a, "torque_sprint_create", map[string]any{"name": fmt.Sprintf("Parity Sprint %d", i), "project_id": projectIDs["Alpha Query"], "cost_budget": "3.5"})
		require.False(t, isErr, "sprint create: %s", sprintText)
		var sprint map[string]any
		parseData(t, sprintText, &sprint)
		sprintIDs = append(sprintIDs, sprint["ID"].(string))
	}
	_, isErr = callTool(t, a, "torque_sprint_create", map[string]any{"name": "Other Sprint", "project_id": projectIDs["Beta Query"], "cost_budget": "3.5"})
	require.False(t, isErr)

	var epicIDs []string
	for i := 0; i < 3; i++ {
		epicText, isErr := callTool(t, a, "torque_epic_create", map[string]any{"name": fmt.Sprintf("Parity Epic %d", i), "description": "shared-query-marker", "project_id": projectIDs["Alpha Query"]})
		require.False(t, isErr, "epic create: %s", epicText)
		var epic map[string]any
		parseData(t, epicText, &epic)
		epicIDs = append(epicIDs, epic["ID"].(string))
	}
	_, isErr = callTool(t, a, "torque_epic_create", map[string]any{"name": "Other Epic", "description": "nope", "project_id": projectIDs["Alpha Query"]})
	require.False(t, isErr)

	var issueIDs []string
	for _, title := range []string{"first issue marker", "second issue marker"} {
		text, isErr := callTool(t, a, "torque_issue_create", map[string]any{"title": title, "body": "body marker", "project_id": projectIDs["Alpha Query"]})
		require.False(t, isErr, "issue create: %s", text)
		var issue map[string]any
		parseData(t, text, &issue)
		issueIDs = append(issueIDs, issue["ID"].(string))
	}
	_, isErr = callTool(t, a, "torque_issue_create", map[string]any{"title": "wrong project issue marker", "body": "body marker", "project_id": projectIDs["Beta Query"]})
	require.False(t, isErr)

	taskText, isErr := callTool(t, a, "torque_task_create", map[string]any{"title": "comment target", "description": "x"})
	require.False(t, isErr, "task create: %s", taskText)
	var task map[string]any
	parseData(t, taskText, &task)
	taskID := task["ID"].(string)
	var commentIDs []string
	for i := 0; i < 3; i++ {
		text, isErr := callTool(t, a, "torque_comment_add", map[string]any{"entity_type": "task", "entity_id": taskID, "author": "parity", "content": fmt.Sprintf("comment marker %d", i)})
		require.False(t, isErr)
		var comment map[string]any
		parseData(t, text, &comment)
		commentIDs = append(commentIDs, fmt.Sprintf("%.0f", comment["id"].(float64)))
	}
	_, isErr = callTool(t, a, "torque_comment_add", map[string]any{"entity_type": "task", "entity_id": taskID, "author": "other", "content": "comment marker other author"})
	require.False(t, isErr)

	t.Run("projects", func(t *testing.T) {
		mcpIDs := collectMCPAdjacentIDs(t, a, "torque_project_list", map[string]any{"status": "active", "limit": "2", "sort_by": "name", "sort_dir": "asc"})
		httpIDs := collectHTTPAdjacentIDs(t, ts.URL+"/api/v1/projects", url.Values{"status": {"active"}, "limit": {"2"}, "sort_by": {"name"}, "sort_dir": {"asc"}})
		require.Equal(t, mcpIDs, httpIDs)
		require.Equal(t, []string{projectIDs["Alpha Query"], projectIDs["Beta Query"], projectIDs["Gamma Query"]}, httpIDs)
	})

	t.Run("sprints", func(t *testing.T) {
		mcpIDs := collectMCPAdjacentIDs(t, a, "torque_sprint_list", map[string]any{"project_id": projectIDs["Alpha Query"], "cost_budget_min": "1", "limit": "1", "sort_by": "name"})
		httpIDs := collectHTTPAdjacentIDs(t, ts.URL+"/api/v1/sprints", url.Values{"project_id": {projectIDs["Alpha Query"]}, "cost_budget_min": {"1"}, "limit": {"1"}, "sort_by": {"name"}})
		require.Equal(t, mcpIDs, httpIDs)
		require.ElementsMatch(t, sprintIDs, httpIDs)
	})

	t.Run("epics", func(t *testing.T) {
		mcpIDs := collectMCPAdjacentIDs(t, a, "torque_epic_list", map[string]any{"project_id": projectIDs["Alpha Query"], "search": "marker", "limit": "1", "sort_by": "name"})
		httpIDs := collectHTTPAdjacentIDs(t, ts.URL+"/api/v1/epics", url.Values{"project_id": {projectIDs["Alpha Query"]}, "search": {"marker"}, "limit": {"1"}, "sort_by": {"name"}})
		require.Equal(t, mcpIDs, httpIDs)
		require.ElementsMatch(t, epicIDs, httpIDs)
	})

	t.Run("issues", func(t *testing.T) {
		mcpIDs := collectMCPAdjacentIDs(t, a, "torque_issue_list", map[string]any{"project_id": projectIDs["Alpha Query"], "query": "marker", "limit": "1", "sort_by": "created_at"})
		httpIDs := collectHTTPAdjacentIDs(t, ts.URL+"/api/v1/issues", url.Values{"project_id": {projectIDs["Alpha Query"]}, "query": {"marker"}, "limit": {"1"}, "sort_by": {"created_at"}})
		require.Equal(t, mcpIDs, httpIDs)
		require.ElementsMatch(t, issueIDs, httpIDs)
	})

	t.Run("comments", func(t *testing.T) {
		mcpIDs := collectMCPAdjacentIDs(t, a, "torque_comment_list", map[string]any{"entity_type": "task", "entity_id": taskID, "author": "parity", "limit": "2"})
		httpIDs := collectHTTPAdjacentIDs(t, ts.URL+"/api/v1/comments", url.Values{"entity_type": {"task"}, "entity_id": {taskID}, "author": {"parity"}, "limit": {"2"}})
		require.Equal(t, mcpIDs, httpIDs)
		require.Equal(t, commentIDs, httpIDs)

		mcpSearchIDs := collectMCPAdjacentIDs(t, a, "torque_comment_search", map[string]any{"query": "marker", "entity_type": "task", "entity_id": taskID, "limit": "2"})
		httpSearchIDs := collectHTTPAdjacentIDs(t, ts.URL+"/api/v1/comments/search", url.Values{"query": {"marker"}, "entity_type": {"task"}, "entity_id": {taskID}, "limit": {"2"}})
		require.Equal(t, mcpSearchIDs, httpSearchIDs)

		entityIDsArg := fmt.Sprintf(`["%s"]`, taskID)
		mcpSetIDs := collectMCPAdjacentIDs(t, a, "torque_comment_list", map[string]any{"entity_type": "task", "entity_ids": entityIDsArg, "limit": "2"})
		httpSetIDs := collectHTTPAdjacentIDs(t, ts.URL+"/api/v1/comments", url.Values{"entity_type": {"task"}, "entity_ids": {entityIDsArg}, "limit": {"2"}})
		require.Equal(t, mcpSetIDs, httpSetIDs)
	})
}

func TestFullStack_AdjacentHTTPQueryValidationAndLegacyShapes(t *testing.T) {
	a, ts, _ := setupAdjacentQueryParitySurfaces(t)
	repoPath := t.TempDir()
	for i := 0; i < service.DefaultGenericQueryLimit+2; i++ {
		text, isErr := callTool(t, a, "torque_project_create", map[string]any{"name": fmt.Sprintf("archivable-%03d", i), "repo_path": repoPath})
		require.False(t, isErr, "project create %d: %s", i, text)
		var p map[string]any
		parseData(t, text, &p)
		if i%2 == 0 {
			_, isErr = callTool(t, a, "torque_project_archive", map[string]any{"id": p["ID"].(string)})
			require.False(t, isErr)
		}
	}
	resp, err := http.Get(ts.URL + "/api/v1/projects?include_archived=true")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var legacy struct {
		Projects []map[string]any `json:"projects"`
		Items    []map[string]any `json:"items"`
		Meta     map[string]any   `json:"meta"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&legacy))
	require.Empty(t, legacy.Items)
	require.Empty(t, legacy.Meta)
	require.Greater(t, len(legacy.Projects), service.DefaultGenericQueryLimit)

	resp, err = http.Get(ts.URL + "/api/v1/projects?unknown=1")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var fieldErr map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&fieldErr))
	require.Equal(t, "unknown", fieldErr["field"])

	resp, err = http.Get(ts.URL + "/api/v1/sprints?status=active&status=inactive")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	fieldErr = map[string]string{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&fieldErr))
	require.Equal(t, "status", fieldErr["field"])

	resp, err = http.Get(ts.URL + "/api/v1/issues/search?q=one&query=two")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	fieldErr = map[string]string{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&fieldErr))
	require.Equal(t, "query", fieldErr["field"])

	resp, err = http.Get(ts.URL + "/api/v1/issues/search?q=one&limit=-1")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	fieldErr = map[string]string{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&fieldErr))
	require.Equal(t, "limit", fieldErr["field"])

	badCursor := pagination.Encode("created_at", "asc", "not-a-time", "1")
	resp, err = http.Get(ts.URL + "/api/v1/comments?entity_type=task&entity_id=T-1&limit=1&sort_by=created_at&cursor=" + url.QueryEscape(badCursor))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	fieldErr = map[string]string{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&fieldErr))
	require.Equal(t, "cursor", fieldErr["field"])

	badCommentIDCursor := pagination.Encode("created_at", "asc", "2026-01-02 15:04:05", "not-int")
	resp, err = http.Get(ts.URL + "/api/v1/comments?entity_type=task&entity_id=T-1&limit=1&sort_by=created_at&cursor=" + url.QueryEscape(badCommentIDCursor))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	fieldErr = map[string]string{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&fieldErr))
	require.Equal(t, "cursor", fieldErr["field"])

	badIssueCursor := pagination.Encode("created_at", "asc", "not-a-time", "CW-1")
	resp, err = http.Get(ts.URL + "/api/v1/issues?limit=1&sort_by=created_at&cursor=" + url.QueryEscape(badIssueCursor))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	fieldErr = map[string]string{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&fieldErr))
	require.Equal(t, "cursor", fieldErr["field"])

	for _, raw := range []string{"null", `[null]`, `[]`} {
		resp, err = http.Get(ts.URL + "/api/v1/comments?entity_type=task&entity_ids=" + url.QueryEscape(raw))
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, raw)
		fieldErr = map[string]string{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&fieldErr))
		require.NotEmpty(t, fieldErr["field"])
	}

	resp, err = http.Get(ts.URL + "/api/v1/comments?entity_type=task&entity_id=T-1&entity_ids=%5B%5D")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.NotEqual(t, http.StatusBadRequest, resp.StatusCode)

	resp, err = http.Get(ts.URL + "/api/v1/comments/search?query=x&entity_ids=%5B%5D")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.NotEqual(t, http.StatusBadRequest, resp.StatusCode)

	resp, err = http.Get(ts.URL + "/api/v1/tasks/T-1/comments?entity_type=project")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	fieldErr = map[string]string{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&fieldErr))
	require.Equal(t, "entity_type", fieldErr["field"])
}

func TestFullStack_AdjacentMCPStrictListInputs(t *testing.T) {
	a, _, _ := setupAdjacentQueryParitySurfaces(t)
	cases := []struct {
		name  string
		tool  string
		args  map[string]any
		field string
	}{
		{"empty numeric", "torque_project_list", map[string]any{"limit": ""}, "limit"},
		{"unsafe native float", "torque_project_list", map[string]any{"limit": float64(1<<53 + 1)}, "limit"},
		{"native overflow", "torque_project_list", map[string]any{"limit": 1e30}, "limit"},
		{"malformed cost", "torque_sprint_list", map[string]any{"cost_budget_min": "nope"}, "cost_budget_min"},
		{"bad entity ids", "torque_comment_list", map[string]any{"entity_type": "task", "entity_ids": `[1]`}, "entity_ids"},
		{"native bad entity ids", "torque_comment_list", map[string]any{"entity_type": "task", "entity_ids": []any{"ok", nil}}, "entity_ids"},
		{"null entity ids", "torque_comment_list", map[string]any{"entity_type": "task", "entity_ids": `null`}, "entity_ids"},
		{"null member entity ids", "torque_comment_list", map[string]any{"entity_type": "task", "entity_ids": `[null]`}, "entity_ids"},
		{"empty entity ids scope", "torque_comment_list", map[string]any{"entity_type": "task", "entity_ids": `[]`}, "entity_id"},
		{"native nil entity ids", "torque_comment_list", map[string]any{"entity_type": "task", "entity_ids": nil}, "entity_ids"},
		{"search native bad entity ids", "torque_comment_search", map[string]any{"query": "x", "entity_type": "task", "entity_ids": []any{nil}}, "entity_ids"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := callTool(t, a, tc.tool, tc.args)
			require.True(t, isErr, "expected error: %s", text)
			_, _, field := parseError(t, text)
			require.Equal(t, tc.field, field)
		})
	}

	text, isErr := callTool(t, a, "torque_project_list", map[string]any{"limit": " 1 "})
	require.False(t, isErr, "whitespace integer should be accepted: %s", text)
}
