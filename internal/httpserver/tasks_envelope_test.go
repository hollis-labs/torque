package httpserver_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestTaskListPageEnvelope(t *testing.T) {
	ts, store := setupRunsTestServer(t)
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "page-project", Name: "page project"}))
	require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: "page-red", Name: "red"}))
	for i := 0; i < 205; i++ {
		task := &sqlstore.TaskRecord{ID: fmt.Sprintf("page-%03d", i), Title: "other", Status: []string{"doing", "done"}[i%2], Priority: i % 3}
		if i < 5 {
			task.Title = "needle"
		}
		if i < 7 {
			task.ProjectID = sql.NullString{String: "page-project", Valid: true}
		}
		require.NoError(t, store.CreateTask(task))
		if i < 4 {
			require.NoError(t, store.SetTaskTags(task.ID, []string{"page-red"}))
		}
	}
	type envelope struct {
		Items []map[string]any `json:"items"`
		Meta  map[string]any   `json:"meta"`
	}
	get := func(path string, q url.Values) envelope {
		t.Helper()
		resp, err := http.Get(ts.URL + path + "?" + q.Encode())
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
		var raw map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
		require.Len(t, raw, 2)
		require.Contains(t, raw, "items")
		require.Contains(t, raw, "meta")
		var e envelope
		require.NoError(t, json.Unmarshal(raw["items"], &e.Items))
		require.NoError(t, json.Unmarshal(raw["meta"], &e.Meta))
		return e
	}
	first := get("/api/v1/tasks", url.Values{})
	require.Len(t, first.Items, 50)
	require.Equal(t, float64(50), first.Meta["limit"])
	require.True(t, first.Meta["has_more"].(bool))
	require.NotContains(t, first.Meta, "total")
	require.NotContains(t, first.Meta, "offset")
	require.NotContains(t, first.Meta, "next_offset")
	oversized := get("/api/v1/tasks", url.Values{"limit": {"1000"}, "include_total": {"true"}})
	require.Len(t, oversized.Items, 200)
	require.Equal(t, float64(200), oversized.Meta["limit"])
	require.Equal(t, float64(205), oversized.Meta["total"])
	explicit := get("/api/v1/tasks", url.Values{"limit": {"2"}, "offset": {"0"}})
	require.Equal(t, float64(0), explicit.Meta["offset"])
	require.Equal(t, float64(2), explicit.Meta["next_offset"])
	cursor := explicit.Meta["next_cursor"].(string)
	next := get("/api/v1/tasks", url.Values{"limit": {"2"}, "cursor": {cursor}, "offset": {"0"}})
	require.NotContains(t, next.Meta, "offset")
	require.NotContains(t, next.Meta, "next_offset")
	require.NotEqual(t, explicit.Items[0]["id"], next.Items[0]["id"])
	last := get("/api/v1/tasks", url.Values{"limit": {"5"}, "offset": {"200"}, "include_total": {"true"}})
	require.Len(t, last.Items, 5)
	require.False(t, last.Meta["has_more"].(bool))
	require.Contains(t, last.Meta, "next_offset")
	require.Nil(t, last.Meta["next_offset"])
	require.Nil(t, last.Meta["next_cursor"])
	require.Equal(t, float64(205), last.Meta["total"])
	empty := get("/api/v1/tasks", url.Values{"offset": {"205"}, "include_total": {"false"}})
	require.Empty(t, empty.Items)
	require.NotNil(t, empty.Items)
	require.False(t, empty.Meta["has_more"].(bool))
	require.Nil(t, empty.Meta["next_offset"])
	require.NotContains(t, empty.Meta, "total")
	filtered := get("/api/v1/tasks", url.Values{"status": {"doing"}, "project_id": {"page-project"}, "tags": {"page-red"}, "search": {"needle"}, "limit": {"1"}, "offset": {"1"}, "include_total": {"true"}})
	require.Len(t, filtered.Items, 1)
	require.Equal(t, float64(2), filtered.Meta["total"])
	require.False(t, filtered.Meta["has_more"].(bool))
	sorted := get("/api/v1/tasks", url.Values{"sort_by": {"priority"}, "sort_dir": {"desc"}, "limit": {"2"}})
	require.Equal(t, "page-002", sorted.Items[0]["id"])
	require.Equal(t, "page-005", sorted.Items[1]["id"])
	search := get("/api/v1/tasks/search", url.Values{"q": {"needle"}, "limit": {"2"}, "offset": {"0"}, "include_total": {"true"}})
	require.Len(t, search.Items, 2)
	require.Equal(t, float64(5), search.Meta["total"])
	require.Equal(t, float64(2), search.Meta["next_offset"])
	searchNext := get("/api/v1/tasks/search", url.Values{"q": {"needle"}, "limit": {"2"}, "cursor": {search.Meta["next_cursor"].(string)}})
	require.Len(t, searchNext.Items, 2)
	require.NotContains(t, searchNext.Meta, "total")
	require.NotEqual(t, search.Items[0]["id"], searchNext.Items[0]["id"])
	searchLast := get("/api/v1/tasks/search", url.Values{"q": {"needle"}, "limit": {"2"}, "offset": {"4"}})
	require.Len(t, searchLast.Items, 1)
	require.False(t, searchLast.Meta["has_more"].(bool))
	require.Nil(t, searchLast.Meta["next_offset"])
	searchEmpty := get("/api/v1/tasks/search", url.Values{"q": {"absent text"}, "include_total": {"true"}})
	require.Empty(t, searchEmpty.Items)
	require.NotNil(t, searchEmpty.Items)
	require.Equal(t, float64(0), searchEmpty.Meta["total"])
	require.False(t, searchEmpty.Meta["has_more"].(bool))
	require.Nil(t, searchEmpty.Meta["next_cursor"])
	require.NotContains(t, searchEmpty.Meta, "offset")
	for _, path := range []string{"/api/v1/tasks", "/api/v1/tasks/search"} {
		for _, query := range []url.Values{
			{"unknown": {"x"}}, {"limit": {"-1"}}, {"limit": {"bad"}}, {"include_total": {"bad"}}, {"sort_by": {"bad"}}, {"cursor": {cursor}, "offset": {"1"}}, {"cursor": {cursor}, "sort_dir": {"desc"}}, {"include_total": {"true", "false"}},
		} {
			query.Set("q", "needle")
			if path == "/api/v1/tasks" {
				query.Del("q")
			}
			resp, err := http.Get(ts.URL + path + "?" + query.Encode())
			require.NoError(t, err)
			resp.Body.Close()
			require.Equal(t, 400, resp.StatusCode, "%s %v", path, query)
		}
	}
	resp, err := http.Get(ts.URL + "/api/v1/tasks/search")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, 400, resp.StatusCode)
}
