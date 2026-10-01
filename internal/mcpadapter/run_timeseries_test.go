package mcpadapter_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRunTimeSeriesHTTPMCPParityAndTotals(t *testing.T) {
	a, ts, db := setupTaskQueryParitySurfaces(t)
	defer ts.Close()
	for _, q := range []string{
		`INSERT INTO projects (id,name) VALUES ('series-p','project')`,
		`INSERT INTO sprints (id,name) VALUES ('series-s','sprint')`,
		`INSERT INTO epics (id,name) VALUES ('series-e','epic')`,
		`INSERT INTO tasks (id,title,status,project_id,sprint_id,epic_id) VALUES ('series-t','series','doing','series-p','series-s','series-e')`,
		`INSERT INTO tasks (id,title,status) VALUES ('series-other','other','doing')`,
	} {
		_, err := db.Exec(q)
		require.NoError(t, err)
	}
	stamps := []string{"2026-10-01 23:29:59.999999999 +0000 UTC", "2026-10-01 23:30:00", "2026-10-01 23:59:59.999999999 +0000 UTC", "2026-10-02T00:00:00Z", "2026-10-02 01:00:00.0 +0000 UTC", "2026-10-02 02:00:00", "2026-10-02T03:00:00.000000001Z"}
	for i, stamp := range stamps {
		id := i + 1
		_, err := db.Exec(`INSERT INTO runs (id,task_id,executor,status,started_at,prompt_tokens,completion_tokens,cost) VALUES (?,'series-t','mock',?,?,?,?,999)`, id, []string{"done", "failed", "running"}[i%3], stamp, id*10, id*100)
		require.NoError(t, err)
		if id != 4 {
			for _, cost := range []float64{float64(id), 0.5} {
				_, err = db.Exec(`INSERT INTO cost_ledger (task_id,run_id,cost,prompt_tokens,completion_tokens,recorded_at) VALUES ('series-t',?,?,9999,9999,'2000-01-01')`, id, cost)
				require.NoError(t, err)
			}
		}
	}
	_, err := db.Exec(`INSERT INTO runs (id,task_id,executor,status,started_at,prompt_tokens) VALUES (8,'series-other','mock','done','2026-10-02 00:00:00',9999)`)
	require.NoError(t, err)
	get := func(args map[string]any) service.RunTimeSeriesResult {
		t.Helper()
		text, isErr := callTool(t, a, "torque_run_timeseries", args)
		require.False(t, isErr, text)
		var m service.RunTimeSeriesResult
		parseData(t, text, &m)
		q := url.Values{}
		for k, v := range args {
			q.Set(k, fmt.Sprint(v))
		}
		resp, err := http.Get(ts.URL + "/api/v1/runs/timeseries?" + q.Encode())
		require.NoError(t, err)
		defer resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
		var h service.RunTimeSeriesResult
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&h))
		require.Equal(t, m, h)
		return h
	}
	cohort := map[string]any{"since": "2026-10-01T23:30:00Z", "until": "2026-10-02T03:00:00Z", "project_id": "series-p", "sprint_id": "series-s", "epic_id": "series-e", "task_id": "series-t"}
	for _, status := range []string{"", "done,failed"} {
		for _, bucket := range []string{"hour", "day"} {
			for _, offset := range []int{0, 30, -330} {
				args := map[string]any{}
				for k, v := range cohort {
					args[k] = v
				}
				args["bucket"] = bucket
				args["tz_offset_minutes"] = offset
				if status != "" {
					args["status"] = status
				}
				series := get(args)
				facetArgs := map[string]any{}
				for k, v := range cohort {
					facetArgs[k] = v
				}
				if status != "" {
					facetArgs["status"] = status
				}
				text, isErr := callTool(t, a, "torque_run_facets", facetArgs)
				require.False(t, isErr, text)
				var facets sqlstore.EntityFacetResult
				parseData(t, text, &facets)
				require.Equal(t, facets.MatchingCount, series.Totals.Count)
				require.Equal(t, facets.Totals.PromptTokens, series.Totals.PromptTokens)
				require.Equal(t, facets.Totals.CompletionTokens, series.Totals.CompletionTokens)
				require.InDelta(t, facets.Totals.Cost, series.Totals.Cost, 1e-9)
				facetArgs["include_total"] = "true"
				text, isErr = callTool(t, a, "torque_run_list", facetArgs)
				require.False(t, isErr, text)
				var page struct{ Meta struct{ Total int } }
				parseData(t, text, &page)
				require.Equal(t, page.Meta.Total, series.Totals.Count)
				sum := service.RunTimeSeriesTotals{}
				for i, b := range series.Buckets {
					sum.Count += b.Count
					sum.Cost += b.Cost
					sum.PromptTokens += b.PromptTokens
					sum.CompletionTokens += b.CompletionTokens
					statusSum := 0
					for _, count := range b.StatusCounts {
						statusSum += count
					}
					require.Equal(t, b.Count, statusSum)
					local := b.Start.In(time.FixedZone("", offset*60))
					require.Zero(t, local.Minute())
					require.Zero(t, local.Second())
					if bucket == "day" {
						require.Zero(t, local.Hour())
					}
					if i > 0 {
						step := time.Hour
						if bucket == "day" {
							step = 24 * time.Hour
						}
						require.Equal(t, step, b.Start.Sub(series.Buckets[i-1].Start))
					}
				}
				require.Equal(t, series.Totals, sum)
			}
		}
	}
	args := map[string]any{}
	for k, v := range cohort {
		args[k] = v
	}
	args["bucket"] = "hour"
	series := get(args)
	require.Len(t, series.Buckets, 5)
	require.Equal(t, 2, series.Buckets[0].Count)
	require.Equal(t, 1, series.Buckets[1].Count)
	require.Zero(t, series.Buckets[4].Count)
	millisArgs := map[string]any{}
	for k, v := range args {
		millisArgs[k] = v
	}
	millisArgs["since"] = fmt.Sprint(time.Date(2026, 10, 1, 23, 30, 0, 0, time.UTC).UnixMilli())
	millisArgs["until"] = fmt.Sprint(time.Date(2026, 10, 2, 3, 0, 0, 0, time.UTC).UnixMilli())
	require.Equal(t, series, get(millisArgs))
	args["tz_offset_minutes"] = 30
	shifted := get(args)
	require.Equal(t, "2026-10-01T23:30:00Z", shifted.Buckets[0].Start.Format(time.RFC3339))
	require.Equal(t, 3, shifted.Buckets[0].Count)
	args["task_id"] = "missing"
	empty := get(args)
	require.NotEmpty(t, empty.Buckets)
	require.Zero(t, empty.Totals.Count)
	for _, b := range empty.Buckets {
		require.Zero(t, b.Count)
		require.NotNil(t, b.StatusCounts)
	}
	args = map[string]any{"since": "2026-01-01T00:00:00Z", "until": "2026-07-19T00:00:00Z"}
	require.Len(t, get(args).Buckets, 200)
	_, isErr := callTool(t, a, "torque_run_timeseries", map[string]any{"since": "2026-01-01T00:00:00Z", "until": "2026-07-20T00:00:00Z"})
	require.True(t, isErr)
	for _, bad := range []map[string]any{
		{}, {"since": "bad", "until": "2026-01-01T00:00:00Z"}, {"since": "2026-01-01T00:00:00Z"},
		{"since": "2026-01-02T00:00:00Z", "until": "2026-01-01T00:00:00Z"},
		{"since": "2026-01-01T00:00:00Z", "until": "2026-07-20T00:00:00Z"},
		{"since": "2026-01-01T00:00:00Z", "until": "2026-01-10T00:00:00Z", "bucket": "hour"},
		{"since": "2026-01-01T00:00:00Z", "until": "2026-01-01T00:00:00Z", "bucket": "week"},
		{"since": "2026-01-01T00:00:00Z", "until": "2026-01-01T00:00:00Z", "tz_offset_minutes": 841},
		{"since": "2026-01-01T00:00:00Z", "until": "2026-01-01T00:00:00Z", "tz_offset_minutes": "0.5"},
		{"since": "2026-01-01T00:00:00Z", "until": "2026-01-01T00:00:00Z", "limit": 0},
	} {
		text, isErr := callTool(t, a, "torque_run_timeseries", bad)
		require.True(t, isErr, text)
		require.Contains(t, text, "arg_invalid")
		q := url.Values{}
		for k, v := range bad {
			q.Set(k, fmt.Sprint(v))
		}
		resp, err := http.Get(ts.URL + "/api/v1/runs/timeseries?" + q.Encode())
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, 400, resp.StatusCode, "%v", bad)
	}
}
