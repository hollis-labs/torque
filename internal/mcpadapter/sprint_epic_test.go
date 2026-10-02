package mcpadapter_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSprintEpicCRUDAndValidation(t *testing.T) {
	a, ts, svc := setupAdjacentQueryParitySurfaces(t)
	p, err := svc.Project.Create(service.ProjectCreateInput{Name: "project", RepoPath: t.TempDir()})
	require.NoError(t, err)
	other, err := svc.Project.Create(service.ProjectCreateInput{Name: "other", RepoPath: t.TempDir()})
	require.NoError(t, err)
	epic, err := svc.Epic.Create(service.EpicCreateInput{Name: "epic", ProjectID: p.ID})
	require.NoError(t, err)
	raw, bad := callTool(t, a, "torque_sprint_create", map[string]any{"name": "mcp", "project_id": p.ID, "epic_id": epic.ID})
	require.False(t, bad, raw)
	var sp sqlstore.SprintRecord
	parseData(t, raw, &sp)
	require.True(t, sp.EpicID.Valid)
	require.Equal(t, epic.ID, sp.EpicID.String)
	// Missing fields leave the link alone; empty clears to SQL NULL.
	raw, bad = callTool(t, a, "torque_sprint_update", map[string]any{"id": sp.ID, "goal": "changed"})
	require.False(t, bad, raw)
	got, err := svc.Sprint.Get(sp.ID)
	require.NoError(t, err)
	require.Equal(t, epic.ID, got.EpicID.String)
	raw, bad = callTool(t, a, "torque_sprint_update", map[string]any{"id": sp.ID, "epic_id": ""})
	require.False(t, bad, raw)
	got, err = svc.Sprint.Get(sp.ID)
	require.NoError(t, err)
	require.False(t, got.EpicID.Valid)
	request := func(method, path string, body string, status int) map[string]any {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+"/api/v1"+path, bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		var result map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		require.Equal(t, status, resp.StatusCode, result)
		return result
	}
	body := fmt.Sprintf(`{"name":"http","project_id":%q,"epic_id":%q}`, p.ID, epic.ID)
	row := request("POST", "/sprints", body, 201)
	require.Equal(t, epic.ID, row["epic_id"])
	id := row["id"].(string)
	for _, transport := range []string{"http", "mcp"} {
		t.Run(transport, func(t *testing.T) {
			for _, e := range []string{"missing", epic.ID} {
				project := p.ID
				if e == epic.ID {
					project = other.ID
				}
				if transport == "http" {
					request("POST", "/sprints", fmt.Sprintf(`{"name":"invalid","project_id":%q,"epic_id":%q}`, project, e), 400)
				} else {
					raw, bad := callTool(t, a, "torque_sprint_create", map[string]any{"name": "invalid", "project_id": project, "epic_id": e})
					require.True(t, bad, raw)
				}
			}
		})
	}
	request("PUT", "/sprints/"+id, `{"epic_id":"missing"}`, 400)
	request("PUT", "/sprints/"+id, fmt.Sprintf(`{"project_id":%q}`, other.ID), 400)
	request("PUT", "/sprints/"+id, `{"epic_id":42}`, 400)
	raw, bad = callTool(t, a, "torque_sprint_update", map[string]any{"id": id, "epic_id": 42})
	require.True(t, bad, raw)
	raw, bad = callTool(t, a, "torque_sprint_update", map[string]any{"id": id, "project_id": other.ID})
	require.True(t, bad, raw)
	raw, bad = callTool(t, a, "torque_sprint_update", map[string]any{"id": id, "epic_id": "missing"})
	require.True(t, bad, raw)
	retained, err := svc.Sprint.Get(id)
	require.NoError(t, err)
	require.Equal(t, p.ID, retained.ProjectID.String)
	require.Equal(t, epic.ID, retained.EpicID.String)
	raw, bad = callTool(t, a, "torque_sprint_update", map[string]any{"id": sp.ID, "epic_id": epic.ID})
	require.False(t, bad, raw)
	raw, bad = callTool(t, a, "torque_sprint_update", map[string]any{"id": sp.ID, "epic_id": nil})
	require.False(t, bad, raw)
	got, err = svc.Sprint.Get(sp.ID)
	require.NoError(t, err)
	require.False(t, got.EpicID.Valid)
	// Clearing the association and changing the project together validates the resulting pair.
	raw, bad = callTool(t, a, "torque_sprint_update", map[string]any{"id": sp.ID, "project_id": other.ID, "epic_id": ""})
	require.False(t, bad, raw)

	row = request("PUT", "/sprints/"+id, `{"epic_id":null}`, 200)
	require.Nil(t, row["epic_id"])
	row = request("PUT", "/sprints/"+id, fmt.Sprintf(`{"epic_id":%q}`, epic.ID), 200)
	require.Equal(t, epic.ID, row["epic_id"])
	require.NoError(t, svc.Epic.Delete(epic.ID))
	row = request("GET", "/sprints/"+id, "", 200)
	require.Nil(t, row["epic_id"])
}

func TestSprintEpicCohortHTTPMCP(t *testing.T) {
	a, ts, svc := setupAdjacentQueryParitySurfaces(t)
	store := svc.Store()
	str := func(v string) sql.NullString { return sql.NullString{String: v, Valid: true} }
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "P", Name: "project"}))
	for _, id := range []string{"E", "OTHER"} {
		require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: id, Name: id, ProjectID: str("P")}))
	}
	// Archived epic links still participate. Only sprint archival affects visibility.
	require.NoError(t, store.ArchiveEpic("E"))
	for i := 0; i < 4; i++ {
		epic := "E"
		if i == 3 {
			epic = "OTHER"
		}
		id := fmt.Sprintf("SP%d", i)
		require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: id, Name: "needle", ProjectID: str("P"), EpicID: str(epic), CostBudget: sql.NullFloat64{Float64: 5, Valid: true}}))
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: fmt.Sprintf("T%d", i), Title: "child", Status: "todo", Kind: "agent", Manual: true, SprintID: str(id)}))
	}
	require.NoError(t, store.ArchiveSprint("SP2"))
	for _, archived := range []bool{false, true} {
		t.Run(fmt.Sprint(archived), func(t *testing.T) {
			count := 2
			if archived {
				count = 3
			}
			filters := fmt.Sprintf("epic_id=E&project_id=P&status=active&search=needle&cost_budget_min=1&include_archived=%t", archived)
			args := map[string]any{"epic_id": "E", "project_id": "P", "status": "active", "search": "needle", "cost_budget_min": "1", "include_archived": fmt.Sprint(archived), "limit": "1", "sort_by": "name", "sort_dir": "asc", "include_total": "true"}
			var httpIDs, mcpIDs []string
			cursor := ""
			for page := 0; page < count; page++ {
				path := ts.URL + "/api/v1/sprints?" + filters + "&limit=1&sort_by=name&sort_dir=asc&include_total=true"
				if cursor != "" {
					path += "&cursor=" + url.QueryEscape(cursor)
					args["cursor"] = cursor
				}
				h := decodeHTTPAdjacentEnvelope(t, path)
				m := mcpAdjacentPage(t, a, "torque_sprint_list", args)
				require.Equal(t, h.Meta.Total, m.Meta.Total)
				require.Equal(t, count, *h.Meta.Total)
				require.Equal(t, idsOf(h.Items), idsOf(m.Items))
				require.Equal(t, "E", h.Items[0]["epic_id"])
				require.Equal(t, "E", m.Items[0]["epic_id"])
				httpIDs = append(httpIDs, idsOf(h.Items)...)
				mcpIDs = append(mcpIDs, idsOf(m.Items)...)
				if !h.Meta.HasMore {
					require.Equal(t, count, len(httpIDs))
					break
				}
				require.NotNil(t, h.Meta.NextCursor)
				cursor = *h.Meta.NextCursor
			}
			require.Equal(t, httpIDs, mcpIDs)
			resp, err := http.Get(ts.URL + "/api/v1/sprints/facets?" + filters)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, 200, resp.StatusCode)
			var h sqlstore.EntityFacetResult
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&h))
			delete(args, "limit")
			delete(args, "cursor")
			delete(args, "sort_by")
			delete(args, "sort_dir")
			delete(args, "include_total")
			raw, bad := callTool(t, a, "torque_sprint_facets", args)
			require.False(t, bad, raw)
			var m sqlstore.EntityFacetResult
			parseData(t, raw, &m)
			require.Equal(t, h, m)
			require.Equal(t, count, h.MatchingCount)
			require.Equal(t, count, h.TaskTotals.Total)
			require.Len(t, h.TaskRollups.Scopes, count)
			for _, scope := range h.TaskRollups.Scopes {
				require.Equal(t, 1, scope.Total)
			}
			for _, facet := range h.Facets {
				for _, bucket := range facet.Buckets {
					require.Equal(t, count, bucket.Count)
				}
			}
		})
	}
	// Independent epic task ownership remains empty despite the sprint link.
	rows, err := store.ListTasks(sqlstore.TaskFilter{EpicID: "E"})
	require.NoError(t, err)
	require.Empty(t, rows)
}
