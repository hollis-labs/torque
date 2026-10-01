package mcpadapter_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/hollis-labs/torque/internal/service/pagination"
	"github.com/stretchr/testify/require"
)

// All row-list tools are named here, including the CW-0565 service families.
// Pending rows become contract cases when that dependency is integrated.
func TestMCPListPolicyTable(t *testing.T) {
	svc := setupServiceDirect(t)
	for _, feature := range []string{"projects", "epics", "sprints", "collections"} {
		require.NoError(t, svc.Feature.Enable(feature))
	}
	task, err := svc.Task.Create(service.TaskCreateInput{Title: "table fixture", Manual: true})
	require.NoError(t, err)
	a := adapterFromService(svc)
	cases := []struct {
		name    string
		args    map[string]any
		pending string
	}{
		{"torque_task_list", nil, ""}, {"torque_run_list", nil, ""}, {"torque_project_list", nil, ""}, {"torque_epic_list", nil, ""}, {"torque_sprint_list", nil, ""}, {"torque_issue_list", nil, ""}, {"torque_plan_list", nil, ""}, {"torque_tag_list", nil, ""},
		{"torque_comment_list", map[string]any{"entity_type": "task", "entity_id": task.ID}, ""},
		{"torque_comment_search", map[string]any{"query": "fixture"}, ""},
		{"torque_task_subtodo_list", map[string]any{"task_id": task.ID}, ""},
		{"torque_session_list", nil, "CW-0565"}, {"torque_session_checkpoint_list", nil, "CW-0565 (new tool)"},
		{"torque_artifact_list", nil, "CW-0565"}, {"torque_collection_list", nil, "CW-0565"}, {"torque_collection_tasks_list", nil, "CW-0565"}, {"torque_collection_inbox_list", nil, "CW-0565"},
		{"torque_plan_list_children", nil, "CW-0565"}, {"torque_task_checkpoint_list", nil, "CW-0565"}, {"torque_task_checkpoints_pending", nil, "CW-0565"}, {"torque_template_list", nil, "CW-0565"}, {"torque_models_list", nil, "CW-0565"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.pending != "" {
				t.Skip("cursor adapter pending " + tc.pending)
			}
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
