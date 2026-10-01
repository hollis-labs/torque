package mcpadapter_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/stretchr/testify/require"
)

func TestRunListHTTPMCPParity(t *testing.T) {
	a, ts, db := setupTaskQueryParitySurfaces(t)
	defer ts.Close()
	_, err := db.Exec(`INSERT INTO tasks (id,title,status) VALUES ('runs-parity','runs','doing')`)
	require.NoError(t, err)
	// Same instant, three legacy/canonical timestamp spellings, plus two
	// nanosecond-separated instants. Tie-breaking must use numeric IDs.
	for i, stamp := range []string{"2026-10-01 01:02:03", "2026-10-01 01:02:03.000000000 +0000 UTC", "2026-10-01T01:02:03Z", "2026-10-01 01:02:03.000000001 +0000 UTC", "2026-10-01 01:02:03.000000002 +0000 UTC"} {
		_, err := db.Exec(`INSERT INTO runs (id,task_id,executor,status,started_at,ended_at,cost) VALUES (?, 'runs-parity','mock',?,?,?,?)`, i+9, []string{"running", "done", "done", "failed", "done"}[i], stamp, []any{nil, "2026-10-01 01:02:03.010000000 +0000 UTC", "2026-10-01 01:02:03.020000000 +0000 UTC", "2026-10-01 01:02:03.010000001 +0000 UTC", "2026-10-01 01:02:03.020000002 +0000 UTC"}[i], []float64{0, 2.5, 1, 2.5, 1}[i])
		require.NoError(t, err)
	}
	type envelope struct {
		Items []sqlstore.RunRecord `json:"items"`
		Meta  pagination.PageMeta  `json:"meta"`
	}
	get := func(params map[string]any) envelope {
		t.Helper()
		args := map[string]any{"verbose": "true"}
		q := url.Values{}
		for k, v := range params {
			args[k] = v
			q.Set(k, v.(string))
		}
		text, isErr := callTool(t, a, "torque_run_list", args)
		require.False(t, isErr, text)
		var m envelope
		parseData(t, text, &m)
		resp, err := http.Get(ts.URL + "/api/v1/runs?" + q.Encode())
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
		var h envelope
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&h))
		require.Equal(t, m, h)
		return h
	}
	for _, tc := range []struct {
		sort, dir string
		ids       []int64
	}{
		{"started_at", "asc", []int64{9, 10, 11, 12, 13}}, {"started_at", "desc", []int64{13, 12, 9, 10, 11}},
		{"status", "asc", []int64{10, 11, 13, 12, 9}}, {"status", "desc", []int64{9, 12, 10, 11, 13}},
		{"duration", "asc", []int64{9, 10, 12, 11, 13}}, {"duration", "desc", []int64{11, 13, 10, 12, 9}},
		{"cost", "asc", []int64{9, 11, 13, 10, 12}}, {"cost", "desc", []int64{10, 12, 11, 13, 9}},
	} {
		t.Run(tc.sort+"/"+tc.dir, func(t *testing.T) {
			args := map[string]any{"sort_by": tc.sort, "sort_dir": tc.dir, "limit": "1", "include_total": "true"}
			var ids []int64
			for n := 0; ; n++ {
				require.Less(t, n, 6)
				page := get(args)
				require.Equal(t, 5, *page.Meta.Total)
				require.Len(t, page.Items, 1)
				ids = append(ids, page.Items[0].ID)
				if !page.Meta.HasMore {
					require.Nil(t, page.Meta.NextCursor)
					break
				}
				args["cursor"] = *page.Meta.NextCursor
			}
			require.Equal(t, tc.ids, ids)
		})
	}
	page := get(map[string]any{"status": "done,failed", "since": "2026-10-01T01:02:03.000000001Z", "until": "2026-10-01T01:02:03.000000002Z", "include_total": "true"})
	require.Len(t, page.Items, 2)
	require.Equal(t, 2, *page.Meta.Total)
	page = get(map[string]any{"limit": "1000"})
	require.Equal(t, 200, page.Meta.Limit)
	require.Nil(t, page.Meta.Total)
	for _, args := range []map[string]any{{"limit": "garbage"}, {"limit": "-1"}, {"include_total": "perhaps"}, {"sort_by": "task_id"}, {"offset": "-1"}, {"cursor": "broken"}} {
		text, isErr := callTool(t, a, "torque_run_list", args)
		require.True(t, isErr, text)
		require.Contains(t, text, "arg_invalid")
		q := url.Values{}
		for k, v := range args {
			q.Set(k, v.(string))
		}
		resp, err := http.Get(ts.URL + "/api/v1/runs?" + q.Encode())
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, 400, resp.StatusCode)
	}
}

func TestRunListByteTrimPreservesCursor(t *testing.T) {
	a, db := setupAdapterWithDB(t)
	_, err := db.Exec(`INSERT INTO tasks (id,title,status) VALUES ('large-runs','large','doing')`)
	require.NoError(t, err)
	for i := 1; i <= 4; i++ {
		_, err := db.Exec(`INSERT INTO runs (id,task_id,executor,status,started_at,error_message) VALUES (?, 'large-runs','mock','done','2026-10-01 00:00:00',?)`, i, strings.Repeat("x", 40000))
		require.NoError(t, err)
	}
	type envelope struct {
		Items []sqlstore.RunRecord `json:"items"`
		Meta  pagination.PageMeta  `json:"meta"`
	}
	var ids []int64
	cursor := ""
	for n := 0; ; n++ {
		require.Less(t, n, 4)
		text, isErr := callTool(t, a, "torque_run_list", map[string]any{"verbose": "true", "limit": "4", "cursor": cursor})
		require.False(t, isErr, text)
		var page envelope
		parseData(t, text, &page)
		require.NotEmpty(t, page.Items)
		require.Less(t, len(text), 102400)
		for _, row := range page.Items {
			ids = append(ids, row.ID)
		}
		if !page.Meta.HasMore {
			break
		}
		require.NotNil(t, page.Meta.NextCursor)
		cursor = *page.Meta.NextCursor
	}
	require.Equal(t, []int64{1, 2, 3, 4}, ids)
	_, err = db.Exec(`UPDATE runs SET error_message=? WHERE id=1`, strings.Repeat("x", 110000))
	require.NoError(t, err)
	text, isErr := callTool(t, a, "torque_run_list", map[string]any{"verbose": "true"})
	require.True(t, isErr, text)
	require.Contains(t, text, "arg_invalid")
	text, isErr = callTool(t, a, "torque_run_list", map[string]any{})
	require.False(t, isErr, text)
}
