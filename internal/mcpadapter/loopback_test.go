package mcpadapter_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"

	_ "modernc.org/sqlite"
)

// loopbackFixture bundles the loopback adapter, the sibling global adapter
// (for FSM driving the bound task through intermediate states), and the
// bound task ID. Tests that need to push the task into "doing" before
// exercising review/done call promote(t, fix).
type loopbackFixture struct {
	loopback *mcpadapter.Adapter
	global   *mcpadapter.Adapter
	taskID   string
	storeRef *sqlstore.Store
}

// store exposes the underlying sqlstore.Store for tests that need to seed
// rows directly (runs, artifacts) ahead of exercising a loopback tool.
func (fix *loopbackFixture) store() *sqlstore.Store { return fix.storeRef }

func setupLoopback(t *testing.T) *loopbackFixture {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	svc := service.New(store)

	global := mcpadapter.New(svc, nil)

	text, isErr := callTool(t, global, "torque_task_create", map[string]interface{}{
		"title":       "loopback-target",
		"description": "loopback adapter test target",
	})
	require.False(t, isErr, "seed task: %s", text)
	var seeded map[string]interface{}
	parseData(t, text, &seeded)
	taskID, ok := seeded["ID"].(string)
	require.True(t, ok, "task ID missing: %v", seeded)

	return &loopbackFixture{
		loopback: mcpadapter.NewLoopback(svc, taskID),
		global:   global,
		taskID:   taskID,
		storeRef: store,
	}
}

// promote drives the bound task through todo -> doing via the global adapter
// so loopback tests targeting "review" can exercise the FSM-valid path.
func (fix *loopbackFixture) promote(t *testing.T, status string) {
	t.Helper()
	text, isErr := callTool(t, fix.global, "torque_task_transition", map[string]interface{}{
		"id":     fix.taskID,
		"status": status,
	})
	require.False(t, isErr, "promote to %s: %s", status, text)
}

// rawJSONRPCError calls a tool by name on the given adapter and returns the
// JSON-RPC error envelope, if any. Used to verify "tool not found" responses
// without going through callTool's "result must exist" assertion.
func rawJSONRPCError(t *testing.T, a *mcpadapter.Adapter, name string, args map[string]interface{}) string {
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

	if errObj, ok := parsed["error"].(map[string]interface{}); ok {
		if msg, ok := errObj["message"].(string); ok {
			return msg
		}
	}
	return ""
}

func TestLoopback_PanicsOnEmptyTaskID(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	svc := service.New(store)

	assert.PanicsWithValue(t,
		"mcpadapter: NewLoopback requires non-empty taskID",
		func() { mcpadapter.NewLoopback(svc, "") },
	)
}

func TestLoopback_ExcludedToolsReturnNotFound(t *testing.T) {
	fix := setupLoopback(t)

	// torque_task_transition is the canonical "should not exist on
	// loopback" — agents do not drive their own FSM. If this call were to
	// hit a real handler, the loopback's task ID binding would be defeated.
	errMsg := rawJSONRPCError(t, fix.loopback, "torque_task_transition", map[string]interface{}{
		"id":     fix.taskID,
		"status": "doing",
	})
	assert.Contains(t, errMsg, "not found", "transition must not be exposed; got: %q", errMsg)

	// Sanity: the same call WORKS on the global adapter.
	errMsg = rawJSONRPCError(t, fix.global, "torque_task_transition", map[string]interface{}{
		"id":     fix.taskID,
		"status": "doing",
	})
	assert.Empty(t, errMsg, "transition must be exposed on global adapter; got error: %q", errMsg)
}

func TestLoopback_SummaryPostsCommentWithAgentAuthor(t *testing.T) {
	fix := setupLoopback(t)

	text, isErr := callTool(t, fix.loopback, "torque_task_summary", map[string]interface{}{
		"text": "Refactored streamparser; 3 new tests; all green.",
	})
	require.False(t, isErr, "summary call: %s", text)

	var comment map[string]interface{}
	parseData(t, text, &comment)
	assert.Equal(t, "task", comment["entity_type"])
	assert.Equal(t, fix.taskID, comment["entity_id"])
	assert.Equal(t, "agent", comment["author"])
	content, _ := comment["content"].(string)
	assert.Contains(t, content, "Summary: ")
	assert.Contains(t, content, "Refactored streamparser")
}

func TestLoopback_SummaryRequiresText(t *testing.T) {
	fix := setupLoopback(t)
	text, isErr := callTool(t, fix.loopback, "torque_task_summary", map[string]interface{}{})
	require.True(t, isErr, "summary without text should error: %s", text)
	code, _, field := parseError(t, text)
	assert.Equal(t, string(mcpadapter.ErrCodeArgInvalid), code)
	assert.Equal(t, "text", field)
}

func TestLoopback_BlockedSetsReasonAndTransitions(t *testing.T) {
	fix := setupLoopback(t)

	// todo -> blocked is FSM-valid directly; no promote needed.
	text, isErr := callTool(t, fix.loopback, "torque_task_blocked", map[string]interface{}{
		"reason": "Need staging API credentials.",
	})
	require.False(t, isErr, "blocked call: %s", text)

	var resp map[string]interface{}
	parseData(t, text, &resp)
	assert.Equal(t, fix.taskID, resp["task_id"])
	assert.Equal(t, "blocked", resp["status"])
	assert.Equal(t, "Need staging API credentials.", resp["blocked_reason"])

	// Re-fetch the task via the global adapter to confirm persisted state.
	text, isErr = callTool(t, fix.global, "torque_task_get", map[string]interface{}{"id": fix.taskID})
	require.False(t, isErr)
	var got map[string]interface{}
	parseData(t, text, &got)
	assert.Equal(t, "blocked", got["Status"])
	assert.Equal(t, "Need staging API credentials.", got["BlockedReason"])
}

func TestLoopback_BlockedRequiresReason(t *testing.T) {
	fix := setupLoopback(t)
	text, isErr := callTool(t, fix.loopback, "torque_task_blocked", map[string]interface{}{})
	require.True(t, isErr)
	code, _, field := parseError(t, text)
	assert.Equal(t, string(mcpadapter.ErrCodeArgInvalid), code)
	assert.Equal(t, "reason", field)
}

func TestLoopback_ReviewTransitionsAndPostsCommentWhenReasonGiven(t *testing.T) {
	fix := setupLoopback(t)

	// FSM: todo -> review is NOT valid. Promote to doing first.
	fix.promote(t, "doing")

	text, isErr := callTool(t, fix.loopback, "torque_task_review", map[string]interface{}{
		"reason": "Edge case X needs human eyes.",
	})
	require.False(t, isErr, "review call: %s", text)

	var resp map[string]interface{}
	parseData(t, text, &resp)
	assert.Equal(t, fix.taskID, resp["task_id"])
	assert.Equal(t, "review", resp["status"])

	// Confirm a comment with author=agent + the reason was posted.
	text, isErr = callTool(t, fix.global, "torque_comment_list", map[string]interface{}{
		"entity_type": "task",
		"entity_id":   fix.taskID,
	})
	require.False(t, isErr)
	var listResp map[string]interface{}
	parseData(t, text, &listResp)
	items, ok := listResp["items"].([]interface{})
	require.True(t, ok, "comment_list should return items array: %v", listResp)
	require.NotEmpty(t, items, "review with reason should post a comment")
	last, _ := items[len(items)-1].(map[string]interface{})
	assert.Equal(t, "agent", last["author"])
	excerpt, _ := last["excerpt"].(string)
	assert.Contains(t, excerpt, "Edge case X")
}

func TestLoopback_ReviewWithoutReasonOnlyTransitions(t *testing.T) {
	fix := setupLoopback(t)
	fix.promote(t, "doing")

	text, isErr := callTool(t, fix.loopback, "torque_task_review", map[string]interface{}{})
	require.False(t, isErr, "review without reason: %s", text)

	var resp map[string]interface{}
	parseData(t, text, &resp)
	assert.Equal(t, "review", resp["status"])

	// No comment should have been posted (reason was empty).
	text, isErr = callTool(t, fix.global, "torque_comment_list", map[string]interface{}{
		"entity_type": "task",
		"entity_id":   fix.taskID,
	})
	require.False(t, isErr)
	var listResp map[string]interface{}
	parseData(t, text, &listResp)
	items, _ := listResp["items"].([]interface{})
	assert.Empty(t, items, "review without reason should not post a comment")
}

func TestLoopback_ArtifactCreateBindsTaskIDImplicitly(t *testing.T) {
	fix := setupLoopback(t)

	text, isErr := callTool(t, fix.loopback, "torque_artifact_create", map[string]interface{}{
		"type":      "file",
		"file_path": "/tmp/loopback-test.log",
	})
	require.False(t, isErr, "artifact create: %s", text)

	var rec map[string]interface{}
	parseData(t, text, &rec)
	// ArtifactRecord has no JSON tags → PascalCase field names in JSON.
	assert.Equal(t, fix.taskID, rec["TaskID"], "artifact must be bound to loopback's taskID")
	assert.Equal(t, "file", rec["Type"])
	assert.Equal(t, "/tmp/loopback-test.log", rec["FilePath"])
}

func TestLoopback_ArtifactCreateRejectsMissingType(t *testing.T) {
	fix := setupLoopback(t)
	text, isErr := callTool(t, fix.loopback, "torque_artifact_create", map[string]interface{}{})
	require.True(t, isErr)
	code, _, field := parseError(t, text)
	assert.Equal(t, string(mcpadapter.ErrCodeArgInvalid), code)
	assert.Equal(t, "type", field)
}

func TestLoopback_CommentAddBindsTaskIDAndAuthorImplicitly(t *testing.T) {
	fix := setupLoopback(t)

	text, isErr := callTool(t, fix.loopback, "torque_comment_add", map[string]interface{}{
		"content": "Mid-task observation about file Y.",
	})
	require.False(t, isErr, "comment add: %s", text)

	var rec map[string]interface{}
	parseData(t, text, &rec)
	assert.Equal(t, "task", rec["entity_type"])
	assert.Equal(t, fix.taskID, rec["entity_id"])
	assert.Equal(t, "agent", rec["author"])
	assert.Equal(t, "Mid-task observation about file Y.", rec["content"])
}

func TestLoopback_CommentAddRequiresContent(t *testing.T) {
	fix := setupLoopback(t)
	text, isErr := callTool(t, fix.loopback, "torque_comment_add", map[string]interface{}{})
	require.True(t, isErr)
	code, _, field := parseError(t, text)
	assert.Equal(t, string(mcpadapter.ErrCodeArgInvalid), code)
	assert.Equal(t, "content", field)
}

func TestLoopback_SubtodoAddBindsTaskID(t *testing.T) {
	fix := setupLoopback(t)

	text, isErr := callTool(t, fix.loopback, "torque_task_subtodo_add", map[string]interface{}{
		"id":       "check-1",
		"text":     "write regression test",
		"required": true,
	})
	require.False(t, isErr, "subtodo add: %s", text)

	var items []map[string]interface{}
	parseData(t, text, &items)
	require.Len(t, items, 1)
	// Subtodo has lowercase json tags.
	assert.Equal(t, "check-1", items[0]["id"])
	assert.Equal(t, "write regression test", items[0]["text"])
	assert.Equal(t, true, items[0]["required"])
}

// TestLoopback_SubtodoAddRequiresText covers FIX-002: id is now optional
// (the server generates one when omitted), so the only remaining required
// field is text.
func TestLoopback_SubtodoAddRequiresText(t *testing.T) {
	fix := setupLoopback(t)

	text, isErr := callTool(t, fix.loopback, "torque_task_subtodo_add", map[string]interface{}{})
	require.True(t, isErr)
	code, _, field := parseError(t, text)
	assert.Equal(t, string(mcpadapter.ErrCodeArgInvalid), code)
	assert.Equal(t, "text", field)

	text, isErr = callTool(t, fix.loopback, "torque_task_subtodo_add", map[string]interface{}{"id": "x"})
	require.True(t, isErr)
	code, _, field = parseError(t, text)
	assert.Equal(t, string(mcpadapter.ErrCodeArgInvalid), code)
	assert.Equal(t, "text", field)
}

// TestLoopback_SubtodoAddOmittedIDAutoGenerates covers FIX-002: omitting id
// succeeds and returns a server-generated id under the "sub_" prefix.
func TestLoopback_SubtodoAddOmittedIDAutoGenerates(t *testing.T) {
	fix := setupLoopback(t)

	text, isErr := callTool(t, fix.loopback, "torque_task_subtodo_add", map[string]interface{}{
		"text": "write regression test",
	})
	require.False(t, isErr, "subtodo add: %s", text)

	var items []map[string]interface{}
	parseData(t, text, &items)
	require.Len(t, items, 1)
	genID, _ := items[0]["id"].(string)
	assert.True(t, strings.HasPrefix(genID, "sub_"), "generated id %q should carry the sub_ prefix", genID)
	assert.Equal(t, "write regression test", items[0]["text"])
}

func TestLoopback_SubtodoDoneFlow(t *testing.T) {
	fix := setupLoopback(t)

	_, isErr := callTool(t, fix.loopback, "torque_task_subtodo_add", map[string]interface{}{
		"id":   "check-1",
		"text": "write regression test",
	})
	require.False(t, isErr)

	text, isErr := callTool(t, fix.loopback, "torque_task_subtodo_done", map[string]interface{}{
		"id":       "check-1",
		"evidence": "abc123 / PR #42",
	})
	require.False(t, isErr, "subtodo done: %s", text)

	var items []map[string]interface{}
	parseData(t, text, &items)
	require.Len(t, items, 1)
	assert.Equal(t, "check-1", items[0]["id"])
	assert.Equal(t, true, items[0]["done"])
	assert.Equal(t, "abc123 / PR #42", items[0]["evidence"])
}

func TestLoopback_SubtodoDoneRequiresID(t *testing.T) {
	fix := setupLoopback(t)
	text, isErr := callTool(t, fix.loopback, "torque_task_subtodo_done", map[string]interface{}{})
	require.True(t, isErr)
	code, _, field := parseError(t, text)
	assert.Equal(t, string(mcpadapter.ErrCodeArgInvalid), code)
	assert.Equal(t, "id", field)
}
