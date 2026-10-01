package mcpadapter_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/require"
)

// Exercises the public contract with cohorts larger than both the default page
// and the maximum page, including exclusions that must also apply to COUNT.
func TestFullStack_AdjacentCursorOnlyPagesAndTotals(t *testing.T) {
	a, ts, svc := setupAdjacentQueryParitySurfaces(t)
	store := svc.Store()
	repoPath := t.TempDir()
	for i := 0; i < 207; i++ {
		id := fmt.Sprintf("%03d", i)
		name, author, status := "keep-"+id, "keep", "backlog"
		if i == 205 {
			name, author, status = "omit", "other", "doing"
		}
		require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "PRJ-" + id, Name: name, RepoPath: repoPath}))
		require.NoError(t, store.CreateEpic(&sqlstore.EpicRecord{ID: "EP-" + id, Name: name, ProjectID: sql.NullString{String: "PRJ-000", Valid: true}}))
		require.NoError(t, store.CreateSprint(&sqlstore.SprintRecord{ID: "SP-" + id, Name: name, ProjectID: sql.NullString{String: "PRJ-000", Valid: true}, CostBudget: sql.NullFloat64{Float64: 3, Valid: true}}))
		if i == 206 {
			require.NoError(t, store.ArchiveProject("PRJ-"+id))
			require.NoError(t, store.ArchiveEpic("EP-"+id))
			require.NoError(t, store.ArchiveSprint("SP-"+id))
			continue
		}
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-" + id, Title: name, Description: name, Kind: "issue", Status: status, Manual: true, ProjectID: sql.NullString{String: "PRJ-000", Valid: true}}))
		require.NoError(t, store.AddComment(&sqlstore.CommentRecord{EntityType: "task", EntityID: "CW-000", Author: author, Content: name}))
	}
	cases := []struct {
		name, path, tool, filter, value string
		base                            url.Values
		sortFields                      []string
	}{
		{"projects", "/projects", "torque_project_list", "search", "keep", nil, []string{"name", "status", "updated_at", "created_at"}},
		{"epics", "/epics", "torque_epic_list", "search", "keep", url.Values{"project_id": {"PRJ-000"}}, []string{"name", "status", "updated_at", "created_at"}},
		{"sprints", "/sprints", "torque_sprint_list", "search", "keep", url.Values{"project_id": {"PRJ-000"}, "cost_budget_min": {"2"}, "cost_budget_max": {"3"}}, []string{"name", "status", "updated_at", "created_at"}},
		{"issues", "/issues", "torque_issue_list", "query", "keep", url.Values{"project_id": {"PRJ-000"}, "status": {"backlog"}}, []string{"priority", "status", "updated_at", "created_at"}},
		{"comments", "/comments", "torque_comment_list", "author", "keep", url.Values{"entity_type": {"task"}, "entity_id": {"CW-000"}}, []string{"created_at"}},
		{"comment search", "/comments/search", "torque_comment_search", "query", "keep", url.Values{"entity_type": {"task"}, "entity_id": {"CW-000"}}, []string{"created_at"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := url.Values{}
			args := map[string]any{}
			for k, v := range tc.base {
				params[k] = v
				args[k] = v[0]
			}
			params.Set(tc.filter, tc.value)
			args[tc.filter] = tc.value
			getHTTP := func() adjacentEnvelope {
				return decodeHTTPAdjacentEnvelope(t, ts.URL+"/api/v1"+tc.path+"?"+params.Encode())
			}
			getMCP := func() adjacentEnvelope { return mcpAdjacentPage(t, a, tc.tool, args) }
			h, m := getHTTP(), getMCP()
			for _, page := range []adjacentEnvelope{h, m} {
				require.Len(t, page.Items, 50)
				require.Equal(t, 50, page.Meta.Returned)
				require.Equal(t, 50, page.Meta.Limit)
				require.True(t, page.Meta.HasMore)
				require.NotNil(t, page.Meta.NextCursor)
				require.Nil(t, page.Meta.Total)
			}
			require.Equal(t, idsOf(h.Items), idsOf(m.Items))
			params.Set("limit", "999")
			params.Set("include_total", "true")
			args["limit"], args["include_total"] = "999", true
			h, m = getHTTP(), getMCP()
			for _, page := range []adjacentEnvelope{h, m} {
				require.Len(t, page.Items, 200)
				require.Equal(t, 200, page.Meta.Limit)
				require.True(t, page.Meta.HasMore)
				require.NotNil(t, page.Meta.Total)
				require.Equal(t, 205, *page.Meta.Total)
			}
			require.Equal(t, idsOf(h.Items), idsOf(m.Items))
			params.Set("cursor", *h.Meta.NextCursor)
			args["cursor"] = *m.Meta.NextCursor
			h, m = getHTTP(), getMCP()
			for _, page := range []adjacentEnvelope{h, m} {
				require.Len(t, page.Items, 5)
				require.False(t, page.Meta.HasMore)
				require.Nil(t, page.Meta.NextCursor)
				require.NotNil(t, page.Meta.Total)
				require.Equal(t, 205, *page.Meta.Total)
			}
			require.Equal(t, idsOf(h.Items), idsOf(m.Items))
			params.Del("cursor")
			delete(args, "cursor")
			params.Set("limit", "2")
			args["limit"] = "2"
			for _, field := range tc.sortFields {
				for _, dir := range []string{"asc", "desc"} {
					params.Set("sort_by", field)
					params.Set("sort_dir", dir)
					args["sort_by"], args["sort_dir"] = field, dir
					firstH, firstM := getHTTP(), getMCP()
					require.Equal(t, idsOf(firstH.Items), idsOf(firstM.Items))
					params.Set("cursor", *firstH.Meta.NextCursor)
					args["cursor"] = *firstM.Meta.NextCursor
					nextH, nextM := getHTTP(), getMCP()
					require.Equal(t, idsOf(nextH.Items), idsOf(nextM.Items))
					require.NotEqual(t, idsOf(firstH.Items), idsOf(nextH.Items))
					for _, id := range idsOf(firstH.Items) {
						require.NotContains(t, idsOf(nextH.Items), id)
					}
					require.Equal(t, 205, *nextH.Meta.Total)
					require.Equal(t, 205, *nextM.Meta.Total)
					params.Del("cursor")
					delete(args, "cursor")
				}
			}
			params.Set(tc.filter, "no matching marker")
			args[tc.filter] = "no matching marker"
			h, m = getHTTP(), getMCP()
			for _, page := range []adjacentEnvelope{h, m} {
				require.Empty(t, page.Items)
				require.Equal(t, 0, *page.Meta.Total)
				require.False(t, page.Meta.HasMore)
				require.Nil(t, page.Meta.NextCursor)
			}
			params.Set("include_total", "bogus")
			args["include_total"] = "bogus"
			resp, err := http.Get(ts.URL + "/api/v1" + tc.path + "?" + params.Encode())
			require.NoError(t, err)
			resp.Body.Close()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			text, isErr := callTool(t, a, tc.tool, args)
			require.True(t, isErr, text)
			_, _, field := parseError(t, text)
			require.Equal(t, "include_total", field)
		})
	}
	// Default calls cannot select legacy shapes even without advanced parameters.
	for _, path := range []string{"/projects", "/epics", "/sprints", "/issues", "/tasks/CW-000/comments"} {
		page := decodeHTTPAdjacentEnvelope(t, ts.URL+"/api/v1"+path)
		require.Len(t, page.Items, 50)
		require.Equal(t, 50, page.Meta.Limit)
		require.True(t, page.Meta.HasMore)
		require.Nil(t, page.Meta.Total)
	}
}
