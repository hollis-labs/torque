package mcpadapter_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/stretchr/testify/require"
)

func TestTagSubtodoHTTPMCPPageParity(t *testing.T) {
	a, ts, db := setupTaskQueryParitySurfaces(t)
	defer ts.Close()
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	checklist := make([]sqlstore.Subtodo, 205)
	for i := range checklist {
		checklist[i] = sqlstore.Subtodo{ID: fmt.Sprintf("check-%03d", i), Text: fmt.Sprintf("Check %d", i)}
		require.NoError(t, store.CreateTag(&sqlstore.TagRecord{Slug: fmt.Sprintf("tag-%03d", i), Name: fmt.Sprintf("Tag %03d", i), Color: "blue"}))
	}
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "checklist", Title: "Checklist", Manual: true}))
	require.NoError(t, store.SetSubtodos("checklist", checklist))
	for _, family := range []struct {
		name, path, tool, key string
		base                  map[string]interface{}
	}{
		{"tags", "/tags", "torque_tag_list", "slug", map[string]interface{}{"query": "Tag", "color": "blue"}},
		{"subtodos", "/tasks/checklist/subtodos", "torque_task_subtodo_list", "id", map[string]interface{}{"task_id": "checklist", "verbose": true}},
	} {
		t.Run(family.name, func(t *testing.T) {
			for _, limit := range []string{"", "0", "999"} {
				args := map[string]interface{}{}
				q := url.Values{}
				for k, v := range family.base {
					args[k] = v
					if k != "task_id" && k != "verbose" {
						q.Set(k, fmt.Sprint(v))
					}
				}
				if limit != "" {
					args["limit"] = limit
					q.Set("limit", limit)
				}
				hp := httpTaskPage(t, ts.URL+"/api/v1"+family.path+"?"+q.Encode())
				text, failed := callTool(t, a, family.tool, args)
				require.False(t, failed, text)
				var mp map[string]interface{}
				parseData(t, text, &mp)
				hm, mm := hp["meta"].(map[string]interface{}), mp["meta"].(map[string]interface{})
				want := 50
				if limit == "999" {
					want = 200
				}
				require.Len(t, hp["items"], want)
				require.Len(t, mp["items"], want)
				require.Equal(t, float64(want), hm["limit"])
				require.Equal(t, hm["returned"], mm["returned"])
				require.NotContains(t, hm, "total")
				require.NotContains(t, mm, "total")
				require.Equal(t, true, hm["has_more"])
				require.Equal(t, hm["next_cursor"], mm["next_cursor"])
			}
			args := map[string]interface{}{"limit": "200", "include_total": true}
			q := url.Values{"limit": {"200"}, "include_total": {"true"}}
			for k, v := range family.base {
				args[k] = v
				if k != "task_id" && k != "verbose" {
					q.Set(k, fmt.Sprint(v))
				}
			}
			seen := map[string]bool{}
			for {
				hp := httpTaskPage(t, ts.URL+"/api/v1"+family.path+"?"+q.Encode())
				text, failed := callTool(t, a, family.tool, args)
				require.False(t, failed, text)
				var mp map[string]interface{}
				parseData(t, text, &mp)
				hm, mm := hp["meta"].(map[string]interface{}), mp["meta"].(map[string]interface{})
				require.Equal(t, float64(205), hm["total"])
				require.Equal(t, hm["total"], mm["total"])
				hi, mi := hp["items"].([]interface{}), mp["items"].([]interface{})
				require.Len(t, mi, len(hi))
				require.Equal(t, float64(len(hi)), hm["returned"])
				for i, item := range hi {
					id := item.(map[string]interface{})[family.key].(string)
					require.False(t, seen[id])
					seen[id] = true
					require.Equal(t, id, mi[i].(map[string]interface{})[family.key])
				}
				if !hm["has_more"].(bool) {
					require.Nil(t, hm["next_cursor"])
					require.Nil(t, mm["next_cursor"])
					break
				}
				require.Equal(t, hm["next_cursor"], mm["next_cursor"])
				cursor := hm["next_cursor"].(string)
				args["cursor"] = cursor
				q.Set("cursor", cursor)
			}
			require.Len(t, seen, 205)
			for _, query := range []string{"include_total=perhaps", "include_total=true&include_total=false", "sort_by=nope", "sort_dir=nope", "offset=0", "unknown=yes", "cursor=broken", "limit=-1"} {
				resp, err := http.Get(ts.URL + "/api/v1" + family.path + "?" + query)
				require.NoError(t, err)
				resp.Body.Close()
				require.Equal(t, http.StatusBadRequest, resp.StatusCode, query)
			}
		})
	}
}

func TestTagSubtodoEmptyAndSortContract(t *testing.T) {
	a, ts, db := setupTaskQueryParitySurfaces(t)
	defer ts.Close()
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "empty", Title: "Empty", Manual: true}))
	for _, family := range []struct {
		path, tool string
		args       map[string]interface{}
	}{
		{"/tags", "torque_tag_list", map[string]interface{}{}},
		{"/tasks/empty/subtodos", "torque_task_subtodo_list", map[string]interface{}{"task_id": "empty"}},
	} {
		for _, total := range []bool{false, true} {
			family.args["include_total"] = total
			hp := httpTaskPage(t, ts.URL+"/api/v1"+family.path+fmt.Sprintf("?include_total=%t", total))
			text, failed := callTool(t, a, family.tool, family.args)
			require.False(t, failed, text)
			var mp map[string]interface{}
			parseData(t, text, &mp)
			for _, page := range []map[string]interface{}{hp, mp} {
				require.Empty(t, page["items"])
				meta := page["meta"].(map[string]interface{})
				require.Equal(t, false, meta["has_more"])
				require.Nil(t, meta["next_cursor"])
				if total {
					require.Equal(t, float64(0), meta["total"])
				} else {
					require.NotContains(t, meta, "total")
				}
			}
		}
		for _, args := range []map[string]interface{}{{"unknown": "yes"}, {"include_total": "perhaps"}, {"sort_by": "nope"}, {"cursor": "broken"}} {
			if family.tool == "torque_task_subtodo_list" {
				args["task_id"] = "empty"
			}
			text, failed := callTool(t, a, family.tool, args)
			require.True(t, failed, text)
		}
	}
	resp, err := http.Get(ts.URL + "/api/v1/tasks/missing/subtodos")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	items := []sqlstore.Subtodo{{ID: "z", Text: "First"}, {ID: "a", Text: "Second"}, {ID: "m", Text: "Last"}}
	require.NoError(t, store.SetSubtodos("empty", items))
	q := url.Values{"limit": {"1"}, "sort_by": {"position"}, "sort_dir": {"desc"}, "include_total": {"true"}}
	args := map[string]interface{}{"task_id": "empty", "verbose": true, "limit": "1", "sort_by": "position", "sort_dir": "desc", "include_total": true}
	for _, id := range []string{"m", "a", "z"} {
		hp := httpTaskPage(t, ts.URL+"/api/v1/tasks/empty/subtodos?"+q.Encode())
		text, failed := callTool(t, a, "torque_task_subtodo_list", args)
		require.False(t, failed, text)
		var mp map[string]interface{}
		parseData(t, text, &mp)
		for _, page := range []map[string]interface{}{hp, mp} {
			require.Equal(t, id, page["items"].([]interface{})[0].(map[string]interface{})["id"])
			require.Equal(t, float64(3), page["meta"].(map[string]interface{})["total"])
		}
		cursor := hp["meta"].(map[string]interface{})["next_cursor"]
		if cursor != nil {
			args["cursor"] = cursor
			q.Set("cursor", cursor.(string))
		}
	}
	for _, c := range []string{pagination.Encode("position", "asc", "1", "a"), pagination.Encode("position", "desc", "-1", "a")} {
		resp, err := http.Get(ts.URL + "/api/v1/tasks/empty/subtodos?sort_dir=desc&cursor=" + url.QueryEscape(c))
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	}
	// A valid position cursor beyond the end produces an empty final page.
	page := httpTaskPage(t, ts.URL+"/api/v1/tasks/empty/subtodos?cursor="+pagination.Encode("position", "asc", "999", "past"))
	require.Empty(t, page["items"])
	require.Equal(t, false, page["meta"].(map[string]interface{})["has_more"])
}
