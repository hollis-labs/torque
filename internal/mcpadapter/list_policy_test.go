package mcpadapter_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hollis-labs/substrate/llm-core/modelsdev"
	"github.com/hollis-labs/torque/internal/modelcatalog"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/agent"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/stretchr/testify/require"
)

// Behavioral policy cases include the integrated CW-0565 resource families.
func TestMCPListPolicyTable(t *testing.T) {
	svc := setupServiceDirect(t)
	for _, feature := range []string{"projects", "epics", "sprints", "collections"} {
		require.NoError(t, svc.Feature.Enable(feature))
	}
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "table fixture", Manual: true})
	require.NoError(t, err)
	store := svc.Store()
	require.NoError(t, store.CreateCollection(&sqlstore.CollectionRecord{ID: "COL-table", Name: "table fixture"}))
	require.NoError(t, store.AddTaskToCollection(task.ID, "COL-table", 1))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "IN-table", Title: "inbox fixture", Manual: true}))
	require.NoError(t, store.AddTaskToInbox("IN-table"))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "PL-table", Title: "plan fixture", Kind: "plan", Manual: true}))
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CH-table", Title: "child fixture", ParentID: sql.NullString{String: "PL-table", Valid: true}, Manual: true}))
	require.NoError(t, store.CreateArtifact(&sqlstore.ArtifactRecord{TaskID: task.ID, Type: "fixture", Content: "fixture"}))
	require.NoError(t, store.CreateTemplate(&sqlstore.TemplateRecord{ID: "TPL-table", Version: 1, Name: "fixture", Kind: "task"}))
	require.NoError(t, store.CreateCheckpoint(&sqlstore.CheckpointRecord{TaskID: task.ID, CorrelationID: "table", Type: "message", PayloadJSON: `{}`, EmitterSourceType: "system"}))
	require.NoError(t, store.CreateSession(&sqlstore.SessionRecord{ID: "SES-table", Workdir: t.TempDir(), State: "running"}))
	require.NoError(t, store.CreateSessionCheckpoint(&sqlstore.SessionCheckpointRecord{ID: "SCP-table", SessionID: "SES-table", Note: "fixture"}))
	// The catalog is a local fake; neither model discovery nor session reads
	// should reach an operator cache or launch a provider CLI.
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewEncoder(w).Encode(map[string]modelsdev.Provider{
			"fixture": {ID: "fixture", Name: "fixture", Models: map[string]modelsdev.Model{"one": {ID: "one", Name: "one"}}},
		}))
	}))
	defer catalog.Close()
	svc.Models = modelcatalog.New(modelsdev.WithURL(catalog.URL), modelsdev.WithHTTPClient(catalog.Client()), modelsdev.WithCacheDir(t.TempDir()))
	require.NoError(t, svc.Models.Refresh(context.Background()))
	a := adapterFromService(svc).WithSessions(agent.NewManager(&agent.Dependencies{Store: store}))
	cases := []struct {
		name string
		args map[string]any
	}{
		{"torque_task_list", nil}, {"torque_run_list", nil}, {"torque_project_list", nil}, {"torque_epic_list", nil}, {"torque_sprint_list", nil}, {"torque_issue_list", nil}, {"torque_plan_list", nil}, {"torque_tag_list", nil},
		{"torque_comment_list", map[string]any{"entity_type": "task", "entity_id": task.ID}},
		{"torque_comment_search", map[string]any{"query": "fixture"}},
		{"torque_task_subtodo_list", map[string]any{"task_id": task.ID}},
		{"torque_session_list", nil}, {"torque_session_checkpoint_list", map[string]any{"session_id": "SES-table"}},
		{"torque_artifact_list", map[string]any{"task_id": task.ID}}, {"torque_collection_list", nil}, {"torque_collection_tasks_list", map[string]any{"collection_id": "COL-table"}}, {"torque_collection_inbox_list", nil},
		{"torque_plan_list_children", map[string]any{"plan_id": "PL-table"}}, {"torque_task_checkpoint_list", map[string]any{"task_id": task.ID}}, {"torque_task_checkpoints_pending", nil}, {"torque_template_list", nil}, {"torque_models_list", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, input := range []string{"", "999"} {
				args := map[string]any{}
				for k, v := range tc.args {
					args[k] = v
				}
				want := pagination.DefaultLimit
				if input != "" {
					args["limit"] = input
					want = pagination.MaxLimit
				}
				text, isErr := callTool(t, a, tc.name, args)
				require.False(t, isErr, text)
				var env struct {
					Items []json.RawMessage
					Meta  map[string]json.RawMessage
				}
				parseData(t, text, &env)
				require.Contains(t, env.Meta, "next_cursor")
				require.Contains(t, env.Meta, "has_more")
				var limit, returned int
				require.NoError(t, json.Unmarshal(env.Meta["limit"], &limit))
				require.NoError(t, json.Unmarshal(env.Meta["returned"], &returned))
				require.Equal(t, want, limit)
				require.Equal(t, len(env.Items), returned)
				require.LessOrEqual(t, returned, want)
			}
		})
	}

}

func TestSubtodoCursorTraversalAndByteCap(t *testing.T) {
	for _, dir := range []string{"asc", "desc"} {
		t.Run(dir, func(t *testing.T) {
			svc := setupServiceDirect(t)
			task, err := svc.Task.Create(service.TaskCreateInput{Title: "checklist", Manual: true})
			require.NoError(t, err)
			specs := make([]sqlstore.Subtodo, 7)
			for i := range specs {
				specs[i] = sqlstore.Subtodo{ID: fmt.Sprintf("item-%d", i), Text: strings.Repeat("x", 35000)}
			}
			succeeded, failed := svc.Task.BulkAddSubtodo(task.ID, specs)
			require.Empty(t, failed)
			require.Len(t, succeeded, 7)
			a := adapterFromService(svc)
			args := map[string]any{"task_id": task.ID, "limit": "999", "sort_by": "position", "sort_dir": dir, "verbose": "true"}
			seen := []string{}
			for page := 0; page < 7; page++ {
				text, isErr := callTool(t, a, "torque_task_subtodo_list", args)
				require.False(t, isErr, text)
				require.LessOrEqual(t, len(text), soft100KB)
				var env struct {
					Items []sqlstore.Subtodo
					Meta  struct {
						Returned, Limit    int
						HasMore, Truncated bool
						NextCursor         *string
						Hint               string
					}
				}
				parseData(t, text, &env)
				// Explicit tags avoid Go's name matching ambiguity around underscores.
				var meta map[string]any
				var data map[string]json.RawMessage
				parseData(t, text, &data)
				require.NoError(t, json.Unmarshal(data["meta"], &meta))
				require.Equal(t, float64(len(env.Items)), meta["returned"])
				require.Equal(t, float64(200), meta["limit"])
				require.NotEmpty(t, env.Items)
				for _, item := range env.Items {
					seen = append(seen, item.ID)
				}
				if !meta["has_more"].(bool) {
					require.Nil(t, meta["next_cursor"])
					break
				}
				cursor := meta["next_cursor"].(string)
				require.NotEmpty(t, cursor)
				hint := meta["hint"].(string)
				require.Contains(t, hint, "Torque server page")
				require.Contains(t, hint, "Host preview/cache")
				prefix := "call torque_task_subtodo_list "
				_, call, _ := strings.Cut(hint, prefix)
				call, _, _ = strings.Cut(call, ". Host preview/cache")
				var next map[string]any
				require.NoError(t, json.Unmarshal([]byte(call), &next))
				require.Equal(t, task.ID, next["task_id"])
				require.Equal(t, dir, next["sort_dir"])
				require.Equal(t, "position", next["sort_by"])
				require.Equal(t, cursor, next["cursor"])
				args = next
			}
			want := []string{}
			for i := 0; i < 7; i++ {
				j := i
				if dir == "desc" {
					j = 6 - i
				}
				want = append(want, fmt.Sprintf("item-%d", j))
			}
			require.Equal(t, want, seen)
		})
	}
}

func TestCursorCapRejectsSingleOversizedRecord(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]any{"title": "oversized", "description": strings.Repeat("x", 110000), "manual": true})
	require.False(t, isErr, text)
	text, isErr = callTool(t, a, "torque_task_list", map[string]any{"verbose": "true"})
	require.True(t, isErr, text)
	code, message, _ := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Contains(t, message, "verbose=false")
}

func TestTaskByteCapHintPreservesCohortAndSort(t *testing.T) {
	svc := setupServiceDirect(t)
	for i := 0; i < 8; i++ {
		_, err := svc.Task.Create(service.TaskCreateInput{Title: fmt.Sprintf("needle-%d", i), Description: strings.Repeat("x", 30000), Priority: i, Tags: []string{"api"}, Manual: true})
		require.NoError(t, err)
	}
	a := adapterFromService(svc)
	args := map[string]any{"status": "todo", "search": "needle", "tags": "[\"api\"]", "sort_by": "priority", "sort_dir": "desc", "limit": "200", "verbose": "true", "include_total": "true", "_traceparent": ""}
	seen := map[string]bool{}
	for page := 0; page < 8; page++ {
		text, isErr := callTool(t, a, "torque_task_list", args)
		require.False(t, isErr, text)
		require.LessOrEqual(t, len(text), soft100KB)
		var env struct {
			Items []struct{ ID string }
			Meta  map[string]any
		}
		parseData(t, text, &env)
		require.Equal(t, float64(len(env.Items)), env.Meta["returned"])
		require.Equal(t, float64(8), env.Meta["total"])
		for _, item := range env.Items {
			require.False(t, seen[item.ID], "repeated task")
			seen[item.ID] = true
		}
		if !env.Meta["has_more"].(bool) {
			require.Nil(t, env.Meta["next_cursor"])
			break
		}
		hint := env.Meta["hint"].(string)
		_, call, ok := strings.Cut(hint, "call torque_task_list ")
		require.True(t, ok)
		call, _, _ = strings.Cut(call, ". Host preview/cache")
		var next map[string]any
		require.NoError(t, json.Unmarshal([]byte(call), &next))
		for _, key := range []string{"status", "search", "tags", "sort_by", "sort_dir", "verbose", "include_total"} {
			require.Equal(t, args[key], next[key], key)
		}
		require.NotContains(t, next, "offset")
		require.NotContains(t, next, "_traceparent")
		require.Equal(t, env.Meta["next_cursor"], next["cursor"])
		args = next
	}
	require.Len(t, seen, 8, "byte-trimmed rows must stay reachable")
}
