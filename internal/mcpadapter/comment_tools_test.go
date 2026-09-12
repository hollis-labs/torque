package mcpadapter_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/mcpadapter"
)

// commentListCursorEnvelope mirrors torque_comment_list/search's
// PRIM-001/PRIM-002 cursor-pagination response shape (see
// taskListCursorEnvelope's twin in task_tools_test.go).
type commentListCursorEnvelope struct {
	Items []map[string]interface{} `json:"items"`
	Meta  struct {
		Returned   int     `json:"returned"`
		Limit      int     `json:"limit"`
		HasMore    bool    `json:"has_more"`
		NextCursor *string `json:"next_cursor"`
	} `json:"meta"`
}

func createTaskForComments(t *testing.T, a *mcpadapter.Adapter, title string) string {
	t.Helper()
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       title,
		"description": "x",
	})
	require.False(t, isErr, "task create should not error: %s", text)
	var rec map[string]interface{}
	parseData(t, text, &rec)
	return rec["ID"].(string)
}

func TestCommentAdd_EntityTypeSupport(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	taskID := createTaskForComments(t, a, "T1")

	projText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{"name": "P1", "repo_path": t.TempDir()})
	require.False(t, isErr, "project create should not error: %s", projText)
	var proj map[string]interface{}
	parseData(t, projText, &proj)
	projectID := proj["ID"].(string)

	epicText, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{"name": "E1"})
	require.False(t, isErr, "epic create should not error: %s", epicText)
	var epic map[string]interface{}
	parseData(t, epicText, &epic)
	epicID := epic["ID"].(string)

	sprintText, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{"name": "S1"})
	require.False(t, isErr, "sprint create should not error: %s", sprintText)
	var sprint map[string]interface{}
	parseData(t, sprintText, &sprint)
	sprintID := sprint["ID"].(string)

	for _, tc := range []struct {
		entityType, entityID string
	}{
		{"task", taskID},
		{"project", projectID},
		{"epic", epicID},
		{"sprint", sprintID},
	} {
		t.Run(tc.entityType, func(t *testing.T) {
			addText, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
				"entity_type": tc.entityType,
				"entity_id":   tc.entityID,
				"author":      "alice",
				"content":     "hello " + tc.entityType,
			})
			require.False(t, isErr, "add should not error: %s", addText)

			listText, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{
				"entity_type": tc.entityType,
				"entity_id":   tc.entityID,
				"verbose":     "true",
			})
			require.False(t, isErr, "list should not error: %s", listText)
			var env commentListCursorEnvelope
			parseData(t, listText, &env)
			require.Len(t, env.Items, 1)
			assert.Equal(t, "hello "+tc.entityType, env.Items[0]["content"])
		})
	}

	t.Run("unsupported entity_type rejected", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
			"entity_type": "collection",
			"entity_id":   "whatever",
			"content":     "x",
		})
		require.True(t, isErr, "expected error: %s", text)
		code, _, field := parseError(t, text)
		assert.Equal(t, "arg_invalid", code)
		assert.Equal(t, "entity_type", field)
	})
}

func TestCommentList_LimitAndSortValidation(t *testing.T) {
	a := setupAdapter(t)
	taskID := createTaskForComments(t, a, "T1")

	t.Run("entity_type required", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{"entity_id": taskID})
		require.True(t, isErr, "expected error: %s", text)
		code, _, _ := parseError(t, text)
		assert.Equal(t, "arg_invalid", code)
	})

	t.Run("entity_id or entity_ids required", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{"entity_type": "task"})
		require.True(t, isErr, "expected error: %s", text)
		code, _, _ := parseError(t, text)
		assert.Equal(t, "arg_invalid", code)
	})

	t.Run("bad sort_by rejected", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{
			"entity_type": "task", "entity_id": taskID, "sort_by": "author",
		})
		require.True(t, isErr, "expected error: %s", text)
		code, _, field := parseError(t, text)
		assert.Equal(t, "arg_invalid", code)
		assert.Equal(t, "sort_by", field)
	})

	t.Run("bad sort_dir rejected", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{
			"entity_type": "task", "entity_id": taskID, "sort_dir": "sideways",
		})
		require.True(t, isErr, "expected error: %s", text)
		code, _, field := parseError(t, text)
		assert.Equal(t, "arg_invalid", code)
		assert.Equal(t, "sort_dir", field)
	})
}

func TestCommentList_CursorPagination_NoDuplicatesOrSkips(t *testing.T) {
	a := setupAdapter(t)
	taskID := createTaskForComments(t, a, "T1")

	const total = 9
	for i := 0; i < total; i++ {
		text, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
			"entity_type": "task",
			"entity_id":   taskID,
			"author":      "alice",
			"content":     fmt.Sprintf("comment %d", i),
		})
		require.False(t, isErr, "add should not error: %s", text)
	}

	seen := make(map[float64]bool, total)
	cursor := ""
	pages := 0
	for {
		pages++
		require.LessOrEqual(t, pages, total, "too many pages — likely an infinite loop from a broken cursor")

		args := map[string]interface{}{
			"entity_type": "task",
			"entity_id":   taskID,
			"limit":       "3",
			"verbose":     "true",
		}
		if cursor != "" {
			args["cursor"] = cursor
		}
		text, isErr := callTool(t, a, "torque_comment_list", args)
		require.False(t, isErr, "list should not error: %s", text)

		var env commentListCursorEnvelope
		parseData(t, text, &env)
		for _, item := range env.Items {
			id := item["id"].(float64)
			assert.False(t, seen[id], "duplicate id %v across pages", id)
			seen[id] = true
		}

		if !env.Meta.HasMore {
			require.Nil(t, env.Meta.NextCursor, "next_cursor must be null once exhausted")
			break
		}
		require.NotNil(t, env.Meta.NextCursor, "next_cursor must be set when has_more=true")
		cursor = *env.Meta.NextCursor
	}

	assert.Len(t, seen, total, "every comment must be visited exactly once")
}

func TestCommentList_AuthorAndDateFilters(t *testing.T) {
	a := setupAdapter(t)
	taskID := createTaskForComments(t, a, "T1")

	_, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID, "author": "alice", "content": "from alice",
	})
	require.False(t, isErr)
	_, isErr = callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID, "author": "bob", "content": "from bob",
	})
	require.False(t, isErr)

	text, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID, "author": "alice", "verbose": "true",
	})
	require.False(t, isErr, "list should not error: %s", text)
	var env commentListCursorEnvelope
	parseData(t, text, &env)
	require.Len(t, env.Items, 1)
	assert.Equal(t, "from alice", env.Items[0]["content"])

	t.Run("bad created_after rejected", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{
			"entity_type": "task", "entity_id": taskID, "created_after": "not-a-date",
		})
		require.True(t, isErr, "expected error: %s", text)
		code, _, field := parseError(t, text)
		assert.Equal(t, "arg_invalid", code)
		assert.Equal(t, "created_after", field)
	})

	t.Run("future created_after excludes everything", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{
			"entity_type": "task", "entity_id": taskID, "created_after": "2099-01-01T00:00:00Z",
		})
		require.False(t, isErr, "list should not error: %s", text)
		var env commentListCursorEnvelope
		parseData(t, text, &env)
		assert.Empty(t, env.Items)
	})
}

func TestCommentList_EntityIDsMultiFilter(t *testing.T) {
	a := setupAdapter(t)
	taskA := createTaskForComments(t, a, "A")
	taskB := createTaskForComments(t, a, "B")
	taskC := createTaskForComments(t, a, "C")

	for taskID, content := range map[string]string{taskA: "on A", taskB: "on B", taskC: "on C"} {
		_, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
			"entity_type": "task", "entity_id": taskID, "author": "alice", "content": content,
		})
		require.False(t, isErr)
	}

	idsJSON, err := json.Marshal([]string{taskA, taskB})
	require.NoError(t, err)

	text, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{
		"entity_type": "task", "entity_ids": string(idsJSON), "verbose": "true",
	})
	require.False(t, isErr, "list should not error: %s", text)
	var env commentListCursorEnvelope
	parseData(t, text, &env)
	require.Len(t, env.Items, 2)
	var contents []string
	for _, item := range env.Items {
		contents = append(contents, item["content"].(string))
	}
	assert.Contains(t, contents, "on A")
	assert.Contains(t, contents, "on B")
	assert.NotContains(t, contents, "on C")
}

func TestCommentSearch_PaginationAndSort(t *testing.T) {
	a := setupAdapter(t)
	taskID := createTaskForComments(t, a, "T1")

	for i := 0; i < 5; i++ {
		_, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
			"entity_type": "task", "entity_id": taskID, "author": "alice",
			"content": fmt.Sprintf("searchable %d", i),
		})
		require.False(t, isErr)
	}

	text, isErr := callTool(t, a, "torque_comment_search", map[string]interface{}{
		"query": "searchable", "limit": "2",
	})
	require.False(t, isErr, "search should not error: %s", text)
	var env commentListCursorEnvelope
	parseData(t, text, &env)
	require.Len(t, env.Items, 2)
	assert.True(t, env.Meta.HasMore)
	require.NotNil(t, env.Meta.NextCursor)

	t.Run("bad sort_by rejected", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_search", map[string]interface{}{
			"query": "searchable", "sort_by": "author",
		})
		require.True(t, isErr, "expected error: %s", text)
		code, _, _ := parseError(t, text)
		assert.Equal(t, "arg_invalid", code)
	})
}

func TestCommentUpdate_AuthorScoped(t *testing.T) {
	a := setupAdapter(t)
	taskID := createTaskForComments(t, a, "T1")

	addText, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID, "author": "alice", "content": "original",
	})
	require.False(t, isErr, "add should not error: %s", addText)
	var added map[string]interface{}
	parseData(t, addText, &added)
	id := fmt.Sprintf("%.0f", added["id"].(float64))

	t.Run("mismatched author rejected", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_update", map[string]interface{}{
			"id": id, "author": "bob", "content": "hijacked",
		})
		require.True(t, isErr, "expected error: %s", text)
		code, _, _ := parseError(t, text)
		assert.Equal(t, "permission", code)
	})

	t.Run("original author can edit", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_update", map[string]interface{}{
			"id": id, "author": "alice", "content": "edited",
		})
		require.False(t, isErr, "update should not error: %s", text)
		var updated map[string]interface{}
		parseData(t, text, &updated)
		assert.Equal(t, "edited", updated["content"])
	})

	t.Run("nonexistent id is not_found", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_update", map[string]interface{}{
			"id": "999999", "author": "alice", "content": "x",
		})
		require.True(t, isErr, "expected error: %s", text)
		code, _, _ := parseError(t, text)
		assert.Equal(t, "not_found", code)
	})
}

func TestCommentDelete_AuthorScoped(t *testing.T) {
	a := setupAdapter(t)
	taskID := createTaskForComments(t, a, "T1")

	addText, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID, "author": "alice", "content": "content",
	})
	require.False(t, isErr, "add should not error: %s", addText)
	var added map[string]interface{}
	parseData(t, addText, &added)
	id := fmt.Sprintf("%.0f", added["id"].(float64))

	t.Run("mismatched author rejected", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_delete", map[string]interface{}{
			"id": id, "author": "bob",
		})
		require.True(t, isErr, "expected error: %s", text)
		code, _, _ := parseError(t, text)
		assert.Equal(t, "permission", code)
	})

	t.Run("original author can delete", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_delete", map[string]interface{}{
			"id": id, "author": "alice",
		})
		require.False(t, isErr, "delete should not error: %s", text)

		listText, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{
			"entity_type": "task", "entity_id": taskID,
		})
		require.False(t, isErr)
		var env commentListCursorEnvelope
		parseData(t, listText, &env)
		assert.Empty(t, env.Items)
	})
}

func TestCommentDelete_ForceGuardMatrix(t *testing.T) {
	a := setupAdapter(t)
	taskID := createTaskForComments(t, a, "force delete")

	add := func(author string, includeAuthor bool) float64 {
		t.Helper()
		args := map[string]interface{}{
			"entity_type": "task",
			"entity_id":   taskID,
			"content":     "content",
		}
		if includeAuthor {
			args["author"] = author
		}
		text, isErr := callTool(t, a, "torque_comment_add", args)
		require.False(t, isErr, "add should not error: %s", text)
		var added map[string]interface{}
		parseData(t, text, &added)
		return added["id"].(float64)
	}
	exists := func(id float64) bool {
		t.Helper()
		listText, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{
			"entity_type": "task", "entity_id": taskID, "verbose": "true",
		})
		require.False(t, isErr, "list should not error: %s", listText)
		var env commentListCursorEnvelope
		parseData(t, listText, &env)
		for _, item := range env.Items {
			if item["id"] == id {
				return true
			}
		}
		return false
	}

	assertDelete := func(name string, id float64, author any, force any, wantErr bool, wantCode string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			args := map[string]interface{}{"id": id, "author": author}
			if force != nil {
				args["force"] = force
			}
			text, isErr := callTool(t, a, "torque_comment_delete", args)
			if wantErr {
				require.True(t, isErr, "expected error: %s", text)
				code, _, _ := parseError(t, text)
				assert.Equal(t, wantCode, code)
				assert.True(t, exists(id))
				return
			}
			require.False(t, isErr, "delete should not error: %s", text)
			assert.False(t, exists(id))
		})
	}

	assertDelete("matching author omitted force succeeds", add("alice", true), "alice", nil, false, "")
	assertDelete("mismatching author omitted force rejects", add("alice", true), "bob", nil, true, "permission")
	assertDelete("mismatching author force false rejects", add("alice", true), "bob", false, true, "permission")
	assertDelete("mismatching author force true succeeds", add("alice", true), "bob", true, false, "")
	assertDelete("explicit empty author omitted force succeeds", add("", false), "", nil, false, "")
	assertDelete("empty author force false succeeds", add("", false), "", false, false, "")
	assertDelete("empty author mismatched force true succeeds", add("", false), "bob", true, false, "")

	t.Run("force true preserves missing-comment error", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_delete", map[string]interface{}{
			"id": "999999", "author": "bob", "force": true,
		})
		require.True(t, isErr, "expected not_found: %s", text)
		code, _, _ := parseError(t, text)
		assert.Equal(t, "not_found", code)
	})
}

func TestCommentDelete_RejectsImpreciseIDAndAuthorTypesWithoutDeleting(t *testing.T) {
	a := setupAdapter(t)
	taskID := createTaskForComments(t, a, "parser guard")
	addText, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task", "entity_id": taskID, "author": "alice", "content": "content",
	})
	require.False(t, isErr, "add should not error: %s", addText)
	var added map[string]interface{}
	parseData(t, addText, &added)
	id := added["id"].(float64)

	cases := []struct {
		name string
		args map[string]interface{}
	}{
		{
			name: "fractional id",
			args: map[string]interface{}{"id": id + 0.5, "author": "alice", "force": true},
		},
		{
			name: "numeric author",
			args: map[string]interface{}{"id": id, "author": 7, "force": true},
		},
		{
			name: "null author",
			args: map[string]interface{}{"id": id, "author": nil, "force": true},
		},
		{
			name: "missing author under force",
			args: map[string]interface{}{"id": id, "force": true},
		},
		{
			name: "null force",
			args: map[string]interface{}{"id": id, "author": "bob", "force": nil},
		},
		{
			name: "number force",
			args: map[string]interface{}{"id": id, "author": "bob", "force": 1},
		},
		{
			name: "string force rejected by boolean schema contract",
			args: map[string]interface{}{"id": id, "author": "bob", "force": "true"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := callTool(t, a, "torque_comment_delete", tc.args)
			require.True(t, isErr, "expected parser rejection: %s", text)
			code, _, _ := parseError(t, text)
			assert.Equal(t, "arg_invalid", code)

			listText, listErr := callTool(t, a, "torque_comment_list", map[string]interface{}{
				"entity_type": "task", "entity_id": taskID, "verbose": "true",
			})
			require.False(t, listErr, "list should not error: %s", listText)
			var env commentListCursorEnvelope
			parseData(t, listText, &env)
			require.Len(t, env.Items, 1)
			assert.Equal(t, "content", env.Items[0]["content"])
		})
	}
}

func TestCommentBulkAdd(t *testing.T) {
	a := setupAdapter(t)
	taskA := createTaskForComments(t, a, "A")
	taskB := createTaskForComments(t, a, "B")

	targets, err := json.Marshal([]map[string]string{
		{"entity_type": "task", "entity_id": taskA},
		{"entity_type": "task", "entity_id": taskB},
		{"entity_type": "collection", "entity_id": "bad"},
	})
	require.NoError(t, err)

	text, isErr := callTool(t, a, "torque_comment_bulk_add", map[string]interface{}{
		"targets": string(targets),
		"author":  "alice",
		"content": "broadcast note",
	})
	require.False(t, isErr, "bulk_add should not error at the call level: %s", text)

	var result struct {
		Succeeded []map[string]interface{} `json:"succeeded"`
		Failed    []struct {
			Target map[string]interface{} `json:"target"`
			Error  struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"failed"`
	}
	parseData(t, text, &result)
	require.Len(t, result.Succeeded, 2)
	require.Len(t, result.Failed, 1)
	assert.Equal(t, "arg_invalid", result.Failed[0].Error.Code)
	assert.Equal(t, "collection", result.Failed[0].Target["entity_type"])

	listText, isErr := callTool(t, a, "torque_comment_list", map[string]interface{}{
		"entity_type": "task", "entity_id": taskA,
	})
	require.False(t, isErr)
	var env commentListCursorEnvelope
	parseData(t, listText, &env)
	require.Len(t, env.Items, 1)
}

func TestCommentBulkAdd_RequiresNonEmptyTargets(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_comment_bulk_add", map[string]interface{}{
		"targets": "[]",
		"content": "x",
	})
	require.True(t, isErr, "expected error: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
}
