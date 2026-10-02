package mcpadapter_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/stretchr/testify/require"
)

func TestTaskTitleSortHTTPMCP(t *testing.T) {
	a, ts, db := setupTaskQueryParitySurfaces(t)
	defer ts.Close()
	for _, task := range []struct{ id, title string }{
		{"T-9", "zebra"}, {"T-4", "beta"}, {"T-3", "aLPHa"}, {"T-2", "Alpha"}, {"T-1", "alpha"}, {"T-0", ""}, {"T-8", "BETA"},
	} {
		_, err := db.Exec(`INSERT INTO tasks(id,title,description,status,kind) VALUES(?,?,'needle','todo','agent')`, task.id, task.title)
		require.NoError(t, err)
	}
	type page struct {
		Items []struct {
			ID string `json:"id"`
		}
		Meta pagination.PageMeta
	}
	ids := func(p page) []string {
		out := make([]string, 0, len(p.Items))
		for _, item := range p.Items {
			out = append(out, item.ID)
		}
		return out
	}
	read := func(t *testing.T, path string, q url.Values) page {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/v1/" + path + "?" + q.Encode())
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
		var h page
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&h))
		if q.Has("offset") {
			return h
		} // MCP task lists intentionally expose cursor mode only.
		args := map[string]any{}
		for key := range q {
			args[key] = q.Get(key)
		}
		if path == "tasks/search" {
			delete(args, "q")
			args["search"] = q.Get("q")
		}
		text, isErr := callTool(t, a, "torque_task_list", args)
		require.False(t, isErr, text)
		var m page
		parseData(t, text, &m)
		require.Equal(t, ids(h), ids(m))
		require.Equal(t, h.Meta.NextCursor, m.Meta.NextCursor)
		require.Equal(t, h.Meta.HasMore, m.Meta.HasMore)
		require.Equal(t, h.Meta.Total, m.Meta.Total)
		require.Equal(t, h.Meta.OffsetMeta, m.Meta.OffsetMeta)
		return h
	}
	for _, tc := range []struct {
		dir  string
		want []string
	}{
		{"asc", []string{"T-0", "T-1", "T-2", "T-3", "T-4", "T-8", "T-9"}},
		{"desc", []string{"T-9", "T-4", "T-8", "T-1", "T-2", "T-3", "T-0"}},
	} {
		for _, path := range []string{"tasks", "tasks/search"} {
			t.Run(path+"/"+tc.dir, func(t *testing.T) {
				q := url.Values{"sort_by": {"title"}, "sort_dir": {tc.dir}, "limit": {"1"}, "include_total": {"true"}}
				if path == "tasks/search" {
					q.Set("q", "needle")
				}
				var seen []string
				for n := 0; n < len(tc.want); n++ {
					p := read(t, path, q)
					seen = append(seen, ids(p)...)
					require.NotNil(t, p.Meta.Total)
					require.Equal(t, len(tc.want), *p.Meta.Total)
					if n == len(tc.want)-1 {
						require.False(t, p.Meta.HasMore)
						require.Nil(t, p.Meta.NextCursor)
					} else {
						require.True(t, p.Meta.HasMore)
						require.NotNil(t, p.Meta.NextCursor)
						q.Set("cursor", *p.Meta.NextCursor)
					}
				}
				require.Equal(t, tc.want, seen)
				for _, mismatch := range []struct{ key, value string }{{"sort_by", "priority"}, {"sort_dir", map[string]string{"asc": "desc", "desc": "asc"}[tc.dir]}} {
					bad := url.Values{}
					for key, values := range q {
						bad[key] = append([]string(nil), values...)
					}
					bad.Set(mismatch.key, mismatch.value)
					resp, err := http.Get(ts.URL + "/api/v1/" + path + "?" + bad.Encode())
					require.NoError(t, err)
					resp.Body.Close()
					require.Equal(t, 400, resp.StatusCode)
					args := map[string]any{}
					for key := range bad {
						args[key] = bad.Get(key)
					}
					delete(args, "q")
					if path == "tasks/search" {
						args["search"] = "needle"
					}
					text, isErr := callTool(t, a, "torque_task_list", args)
					require.True(t, isErr, text)
					code, _, field := parseError(t, text)
					require.Equal(t, "arg_invalid", code)
					require.Equal(t, "cursor", field)
				}
				q.Del("cursor")
				q.Set("offset", "0")
				q.Set("limit", "2")
				first := read(t, path, q)
				require.Equal(t, tc.want[:2], ids(first))
				require.NotNil(t, first.Meta.NextOffset)
				require.Equal(t, 2, *first.Meta.NextOffset)
				q.Set("offset", "2")
				second := read(t, path, q)
				require.Equal(t, tc.want[2:4], ids(second))
				require.Equal(t, 4, *second.Meta.NextOffset)
			})
		}
	}
	// Issue lists retain their independent sort contract.
	resp, err := http.Get(ts.URL + "/api/v1/issues?sort_by=title")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, 400, resp.StatusCode)
	text, isErr := callTool(t, a, "torque_issue_list", map[string]any{"sort_by": "title"})
	require.True(t, isErr, text)
	code, _, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "sort_by", field)

}
