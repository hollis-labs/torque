package mcpadapter_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// epicRecordPriority extracts the numeric value out of an EpicRecord's
// Priority field as it appears in a raw (non-brief) MCP JSON response —
// sql.NullInt64 marshals as {"Int64":N,"Valid":bool} rather than a bare
// number, unlike briefEpic's plain int64 Priority.
func epicRecordPriority(t *testing.T, rec map[string]interface{}) float64 {
	t.Helper()
	p, ok := rec["Priority"].(map[string]interface{})
	require.True(t, ok, "Priority field must be the sql.NullInt64 JSON shape: %#v", rec["Priority"])
	require.Equal(t, true, p["Valid"], "Priority must be Valid: %#v", p)
	return p["Int64"].(float64)
}

// epicListCursorEnvelope mirrors torque_epic_list's PRIM-001/PRIM-002
// {items, meta} response shape for test parsing — Epic's analog to
// taskListCursorEnvelope (task_tools_test.go).
type epicListCursorEnvelope struct {
	Items []map[string]interface{} `json:"items"`
	Meta  struct {
		Truncated  bool    `json:"truncated"`
		Returned   int     `json:"returned"`
		Limit      int     `json:"limit"`
		HasMore    bool    `json:"has_more"`
		NextCursor *string `json:"next_cursor"`
	} `json:"meta"`
}

// TestFullStack_EpicCreate_Priority covers ENT-EPIC's "priority settable
// via create" acceptance criterion end to end through the MCP surface.
func TestFullStack_EpicCreate_Priority(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	text, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{
		"name":     "Auth Overhaul",
		"priority": "1",
	})
	require.False(t, isErr, "create should not error: %s", text)

	var rec map[string]interface{}
	parseData(t, text, &rec)
	assert.Equal(t, float64(1), epicRecordPriority(t, rec))
}

// TestFullStack_EpicUpdate_Priority covers ENT-EPIC's "priority changeable
// via update" acceptance criterion, including that it round-trips through
// get.
func TestFullStack_EpicUpdate_Priority(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	text, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{"name": "Epic"})
	require.False(t, isErr)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_epic_update", map[string]interface{}{
		"id":       id,
		"priority": "3",
	})
	require.False(t, isErr, "update should not error: %s", text)

	text, isErr = callTool(t, a, "torque_epic_get", map[string]interface{}{"id": id})
	require.False(t, isErr)
	var got map[string]interface{}
	parseData(t, text, &got)
	assert.Equal(t, float64(3), epicRecordPriority(t, got))
}

// TestFullStack_EpicList_Search is ENT-EPIC's "merged search" acceptance
// criterion end to end: no separate _search tool, search lives on list.
func TestFullStack_EpicList_Search(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	_, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{"name": "Auth Overhaul", "description": "Replace entire auth stack"})
	require.False(t, isErr)
	_, isErr = callTool(t, a, "torque_epic_create", map[string]interface{}{"name": "Billing Rewrite", "description": "New invoicing pipeline"})
	require.False(t, isErr)

	text, isErr := callTool(t, a, "torque_epic_list", map[string]interface{}{"search": "auth"})
	require.False(t, isErr, "list should not error: %s", text)

	var env epicListCursorEnvelope
	parseData(t, text, &env)
	require.Len(t, env.Items, 1)
	assert.Equal(t, "Auth Overhaul", env.Items[0]["name"])
}

// TestFullStack_EpicList_CursorPagination_NoDuplicatesOrSkips is PRIM-001's
// acceptance criterion applied to Epic: paging through with a small limit
// must visit every created epic exactly once.
func TestFullStack_EpicList_CursorPagination_NoDuplicatesOrSkips(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	const total = 9
	created := make(map[string]bool, total)
	for i := 0; i < total; i++ {
		text, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{
			"name": fmt.Sprintf("epic %d", i),
		})
		require.False(t, isErr, "create should not error: %s", text)
		var rec map[string]interface{}
		parseData(t, text, &rec)
		created[rec["ID"].(string)] = true
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		require.LessOrEqual(t, pages, total, "too many pages — likely an infinite loop from a broken cursor")

		args := map[string]interface{}{"limit": "4"}
		if cursor != "" {
			args["cursor"] = cursor
		}
		text, isErr := callTool(t, a, "torque_epic_list", args)
		require.False(t, isErr, "list should not error: %s", text)

		var env epicListCursorEnvelope
		parseData(t, text, &env)

		for _, item := range env.Items {
			id := item["id"].(string)
			require.False(t, seen[id], "duplicate id %s seen across pages", id)
			seen[id] = true
		}

		if !env.Meta.HasMore {
			require.Nil(t, env.Meta.NextCursor, "next_cursor must be null once exhausted")
			break
		}
		require.NotNil(t, env.Meta.NextCursor, "next_cursor must be set when has_more=true")
		cursor = *env.Meta.NextCursor
	}

	require.Len(t, seen, total, "expected every created epic to appear exactly once across pages")
	for id := range created {
		require.True(t, seen[id], "epic %s missing from paged results", id)
	}
}

// TestFullStack_EpicList_InvalidSortBy is PRIM-002's acceptance criterion:
// an unrecognized sort_by must return a clean error.code=arg_invalid.
func TestFullStack_EpicList_InvalidSortBy(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	text, isErr := callTool(t, a, "torque_epic_list", map[string]interface{}{
		"sort_by": "not_a_real_field",
	})
	require.True(t, isErr, "list should error on invalid sort_by: %s", text)

	code, _, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "sort_by", field)
}

// TestFullStack_EpicList_CursorSortMismatchRejected verifies DEC-001's
// cursor/sort binding for Epic.
func TestFullStack_EpicList_CursorSortMismatchRejected(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	for i := 0; i < 2; i++ {
		_, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{"name": fmt.Sprintf("epic %d", i)})
		require.False(t, isErr)
	}

	text, isErr := callTool(t, a, "torque_epic_list", map[string]interface{}{
		"limit":    "1",
		"sort_by":  "name",
		"sort_dir": "asc",
	})
	require.False(t, isErr, "list should not error: %s", text)
	var env epicListCursorEnvelope
	parseData(t, text, &env)
	require.True(t, env.Meta.HasMore)
	require.NotNil(t, env.Meta.NextCursor)

	text, isErr = callTool(t, a, "torque_epic_list", map[string]interface{}{
		"limit":    "1",
		"sort_by":  "name",
		"sort_dir": "desc", // different sort_dir than the cursor was issued under
		"cursor":   *env.Meta.NextCursor,
	})
	require.True(t, isErr, "list should reject cursor issued under a different sort_dir: %s", text)
	code, _, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "cursor", field)
}

// TestFullStack_EpicArchiveUnarchive covers PRIM-004 wired onto the MCP
// surface: archive excludes from the default list, unarchive restores it,
// and neither touches status.
func TestFullStack_EpicArchiveUnarchive(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	text, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{"name": "Epic"})
	require.False(t, isErr)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_epic_archive", map[string]interface{}{"id": id})
	require.False(t, isErr, "archive should not error: %s", text)
	var archiveResp map[string]interface{}
	parseData(t, text, &archiveResp)
	assert.Equal(t, true, archiveResp["archived"])

	// Default list excludes archived rows.
	text, isErr = callTool(t, a, "torque_epic_list", map[string]interface{}{})
	require.False(t, isErr)
	var env epicListCursorEnvelope
	parseData(t, text, &env)
	assert.Len(t, env.Items, 0)

	// include_archived surfaces it.
	text, isErr = callTool(t, a, "torque_epic_list", map[string]interface{}{"include_archived": "true"})
	require.False(t, isErr)
	parseData(t, text, &env)
	require.Len(t, env.Items, 1)
	assert.Equal(t, id, env.Items[0]["id"])

	// Status is untouched by archiving.
	text, isErr = callTool(t, a, "torque_epic_get", map[string]interface{}{"id": id})
	require.False(t, isErr)
	var got map[string]interface{}
	parseData(t, text, &got)
	assert.Equal(t, "active", got["Status"])

	text, isErr = callTool(t, a, "torque_epic_unarchive", map[string]interface{}{"id": id})
	require.False(t, isErr, "unarchive should not error: %s", text)
	var unarchiveResp map[string]interface{}
	parseData(t, text, &unarchiveResp)
	assert.Equal(t, true, unarchiveResp["unarchived"])

	text, isErr = callTool(t, a, "torque_epic_list", map[string]interface{}{})
	require.False(t, isErr)
	parseData(t, text, &env)
	require.Len(t, env.Items, 1)
}

// TestFullStack_EpicBulkUpdate_PartialSuccess is PRIM-003's acceptance
// criterion applied to Epic: a bad id in the batch fails without blocking
// the rest, and the response is ok=true with failed[] populated.
func TestFullStack_EpicBulkUpdate_PartialSuccess(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	text, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{"name": "Epic A"})
	require.False(t, isErr)
	var e1 map[string]interface{}
	parseData(t, text, &e1)
	id1 := e1["ID"].(string)

	text, isErr = callTool(t, a, "torque_epic_create", map[string]interface{}{"name": "Epic B"})
	require.False(t, isErr)
	var e2 map[string]interface{}
	parseData(t, text, &e2)
	id2 := e2["ID"].(string)

	text, isErr = callTool(t, a, "torque_epic_bulk_update", map[string]interface{}{
		"ids":      fmt.Sprintf(`["%s","%s","EP-nonexistent"]`, id1, id2),
		"status":   "inactive",
		"priority": "1",
	})
	require.False(t, isErr, "bulk_update call itself must not be an error: %s", text)

	var result struct {
		Succeeded []string `json:"succeeded"`
		Failed    []struct {
			ID    string `json:"id"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"failed"`
	}
	parseData(t, text, &result)
	assert.ElementsMatch(t, []string{id1, id2}, result.Succeeded)
	require.Len(t, result.Failed, 1)
	assert.Equal(t, "EP-nonexistent", result.Failed[0].ID)
	assert.Equal(t, "not_found", result.Failed[0].Error.Code)

	text, isErr = callTool(t, a, "torque_epic_get", map[string]interface{}{"id": id1})
	require.False(t, isErr)
	var got map[string]interface{}
	parseData(t, text, &got)
	assert.Equal(t, "inactive", got["Status"])
	assert.Equal(t, float64(1), epicRecordPriority(t, got))
}

// TestFullStack_EpicBulkUpdate_ArgErrors covers reqIDs' call-level
// validation (empty/malformed ids) reused from Task's bulk pattern.
func TestFullStack_EpicBulkUpdate_ArgErrors(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	text, isErr := callTool(t, a, "torque_epic_bulk_update", map[string]interface{}{
		"ids":    "[]",
		"status": "inactive",
	})
	require.True(t, isErr, "empty ids must be a call-level error: %s", text)
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "ids", field)

	text, isErr = callTool(t, a, "torque_epic_bulk_update", map[string]interface{}{
		"ids": `["EP-1"]`,
	})
	require.True(t, isErr, "no updatable field must be a call-level error: %s", text)
	code, _, _ = parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
}
