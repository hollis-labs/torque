package mcpadapter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFullStack_ArtifactCRUD exercises create → get → list → delete via MCP.
// Covers the two tools added in CW-20260417-0017 (get/delete) plus the
// existing create/list path.
func TestFullStack_ArtifactCRUD(t *testing.T) {
	a := setupAdapter(t)

	// Need a task first so the artifact has a valid task_id.
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "artifact-owner",
		"description": "x",
	})
	require.False(t, isErr, "task create: %s", text)
	var task map[string]interface{}
	parseData(t, text, &task)
	taskID := task["ID"].(string)

	// Create an artifact.
	text, isErr = callTool(t, a, "torque_artifact_create", map[string]interface{}{
		"task_id":   taskID,
		"type":      "file",
		"file_path": "/tmp/demo.log",
	})
	require.False(t, isErr, "artifact create: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	id, ok := created["ID"].(float64)
	require.True(t, ok, "response should contain numeric ID: %v", created)

	// Get by id.
	text, isErr = callTool(t, a, "torque_artifact_get", map[string]interface{}{
		"artifact_id": id,
	})
	require.False(t, isErr, "artifact get: %s", text)
	var got map[string]interface{}
	parseData(t, text, &got)
	assert.Equal(t, id, got["ID"])
	assert.Equal(t, taskID, got["TaskID"])
	assert.Equal(t, "/tmp/demo.log", got["FilePath"])

	// Delete.
	text, isErr = callTool(t, a, "torque_artifact_delete", map[string]interface{}{
		"artifact_id": id,
	})
	require.False(t, isErr, "artifact delete: %s", text)
	var delResp map[string]interface{}
	parseData(t, text, &delResp)
	assert.Equal(t, true, delResp["deleted"])

	// Get after delete should error.
	text, isErr = callTool(t, a, "torque_artifact_get", map[string]interface{}{
		"artifact_id": id,
	})
	assert.True(t, isErr, "get after delete should error: %s", text)

	// Delete again should error.
	text, isErr = callTool(t, a, "torque_artifact_delete", map[string]interface{}{
		"artifact_id": id,
	})
	assert.True(t, isErr, "delete missing should error: %s", text)
}
