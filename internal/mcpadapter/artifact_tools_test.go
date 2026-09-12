package mcpadapter_test

import (
	"database/sql"
	"testing"

	"github.com/hollis-labs/torque/internal/mcpadapter"
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

func TestFullStack_ArtifactCreateUpdateMetadataAndClears(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "artifact-update-owner",
		"description": "x",
	})
	require.False(t, isErr, "task create: %s", text)
	var task map[string]interface{}
	parseData(t, text, &task)
	taskID := task["ID"].(string)

	text, isErr = callTool(t, a, "torque_artifact_create", map[string]interface{}{
		"task_id":  taskID,
		"type":     "inline",
		"content":  "old",
		"metadata": map[string]interface{}{"score": 0.5},
	})
	require.False(t, isErr, "artifact create: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"]
	meta := created["Metadata"].(map[string]interface{})
	assert.Contains(t, meta["String"], `"score":0.5`)
	text, isErr = callTool(t, a, "torque_artifact_get", map[string]interface{}{"artifact_id": id})
	require.False(t, isErr, "artifact get before update: %s", text)
	var before map[string]interface{}
	parseData(t, text, &before)
	createdAt := before["CreatedAt"]

	text, isErr = callTool(t, a, "torque_artifact_update", map[string]interface{}{
		"artifact_id": id,
		"content":     "",
		"url":         "https://example.test/fixed",
		"metadata":    `{"nested":{"big":9007199254740993123}}`,
	})
	require.False(t, isErr, "artifact update: %s", text)
	var updated map[string]interface{}
	parseData(t, text, &updated)
	assert.Equal(t, created["ID"], updated["ID"])
	assert.Equal(t, taskID, updated["TaskID"])
	assert.Equal(t, createdAt, updated["CreatedAt"])
	assert.Equal(t, "", updated["Content"])
	assert.Equal(t, "https://example.test/fixed", updated["URL"])
	meta = updated["Metadata"].(map[string]interface{})
	assert.Contains(t, meta["String"], `9007199254740993123`)

	text, isErr = callTool(t, a, "torque_artifact_update", map[string]interface{}{
		"artifact_id": id,
		"metadata":    map[string]interface{}{},
	})
	require.False(t, isErr, "metadata empty object: %s", text)
	var emptyMeta map[string]interface{}
	parseData(t, text, &emptyMeta)
	assert.Equal(t, true, emptyMeta["Metadata"].(map[string]interface{})["Valid"])
	assert.Equal(t, "{}", emptyMeta["Metadata"].(map[string]interface{})["String"])

	text, isErr = callTool(t, a, "torque_artifact_update", map[string]interface{}{
		"artifact_id": id,
		"metadata":    nil,
	})
	require.False(t, isErr, "metadata clear: %s", text)
	var cleared map[string]interface{}
	parseData(t, text, &cleared)
	assert.Equal(t, false, cleared["Metadata"].(map[string]interface{})["Valid"])
}

func TestFullStack_ArtifactUpdateRejectsUnsafeNativeMetadataWithoutMutation(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "unsafe-native-metadata",
		"description": "x",
	})
	require.False(t, isErr, "task create: %s", text)
	var task map[string]interface{}
	parseData(t, text, &task)

	text, isErr = callTool(t, a, "torque_artifact_create", map[string]interface{}{
		"task_id":  task["ID"],
		"type":     "inline",
		"content":  "safe",
		"metadata": `{"nested":{"big":9007199254740993123}}`,
	})
	require.False(t, isErr, "artifact create: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"]

	text, isErr = callTool(t, a, "torque_artifact_update", map[string]interface{}{
		"artifact_id": id,
		"content":     "mutated",
		"metadata":    map[string]interface{}{"nested": map[string]interface{}{"big": float64(9007199254740992)}},
	})
	require.True(t, isErr, "unsafe native metadata must reject: %s", text)

	text, isErr = callTool(t, a, "torque_artifact_get", map[string]interface{}{"artifact_id": id})
	require.False(t, isErr, "artifact get: %s", text)
	var got map[string]interface{}
	parseData(t, text, &got)
	assert.Equal(t, "safe", got["Content"])
	meta := got["Metadata"].(map[string]interface{})
	assert.Contains(t, meta["String"], `9007199254740993123`)

	text, isErr = callTool(t, a, "torque_artifact_update", map[string]interface{}{
		"artifact_id": id,
		"metadata":    `{"ok":true} {"trailing":true}`,
	})
	require.True(t, isErr, "trailing metadata must reject: %s", text)
}

func TestFullStack_ArtifactRunLinkValidation(t *testing.T) {
	a, db := setupAdapterWithDB(t)
	taskA := createTaskViaMCP(t, a, "artifact-run-a")
	taskB := createTaskViaMCP(t, a, "artifact-run-b")
	runA := insertRunFixture(t, db, taskA)
	runB := insertRunFixture(t, db, taskB)

	text, isErr := callTool(t, a, "torque_artifact_create", map[string]interface{}{
		"task_id": taskA,
		"type":    "inline",
		"run_id":  "999999",
	})
	require.True(t, isErr, "missing run link on create must reject: %s", text)

	text, isErr = callTool(t, a, "torque_artifact_create", map[string]interface{}{
		"task_id": taskA,
		"type":    "inline",
		"run_id":  runA,
	})
	require.False(t, isErr, "artifact create with run link: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"]

	text, isErr = callTool(t, a, "torque_artifact_update", map[string]interface{}{
		"artifact_id": id,
		"run_id":      runB,
		"url":         "https://bad.example",
	})
	require.True(t, isErr, "cross-task run link must reject: %s", text)

	text, isErr = callTool(t, a, "torque_artifact_update", map[string]interface{}{
		"artifact_id": id,
		"run_id":      "999999",
		"url":         "https://missing-run.example",
	})
	require.True(t, isErr, "missing run link on update must reject: %s", text)

	text, isErr = callTool(t, a, "torque_artifact_update", map[string]interface{}{
		"artifact_id": id,
		"run_id":      nil,
	})
	require.False(t, isErr, "run clear must work: %s", text)
	var cleared map[string]interface{}
	parseData(t, text, &cleared)
	assert.Equal(t, false, cleared["RunID"].(map[string]interface{})["Valid"])
	assert.Equal(t, "", cleared["URL"])
}

func TestFullStack_ArtifactMutationStrictInputs(t *testing.T) {
	a := setupAdapter(t)
	taskID := createTaskViaMCP(t, a, "artifact-strict-inputs")
	text, isErr := callTool(t, a, "torque_artifact_create", map[string]interface{}{
		"task_id": taskID,
		"type":    "inline",
		"content": map[string]interface{}{"wrong": true},
	})
	require.True(t, isErr, "wrong-type create content must reject: %s", text)

	text, isErr = callTool(t, a, "torque_artifact_create", map[string]interface{}{
		"task_id": taskID,
		"type":    "inline",
		"content": "original",
	})
	require.False(t, isErr, "artifact create: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"]

	text, isErr = callTool(t, a, "torque_artifact_update", map[string]interface{}{
		"artifact_id": id,
		"content":     nil,
	})
	require.True(t, isErr, "null content must reject: %s", text)

	text, isErr = callTool(t, a, "torque_artifact_delete", map[string]interface{}{
		"artifact_id": "1.2",
	})
	require.True(t, isErr, "fractional mutation ID must reject: %s", text)

	text, isErr = callTool(t, a, "torque_artifact_update", map[string]interface{}{
		"artifact_id": "9223372036854775808",
		"url":         "https://overflow.example",
	})
	require.True(t, isErr, "overflow mutation ID must reject: %s", text)

	text, isErr = callTool(t, a, "torque_artifact_update", map[string]interface{}{
		"artifact_id": "999999",
		"url":         "https://missing.example",
	})
	require.True(t, isErr, "missing artifact update must reject: %s", text)

	text, isErr = callTool(t, a, "torque_artifact_get", map[string]interface{}{"artifact_id": id})
	require.False(t, isErr, "artifact get: %s", text)
	var got map[string]interface{}
	parseData(t, text, &got)
	assert.Equal(t, "original", got["Content"])
}

func createTaskViaMCP(t *testing.T, a *mcpadapter.Adapter, title string) string {
	t.Helper()
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       title,
		"description": "x",
	})
	require.False(t, isErr, "task create: %s", text)
	var task map[string]interface{}
	parseData(t, text, &task)
	return task["ID"].(string)
}

func insertRunFixture(t *testing.T, db *sql.DB, taskID string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO runs (task_id, executor, status, started_at, prompt_tokens, completion_tokens, cost, error_message) VALUES (?, 'cli', 'running', CURRENT_TIMESTAMP, 0, 0, 0, '')`, taskID)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return id
}
