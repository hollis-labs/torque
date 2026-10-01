package mcpadapter_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
)

func TestEntityFacets_CohortParityRollupsAndBounds(t *testing.T) {
	a, ts, svc := setupAdjacentQueryParitySurfaces(t)
	store := svc.Store()
	str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("P%d", i)
		status := "active"
		if i == 2 {
			status = "inactive"
		}
		require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: id, Name: id, Status: status}))
		require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: fmt.Sprintf("E%d", i), Name: "needle", Status: status, ProjectID: str(id)}))
		require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: fmt.Sprintf("S%d", i), Name: id, Status: status, ProjectID: str(id), CostBudget: sql.NullFloat64{Float64: float64(i + 1), Valid: true}}))
	}
	require.NoError(t, store.ArchiveProject("P3"))
	require.NoError(t, store.ArchiveEpic("E3"))
	require.NoError(t, store.ArchiveSprint("S3"))
	for i, status := range []string{"todo", "done", "doing"} {
		kind := "agent"
		if i == 2 {
			kind = "internal"
		}
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: fmt.Sprintf("T%d", i), Title: "child", Status: status, Kind: kind, Executor: "cli", ProjectID: str("P0"), EpicID: str("E0"), SprintID: str("S0")}))
	}
	for _, tc := range []struct {
		entity, filters string
		args            map[string]any
	}{
		{"project", "status=active&search=P", map[string]any{"status": "active", "search": "P"}},
		{"epic", "status=active&search=needle", map[string]any{"status": "active", "search": "needle"}},
		{"sprint", "status=active&search=P&cost_budget_min=1&cost_budget_max=3", map[string]any{"status": "active", "search": "P", "cost_budget_min": "1", "cost_budget_max": "3"}},
	} {
		t.Run(tc.entity, func(t *testing.T) {
			resp, err := http.Get(ts.URL + "/api/v1/" + tc.entity + "s/facets?" + tc.filters + "&bucket_limit=1")
			require.NoError(t, err)
			require.Equal(t, 200, resp.StatusCode)
			var got sqlstore.EntityFacetResult
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
			resp.Body.Close()
			require.Equal(t, 2, got.MatchingCount)
			require.Equal(t, 1, got.BucketLimit)
			require.NotNil(t, got.TaskRollups)
			require.True(t, got.TaskRollups.Truncated)
			require.Equal(t, 2, got.TaskRollups.TotalDistinct)
			require.Len(t, got.TaskRollups.Scopes, 1)
			require.Equal(t, 2, got.TaskRollups.Scopes[0].Total)
			require.Equal(t, map[string]int{"todo": 1, "done": 1}, got.TaskRollups.Scopes[0].Counts)
			tc.args["bucket_limit"] = "1"
			raw, isErr := callTool(t, a, "torque_"+tc.entity+"_facets", tc.args)
			require.False(t, isErr, raw)
			var mcp sqlstore.EntityFacetResult
			parseData(t, raw, &mcp)
			require.Equal(t, got, mcp)
			resp, err = http.Get(ts.URL + "/api/v1/" + tc.entity + "s?" + tc.filters + "&include_total=true&limit=1")
			require.NoError(t, err)
			require.Equal(t, 200, resp.StatusCode)
			var page struct {
				Meta struct {
					Total int `json:"total"`
				} `json:"meta"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
			resp.Body.Close()
			require.Equal(t, page.Meta.Total, got.MatchingCount)

			// Actual list results over exactly the same filters, rather than a count
			// inferred from page length after truncation.
			switch tc.entity {
			case "project":
				rows, err := store.ListProjects(sqlstore.ProjectFilter{Status: "active"})
				require.NoError(t, err)
				require.Equal(t, len(rows), got.MatchingCount)
			case "epic":
				rows, err := store.ListEpics(sqlstore.EpicFilter{Status: "active", Search: "needle"})
				require.NoError(t, err)
				require.Equal(t, len(rows), got.MatchingCount)
			case "sprint":
				min, max := 1.0, 3.0
				rows, err := store.ListSprints(sqlstore.SprintFilter{Status: "active", CostBudgetMin: &min, CostBudgetMax: &max})
				require.NoError(t, err)
				require.Equal(t, len(rows), got.MatchingCount)
			}
		})
	}
	// Full metadata for value buckets; ties order by value, SQL null is distinct.
	raw, isErr := callTool(t, a, "torque_epic_facets", map[string]any{"include_archived": "true", "dimensions": "project_id,project_id", "bucket_limit": "2"})
	require.False(t, isErr, raw)
	var buckets sqlstore.EntityFacetResult
	parseData(t, raw, &buckets)
	require.Equal(t, []string{"project_id"}, buckets.Dimensions)
	require.Equal(t, 4, buckets.Facets[0].TotalDistinct)
	require.True(t, buckets.Facets[0].Truncated)
	require.Equal(t, []sqlstore.EntityFacetBucket{{Value: "P0", Count: 1}, {Value: "P1", Count: 1}}, buckets.Facets[0].Buckets)
	for _, entity := range []string{"project", "epic", "sprint"} {
		for _, key := range []string{"limit", "offset", "cursor", "sort_by", "sort_dir"} {
			resp, err := http.Get(ts.URL + "/api/v1/" + entity + "s/facets?" + key + "=")
			require.NoError(t, err)
			require.Equal(t, 400, resp.StatusCode)
			resp.Body.Close()
			raw, isErr := callTool(t, a, "torque_"+entity+"_facets", map[string]any{key: ""})
			require.True(t, isErr, raw)
		}
		for _, args := range []map[string]any{{"bucket_limit": "-1"}, {"bucket_limit": "nope"}, {"dimensions": "invalid"}, {"dimensions": ""}, {"include_archived": "invalid"}} {
			raw, isErr := callTool(t, a, "torque_"+entity+"_facets", args)
			require.True(t, isErr, raw)
		}
		raw, isErr := callTool(t, a, "torque_"+entity+"_facets", map[string]any{"status": "absent", "bucket_limit": "999"})
		require.False(t, isErr, raw)
		var empty sqlstore.EntityFacetResult
		parseData(t, raw, &empty)
		require.Equal(t, 0, empty.MatchingCount)
		require.Equal(t, 200, empty.BucketLimit)
		require.Empty(t, empty.TaskRollups.Scopes)
	}
}

func TestRunFacets_CohortTotalsAndParity(t *testing.T) {
	a, ts, svc := setupAdjacentQueryParitySurfaces(t)
	store := svc.Store()
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "P", Name: "P"}))
	require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: "E", Name: "E"}))
	require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "S", Name: "S"}))
	str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	for i := 0; i < 3; i++ {
		profile := "legacy"
		launch := ""
		if i == 0 {
			launch = "launch"
		}
		task := fmt.Sprintf("T%d", i)
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: task, Title: task, Status: "todo", Kind: "agent", Executor: "cli", ProjectID: str("P"), EpicID: str("E"), SprintID: str("S"), AgentProfile: profile, LaunchProfile: launch}))
		run, err := store.CreateRun(&sqlstore.RunRecord{TaskID: task, Executor: "cli"})
		require.NoError(t, err)
		require.NoError(t, store.CompleteRun(run, sqlstore.RunCompletion{Status: "done", Cost: 99, PromptTokens: 10, CompletionTokens: 20}))
		_, err = store.DB().Exec("UPDATE runs SET started_at = ? WHERE id = ?", fmt.Sprintf("2026-10-01 12:00:0%d.000", i), run)
		require.NoError(t, err)
		// Ledger intentionally differs from runs.cost; it is canonical. Multiple
		// ledger rows must not multiply token sums or status counts.
		for j := 0; j < 2; j++ {
			_, err = store.AppendCostLedger(&sqlstore.CostLedgerRecord{TaskID: task, RunID: run, Cost: 0.5, CostSource: "provider"})
			require.NoError(t, err)
		}
	}
	filters := []struct {
		http string
		args map[string]any
	}{
		{"", map[string]any{}},
		{"project_id=P&status=done&since=2026-10-01T12:00:00Z&until=2026-10-01T12:00:01Z", map[string]any{"project_id": "P", "status": "done", "since": "2026-10-01T12:00:00Z", "until": "2026-10-01T12:00:01Z"}},
		{"task_id=T0", map[string]any{"task_id": "T0"}},
		{"epic_id=E&sprint_id=S", map[string]any{"epic_id": "E", "sprint_id": "S"}},
		{"since=2027-01-01T00:00:00Z", map[string]any{"since": "2027-01-01T00:00:00Z"}},
	}
	for _, f := range filters {
		resp, err := http.Get(ts.URL + "/api/v1/runs/facets?" + f.http + "&bucket_limit=1")
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		var got sqlstore.EntityFacetResult
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
		resp.Body.Close()
		f.args["bucket_limit"] = "1"
		raw, isErr := callTool(t, a, "torque_run_facets", f.args)
		require.False(t, isErr, raw)
		var mcp sqlstore.EntityFacetResult
		parseData(t, raw, &mcp)
		require.Equal(t, got, mcp)
		resp, err = http.Get(ts.URL + "/api/v1/runs?" + f.http + "&include_total=true&limit=1")
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		var page struct {
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
		resp.Body.Close()
		require.Equal(t, page.Meta.Total, got.MatchingCount)
		require.NotNil(t, got.Totals)
		require.Equal(t, float64(got.MatchingCount), got.Totals.Cost)
		require.Equal(t, int64(got.MatchingCount*10), got.Totals.PromptTokens)
		require.Equal(t, int64(got.MatchingCount*20), got.Totals.CompletionTokens)
		if f.http == "" {
			require.Equal(t, 2, got.Facets[2].TotalDistinct)
			require.True(t, got.Facets[2].Truncated)
			require.Equal(t, "legacy", got.Facets[2].Buckets[0].Value)
			require.Equal(t, 2, got.Facets[2].Buckets[0].Count)
		}
	}
	for _, key := range []string{"limit", "offset", "cursor", "sort_by", "sort_dir"} {
		resp, err := http.Get(ts.URL + "/api/v1/runs/facets?" + key + "=")
		require.NoError(t, err)
		require.Equal(t, 400, resp.StatusCode)
		resp.Body.Close()
		raw, isErr := callTool(t, a, "torque_run_facets", map[string]any{key: ""})
		require.True(t, isErr, raw)
	}
	for _, args := range []map[string]any{{"since": "bad"}, {"until": "bad"}, {"since": "2026-01-02T00:00:00Z", "until": "2026-01-01T00:00:00Z"}, {"dimensions": "bad"}, {"bucket_limit": "-1"}} {
		raw, isErr := callTool(t, a, "torque_run_facets", args)
		require.True(t, isErr, raw)
	}
}

func TestEntityFacets_NullTypedBucketsAndFeatureFlags(t *testing.T) {
	a, ts, svc := setupAdjacentQueryParitySurfaces(t)
	store := svc.Store()
	for _, epic := range []sqlstore.EpicRecord{
		{ID: "E0", Name: "none"},
		{ID: "E1", Name: "one", Priority: sql.NullInt64{Int64: 2, Valid: true}},
		{ID: "E2", Name: "two", Priority: sql.NullInt64{Int64: 10, Valid: true}},
	} {
		require.NoError(t, store.CreateEpic(&epic))
	}
	raw, isErr := callTool(t, a, "torque_epic_facets", map[string]any{"dimensions": "priority"})
	require.False(t, isErr, raw)
	var got sqlstore.EntityFacetResult
	parseData(t, raw, &got)
	require.Equal(t, []sqlstore.EntityFacetBucket{{Value: float64(2), Count: 1}, {Value: float64(10), Count: 1}, {Value: nil, Count: 1}}, got.Facets[0].Buckets)
	require.Len(t, got.TaskRollups.Scopes, 3)
	for _, scope := range got.TaskRollups.Scopes {
		require.Zero(t, scope.Total)
		require.Empty(t, scope.Counts)
	}
	for _, entity := range []string{"project", "epic", "sprint"} {
		require.NoError(t, svc.Feature.Disable(entity+"s"))
		resp, err := http.Get(ts.URL + "/api/v1/" + entity + "s/facets")
		require.NoError(t, err)
		require.Equal(t, 404, resp.StatusCode)
		resp.Body.Close()
		raw, isErr := callTool(t, a, "torque_"+entity+"_facets", map[string]any{})
		require.True(t, isErr, raw)
	}
}

func TestParentFacetFiltersMatchListTotals(t *testing.T) {
	a, ts, svc := setupAdjacentQueryParitySurfaces(t)
	store := svc.Store()
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "P0", Name: "match"}))
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "P1", Name: "other"}))
	str := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	for i := 0; i < 3; i++ {
		project := "P0"
		name := "match"
		if i == 1 {
			project = "P1"
			name = "other"
		}
		require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: fmt.Sprintf("E%d", i), Name: name, ProjectID: str(project)}))
		require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: fmt.Sprintf("S%d", i), Name: name, ProjectID: str(project), CostBudget: sql.NullFloat64{Float64: 1, Valid: true}}))
	}
	require.NoError(t, store.ArchiveProject("P1"))
	require.NoError(t, store.ArchiveEpic("E2"))
	require.NoError(t, store.ArchiveSprint("S2"))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "T", Title: "T", Status: "todo", Kind: "agent", Executor: "cli", SprintID: str("S0")}))
	run, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "T", Executor: "cli"})
	require.NoError(t, err)
	require.NoError(t, store.CompleteRun(run, sqlstore.RunCompletion{Status: "done", Cost: 2}))
	for _, tc := range []struct {
		entity, filter string
		args           map[string]any
	}{
		{"project", "search=match", map[string]any{"search": "match"}},
		{"project", "include_archived=true", map[string]any{"include_archived": "true"}},
		{"epic", "project_id=P0&include_archived=true", map[string]any{"project_id": "P0", "include_archived": "true"}},
		{"epic", "search=absent", map[string]any{"search": "absent"}},
		{"sprint", "project_id=P0&search=match&over_budget=true", map[string]any{"project_id": "P0", "search": "match", "over_budget": "true"}},
		{"sprint", "include_archived=true&cost_budget_max=0", map[string]any{"include_archived": "true", "cost_budget_max": "0"}},
	} {
		resp, err := http.Get(ts.URL + "/api/v1/" + tc.entity + "s?" + tc.filter + "&include_total=true&limit=1")
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		var page struct {
			Meta struct {
				Total int `json:"total"`
			} `json:"meta"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
		resp.Body.Close()
		resp, err = http.Get(ts.URL + "/api/v1/" + tc.entity + "s/facets?" + tc.filter)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		var got sqlstore.EntityFacetResult
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
		resp.Body.Close()
		require.Equal(t, page.Meta.Total, got.MatchingCount)
		raw, isErr := callTool(t, a, "torque_"+tc.entity+"_facets", tc.args)
		require.False(t, isErr, raw)
		var mcp sqlstore.EntityFacetResult
		parseData(t, raw, &mcp)
		require.Equal(t, got, mcp)
	}
}
