package mcpadapter_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubtodoRoundTrip_AddListDone exercises the three MCP tools end-to-end
// against a freshly created task: add two items, list them, mark one done,
// list again and confirm evidence/state.
func TestSubtodoRoundTrip_AddListDone(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "gated task",
		"description": "no checkboxes so no auto-extract",
	})
	require.False(t, isErr, text)
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)
	require.NotEmpty(t, taskID)

	_, isErr = callTool(t, a, "torque_task_subtodo_add", map[string]interface{}{
		"task_id":  taskID,
		"id":       "item-1",
		"text":     "write test",
		"required": true,
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_task_subtodo_add", map[string]interface{}{
		"task_id":  taskID,
		"id":       "item-2",
		"text":     "ship it",
		"required": false,
	})
	require.False(t, isErr)

	listText, isErr := callTool(t, a, "torque_task_subtodo_list", map[string]interface{}{
		"task_id": taskID,
	})
	require.False(t, isErr, listText)
	var env struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, listText, &env)
	require.Len(t, env.Items, 2)
	assert.Equal(t, "item-1", env.Items[0]["id"])
	assert.Equal(t, true, env.Items[0]["required"])

	_, isErr = callTool(t, a, "torque_task_subtodo_done", map[string]interface{}{
		"task_id":  taskID,
		"id":       "item-1",
		"evidence": "commit-abc123",
	})
	require.False(t, isErr)

	// Brief shape drops evidence (no-op field when size is the priority).
	// Use verbose=true to get the full Subtodo including evidence string.
	listText, _ = callTool(t, a, "torque_task_subtodo_list", map[string]interface{}{
		"task_id": taskID,
		"verbose": "true",
	})
	env.Items = nil
	parseData(t, listText, &env)
	require.Len(t, env.Items, 2)
	assert.Equal(t, true, env.Items[0]["done"])
	assert.Equal(t, "commit-abc123", env.Items[0]["evidence"])
	assert.Equal(t, false, env.Items[1]["done"])
}

// TestSubtodoAdd_OmittedIDAutoGenerates covers FIX-002 at the MCP layer:
// torque_task_subtodo_add with id omitted succeeds and returns a
// server-generated id.
func TestSubtodoAdd_OmittedIDAutoGenerates(t *testing.T) {
	a := setupAdapter(t)
	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "auto id",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)

	addText, isErr := callTool(t, a, "torque_task_subtodo_add", map[string]interface{}{
		"task_id": taskID, "text": "no id supplied",
	})
	require.False(t, isErr, addText)

	var items []map[string]interface{}
	parseData(t, addText, &items)
	require.Len(t, items, 1)
	genID, _ := items[0]["id"].(string)
	assert.NotEmpty(t, genID)
	assert.True(t, strings.HasPrefix(genID, "sub_"), "generated id %q should carry the sub_ prefix", genID)

	// The generated id can be used to mark the item done like any other id.
	_, isErr = callTool(t, a, "torque_task_subtodo_done", map[string]interface{}{
		"task_id": taskID, "id": genID,
	})
	require.False(t, isErr)
}

func TestSubtodoAdd_RejectsDuplicateID(t *testing.T) {
	a := setupAdapter(t)
	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "dup",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	taskID, _ := created["ID"].(string)

	_, isErr := callTool(t, a, "torque_task_subtodo_add", map[string]interface{}{
		"task_id": taskID, "id": "x", "text": "one",
	})
	require.False(t, isErr)
	dup, isErr := callTool(t, a, "torque_task_subtodo_add", map[string]interface{}{
		"task_id": taskID, "id": "x", "text": "two",
	})
	require.True(t, isErr, "duplicate id must error: %s", dup)
}
