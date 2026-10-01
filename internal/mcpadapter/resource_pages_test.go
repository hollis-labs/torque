package mcpadapter_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
)

func resourceIdentities(items []map[string]any) []string {
	out := make([]string, 0, len(items))
	for _, v := range items {
		if id, ok := v["ID"]; ok {
			v["id"] = id
		}
		id := fmt.Sprint(v["id"])
		if ver, ok := v["Version"]; ok {
			id += ":" + fmt.Sprint(ver)
		}
		if ver, ok := v["version"]; ok {
			id += ":" + fmt.Sprint(ver)
		}
		out = append(out, id)
	}
	return out
}

func TestFullStack_RemainingResourcePages(t *testing.T) {
	_, old, svc := setupAdjacentQueryParitySurfaces(t)
	old.Close()
	store := svc.Store()
	require.NoError(t, svc.Feature.Enable("collections"))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "ROOT", Title: "root", Kind: "plan", Status: "backlog", Manual: true}))
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: "COL", Name: "root"}))
	mgr := agent.NewManager(&agent.Dependencies{Store: store})
	a := mcpadapter.New(svc, nil).WithSessions(mgr)
	ts := httptest.NewServer(httpserver.New(svc, nil).WithSessions(mgr))
	defer ts.Close()
	for i := 0; i < 206; i++ {
		id := fmt.Sprintf("%03d", i)
		name := "keep-" + id
		status := "backlog"
		if i == 205 {
			name = "omit"
			status = "doing"
		}
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "PL-" + id, Title: name, Kind: "plan", Status: status, Priority: i%5 + 1, Manual: true}))
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CH-" + id, Title: name, Status: status, Priority: i%5 + 1, ParentID: sql.NullString{String: "ROOT", Valid: true}, Metadata: sql.NullString{String: `{"phase_id":"phase"}`, Valid: true}, Manual: true}))
		require.NoError(t, store.AddTaskToCollection("CH-"+id, "COL", i%3))
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "IN-" + id, Title: name, Status: status, Priority: i%5 + 1, Manual: true}))
		require.NoError(t, store.AddTaskToInbox("IN-"+id))
		require.NoError(t, store.CreateArtifact(&sqlstore.ArtifactRecord{TaskID: "ROOT", Type: name, Content: name}))
		require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: "CO-" + id, Name: name}))
		require.NoError(t, store.CreateTemplate(&sqlstore.TemplateRecord{ID: fmt.Sprintf("TPL-%03d", i/3), Version: i%3 + 1, Name: name, Kind: "task"}))
		require.NoError(t, store.CreateCheckpoint(&sqlstore.CheckpointRecord{TaskID: "ROOT", CorrelationID: name, Type: "message", PayloadJSON: `{}`, EmitterSourceType: "system"}))
		require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "SES-" + id, Workdir: name, State: "running", TaskID: sql.NullString{String: "ROOT", Valid: true}}))
		require.NoError(t, store.CreateSessionCheckpoint(&sqlstore.SessionCheckpointRecord{ID: "SCP-" + id, SessionID: "SES-000", Note: name}))
	}
	cases := []struct {
		resource, path, tool string
		base                 url.Values
		args                 map[string]any
	}{
		{"plans", "/plans", "torque_plan_list", nil, nil},
		{"plan_children", "/plans/ROOT/children", "torque_plan_list_children", url.Values{"phase_id": {"phase"}}, map[string]any{"plan_id": "ROOT", "phase_id": "phase"}},
		{"collections", "/collections", "torque_collection_list", nil, nil},
		{"collection_tasks", "/collections/COL/tasks", "torque_collection_tasks_list", nil, map[string]any{"collection_id": "COL"}},
		{"collection_inbox", "/collections/inbox/tasks", "torque_collection_inbox_list", nil, nil},
		{"artifacts", "/artifacts", "torque_artifact_list", url.Values{"task_id": {"ROOT"}}, map[string]any{"task_id": "ROOT"}},
		{"templates", "/templates", "torque_template_list", nil, nil},
		{"checkpoints", "/tasks/ROOT/checkpoints", "torque_task_checkpoint_list", nil, map[string]any{"task_id": "ROOT"}},
		{"pending_checkpoints", "/checkpoints/pending", "torque_task_checkpoints_pending", nil, nil},
		{"sessions", "/sessions", "torque_session_list", nil, nil},
		{"session_checkpoints", "/sessions/SES-000/checkpoints", "torque_session_checkpoint_list", nil, map[string]any{"session_id": "SES-000"}},
	}
	for _, tc := range cases {
		t.Run(tc.resource, func(t *testing.T) {
			params := url.Values{"search": {"keep"}}
			args := map[string]any{"search": "keep"}
			for k, v := range tc.base {
				params[k] = v
			}
			for k, v := range tc.args {
				args[k] = v
			}
			httpPage := func() adjacentEnvelope {
				return decodeHTTPAdjacentEnvelope(t, ts.URL+"/api/v1"+tc.path+"?"+params.Encode())
			}
			mcpPage := func() adjacentEnvelope { return mcpAdjacentPage(t, a, tc.tool, args) }
			h, m := httpPage(), mcpPage()
			require.Len(t, h.Items, 50)
			require.Len(t, m.Items, 50)
			require.Nil(t, h.Meta.Total)
			require.Nil(t, m.Meta.Total)
			require.True(t, h.Meta.HasMore)
			require.Equal(t, resourceIdentities(h.Items), resourceIdentities(m.Items))
			params.Set("limit", "999")
			params.Set("include_total", "true")
			args["limit"] = "999"
			args["include_total"] = "true"
			h, m = httpPage(), mcpPage()
			for _, p := range []adjacentEnvelope{h, m} {
				require.Len(t, p.Items, 200)
				require.NotNil(t, p.Meta.Total)
				require.Equal(t, 205, *p.Meta.Total)
				require.NotNil(t, p.Meta.NextCursor)
			}
			params.Set("cursor", *h.Meta.NextCursor)
			args["cursor"] = *m.Meta.NextCursor
			h, m = httpPage(), mcpPage()
			require.Len(t, h.Items, 5)
			require.Len(t, m.Items, 5)
			require.False(t, h.Meta.HasMore)
			require.Nil(t, h.Meta.NextCursor)
			require.Equal(t, 205, *h.Meta.Total)
			fields, _, _ := service.ResourceSortPolicy(tc.resource)
			for _, field := range fields {
				for _, dir := range []string{"asc", "desc"} {
					params.Del("cursor")
					delete(args, "cursor")
					params.Set("limit", "50")
					args["limit"] = "50"
					params.Set("sort_by", field)
					args["sort_by"] = field
					params.Set("sort_dir", dir)
					args["sort_dir"] = dir
					seen := map[string]bool{}
					for i := 0; i < 5; i++ {
						h, m = httpPage(), mcpPage()
						require.Equal(t, resourceIdentities(h.Items), resourceIdentities(m.Items), field+dir)
						for _, id := range resourceIdentities(h.Items) {
							require.False(t, seen[id], "duplicate %s", id)
							seen[id] = true
						}
						if !h.Meta.HasMore {
							break
						}
						params.Set("cursor", *h.Meta.NextCursor)
						args["cursor"] = *m.Meta.NextCursor
					}
					require.Len(t, seen, 205, field+dir)
				}
			}
			// Explicit offset is separate from cursor mode, with the same
			// filtered cohort total. Invalid paging parameters reject on both surfaces.
			params.Del("cursor")
			delete(args, "cursor")
			params.Set("offset", "200")
			args["offset"] = "200"
			params.Set("limit", "50")
			args["limit"] = "50"
			h, m = httpPage(), mcpPage()
			require.Len(t, h.Items, 5)
			require.Len(t, m.Items, 5)
			require.Equal(t, 205, *h.Meta.Total)
			params.Del("offset")
			delete(args, "offset")
			for key, value := range map[string]string{"limit": "-1", "include_total": "maybe", "sort_by": "unknown", "cursor": "broken", "offset": "-1"} {
				previous, had := args[key]
				oldValues := params[key]
				params.Set(key, value)
				args[key] = value
				resp, err := http.Get(ts.URL + "/api/v1" + tc.path + "?" + params.Encode())
				require.NoError(t, err)
				resp.Body.Close()
				require.Equal(t, 400, resp.StatusCode, key)
				text, isErr := callTool(t, a, tc.tool, args)
				require.True(t, isErr, "%s: %s", key, text)
				if had {
					args[key] = previous
				} else {
					delete(args, key)
				}
				if oldValues == nil {
					params.Del(key)
				} else {
					params[key] = oldValues
				}
			}

			params.Del("cursor")
			delete(args, "cursor")
			params.Set("search", "missing")
			args["search"] = "missing"
			h, m = httpPage(), mcpPage()
			require.Empty(t, h.Items)
			require.Empty(t, m.Items)
			require.Equal(t, 0, *h.Meta.Total)
		})
	}
}
