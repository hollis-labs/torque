package mcpadapter_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// bulkResponse mirrors the {succeeded, failed} envelope every bulk_* tool
// returns (PRIM-003 / ADR-0004 §3).
type bulkResponse struct {
	Succeeded []string `json:"succeeded"`
	Failed    []struct {
		ID    string `json:"id"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Field   string `json:"field"`
		} `json:"error"`
	} `json:"failed"`
}

func TestFullStack_TaskBulkUpdate_PartialSuccess(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "bulk-1", "description": "x",
	})
	var t1 map[string]interface{}
	parseData(t, text, &t1)
	id1 := t1["ID"].(string)

	text, _ = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "bulk-2", "description": "x",
	})
	var t2 map[string]interface{}
	parseData(t, text, &t2)
	id2 := t2["ID"].(string)

	text, isErr := callTool(t, a, "torque_task_bulk_update", map[string]interface{}{
		"ids":      `["` + id1 + `","CW-does-not-exist","` + id2 + `"]`,
		"priority": "5",
	})
	require.False(t, isErr, "bulk_update call itself must not be a call-level error: %s", text)

	var resp bulkResponse
	parseData(t, text, &resp)
	require.ElementsMatch(t, []string{id1, id2}, resp.Succeeded)
	require.Len(t, resp.Failed, 1)
	require.Equal(t, "CW-does-not-exist", resp.Failed[0].ID)
	require.Equal(t, "not_found", resp.Failed[0].Error.Code)

	// Confirm the successful ids actually changed.
	text, _ = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id1})
	var got1 map[string]interface{}
	parseData(t, text, &got1)
	require.Equal(t, float64(5), got1["Priority"])

	text, _ = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id2})
	var got2 map[string]interface{}
	parseData(t, text, &got2)
	require.Equal(t, float64(5), got2["Priority"])
}

// TestFullStack_TaskBulkUpdate_PresenceSemantics mirrors FIX-001's
// presence-in-payload rule (buildTaskUpdateInput, shared by single and bulk
// update): an omitted key leaves the field untouched; an explicit empty
// string clears it. This is exercised through bulk_update to prove the
// shared builder behaves identically to torque_task_update.
func TestFullStack_TaskBulkUpdate_PresenceSemantics(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "presence", "description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{
		"id": id, "blocked_reason": "waiting on X",
	})
	require.False(t, isErr, "seed update should not error: %s", text)

	// bulk_update omits blocked_reason entirely — must stay untouched.
	text, isErr = callTool(t, a, "torque_task_bulk_update", map[string]interface{}{
		"ids":      `["` + id + `"]`,
		"priority": "3",
	})
	require.False(t, isErr, "bulk_update should not error: %s", text)
	var resp1 bulkResponse
	parseData(t, text, &resp1)
	require.Equal(t, []string{id}, resp1.Succeeded)
	require.Empty(t, resp1.Failed)

	text, _ = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id})
	var afterOmit map[string]interface{}
	parseData(t, text, &afterOmit)
	require.Equal(t, "waiting on X", afterOmit["BlockedReason"], "omitted key must leave the field untouched")

	// bulk_update explicitly clears blocked_reason with "".
	text, isErr = callTool(t, a, "torque_task_bulk_update", map[string]interface{}{
		"ids":            `["` + id + `"]`,
		"blocked_reason": "",
	})
	require.False(t, isErr, "bulk_update clear should not error: %s", text)

	text, _ = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id})
	var afterClear map[string]interface{}
	parseData(t, text, &afterClear)
	require.Equal(t, "", afterClear["BlockedReason"], "explicit empty string must clear the field")
}

func TestFullStack_TaskBulkUpdate_ArgErrors(t *testing.T) {
	a := setupAdapter(t)

	// Malformed ids JSON is a call-level arg_invalid, not a per-item failure.
	text, isErr := callTool(t, a, "torque_task_bulk_update", map[string]interface{}{
		"ids": `not-json`, "priority": "1",
	})
	require.True(t, isErr, "malformed ids should be a call-level error: %s", text)
	code, _, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "ids", field)

	// Empty ids array is also a call-level error, not a degenerate no-op success.
	text, isErr = callTool(t, a, "torque_task_bulk_update", map[string]interface{}{
		"ids": `[]`, "priority": "1",
	})
	require.True(t, isErr, "empty ids should be a call-level error: %s", text)
	code, _, field = parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "ids", field)

	// Malformed JSON blob field is still validated up front (shared with
	// single-item torque_task_update).
	text, _ = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "x", "description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_task_bulk_update", map[string]interface{}{
		"ids": `["` + id + `"]`, "tools": `not-json`,
	})
	require.True(t, isErr, "malformed tools JSON should be a call-level error: %s", text)
	code, _, field = parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "tools", field)
}

func TestFullStack_TaskBulkDelete_PartialSuccess(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "del-1", "description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr := callTool(t, a, "torque_task_bulk_delete", map[string]interface{}{
		"ids": `["` + id + `","CW-does-not-exist"]`,
	})
	require.False(t, isErr, "bulk_delete call itself must not be a call-level error: %s", text)

	var resp bulkResponse
	parseData(t, text, &resp)
	require.Equal(t, []string{id}, resp.Succeeded)
	require.Len(t, resp.Failed, 1)
	require.Equal(t, "CW-does-not-exist", resp.Failed[0].ID)
	require.Equal(t, "not_found", resp.Failed[0].Error.Code)

	// The deleted task is really gone now.
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id})
	require.True(t, isErr)
	code, _, _ := parseError(t, text)
	require.Equal(t, "not_found", code)
}

func TestFullStack_TaskBulkTag_AddRemove(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "tag-1", "description": "x", "tags": `["wip","keep"]`,
	})
	var t1 map[string]interface{}
	parseData(t, text, &t1)
	id1 := t1["ID"].(string)

	text, _ = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "tag-2", "description": "x",
	})
	var t2 map[string]interface{}
	parseData(t, text, &t2)
	id2 := t2["ID"].(string)

	text, isErr := callTool(t, a, "torque_task_bulk_tag", map[string]interface{}{
		"ids":    `["` + id1 + `","` + id2 + `"]`,
		"add":    `["p0"]`,
		"remove": `["wip"]`,
	})
	require.False(t, isErr, "bulk_tag call itself must not be a call-level error: %s", text)

	var resp bulkResponse
	parseData(t, text, &resp)
	require.ElementsMatch(t, []string{id1, id2}, resp.Succeeded)
	require.Empty(t, resp.Failed)

	text, _ = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id1})
	var got1 map[string]interface{}
	parseData(t, text, &got1)
	require.ElementsMatch(t, []string{"keep", "p0"}, tagSlugs(t, got1))

	text, _ = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id2})
	var got2 map[string]interface{}
	parseData(t, text, &got2)
	require.ElementsMatch(t, []string{"p0"}, tagSlugs(t, got2))
}

func TestFullStack_TaskBulkTag_NotFound(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "tag-real", "description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr := callTool(t, a, "torque_task_bulk_tag", map[string]interface{}{
		"ids": `["` + id + `","CW-does-not-exist"]`,
		"add": `["p0"]`,
	})
	require.False(t, isErr, "bulk_tag call itself must not be a call-level error: %s", text)

	var resp bulkResponse
	parseData(t, text, &resp)
	require.Equal(t, []string{id}, resp.Succeeded)
	require.Len(t, resp.Failed, 1)
	require.Equal(t, "CW-does-not-exist", resp.Failed[0].ID)
	require.Equal(t, "not_found", resp.Failed[0].Error.Code)
}

func TestFullStack_TaskBulkTag_ArgErrors(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "x", "description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	// Neither add nor remove is a call-level arg error, not a no-op success.
	text, isErr := callTool(t, a, "torque_task_bulk_tag", map[string]interface{}{
		"ids": `["` + id + `"]`,
	})
	require.True(t, isErr, "missing add/remove should be a call-level error: %s", text)
	code, _, _ := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
}

// TestFullStack_TaskBulkTransition_PartialSuccess verifies torque_task_
// bulk_transition (ENT-TASK) now returns the same {succeeded, failed}
// PRIM-003 bulkResult envelope every other bulk_* verb uses, instead of its
// old bespoke {success: int, failed: int, errors?: string} shape.
func TestFullStack_TaskBulkTransition_PartialSuccess(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "bulk-transition-1", "description": "x",
	})
	var t1 map[string]interface{}
	parseData(t, text, &t1)
	id1 := t1["ID"].(string)

	text, _ = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "bulk-transition-2", "description": "x",
	})
	var t2 map[string]interface{}
	parseData(t, text, &t2)
	id2 := t2["ID"].(string)

	// todo -> done is FSM-invalid (must go through doing/review first), so
	// id2 fails while id1... also fails the same way. Force id1 into
	// "review" first so it succeeds and id2 (still "todo") fails, giving a
	// genuine partial-success mix.
	text, isErr := callTool(t, a, "torque_task_transition", map[string]interface{}{"id": id1, "status": "doing"})
	require.False(t, isErr, "seed transition should succeed: %s", text)
	text, isErr = callTool(t, a, "torque_task_transition", map[string]interface{}{"id": id1, "status": "review"})
	require.False(t, isErr, "seed transition should succeed: %s", text)

	text, isErr = callTool(t, a, "torque_task_bulk_transition", map[string]interface{}{
		"ids":    `["` + id1 + `","CW-does-not-exist","` + id2 + `"]`,
		"status": "done",
	})
	require.False(t, isErr, "bulk_transition call itself must not be a call-level error: %s", text)

	var resp bulkResponse
	parseData(t, text, &resp)
	require.Equal(t, []string{id1}, resp.Succeeded)
	require.Len(t, resp.Failed, 2)
	failedIDs := []string{resp.Failed[0].ID, resp.Failed[1].ID}
	require.ElementsMatch(t, []string{"CW-does-not-exist", id2}, failedIDs)

	text, _ = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id1})
	var got1 map[string]interface{}
	parseData(t, text, &got1)
	require.Equal(t, "done", got1["Status"])
}

// tagSlugs extracts the Tags[].Slug list from a taskWithTags-shaped
// response (torque_task_get always includes linked tags).
func tagSlugs(t *testing.T, task map[string]interface{}) []string {
	t.Helper()
	raw, ok := task["Tags"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		m := r.(map[string]interface{})
		out = append(out, m["Slug"].(string))
	}
	return out
}
