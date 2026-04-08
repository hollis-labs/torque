package mcpadapter_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/clockwork-manifold/internal/mcpadapter"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"

	_ "modernc.org/sqlite"
)

func setupAdapter(t *testing.T) *mcpadapter.Adapter {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	svc := service.New(store)
	return mcpadapter.New(svc)
}

func callTool(t *testing.T, a *mcpadapter.Adapter, name string, args map[string]interface{}) (string, bool) {
	t.Helper()

	// Send initialize first to ensure the server is ready.
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
			"name":      name,
			"arguments": args,
		},
	})
	require.NoError(t, err)

	resp := a.Server().HandleMessage(context.Background(), msg)

	respBytes, err := json.Marshal(resp)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(respBytes, &parsed))

	result, ok := parsed["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("no result in response: %s", string(respBytes))
	}

	content, ok := result["content"].([]interface{})
	if !ok || len(content) == 0 {
		return "", false
	}

	first, ok := content[0].(map[string]interface{})
	if !ok {
		return "", false
	}

	text, _ := first["text"].(string)
	isError, _ := result["isError"].(bool)
	return text, isError
}

func TestFullStack_CreateAndGetTask(t *testing.T) {
	a := setupAdapter(t)

	// Create a task.
	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "Fix auth bug",
		"description": "Login returns 500",
		"priority":    1,
	})
	require.False(t, isErr, "create should not error: %s", text)
	require.NotEmpty(t, text)

	// Parse the task ID from the response JSON (struct fields are uppercased).
	var created map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &created))
	id, ok := created["ID"].(string)
	require.True(t, ok, "response should contain string ID")
	require.NotEmpty(t, id)

	// Fetch the task by ID.
	text, isErr = callTool(t, a, "clockwork_task_get", map[string]interface{}{
		"id": id,
	})
	require.False(t, isErr, "get should not error: %s", text)

	var fetched map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &fetched))
	require.Equal(t, "Fix auth bug", fetched["Title"])
}

func TestFullStack_TaskLifecycle(t *testing.T) {
	a := setupAdapter(t)

	// Create a task.
	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "Lifecycle task",
		"description": "Testing full lifecycle",
	})
	require.False(t, isErr, "create should not error: %s", text)

	var created map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &created))
	id := created["ID"].(string)

	// todo → doing.
	text, isErr = callTool(t, a, "clockwork_task_transition", map[string]interface{}{
		"id":     id,
		"status": "doing",
	})
	require.False(t, isErr, "todo→doing should succeed: %s", text)

	// doing → review.
	text, isErr = callTool(t, a, "clockwork_task_transition", map[string]interface{}{
		"id":     id,
		"status": "review",
	})
	require.False(t, isErr, "doing→review should succeed: %s", text)

	// review → done.
	text, isErr = callTool(t, a, "clockwork_task_transition", map[string]interface{}{
		"id":     id,
		"status": "done",
	})
	require.False(t, isErr, "review→done should succeed: %s", text)

	// done → doing (invalid).
	text, isErr = callTool(t, a, "clockwork_task_transition", map[string]interface{}{
		"id":     id,
		"status": "doing",
	})
	require.True(t, isErr, "done→doing should fail; got: %s", text)
}

func TestFullStack_Health(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "clockwork_health", map[string]interface{}{})
	require.False(t, isErr, "health should not error")
	require.Contains(t, text, "running")
}

func TestFullStack_SearchTasks(t *testing.T) {
	a := setupAdapter(t)

	// Create two tasks.
	_, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "Fix login bug",
		"description": "Login is broken",
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "Add unit tests",
		"description": "Write tests for all handlers",
	})
	require.False(t, isErr)

	// Search for "login".
	text, isErr := callTool(t, a, "clockwork_task_search", map[string]interface{}{
		"query": "login",
	})
	require.False(t, isErr, "search should not error: %s", text)

	var results []interface{}
	require.NoError(t, json.Unmarshal([]byte(text), &results))
	require.Len(t, results, 1, "search for 'login' should return exactly 1 result")
}
