package mcpadapter_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
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
	return mcpadapter.New(svc, nil)
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
	assert.False(t, toolIsRegistered(t, a, "torque_sprint_create"),
		"torque_sprint_create should not be registered when sprints feature is disabled")
}

func TestSprintToolsRegisteredWhenEnabled(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	a := adapterFromService(svc)

	text, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
		"name": "Sprint 1",
		"goal": "Ship it",
	})
	require.False(t, isErr, "sprint_create should succeed when feature is enabled: %s", text)

	var sprint map[string]interface{}
	parseData(t, text, &sprint)
	assert.Contains(t, sprint["ID"].(string), "SP-")
	assert.Equal(t, "Ship it", sprint["Goal"], "sprint_create must persist the goal field (CW-20260418-0019)")
}

// TestSprintCreate_GoalRoundTrip_EdgeCaseStrings reproduces CW-20260418-0019
// Instance 2: torque_sprint_create silently dropped multi-line goal
// strings because handleSprintCreate never read goal from the request and
// SprintCreateInput had no Goal field at all. Exercises multi-line,
// special-char, embedded-JSON, and Unicode payloads to guard against
// regression and any future boundary-layer string mangling.
func TestSprintCreate_GoalRoundTrip_EdgeCaseStrings(t *testing.T) {
	cases := map[string]string{
		"multiline":    "Ship feature X.\n\nAcceptance:\n- item 1\n- item 2",
		"special":      `quotes "inside" and backslash \\ and tabs\tand a comma, plus semi;colons`,
		"jsonEmbedded": `{"nested": {"key": "value"}, "array": [1,2,3]}`,
		"unicode":      "日本語 — 한국어 — العربية — 😀🚀",
	}

	for name, goal := range cases {
		name, goal := name, goal
		t.Run(name, func(t *testing.T) {
			svc := setupServiceDirect(t)
			require.NoError(t, svc.Feature.Enable("sprints"))
			a := adapterFromService(svc)

			text, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
				"name": "Goal edge-case " + name,
				"goal": goal,
			})
			require.False(t, isErr, "sprint_create should succeed: %s", text)

			var sprint map[string]interface{}
			parseData(t, text, &sprint)
			assert.Equal(t, goal, sprint["Goal"], "goal must round-trip verbatim")

			// Cross-check: fetch via sprint_get to confirm the DB row matches
			// the create response (guards against the create response being
			// populated from input while DB row silently loses the field).
			text, isErr = callTool(t, a, "torque_sprint_get", map[string]interface{}{
				"id": sprint["ID"],
			})
			require.False(t, isErr, "sprint_get should succeed: %s", text)
			var resp map[string]interface{}
			parseData(t, text, &resp)
			got := resp["sprint"].(map[string]interface{})
			assert.Equal(t, goal, got["Goal"], "goal must persist to DB")
		})
	}
}

func TestSprintFullLifecycleViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	a := adapterFromService(svc)

	// Create sprint
	text, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
		"name":          "Lifecycle Sprint",
		"approval_mode": "approve_each",
		"cost_budget":   float64(100),
	})
	require.False(t, isErr, "sprint_create should succeed: %s", text)

	var sprint map[string]interface{}
	parseData(t, text, &sprint)
	sprintID := sprint["ID"].(string)

	// Get sprint
	text, isErr = callTool(t, a, "torque_sprint_get", map[string]interface{}{
		"id": sprintID,
	})
	require.False(t, isErr, "sprint_get should succeed: %s", text)

	// List active sprints (sprint is active by default)
	text, isErr = callTool(t, a, "torque_sprint_list", map[string]interface{}{
		"status": "active",
	})
	require.False(t, isErr, "sprint_list should succeed: %s", text)

	// Create task in sprint (sprint is active)
	text, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Sprint task",
		"description": "Task in the sprint",
		"sprint_id":   sprintID,
	})
	require.False(t, isErr, "task_create with sprint should succeed: %s", text)

	var task map[string]interface{}
	parseData(t, text, &task)
	taskID := task["ID"].(string)

	// Move task through lifecycle
	_, _ = callTool(t, a, "torque_task_transition", map[string]interface{}{"id": taskID, "status": "doing"})
	_, _ = callTool(t, a, "torque_task_transition", map[string]interface{}{"id": taskID, "status": "review"})

	// Approve individual task
	text, isErr = callTool(t, a, "torque_sprint_approve", map[string]interface{}{
		"id":      sprintID,
		"task_id": taskID,
	})
	require.False(t, isErr, "sprint_approve should succeed: %s", text)

	// Verify task is done
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": taskID})
	require.False(t, isErr)
	var updatedTask map[string]interface{}
	parseData(t, text, &updatedTask)
	assert.Equal(t, "done", updatedTask["Status"])

	// Transition sprint to inactive
	text, isErr = callTool(t, a, "torque_sprint_update", map[string]interface{}{
		"id":     sprintID,
		"status": "inactive",
	})
	require.False(t, isErr, "sprint_update to inactive should succeed: %s", text)

	// Delete sprint
	text, isErr = callTool(t, a, "torque_sprint_delete", map[string]interface{}{
		"id": sprintID,
	})
	require.False(t, isErr, "sprint_delete should succeed: %s", text)
}

func TestProjectToolsViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	// Create project
	text, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":        "Test Project",
		"description": "A test project",
		"repo_path":   t.TempDir(),
	})
	require.False(t, isErr, "project_create should succeed: %s", text)

	var project map[string]interface{}
	parseData(t, text, &project)
	projectID := project["ID"].(string)
	assert.Contains(t, projectID, "PRJ-")

	// List projects — new {items, meta} envelope shape
	text, isErr = callTool(t, a, "torque_project_list", map[string]interface{}{})
	require.False(t, isErr, "project_list should succeed: %s", text)

	var projectEnv struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &projectEnv)
	assert.Len(t, projectEnv.Items, 1)
	assert.Equal(t, float64(1), projectEnv.Meta["returned"])

	// Create task in project
	text, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Project task",
		"description": "Task in the project",
		"project_id":  projectID,
	})
	require.False(t, isErr, "task_create with project should succeed: %s", text)

	// Delete project
	text, isErr = callTool(t, a, "torque_project_delete", map[string]interface{}{
		"id": projectID,
	})
	require.False(t, isErr, "project_delete should succeed: %s", text)
}

func TestEpicToolsViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("epics"))
	a := adapterFromService(svc)

	// Create epic
	text, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{
		"name":        "Auth Overhaul",
		"description": "Replace entire auth stack",
	})
	require.False(t, isErr, "epic_create should succeed: %s", text)

	var epic map[string]interface{}
	parseData(t, text, &epic)
	epicID := epic["ID"].(string)
	assert.Contains(t, epicID, "EP-")

	// Get epic
	text, isErr = callTool(t, a, "torque_epic_get", map[string]interface{}{"id": epicID})
	require.False(t, isErr, "epic_get should succeed: %s", text)

	// Update epic
	text, isErr = callTool(t, a, "torque_epic_update", map[string]interface{}{
		"id":     epicID,
		"name":   "Auth Overhaul v2",
		"status": "inactive",
	})
	require.False(t, isErr, "epic_update should succeed: %s", text)

	// List epics filtered by inactive — new {items, meta} envelope shape
	text, isErr = callTool(t, a, "torque_epic_list", map[string]interface{}{
		"status": "inactive",
	})
	require.False(t, isErr, "epic_list should succeed: %s", text)

	var epicEnv struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &epicEnv)
	assert.Len(t, epicEnv.Items, 1)
	assert.Equal(t, float64(1), epicEnv.Meta["returned"])

	// Create task in epic
	text, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Epic task",
		"description": "Task in the epic",
		"epic_id":     epicID,
	})
	require.False(t, isErr, "task_create with epic should succeed: %s", text)

	// Delete epic
	text, isErr = callTool(t, a, "torque_epic_delete", map[string]interface{}{"id": epicID})
	require.False(t, isErr, "epic_delete should succeed: %s", text)
}

func TestSprintApproveAllViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	a := adapterFromService(svc)

	// Create sprint with approve_sprint mode
	text, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
		"name":          "Batch Approve Sprint",
		"approval_mode": "approve_sprint",
	})
	require.False(t, isErr, "sprint_create should succeed: %s", text)

	var sprint map[string]interface{}
	parseData(t, text, &sprint)
	sprintID := sprint["ID"].(string)

	// Sprint is already active by default — no transition needed

	// Create two tasks, move to review
	text1, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "Task 1", "description": "First", "sprint_id": sprintID,
	})
	text2, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "Task 2", "description": "Second", "sprint_id": sprintID,
	})

	var t1, t2 map[string]interface{}
	parseData(t, text1, &t1)
	parseData(t, text2, &t2)
	t1ID := t1["ID"].(string)
	t2ID := t2["ID"].(string)

	callTool(t, a, "torque_task_transition", map[string]interface{}{"id": t1ID, "status": "doing"})
	callTool(t, a, "torque_task_transition", map[string]interface{}{"id": t1ID, "status": "review"})
	callTool(t, a, "torque_task_transition", map[string]interface{}{"id": t2ID, "status": "doing"})
	callTool(t, a, "torque_task_transition", map[string]interface{}{"id": t2ID, "status": "review"})

	// Approve all (no task_id)
	text, isErr = callTool(t, a, "torque_sprint_approve", map[string]interface{}{
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

	// Create entities. project_create requires repo_path (real-shape project — repo_path is
	// load-bearing for production callers; supplying a value here matches the project_create
	// contract rather than papering over scope drift).
	sprintText, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{"name": "Sprint"})
	require.False(t, isErr)
	projectText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Project",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	epicText, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{"name": "Epic"})
	require.False(t, isErr)

	var sp, pr, ep map[string]interface{}
	parseData(t, sprintText, &sp)
	parseData(t, projectText, &pr)
	parseData(t, epicText, &ep)

	// Create unassociated task
	taskText, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "Unassociated", "description": "No associations yet",
	})
	require.False(t, isErr)

	var task map[string]interface{}
	parseData(t, taskText, &task)
	taskID := task["ID"].(string)

	// Update task to assign to sprint, project, and epic
	text, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":         taskID,
		"sprint_id":  sp["ID"],
		"project_id": pr["ID"],
		"epic_id":    ep["ID"],
	})
	require.False(t, isErr, "task_update with associations should succeed: %s", text)

	// Verify associations
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": taskID})
	require.False(t, isErr)

	var got map[string]interface{}
	parseData(t, text, &got)

	// SprintID is sql.NullString — check the nested structure
	sprintField := got["SprintID"].(map[string]interface{})
	assert.True(t, sprintField["Valid"].(bool))
	assert.Equal(t, sp["ID"], sprintField["String"])
}

// TestSprintCreateProjectIDViaMCP verifies project_id is exposed and persisted on
// the torque_sprint_create tool. Closes the audit gap surfaced 2026-05-12 where
// the underlying store accepted project_id but the MCP tool schema didn't declare
// it, causing the parameter to be invisible to clients.
func TestSprintCreateProjectIDViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	// Create project to associate with
	projectText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Project for sprint",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var pr map[string]interface{}
	parseData(t, projectText, &pr)
	projectID := pr["ID"].(string)

	// Create sprint with project_id
	text, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
		"name":       "Sprint with project",
		"project_id": projectID,
	})
	require.False(t, isErr, "sprint_create with project_id should succeed: %s", text)

	var sprint map[string]interface{}
	parseData(t, text, &sprint)
	sprintID := sprint["ID"].(string)

	// Read back via sprint_get to confirm DB persistence
	text, isErr = callTool(t, a, "torque_sprint_get", map[string]interface{}{"id": sprintID})
	require.False(t, isErr, "sprint_get should succeed: %s", text)
	var resp map[string]interface{}
	parseData(t, text, &resp)
	got := resp["sprint"].(map[string]interface{})
	projectField := got["ProjectID"].(map[string]interface{})
	assert.True(t, projectField["Valid"].(bool), "project_id must persist with Valid=true")
	assert.Equal(t, projectID, projectField["String"])
}

// TestSprintUpdateProjectIDViaMCP verifies project_id can be set on an existing
// sprint via torque_sprint_update (the backfill path for the 3 sprints created
// 2026-05-12 prior to this fix).
func TestSprintUpdateProjectIDViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	projectText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Project for update",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var pr map[string]interface{}
	parseData(t, projectText, &pr)
	projectID := pr["ID"].(string)

	// Create sprint without project_id
	sprintText, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
		"name": "Orphan sprint",
	})
	require.False(t, isErr)
	var sprint map[string]interface{}
	parseData(t, sprintText, &sprint)
	sprintID := sprint["ID"].(string)

	// Update to attach project_id
	text, isErr := callTool(t, a, "torque_sprint_update", map[string]interface{}{
		"id":         sprintID,
		"project_id": projectID,
	})
	require.False(t, isErr, "sprint_update with project_id should succeed: %s", text)

	// Read back
	text, isErr = callTool(t, a, "torque_sprint_get", map[string]interface{}{"id": sprintID})
	require.False(t, isErr)
	var resp map[string]interface{}
	parseData(t, text, &resp)
	got := resp["sprint"].(map[string]interface{})
	projectField := got["ProjectID"].(map[string]interface{})
	assert.True(t, projectField["Valid"].(bool))
	assert.Equal(t, projectID, projectField["String"])
}

// TestSprintUpdateProjectIDClearViaMCP verifies an existing project_id can be
// cleared via torque_sprint_update by passing an empty string. Closes Copilot
// review feedback on PR #51: the documented clear behavior must actually persist
// as SQL NULL (ProjectID.Valid=false), not as project_id = ” (Valid=true).
func TestSprintUpdateProjectIDClearViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	projectText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Project for sprint clear",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var pr map[string]interface{}
	parseData(t, projectText, &pr)
	projectID := pr["ID"].(string)

	// Create sprint with project_id set
	sprintText, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
		"name":       "Sprint to clear",
		"project_id": projectID,
	})
	require.False(t, isErr)
	var sprint map[string]interface{}
	parseData(t, sprintText, &sprint)
	sprintID := sprint["ID"].(string)

	// Clear project_id via empty string
	_, isErr = callTool(t, a, "torque_sprint_update", map[string]interface{}{
		"id":         sprintID,
		"project_id": "",
	})
	require.False(t, isErr, "sprint_update with empty project_id should succeed")

	// Read back: ProjectID.Valid must be false (SQL NULL semantics)
	text, isErr := callTool(t, a, "torque_sprint_get", map[string]interface{}{"id": sprintID})
	require.False(t, isErr)
	var resp map[string]interface{}
	parseData(t, text, &resp)
	got := resp["sprint"].(map[string]interface{})
	projectField := got["ProjectID"].(map[string]interface{})
	assert.False(t, projectField["Valid"].(bool), "project_id must clear to NULL (Valid=false), not empty string")
}

// TestSprintGet_UnknownIDMapsToNotFound is SWEEP-001's per-entity
// error-taxonomy spot-check for Sprint: a get on an unknown sprint id must
// map to error.code=not_found (via the string-match tier over
// sqlstore's untyped "sprint %s not found" error), not fall through to
// internal.
func TestSprintGet_UnknownIDMapsToNotFound(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	a := adapterFromService(svc)

	text, isErr := callTool(t, a, "torque_sprint_get", map[string]interface{}{"id": "SP-does-not-exist"})
	require.True(t, isErr, "get on unknown sprint id must error: %s", text)
	code, _, _ := parseError(t, text)
	assert.Equal(t, "not_found", code)
}

// TestSprintUpdateGoalClearViaMCP verifies torque_sprint_update presence-based
// field detection (SWEEP-001, mirroring FIX-001's fix for Task): an explicit
// "goal": "" must actually clear the goal, not be silently dropped as
// "not provided" the way the pre-fix value-based check (`if v != ""`) did.
func TestSprintUpdateGoalClearViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	a := adapterFromService(svc)

	sprintText, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
		"name": "Sprint with goal",
		"goal": "Ship the thing",
	})
	require.False(t, isErr)
	var sprint map[string]interface{}
	parseData(t, sprintText, &sprint)
	sprintID := sprint["ID"].(string)

	_, isErr = callTool(t, a, "torque_sprint_update", map[string]interface{}{
		"id":   sprintID,
		"goal": "",
	})
	require.False(t, isErr, "sprint_update with empty goal should succeed")

	text, isErr := callTool(t, a, "torque_sprint_get", map[string]interface{}{"id": sprintID})
	require.False(t, isErr)
	var resp map[string]interface{}
	parseData(t, text, &resp)
	got := resp["sprint"].(map[string]interface{})
	assert.Equal(t, "", got["Goal"], "goal must clear to empty string")
}

// TestSprintUpdateCostBudgetZeroViaMCP verifies an explicit cost_budget=0
// actually persists as 0, not silently dropped as "not provided" (SWEEP-001,
// same value-based-detection bug class FIX-001 fixed for Task).
func TestSprintUpdateCostBudgetZeroViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	a := adapterFromService(svc)

	sprintText, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
		"name":        "Sprint with budget",
		"cost_budget": "50",
	})
	require.False(t, isErr)
	var sprint map[string]interface{}
	parseData(t, sprintText, &sprint)
	sprintID := sprint["ID"].(string)

	_, isErr = callTool(t, a, "torque_sprint_update", map[string]interface{}{
		"id":          sprintID,
		"cost_budget": "0",
	})
	require.False(t, isErr, "sprint_update with cost_budget=0 should succeed")

	text, isErr := callTool(t, a, "torque_sprint_get", map[string]interface{}{"id": sprintID})
	require.False(t, isErr)
	var resp map[string]interface{}
	parseData(t, text, &resp)
	got := resp["sprint"].(map[string]interface{})
	budgetField := got["CostBudget"].(map[string]interface{})
	assert.True(t, budgetField["Valid"].(bool))
	assert.Equal(t, float64(0), budgetField["Float64"])
}

// TestSprintUpdateNameCannotBeClearedViaMCP verifies an explicit "name": ""
// is rejected with error.code=arg_invalid rather than silently persisting
// (SWEEP-001, mirroring Task's title-cannot-be-cleared guard).
func TestSprintUpdateNameCannotBeClearedViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	a := adapterFromService(svc)

	sprintText, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{
		"name": "Named sprint",
	})
	require.False(t, isErr)
	var sprint map[string]interface{}
	parseData(t, sprintText, &sprint)
	sprintID := sprint["ID"].(string)

	text, isErr := callTool(t, a, "torque_sprint_update", map[string]interface{}{
		"id":   sprintID,
		"name": "",
	})
	require.True(t, isErr, "sprint_update with empty name should fail")
	code, _, field := parseError(t, text)
	assert.Equal(t, "arg_invalid", code)
	assert.Equal(t, "name", field)
}

// TestEpicCreateProjectIDViaMCP verifies project_id is exposed and persisted on
// the torque_epic_create tool. Same audit-gap class as the sprint fix above.
func TestEpicCreateProjectIDViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("epics"))
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	projectText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Project for epic",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var pr map[string]interface{}
	parseData(t, projectText, &pr)
	projectID := pr["ID"].(string)

	// Create epic with project_id
	text, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{
		"name":       "Epic with project",
		"project_id": projectID,
	})
	require.False(t, isErr, "epic_create with project_id should succeed: %s", text)

	var epic map[string]interface{}
	parseData(t, text, &epic)
	epicID := epic["ID"].(string)

	// Read back via epic_get
	text, isErr = callTool(t, a, "torque_epic_get", map[string]interface{}{"id": epicID})
	require.False(t, isErr)
	var got map[string]interface{}
	parseData(t, text, &got)
	projectField := got["ProjectID"].(map[string]interface{})
	assert.True(t, projectField["Valid"].(bool), "project_id must persist with Valid=true")
	assert.Equal(t, projectID, projectField["String"])
}

// TestEpicUpdateProjectIDViaMCP verifies project_id can be set on an existing
// epic via torque_epic_update.
func TestEpicUpdateProjectIDViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("epics"))
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	projectText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Project for epic update",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var pr map[string]interface{}
	parseData(t, projectText, &pr)
	projectID := pr["ID"].(string)

	epicText, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{
		"name": "Orphan epic",
	})
	require.False(t, isErr)
	var epic map[string]interface{}
	parseData(t, epicText, &epic)
	epicID := epic["ID"].(string)

	text, isErr := callTool(t, a, "torque_epic_update", map[string]interface{}{
		"id":         epicID,
		"project_id": projectID,
	})
	require.False(t, isErr, "epic_update with project_id should succeed: %s", text)

	text, isErr = callTool(t, a, "torque_epic_get", map[string]interface{}{"id": epicID})
	require.False(t, isErr)
	var got map[string]interface{}
	parseData(t, text, &got)
	projectField := got["ProjectID"].(map[string]interface{})
	assert.True(t, projectField["Valid"].(bool))
	assert.Equal(t, projectID, projectField["String"])
}

// TestEpicUpdateProjectIDClearViaMCP verifies an existing project_id can be
// cleared via torque_epic_update by passing an empty string. Closes Copilot
// review feedback on PR #51: clearing must persist as SQL NULL, not as empty string.
func TestEpicUpdateProjectIDClearViaMCP(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("epics"))
	require.NoError(t, svc.Feature.Enable("projects"))
	a := adapterFromService(svc)

	projectText, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Project for epic clear",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var pr map[string]interface{}
	parseData(t, projectText, &pr)
	projectID := pr["ID"].(string)

	epicText, isErr := callTool(t, a, "torque_epic_create", map[string]interface{}{
		"name":       "Epic to clear",
		"project_id": projectID,
	})
	require.False(t, isErr)
	var epic map[string]interface{}
	parseData(t, epicText, &epic)
	epicID := epic["ID"].(string)

	_, isErr = callTool(t, a, "torque_epic_update", map[string]interface{}{
		"id":         epicID,
		"project_id": "",
	})
	require.False(t, isErr, "epic_update with empty project_id should succeed")

	text, isErr := callTool(t, a, "torque_epic_get", map[string]interface{}{"id": epicID})
	require.False(t, isErr)
	var got map[string]interface{}
	parseData(t, text, &got)
	projectField := got["ProjectID"].(map[string]interface{})
	assert.False(t, projectField["Valid"].(bool), "project_id must clear to NULL (Valid=false), not empty string")
}

func TestHealthShowsEnabledFeatures(t *testing.T) {
	svc := setupServiceDirect(t)
	require.NoError(t, svc.Feature.Enable("sprints"))
	require.NoError(t, svc.Feature.Enable("epics"))
	a := adapterFromService(svc)

	text, isErr := callTool(t, a, "torque_health", map[string]interface{}{})
	require.False(t, isErr, "health should not error: %s", text)

	var health map[string]interface{}
	parseData(t, text, &health)
	features := health["enabled_features"].([]interface{})
	assert.Contains(t, features, "sprints")
	assert.Contains(t, features, "epics")
}
