package mcpadapter_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/mcpadapter"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	_ "modernc.org/sqlite"
)

// setupServiceDirect creates a Service backed by an in-memory SQLite store.
func setupServiceDirect(t *testing.T) *service.Service {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return service.New(store)
}

// adapterFromService builds an Adapter from an already-configured service.
func adapterFromService(svc *service.Service) *mcpadapter.Adapter {
	return mcpadapter.New(svc)
}

// toolIsRegistered returns true if the tool exists (no JSON-RPC error in response).
func toolIsRegistered(t *testing.T, a *mcpadapter.Adapter, toolName string) bool {
	t.Helper()

	initMsg, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      0,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test", "version": "0.1.0"},
		},
	})
	require.NoError(t, err)
	a.Server().HandleMessage(context.Background(), initMsg)

	msg, err := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": map[string]interface{}{"name": "test"},
		},
	})
	require.NoError(t, err)

	resp := a.Server().HandleMessage(context.Background(), msg)
	respBytes, err := json.Marshal(resp)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(respBytes, &parsed))

	// If there's a top-level "error" key, the tool was not found
	_, hasError := parsed["error"]
	return !hasError
}

func TestSprintToolsNotRegisteredWhenDisabled(t *testing.T) {
	a := setupAdapter(t)
	// Sprint tools should not be registered when features.sprints is not enabled
	assert.False(t, toolIsRegistered(t, a, "clockwork_sprint_create"),
		"clockwork_sprint_create should not be registered when sprints feature is disabled")
}

func TestSprintToolsRegisteredWhenEnabled(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	a := adapterFromService(svc)

	text, isErr := callTool(t, a, "clockwork_sprint_create", map[string]interface{}{
		"name": "Sprint 1",
		"goal": "Ship it",
	})
	require.False(t, isErr, "sprint_create should succeed when feature is enabled: %s", text)

	var sprint map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &sprint))
	assert.Contains(t, sprint["ID"].(string), "SP-")
}

func TestSprintFullLifecycleViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	a := adapterFromService(svc)

	// Create sprint
	text, isErr := callTool(t, a, "clockwork_sprint_create", map[string]interface{}{
		"name":          "Lifecycle Sprint",
		"goal":          "Test the full lifecycle",
		"approval_mode": "approve_each",
		"cost_budget":   float64(100),
	})
	require.False(t, isErr, "sprint_create should succeed: %s", text)

	var sprint map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &sprint))
	sprintID := sprint["ID"].(string)

	// Get sprint
	text, isErr = callTool(t, a, "clockwork_sprint_get", map[string]interface{}{
		"id": sprintID,
	})
	require.False(t, isErr, "sprint_get should succeed: %s", text)

	// Transition to active
	text, isErr = callTool(t, a, "clockwork_sprint_update", map[string]interface{}{
		"id":     sprintID,
		"status": "active",
	})
	require.False(t, isErr, "sprint_update to active should succeed: %s", text)

	// List sprints
	text, isErr = callTool(t, a, "clockwork_sprint_list", map[string]interface{}{
		"status": "active",
	})
	require.False(t, isErr, "sprint_list should succeed: %s", text)

	// Create task in sprint
	text, isErr = callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "Sprint task",
		"description": "Task in the sprint",
		"sprint_id":   sprintID,
	})
	require.False(t, isErr, "task_create with sprint should succeed: %s", text)

	var task map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &task))
	taskID := task["ID"].(string)

	// Move task through lifecycle
	_, _ = callTool(t, a, "clockwork_task_transition", map[string]interface{}{"id": taskID, "status": "doing"})
	_, _ = callTool(t, a, "clockwork_task_transition", map[string]interface{}{"id": taskID, "status": "review"})

	// Approve individual task
	text, isErr = callTool(t, a, "clockwork_sprint_approve", map[string]interface{}{
		"id":      sprintID,
		"task_id": taskID,
	})
	require.False(t, isErr, "sprint_approve should succeed: %s", text)

	// Verify task is done
	text, isErr = callTool(t, a, "clockwork_task_get", map[string]interface{}{"id": taskID})
	require.False(t, isErr)
	var updatedTask map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &updatedTask))
	assert.Equal(t, "done", updatedTask["Status"])

	// Delete sprint
	text, isErr = callTool(t, a, "clockwork_sprint_delete", map[string]interface{}{
		"id": sprintID,
	})
	require.False(t, isErr, "sprint_delete should succeed: %s", text)
}

func TestProjectToolsViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	// Create project
	text, isErr := callTool(t, a, "clockwork_project_create", map[string]interface{}{
		"name":        "Test Project",
		"description": "A test project",
		"repo_path":   "/tmp/test-project",
	})
	require.False(t, isErr, "project_create should succeed: %s", text)

	var project map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &project))
	projectID := project["ID"].(string)
	assert.Contains(t, projectID, "PRJ-")

	// List projects
	text, isErr = callTool(t, a, "clockwork_project_list", map[string]interface{}{})
	require.False(t, isErr, "project_list should succeed: %s", text)

	var listResult map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &listResult))
	assert.Equal(t, float64(1), listResult["count"])

	// Create task in project
	text, isErr = callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "Project task",
		"description": "Task in the project",
		"project_id":  projectID,
	})
	require.False(t, isErr, "task_create with project should succeed: %s", text)

	// Delete project
	text, isErr = callTool(t, a, "clockwork_project_delete", map[string]interface{}{
		"id": projectID,
	})
	require.False(t, isErr, "project_delete should succeed: %s", text)
}

func TestEpicToolsViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("epics"))
	a := adapterFromService(svc)

	// Create epic
	text, isErr := callTool(t, a, "clockwork_epic_create", map[string]interface{}{
		"name":        "Auth Overhaul",
		"description": "Replace entire auth stack",
	})
	require.False(t, isErr, "epic_create should succeed: %s", text)

	var epic map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &epic))
	epicID := epic["ID"].(string)
	assert.Contains(t, epicID, "EP-")

	// Get epic
	text, isErr = callTool(t, a, "clockwork_epic_get", map[string]interface{}{"id": epicID})
	require.False(t, isErr, "epic_get should succeed: %s", text)

	// Update epic
	text, isErr = callTool(t, a, "clockwork_epic_update", map[string]interface{}{
		"id":     epicID,
		"name":   "Auth Overhaul v2",
		"status": "closed",
	})
	require.False(t, isErr, "epic_update should succeed: %s", text)

	// List epics filtered by closed
	text, isErr = callTool(t, a, "clockwork_epic_list", map[string]interface{}{
		"status": "closed",
	})
	require.False(t, isErr, "epic_list should succeed: %s", text)

	var epicList map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &epicList))
	assert.Equal(t, float64(1), epicList["count"])

	// Create task in epic
	text, isErr = callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "Epic task",
		"description": "Task in the epic",
		"epic_id":     epicID,
	})
	require.False(t, isErr, "task_create with epic should succeed: %s", text)

	// Delete epic
	text, isErr = callTool(t, a, "clockwork_epic_delete", map[string]interface{}{"id": epicID})
	require.False(t, isErr, "epic_delete should succeed: %s", text)
}

func TestSprintApproveAllViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	a := adapterFromService(svc)

	// Create sprint with approve_sprint mode
	text, isErr := callTool(t, a, "clockwork_sprint_create", map[string]interface{}{
		"name":          "Batch Approve Sprint",
		"approval_mode": "approve_sprint",
	})
	require.False(t, isErr, "sprint_create should succeed: %s", text)

	var sprint map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &sprint))
	sprintID := sprint["ID"].(string)

	// Activate sprint
	_, _ = callTool(t, a, "clockwork_sprint_update", map[string]interface{}{
		"id": sprintID, "status": "active",
	})

	// Create two tasks, move to review
	text1, _ := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title": "Task 1", "description": "First", "sprint_id": sprintID,
	})
	text2, _ := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title": "Task 2", "description": "Second", "sprint_id": sprintID,
	})

	var t1, t2 map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text1), &t1))
	require.NoError(t, json.Unmarshal([]byte(text2), &t2))
	t1ID := t1["ID"].(string)
	t2ID := t2["ID"].(string)

	callTool(t, a, "clockwork_task_transition", map[string]interface{}{"id": t1ID, "status": "doing"})
	callTool(t, a, "clockwork_task_transition", map[string]interface{}{"id": t1ID, "status": "review"})
	callTool(t, a, "clockwork_task_transition", map[string]interface{}{"id": t2ID, "status": "doing"})
	callTool(t, a, "clockwork_task_transition", map[string]interface{}{"id": t2ID, "status": "review"})

	// Approve all (no task_id)
	text, isErr = callTool(t, a, "clockwork_sprint_approve", map[string]interface{}{
		"id": sprintID,
	})
	require.False(t, isErr, "sprint_approve_all should succeed: %s", text)
	assert.Contains(t, text, "2 tasks approved")
}

func TestTaskAssociationUpdateViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	require.NoError(t, svc.Feature.Enable("projects"))
	require.NoError(t, svc.Feature.Enable("epics"))
	a := adapterFromService(svc)

	// Create entities
	sprintText, isErr := callTool(t, a, "clockwork_sprint_create", map[string]interface{}{"name": "Sprint"})
	require.False(t, isErr)
	projectText, isErr := callTool(t, a, "clockwork_project_create", map[string]interface{}{"name": "Project"})
	require.False(t, isErr)
	epicText, isErr := callTool(t, a, "clockwork_epic_create", map[string]interface{}{"name": "Epic"})
	require.False(t, isErr)

	var sp, pr, ep map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(sprintText), &sp))
	require.NoError(t, json.Unmarshal([]byte(projectText), &pr))
	require.NoError(t, json.Unmarshal([]byte(epicText), &ep))

	// Create unassociated task
	taskText, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title": "Unassociated", "description": "No associations yet",
	})
	require.False(t, isErr)

	var task map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(taskText), &task))
	taskID := task["ID"].(string)

	// Update task to assign to sprint, project, and epic
	text, isErr := callTool(t, a, "clockwork_task_update", map[string]interface{}{
		"id":         taskID,
		"sprint_id":  sp["ID"],
		"project_id": pr["ID"],
		"epic_id":    ep["ID"],
	})
	require.False(t, isErr, "task_update with associations should succeed: %s", text)

	// Verify associations
	text, isErr = callTool(t, a, "clockwork_task_get", map[string]interface{}{"id": taskID})
	require.False(t, isErr)

	var got map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &got))

	// SprintID is sql.NullString — check the nested structure
	sprintField := got["SprintID"].(map[string]interface{})
	assert.True(t, sprintField["Valid"].(bool))
	assert.Equal(t, sp["ID"], sprintField["String"])
}

func TestHealthShowsEnabledFeatures(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	require.NoError(t, svc.Feature.Enable("epics"))
	a := adapterFromService(svc)

	text, isErr := callTool(t, a, "clockwork_health", map[string]interface{}{})
	require.False(t, isErr, "health should not error: %s", text)

	var health map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &health))
	features := health["enabled_features"].([]interface{})
	assert.Contains(t, features, "sprints")
	assert.Contains(t, features, "epics")
}
