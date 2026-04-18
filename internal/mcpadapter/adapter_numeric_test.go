package mcpadapter_test

// End-to-end coverage for CW-20260418-0011. These tests round-trip through
// server.HandleMessage so they exercise mcp-go's schema validator. The bug
// they regress was ARG_VALIDATION_FAILED at the validator: with WithNumber
// schemas, any string-encoded numeric arg (e.g. "limit": "50" from LLM-backed
// clients) was rejected before the handler ever ran.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFullStack_TaskList_NumericStringLimit dispatches clockwork_task_list
// with limit passed as a string ("50"). Asserts no schema-boundary error and
// that the limit is respected after coercion.
func TestFullStack_TaskList_NumericStringLimit(t *testing.T) {
	a := setupAdapter(t)

	// Seed 3 tasks so the default-50 fallback and the limit-2 path diverge
	// visibly without relying on a huge seed.
	for i := 0; i < 3; i++ {
		_, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
			"title":       fmt.Sprintf("numeric-string-arg %d", i),
			"description": "x",
		})
		require.False(t, isErr, "seed task create should not error")
	}

	// --- string-encoded limit: the bug's canonical reproduction ---
	text, isErr := callTool(t, a, "clockwork_task_list", map[string]interface{}{
		"limit": "2",
	})
	require.False(t, isErr, "string-encoded limit must not trip schema validation: %s", text)

	// Phase C: list tools return {ok, data: {items, meta}, error}.
	var env struct {
		Items []interface{}          `json:"items"`
		Meta  map[string]interface{} `json:"meta"`
	}
	parseData(t, text, &env)
	require.Len(t, env.Items, 2, "string limit \"2\" should clamp results to 2 rows")
	require.Equal(t, float64(2), env.Meta["limit"])

	// --- numeric-encoded limit still works (legacy shape) ---
	text, isErr = callTool(t, a, "clockwork_task_list", map[string]interface{}{
		"limit": float64(2),
	})
	require.False(t, isErr, "numeric limit should still work: %s", text)
	env.Items = nil
	parseData(t, text, &env)
	require.Len(t, env.Items, 2)
}

// TestFullStack_TaskList_StringLimit_NoArgValidationFailed is the minimal
// regression probe: dispatch the raw JSON-RPC message exactly as an
// LLM-backed MCP client would serialize it, and assert the response is NOT
// an ARG_VALIDATION_FAILED error envelope.
func TestFullStack_TaskList_StringLimit_NoArgValidationFailed(t *testing.T) {
	a := setupAdapter(t)

	// initialize
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
			"name": "clockwork_task_list",
			"arguments": map[string]interface{}{
				"limit": "10",
			},
		},
	})
	require.NoError(t, err)

	resp := a.Server().HandleMessage(context.Background(), msg)
	respBytes, err := json.Marshal(resp)
	require.NoError(t, err)

	// The pre-fix failure was a JSON-RPC error with message text containing
	// "got string, want number". Assert no such error envelope is returned.
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal(respBytes, &parsed))
	if errObj, ok := parsed["error"].(map[string]interface{}); ok {
		t.Fatalf("schema-boundary error leaked: %v", errObj)
	}
	require.Contains(t, parsed, "result", "expected a result envelope, got: %s", string(respBytes))
}

// TestFullStack_TaskCreate_PriorityAcceptsString verifies the priority
// schema swap from WithNumber to WithString didn't break the common
// handler path that reads priority via reqInt. Passing priority as a
// string must land the value on the created task as an integer.
func TestFullStack_TaskCreate_PriorityAcceptsString(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "string priority",
		"description": "x",
		"priority":    "4",
	})
	require.False(t, isErr, "string priority must not trip schema validation: %s", text)

	var created map[string]interface{}
	parseData(t, text, &created)
	// TaskRecord has no JSON tags, so Priority comes through capitalized.
	require.EqualValues(t, 4, created["Priority"])
}

// TestFullStack_TaskUpdate_BudgetStringRoundTrip exercises the mixed-numeric
// update path: cost_budget (float, nullable), max_retries (int), token_budget
// (int64, nullable) — all via string-encoded numerics. The presence-gated
// update pattern in handleTaskUpdate means a caller passing "0" for
// cost_budget is asserting an explicit zero budget; this test pins that
// behavior so the silent-zero helper semantics don't leak into
// "explicit-zero was requested" ambiguity here.
func TestFullStack_TaskUpdate_BudgetStringRoundTrip(t *testing.T) {
	a := setupAdapter(t)

	// Create a task to update.
	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "budget update",
		"description": "x",
	})
	require.False(t, isErr, "create: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	// Sentinel round-trip: -1 (unlimited) passed as string.
	text, isErr = callTool(t, a, "clockwork_task_update", map[string]interface{}{
		"id":              id,
		"cost_budget":     "-1",
		"max_retries":     "3",
		"max_duration_ms": "60000",
		"token_budget":    "-1",
	})
	require.False(t, isErr, "update: %s", text)

	var updated map[string]interface{}
	parseData(t, text, &updated)

	// CostBudget lives under sql.NullFloat64: {Float64: -1, Valid: true}
	cb, ok := updated["CostBudget"].(map[string]interface{})
	require.True(t, ok, "CostBudget should be a NullFloat64 struct")
	require.EqualValues(t, -1, cb["Float64"])
	require.Equal(t, true, cb["Valid"])

	require.EqualValues(t, 3, updated["MaxRetries"])

	mdm, ok := updated["MaxDurationMs"].(map[string]interface{})
	require.True(t, ok)
	require.EqualValues(t, 60000, mdm["Int64"])
	require.Equal(t, true, mdm["Valid"])

	tb, ok := updated["TokenBudget"].(map[string]interface{})
	require.True(t, ok)
	require.EqualValues(t, -1, tb["Int64"])
	require.Equal(t, true, tb["Valid"])
}
