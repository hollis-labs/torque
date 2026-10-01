package mcpadapter_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/stretchr/testify/require"
)

func TestRunExecutorProfileCohortHTTPMCP(t *testing.T) {
	a, ts, db := setupTaskQueryParitySurfaces(t)
	defer ts.Close()
	_, err := db.Exec(`INSERT INTO projects(id,name) VALUES('filter-project','project')`)
	require.NoError(t, err)
	for _, task := range []struct{ id, launch, agent string }{{"primary", "codex", "legacy"}, {"fallback", "", "claude"}, {"empty", "", ""}, {"changed", "new", "codex"}} {
		_, err = db.Exec(`INSERT INTO tasks(id,title,status,launch_profile,agent_profile,project_id) VALUES(?,?,'doing',?,?,'filter-project')`, task.id, task.id, task.launch, task.agent)
		require.NoError(t, err)
	}
	for i, run := range []struct{ task, executor, status, stamp string }{
		{"primary", "cli", "done", "2026-10-01 10:00:00"},
		{"primary", "cli", "failed", "2026-10-01 11:00:00"},
		{"primary", "claude", "done", "2026-10-01 12:00:00"},
		{"fallback", "cli", "done", "2026-10-01 10:00:00"},
		{"fallback", "codex", "done", "2026-10-01 11:00:00"},
		{"empty", "cli", "done", "2026-10-01 10:00:00"},
		{"changed", "cli", "done", "2026-10-01 10:00:00"},
		{"primary", "cli", "running", "2026-10-01 13:00:00"},
		{"primary", "cli", "done", "2026-09-30 10:00:00"},
		{"primary", "cli", "done", "2026-10-03 10:00:00"},
		{"fallback", "cli", "failed", "2026-10-01 12:00:00"},
		{"primary", "cli", "done", "2026-10-01 11:00:00"},
	} {
		_, err = db.Exec(`INSERT INTO runs(id,task_id,executor,status,started_at,ended_at,cost,prompt_tokens,completion_tokens) VALUES(?,?,?,?,?,?,?,10,20)`, i+1, run.task, run.executor, run.status, run.stamp, run.stamp, float64(i%3))
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO cost_ledger(task_id,run_id,cost,recorded_at) VALUES(?,?,1,'2000-01-01')`, run.task, i+1)
		require.NoError(t, err)
	}
	parity := func(path, tool string, q url.Values, out any) {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/v1/" + path + "?" + q.Encode())
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
		require.NoError(t, json.NewDecoder(resp.Body).Decode(out))
		args := map[string]any{}
		for key := range q {
			args[key] = q.Get(key)
		}
		// HTTP decoration is additive to the same full RunRecords.
		if tool == "torque_run_list" {
			args["verbose"] = "true"
		}
		text, isErr := callTool(t, a, tool, args)
		require.False(t, isErr, text)
		switch result := out.(type) {
		case *struct {
			Items []sqlstore.RunRecord
			Meta  pagination.PageMeta
		}:
			var m struct {
				Items []sqlstore.RunRecord
				Meta  pagination.PageMeta
			}
			parseData(t, text, &m)
			require.Equal(t, *result, m)
		case *sqlstore.EntityFacetResult:
			var m sqlstore.EntityFacetResult
			parseData(t, text, &m)
			require.Equal(t, *result, m)
		case *service.RunTimeSeriesResult:
			var m service.RunTimeSeriesResult
			parseData(t, text, &m)
			require.Equal(t, *result, m)
		}
	}
	cohort := func() url.Values {
		return url.Values{"since": {"2026-10-01T00:00:00Z"}, "until": {"2026-10-02T00:00:00Z"}, "project_id": {"filter-project"}}
	}
	for _, tc := range []struct {
		name, executor, profile, status string
		ids                             []int64
	}{
		{"all", "", "", "", []int64{1, 2, 3, 4, 5, 6, 7, 8, 11, 12}},
		{"executor CSV", " cli, cli, ,claude ", "", "", []int64{1, 2, 3, 4, 6, 7, 8, 11, 12}},
		{"launch overrides agent", "", "codex", "", []int64{1, 2, 3, 8, 12}},
		{"legacy fallback", "", "claude", "", []int64{4, 5, 11}},
		{"current selector", "", "new", "", []int64{7}},
		{"combined", " cli,cli, ", " codex, codex, ", "done,failed", []int64{1, 2, 12}},
		{"profile CSV", "cli", "codex,claude", "", []int64{1, 2, 4, 8, 11, 12}},
		{"blank ignored", " , ", " , ", "", []int64{1, 2, 3, 4, 5, 6, 7, 8, 11, 12}},
		{"unknown executor", "missing", "", "", []int64{}},
		{"unknown profile", "", "missing", "", []int64{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := cohort()
			q.Set("executor", tc.executor)
			q.Set("profile", tc.profile)
			q.Set("status", tc.status)
			var facets sqlstore.EntityFacetResult
			parity("runs/facets", "torque_run_facets", q, &facets)
			var series service.RunTimeSeriesResult
			parity("runs/timeseries", "torque_run_timeseries", q, &series)
			q.Set("include_total", "true")
			var page struct {
				Items []sqlstore.RunRecord
				Meta  pagination.PageMeta
			}
			parity("runs", "torque_run_list", q, &page)
			ids := []int64{}
			for _, r := range page.Items {
				ids = append(ids, r.ID)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			require.Equal(t, tc.ids, ids)
			require.Equal(t, len(tc.ids), *page.Meta.Total)
			require.Equal(t, *page.Meta.Total, facets.MatchingCount)
			require.Equal(t, facets.MatchingCount, series.Totals.Count)
			require.Equal(t, facets.Totals.PromptTokens, series.Totals.PromptTokens)
			require.InDelta(t, facets.Totals.Cost, series.Totals.Cost, 1e-9)
		})
	}
	// Filtering a profile reproduces its unfiltered bucket count exactly.
	q := cohort()
	var all sqlstore.EntityFacetResult
	parity("runs/facets", "torque_run_facets", q, &all)
	for _, facet := range all.Facets {
		if facet.Dimension != "profile" {
			continue
		}
		for _, bucket := range facet.Buckets {
			value, ok := bucket.Value.(string)
			if !ok || value == "" {
				continue
			}
			q.Set("profile", value)
			var filtered sqlstore.EntityFacetResult
			parity("runs/facets", "torque_run_facets", q, &filtered)
			require.Equal(t, bucket.Count, filtered.MatchingCount)
		}
	}
	// Profile edits immediately change both the filter and the facet.
	_, err = db.Exec(`UPDATE tasks SET launch_profile='replacement' WHERE id='primary'`)
	require.NoError(t, err)
	q = cohort()
	q.Set("profile", "codex")
	var old sqlstore.EntityFacetResult
	parity("runs/facets", "torque_run_facets", q, &old)
	require.Zero(t, old.MatchingCount)
	q.Set("profile", "replacement")
	var current sqlstore.EntityFacetResult
	parity("runs/facets", "torque_run_facets", q, &current)
	require.Equal(t, 5, current.MatchingCount)
	// Cursor paging remains complete and deterministic for each allow-listed sort.
	for _, field := range service.RunQuerySortFields {
		for _, dir := range []string{"asc", "desc"} {
			t.Run(fmt.Sprintf("cursor/%s/%s", field, dir), func(t *testing.T) {
				q := cohort()
				q.Set("profile", "replacement,claude")
				q.Set("executor", "cli")
				q.Set("sort_by", field)
				q.Set("sort_dir", dir)
				q.Set("limit", "2")
				q.Set("include_total", "true")
				ids := []int64{}
				for pages := 0; ; pages++ {
					require.Less(t, pages, 4)
					var page struct {
						Items []sqlstore.RunRecord
						Meta  pagination.PageMeta
					}
					parity("runs", "torque_run_list", q, &page)
					require.Equal(t, 6, *page.Meta.Total)
					for _, r := range page.Items {
						ids = append(ids, r.ID)
					}
					if !page.Meta.HasMore {
						require.Nil(t, page.Meta.NextCursor)
						break
					}
					require.NotNil(t, page.Meta.NextCursor)
					q.Set("cursor", *page.Meta.NextCursor)
				}
				sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
				require.Equal(t, []int64{1, 2, 4, 8, 11, 12}, ids)
			})
		}
	}
}
