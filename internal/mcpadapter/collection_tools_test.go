package mcpadapter_test

import (
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/mcpadapter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupCollectionsAdapter returns an adapter with the collections feature
// flag enabled so the per-tool registration runs. callTool below operates
// against this adapter.
func setupCollectionsAdapter(t *testing.T) *mcpadapter.Adapter {
	t.Helper()
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("collections"))
	return adapterFromService(svc)
}

func TestCollectionToolsNotRegisteredWhenDisabled(t *testing.T) {
	a := setupAdapter(t)
	assert.False(t, toolIsRegistered(t, a, "clockwork_collection_create"),
		"clockwork_collection_create should not be registered when collections feature is disabled")
}

func TestCollectionToolsRegisteredWhenEnabled(t *testing.T) {
	a := setupCollectionsAdapter(t)

	text, isErr := callTool(t, a, "clockwork_collection_create", map[string]interface{}{
		"name":        "Roadmap",
		"description": "Quarterly themes",
	})
	require.False(t, isErr, "collection_create should succeed when feature is enabled: %s", text)

	var collection map[string]interface{}
	parseData(t, text, &collection)
	assert.Contains(t, collection["ID"].(string), "COL-")
	assert.Equal(t, "Roadmap", collection["Name"])
}

func TestCollectionFullLifecycleViaMCP(t *testing.T) {
	a := setupCollectionsAdapter(t)

	// 1. Create collection
	text, isErr := callTool(t, a, "clockwork_collection_create", map[string]interface{}{
		"name": "Lifecycle Bucket",
	})
	require.False(t, isErr, "collection_create should succeed: %s", text)

	var collection map[string]interface{}
	parseData(t, text, &collection)
	collectionID := collection["ID"].(string)

	// 2. Create three tasks
	taskIDs := make([]string, 3)
	for i := range taskIDs {
		text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
			"title":       "Task " + string(rune('A'+i)),
			"description": "Lifecycle task",
		})
		require.False(t, isErr, "task_create should succeed: %s", text)
		var task map[string]interface{}
		parseData(t, text, &task)
		taskIDs[i] = task["ID"].(string)
	}

	// 3. Add tasks to collection
	for _, taskID := range taskIDs {
		text, isErr := callTool(t, a, "clockwork_collection_task_add", map[string]interface{}{
			"collection_id": collectionID,
			"task_id":       taskID,
		})
		require.False(t, isErr, "collection_task_add should succeed: %s", text)
	}

	// 4. List collection tasks — must contain all three in insertion order
	text, isErr = callTool(t, a, "clockwork_collection_tasks_list", map[string]interface{}{
		"collection_id": collectionID,
	})
	require.False(t, isErr, "collection_tasks_list should succeed: %s", text)

	var listEnv struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &listEnv)
	require.Len(t, listEnv.Items, 3)
	for i, taskID := range taskIDs {
		assert.Equal(t, taskID, listEnv.Items[i]["id"], "position %d should be %s", i+1, taskID)
	}

	// 5. Reorder: reverse the list
	reversed := []string{taskIDs[2], taskIDs[1], taskIDs[0]}
	rawIDs := make([]interface{}, len(reversed))
	for i, id := range reversed {
		rawIDs[i] = id
	}
	text, isErr = callTool(t, a, "clockwork_collection_task_reorder", map[string]interface{}{
		"collection_id": collectionID,
		"task_ids":      rawIDs,
	})
	require.False(t, isErr, "collection_task_reorder should succeed: %s", text)

	text, isErr = callTool(t, a, "clockwork_collection_tasks_list", map[string]interface{}{
		"collection_id": collectionID,
	})
	require.False(t, isErr)
	parseData(t, text, &listEnv)
	require.Len(t, listEnv.Items, 3)
	for i, taskID := range reversed {
		assert.Equal(t, taskID, listEnv.Items[i]["id"], "after reorder position %d should be %s", i+1, taskID)
	}

	// 6. Archive
	text, isErr = callTool(t, a, "clockwork_collection_archive", map[string]interface{}{
		"id": collectionID,
	})
	require.False(t, isErr, "collection_archive should succeed: %s", text)

	// 7. List active should be empty; list archived should contain it
	text, isErr = callTool(t, a, "clockwork_collection_list", map[string]interface{}{
		"status": "active",
	})
	require.False(t, isErr)
	parseData(t, text, &listEnv)
	assert.Len(t, listEnv.Items, 0, "archived collection should not appear in active list")

	text, isErr = callTool(t, a, "clockwork_collection_list", map[string]interface{}{
		"status": "archived",
	})
	require.False(t, isErr)
	parseData(t, text, &listEnv)
	assert.Len(t, listEnv.Items, 1)
	assert.Equal(t, collectionID, listEnv.Items[0]["ID"])
}

func TestCollectionInboxRoundtripViaMCP(t *testing.T) {
	a := setupCollectionsAdapter(t)

	// Create a task
	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title": "Inbox Task",
	})
	require.False(t, isErr, "task_create should succeed: %s", text)
	var task map[string]interface{}
	parseData(t, text, &task)
	taskID := task["ID"].(string)

	// Inbox-add
	text, isErr = callTool(t, a, "clockwork_collection_inbox_add", map[string]interface{}{
		"task_id": taskID,
	})
	require.False(t, isErr, "collection_inbox_add should succeed: %s", text)

	// Inbox-list — must contain our task
	text, isErr = callTool(t, a, "clockwork_collection_inbox_list", map[string]interface{}{})
	require.False(t, isErr, "collection_inbox_list should succeed: %s", text)

	var listEnv struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &listEnv)
	require.Len(t, listEnv.Items, 1)
	assert.Equal(t, taskID, listEnv.Items[0]["id"])
}

func TestCollectionTaskMoveViaMCP(t *testing.T) {
	a := setupCollectionsAdapter(t)

	// Two collections
	text, isErr := callTool(t, a, "clockwork_collection_create", map[string]interface{}{"name": "A"})
	require.False(t, isErr)
	var colA map[string]interface{}
	parseData(t, text, &colA)
	colAID := colA["ID"].(string)

	text, isErr = callTool(t, a, "clockwork_collection_create", map[string]interface{}{"name": "B"})
	require.False(t, isErr)
	var colB map[string]interface{}
	parseData(t, text, &colB)
	colBID := colB["ID"].(string)

	// Task starts in A
	text, isErr = callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title": "Movable",
	})
	require.False(t, isErr)
	var task map[string]interface{}
	parseData(t, text, &task)
	taskID := task["ID"].(string)

	_, isErr = callTool(t, a, "clockwork_collection_task_add", map[string]interface{}{
		"collection_id": colAID,
		"task_id":       taskID,
	})
	require.False(t, isErr)

	// Move to B
	text, isErr = callTool(t, a, "clockwork_collection_task_move", map[string]interface{}{
		"task_id":              taskID,
		"target_collection_id": colBID,
	})
	require.False(t, isErr, "collection_task_move should succeed: %s", text)

	// Confirm B has the task; A is empty
	text, isErr = callTool(t, a, "clockwork_collection_tasks_list", map[string]interface{}{
		"collection_id": colBID,
	})
	require.False(t, isErr)
	var listEnv struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &listEnv)
	require.Len(t, listEnv.Items, 1)
	assert.Equal(t, taskID, listEnv.Items[0]["id"])

	text, isErr = callTool(t, a, "clockwork_collection_tasks_list", map[string]interface{}{
		"collection_id": colAID,
	})
	require.False(t, isErr)
	parseData(t, text, &listEnv)
	assert.Len(t, listEnv.Items, 0, "source collection should be empty after move")
}

func TestCollectionTaskRemoveReturnsToInbox(t *testing.T) {
	a := setupCollectionsAdapter(t)

	// Create collection + task; add task; then remove → must show up in inbox.
	colText, isErr := callTool(t, a, "clockwork_collection_create", map[string]interface{}{"name": "C"})
	require.False(t, isErr)
	var col map[string]interface{}
	parseData(t, colText, &col)

	taskText, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{"title": "Roundtrip"})
	require.False(t, isErr)
	var task map[string]interface{}
	parseData(t, taskText, &task)
	taskID := task["ID"].(string)

	_, isErr = callTool(t, a, "clockwork_collection_task_add", map[string]interface{}{
		"collection_id": col["ID"],
		"task_id":       taskID,
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "clockwork_collection_task_remove", map[string]interface{}{
		"task_id": taskID,
	})
	require.False(t, isErr)

	text, isErr := callTool(t, a, "clockwork_collection_inbox_list", map[string]interface{}{})
	require.False(t, isErr)

	var listEnv struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &listEnv)
	require.Len(t, listEnv.Items, 1)
	assert.Equal(t, taskID, listEnv.Items[0]["id"])
}
