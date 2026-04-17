package mcpadapter_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSubtodoRoundTrip_AddListDone exercises the three MCP tools end-to-end
// against a freshly created task: add two items, list them, mark one done,
// list again and confirm evidence/state.
func TestSubtodoRoundTrip_AddListDone(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "gated task",
		"description": "no checkboxes so no auto-extract",
	})
	require.False(t, isErr, text)
	var created map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &created))
	taskID, _ := created["ID"].(string)
	require.NotEmpty(t, taskID)

	_, isErr = callTool(t, a, "clockwork_task_subtodo_add", map[string]interface{}{
		"task_id":  taskID,
		"id":       "item-1",
		"text":     "write test",
		"required": true,
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "clockwork_task_subtodo_add", map[string]interface{}{
		"task_id":  taskID,
		"id":       "item-2",
		"text":     "ship it",
		"required": false,
	})
	require.False(t, isErr)

	listText, isErr := callTool(t, a, "clockwork_task_subtodo_list", map[string]interface{}{
		"task_id": taskID,
	})
	require.False(t, isErr, listText)
	var items []map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(listText), &items))
	require.Len(t, items, 2)
	assert.Equal(t, "item-1", items[0]["id"])
	assert.Equal(t, true, items[0]["required"])

	_, isErr = callTool(t, a, "clockwork_task_subtodo_done", map[string]interface{}{
		"task_id":  taskID,
		"id":       "item-1",
		"evidence": "commit-abc123",
	})
	require.False(t, isErr)

	listText, _ = callTool(t, a, "clockwork_task_subtodo_list", map[string]interface{}{
		"task_id": taskID,
	})
	items = nil
	require.NoError(t, json.Unmarshal([]byte(listText), &items))
	require.Len(t, items, 2)
	assert.Equal(t, true, items[0]["done"])
	assert.Equal(t, "commit-abc123", items[0]["evidence"])
	assert.Equal(t, false, items[1]["done"])
}

func TestSubtodoAdd_RejectsDuplicateID(t *testing.T) {
	a := setupAdapter(t)
	text, _ := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title": "dup",
	})
	var created map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &created))
	taskID, _ := created["ID"].(string)

	_, isErr := callTool(t, a, "clockwork_task_subtodo_add", map[string]interface{}{
		"task_id": taskID, "id": "x", "text": "one",
	})
	require.False(t, isErr)
	dup, isErr := callTool(t, a, "clockwork_task_subtodo_add", map[string]interface{}{
		"task_id": taskID, "id": "x", "text": "two",
	})
	require.True(t, isErr, "duplicate id must error: %s", dup)
}
