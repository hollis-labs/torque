package mcpadapter_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/mcpadapter"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"

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
	return mcpadapter.New(svc, nil)
}

// setupAdapterWithFeatures creates an adapter with projects, sprints, and epics enabled.
// Used by tests that need to set project_id / sprint_id / epic_id on tasks via the service.
func setupAdapterWithFeatures(t *testing.T) *mcpadapter.Adapter {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	svc := service.New(store)
	require.NoError(t, svc.Feature.Enable("projects"))
	require.NoError(t, svc.Feature.Enable("sprints"))
	require.NoError(t, svc.Feature.Enable("epics"))
	return mcpadapter.New(svc, nil)
}

func setupTaskQueryParitySurfaces(t *testing.T) (*mcpadapter.Adapter, *httptest.Server, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	svc := service.New(store)
	return mcpadapter.New(svc, nil), httptest.NewServer(httpserver.New(svc, nil)), db
}

// dataBytes extracts the marshaled `data` field from a Phase C
// `{ok, data, error}` response text. Returns the raw JSON so callers can
// unmarshal into whatever shape they expect. Fails the test if the response
// is not ok=true.
func dataBytes(t *testing.T, text string) []byte {
	t.Helper()
	var env struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error json.RawMessage `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &env), "response must be valid JSON: %s", text)
	require.True(t, env.OK, "expected ok=true, got: %s", text)
	return env.Data
}

// parseData unmarshals the `data` field of a Phase C envelope into out.
func parseData(t *testing.T, text string, out any) {
	t.Helper()
	require.NoError(t, json.Unmarshal(dataBytes(t, text), out), "data unmarshal: %s", text)
}

// parseError unmarshals the `error` field of a Phase C envelope. Fails the
// test if ok=true.
func parseError(t *testing.T, text string) (code, message, field string) {
	t.Helper()
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Field   string `json:"field"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &env), "response must be valid JSON: %s", text)
	require.False(t, env.OK, "expected ok=false, got: %s", text)
	return env.Error.Code, env.Error.Message, env.Error.Field
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
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Fix auth bug",
		"description": "Login returns 500",
		"priority":    1,
	})
	require.False(t, isErr, "create should not error: %s", text)
	require.NotEmpty(t, text)

	// Parse the task ID from the response JSON (struct fields are uppercased).
	var created map[string]interface{}
	parseData(t, text, &created)
	id, ok := created["ID"].(string)
	require.True(t, ok, "response should contain string ID")
	require.NotEmpty(t, id)

	// Fetch the task by ID.
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{
		"id": id,
	})
	require.False(t, isErr, "get should not error: %s", text)

	var fetched map[string]interface{}
	parseData(t, text, &fetched)
	require.Equal(t, "Fix auth bug", fetched["Title"])
}

func TestFullStack_TaskCreate_Facets(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":                  "facets",
		"description":            "x",
		"kind":                   "external",
		"manual":                 true,
		"source_type":            "user",
		"source_ref":             "chrispian",
		"trust":                  "normal",
		"checkpoint_mode":        "none",
		"on_checkpoint_response": "resume",
	})
	require.False(t, isErr, "create should not error: %s", text)

	var created map[string]interface{}
	parseData(t, text, &created)
	require.Equal(t, "external", created["Kind"])
	require.Equal(t, "user", created["SourceType"])
	ref := created["SourceRef"].(map[string]interface{})
	require.Equal(t, "chrispian", ref["String"])
	require.Equal(t, true, ref["Valid"])
	require.Equal(t, "normal", created["Trust"])
	require.Equal(t, "none", created["CheckpointMode"])
	require.Equal(t, "resume", created["OnCheckpointResponse"])
}

func TestFullStack_TaskCreate_FacetDefaults(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "defaults",
		"description": "x",
	})
	require.False(t, isErr, "create should not error: %s", text)

	var created map[string]interface{}
	parseData(t, text, &created)
	require.Equal(t, "agent", created["Kind"])
	require.Equal(t, "user", created["SourceType"])
	require.Equal(t, "normal", created["Trust"])
	require.Equal(t, "none", created["CheckpointMode"])
	require.Equal(t, "resume", created["OnCheckpointResponse"])
}

func TestFullStack_TaskList_FilterByKind(t *testing.T) {
	a := setupAdapter(t)

	_, _ = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "agent one",
		"description": "x",
	})
	_, _ = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "external one",
		"description": "x",
		"kind":        "external",
		"manual":      true,
	})

	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
		"kind": "external",
	})
	require.False(t, isErr, "list should not error: %s", text)

	var envelope struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &envelope)
	require.Len(t, envelope.Items, 1)
	// Brief shape uses lowercase "kind".
	require.Equal(t, "external", envelope.Items[0]["kind"])
	require.Equal(t, false, envelope.Meta["truncated"])
	require.Equal(t, float64(1), envelope.Meta["returned"])
}

// CW-20260503-0011 (S1.1): MCP list/search tools default-exclude
// kind=internal so spawned agents and operator queries don't surface
// Reviewer end-agents and other automation primitives by accident.
// include_internal="true" / explicit kind="internal" both opt-in.
func TestFullStack_TaskList_DefaultExcludesInternal(t *testing.T) {
	a := setupAdapter(t)

	_, _ = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":         "agent one",
		"description":   "x",
		"kind":          "agent",
		"executor":      "cli",
		"agent_profile": "cli",
	})
	_, _ = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":         "internal end-agent",
		"description":   "x",
		"kind":          "internal",
		"executor":      "cli",
		"agent_profile": "reviewer",
	})

	parseList := func(text string) []map[string]interface{} {
		var envelope struct {
			Items []map[string]interface{} `json:"items"`
		}
		parseData(t, text, &envelope)
		return envelope.Items
	}

	// Default — no include_internal — hides the internal row.
	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{})
	require.False(t, isErr, "list should not error: %s", text)
	items := parseList(text)
	require.Len(t, items, 1)
	require.Equal(t, "agent", items[0]["kind"])

	// include_internal=true surfaces both.
	text, _ = callTool(t, a, "torque_task_list", map[string]interface{}{
		"include_internal": "true",
	})
	require.Len(t, parseList(text), 2)

	// Explicit kind=internal filter — internal-only.
	text, _ = callTool(t, a, "torque_task_list", map[string]interface{}{
		"kind": "internal",
	})
	internalOnly := parseList(text)
	require.Len(t, internalOnly, 1)
	require.Equal(t, "internal", internalOnly[0]["kind"])

	// torque_task_list's search param mirrors the same default-exclude
	// (FIX-006: formerly asserted via the now-removed torque_task_search).
	text, _ = callTool(t, a, "torque_task_list", map[string]interface{}{
		"search": "x",
	})
	require.Len(t, parseList(text), 1, "search default-excludes internal")

	text, _ = callTool(t, a, "torque_task_list", map[string]interface{}{
		"search":           "x",
		"include_internal": "1",
	})
	require.Len(t, parseList(text), 2, "search opt-in surfaces internal")
}

func TestFullStack_TaskUpdate_Facets(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "u",
		"description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":                     id,
		"checkpoint_mode":        "blocking",
		"on_checkpoint_response": "review",
		"source_ref":             "ctx-123",
	})
	require.False(t, isErr, "update should not error: %s", text)

	var updated map[string]interface{}
	parseData(t, text, &updated)
	require.Equal(t, "blocking", updated["CheckpointMode"])
	require.Equal(t, "review", updated["OnCheckpointResponse"])
	ref := updated["SourceRef"].(map[string]interface{})
	require.Equal(t, "ctx-123", ref["String"])
}

// TestFullStack_TaskUpdate_WritableFields exercises each field newly exposed on
// the torque_task_update MCP tool for HTTP PUT parity (CW-20260417-0007).
// Each field gets a round-trip check via the returned TaskRecord JSON.
func TestFullStack_TaskUpdate_WritableFields(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "writable-fields",
		"description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	// Scalars / simple strings / bools / numbers.
	text, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":                 id,
		"manual":             true,
		"executor":           "cli",
		"agent_profile":      "claude-code",
		"working_dir":        "/tmp/wd",
		"system_prompt":      "You are a helpful test agent.",
		"on_done":            "close",
		"on_fail":            "block",
		"on_review":          "notify",
		"on_done_merge":      "pr",
		"blocked_reason":     "waiting on X",
		"deliverable_preset": "diff-only",
		"cost_budget":        float64(1.5),
		"max_retries":        float64(7),
		"max_duration_ms":    float64(60000),
		"token_budget":       float64(200000),
	})
	require.False(t, isErr, "scalar update should not error: %s", text)
	var u1 map[string]interface{}
	parseData(t, text, &u1)
	require.Equal(t, true, u1["Manual"])
	require.Equal(t, "cli", u1["Executor"])
	require.Equal(t, "claude-code", u1["AgentProfile"])
	require.Equal(t, "/tmp/wd", u1["WorkingDir"])
	require.Equal(t, "You are a helpful test agent.", u1["SystemPrompt"])
	require.Equal(t, "close", u1["OnDone"])
	require.Equal(t, "block", u1["OnFail"])
	require.Equal(t, "notify", u1["OnReview"])
	require.Equal(t, "pr", u1["OnDoneMerge"])
	require.Equal(t, "waiting on X", u1["BlockedReason"])
	require.Equal(t, "diff-only", u1["DeliverablePreset"])
	cb := u1["CostBudget"].(map[string]interface{})
	require.Equal(t, float64(1.5), cb["Float64"])
	require.Equal(t, true, cb["Valid"])
	require.Equal(t, float64(7), u1["MaxRetries"])
	md := u1["MaxDurationMs"].(map[string]interface{})
	require.Equal(t, float64(60000), md["Int64"])
	require.Equal(t, true, md["Valid"])
	tb := u1["TokenBudget"].(map[string]interface{})
	require.Equal(t, float64(200000), tb["Int64"])
	require.Equal(t, true, tb["Valid"])

	// JSON-blob fields (passed as JSON-encoded strings).
	// Need a real task ID for depends_on to satisfy validateTaskWrites.
	text, _ = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "dep",
		"description": "x",
	})
	var dep map[string]interface{}
	parseData(t, text, &dep)
	depID := dep["ID"].(string)

	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":               id,
		"tools":            `["Read","Write"]`,
		"permissions":      `{"net":"allow"}`,
		"environment":      `{"FOO":"bar"}`,
		"files":            `["a.go","b.go"]`,
		"escalation_chain": `["oncall","lead"]`,
		"quality_gates":    `["lint","tests"]`,
		"deliverables":     `[{"type":"diff","required":true}]`,
		"depends_on":       `["` + depID + `"]`,
		"metadata":         `{"meta_key":"meta_val"}`,
	})
	require.False(t, isErr, "json-blob update should not error: %s", text)
	var u2 map[string]interface{}
	parseData(t, text, &u2)

	// Blob fields are sql.NullString on TaskRecord: {String, Valid}.
	expectNullStringJSON := func(key, wantJSON string) {
		ns := u2[key].(map[string]interface{})
		require.Equal(t, true, ns["Valid"], "%s should be Valid", key)
		require.JSONEq(t, wantJSON, ns["String"].(string), "%s JSON mismatch", key)
	}
	expectNullStringJSON("Tools", `["Read","Write"]`)
	expectNullStringJSON("Permissions", `{"net":"allow"}`)
	expectNullStringJSON("Environment", `{"FOO":"bar"}`)
	expectNullStringJSON("Files", `["a.go","b.go"]`)
	expectNullStringJSON("EscalationChain", `["oncall","lead"]`)
	expectNullStringJSON("QualityGates", `["lint","tests"]`)
	expectNullStringJSON("Deliverables", `[{"type":"diff","required":true}]`)
	expectNullStringJSON("Metadata", `{"meta_key":"meta_val"}`)

	// DependsOn (migration 027 / FK-003) is the one exception: it lives in
	// the task_dependencies join table now, not a sql.NullString column, so
	// it renders as a clean JSON array of task IDs instead of the
	// {String, Valid} shape the other blob fields still use.
	depsOut, ok := u2["DependsOn"].([]interface{})
	require.True(t, ok, "DependsOn should be a plain JSON array, got %T", u2["DependsOn"])
	require.Equal(t, []interface{}{depID}, depsOut)
}

// TestFullStack_TaskCreate_DescriptionOptional covers FIX-001 item 1: neither
// the DB column nor TaskService.Create requires description, so the MCP
// schema must not either. A create call that omits description entirely
// (not "") must succeed.
func TestFullStack_TaskCreate_DescriptionOptional(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "no description supplied",
	})
	require.False(t, isErr, "create without description should not error: %s", text)

	var created map[string]interface{}
	parseData(t, text, &created)
	require.Equal(t, "no description supplied", created["Title"])
	require.Equal(t, "", created["Description"])
}

// TestFullStack_TaskUpdate_PresenceBasedFields covers FIX-001 item 2: title,
// description, and priority must use presence-based detection like every
// other field on torque_task_update, not value-based detection. Regression
// coverage for: (a) "description":"" actually clearing the column, verified
// via a follow-up torque_task_get read of the persisted row rather than just
// checking the update call's 200 response, and (b) "priority":0 being
// detected as present and applied, not silently dropped as "unset".
func TestFullStack_TaskUpdate_PresenceBasedFields(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "presence-based",
		"description": "original description",
		"priority":    3,
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)
	require.Equal(t, float64(3), created["Priority"])

	// description: "" must clear the column, not silently no-op.
	text, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":          id,
		"description": "",
	})
	require.False(t, isErr, "clearing description should not error: %s", text)

	// Verify against a fresh read of the persisted row, not just the update
	// call's response.
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id})
	require.False(t, isErr, "get should not error: %s", text)
	var fetched map[string]interface{}
	parseData(t, text, &fetched)
	require.Equal(t, "", fetched["Description"], "description should be cleared in the persisted row")
	require.Equal(t, "presence-based", fetched["Title"], "title should be untouched")

	// priority: 0 must be detected as present and applied.
	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":       id,
		"priority": 0,
	})
	require.False(t, isErr, "setting priority=0 should not error: %s", text)
	var updated map[string]interface{}
	parseData(t, text, &updated)
	require.Equal(t, float64(0), updated["Priority"], "priority=0 should be applied, not treated as unset")

	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id})
	require.False(t, isErr, "get should not error: %s", text)
	parseData(t, text, &fetched)
	require.Equal(t, float64(0), fetched["Priority"], "priority=0 should persist in the DB row")
}

// TestFullStack_TaskUpdate_TitleCannotBeCleared covers FIX-001 item 3: once
// title is presence-based, an explicit "title":"" reaches TaskService.Update
// and must be rejected with a clean arg_invalid ValidationError, not a raw
// SQLite NOT NULL failure.
func TestFullStack_TaskUpdate_TitleCannotBeCleared(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "keep me",
		"description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":    id,
		"title": "",
	})
	require.True(t, isErr, "clearing title should error: %s", text)
	code, msg, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "title", field)
	require.NotContains(t, msg, "NOT NULL", "error should be a clean validation error, not a raw SQLite failure")

	// Title must be unchanged.
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id})
	require.False(t, isErr, "get should not error: %s", text)
	var fetched map[string]interface{}
	parseData(t, text, &fetched)
	require.Equal(t, "keep me", fetched["Title"])
}

func TestFullStack_TaskLifecycle(t *testing.T) {
	a := setupAdapter(t)

	// Create a task.
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Lifecycle task",
		"description": "Testing full lifecycle",
	})
	require.False(t, isErr, "create should not error: %s", text)

	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	// todo → doing.
	text, isErr = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id":     id,
		"status": "doing",
	})
	require.False(t, isErr, "todo→doing should succeed: %s", text)

	// doing → review.
	text, isErr = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id":     id,
		"status": "review",
	})
	require.False(t, isErr, "doing→review should succeed: %s", text)

	// review → done.
	text, isErr = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id":     id,
		"status": "done",
	})
	require.False(t, isErr, "review→done should succeed: %s", text)

	// done → doing (invalid).
	text, isErr = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id":     id,
		"status": "doing",
	})
	require.True(t, isErr, "done→doing should fail; got: %s", text)
}

func TestFullStack_Health(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_health", map[string]interface{}{})
	require.False(t, isErr, "health should not error")
	require.Contains(t, text, "running")
}

// TestFullStack_TaskList_Search is FIX-006's port of the former
// torque_task_search's TestFullStack_SearchTasks — torque_task_list's search
// param is now the only way to free-text search tasks.
func TestFullStack_TaskList_Search(t *testing.T) {
	a := setupAdapter(t)

	// Create two tasks.
	_, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Fix login bug",
		"description": "Login is broken",
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Add unit tests",
		"description": "Write tests for all handlers",
	})
	require.False(t, isErr)

	// Search for "login".
	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
		"search": "login",
	})
	require.False(t, isErr, "search should not error: %s", text)

	var envelope struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &envelope)
	require.Len(t, envelope.Items, 1, "search for 'login' should return exactly 1 result")
	require.Equal(t, false, envelope.Meta["truncated"])
}

// TestFullStack_TaskList_FilterByProjectID verifies task_list filters on project_id.
func TestFullStack_TaskList_FilterByProjectID(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	// Create the projects first (features are enabled, so task_create validates existence).
	// repo_path supplied because project_create requires it for real-shape projects;
	// distinct paths keep the two projects unambiguously separate fixtures.
	text, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Alpha",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr, "create project Alpha: %s", text)
	var projAlpha map[string]interface{}
	parseData(t, text, &projAlpha)
	alphaID := projAlpha["ID"].(string)

	text, isErr = callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Beta",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr, "create project Beta: %s", text)
	var projBeta map[string]interface{}
	parseData(t, text, &projBeta)
	betaID := projBeta["ID"].(string)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Project Alpha task",
		"description": "belongs to alpha",
		"project_id":  alphaID,
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Project Beta task",
		"description": "belongs to beta",
		"project_id":  betaID,
	})
	require.False(t, isErr)

	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{
		"project_id": alphaID,
	})
	require.False(t, isErr, "task_list with project_id should not error: %s", text)

	var envelope struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &envelope)
	require.Len(t, envelope.Items, 1)
	require.Equal(t, "Project Alpha task", envelope.Items[0]["title"])
}

// TestFullStack_TaskList_FilterByTags verifies task_list filters on tags (AND-match).
func TestFullStack_TaskList_FilterByTags(t *testing.T) {
	a := setupAdapter(t)

	_, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Backend task",
		"description": "x",
		"tags":        `["backend","p1"]`,
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Frontend task",
		"description": "x",
		"tags":        `["frontend"]`,
	})
	require.False(t, isErr)

	// Filter to tasks with "backend" tag.
	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
		"tags": `["backend"]`,
	})
	require.False(t, isErr, "task_list with tags should not error: %s", text)

	var envelope struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &envelope)
	require.Len(t, envelope.Items, 1)
	require.Equal(t, "Backend task", envelope.Items[0]["title"])
}

// TestFullStack_TaskList_RepeatedTagPredicates verifies that duplicate tag
// slugs in the MCP tags filter are normalized (CW-20260911-0081).
func TestFullStack_TaskList_RepeatedTagPredicates(t *testing.T) {
	a := setupAdapter(t)

	_, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Torque task",
		"description": "x",
		"tags":        `["torque"]`,
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Torque + API task",
		"description": "x",
		"tags":        `["torque","api"]`,
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "MCP task",
		"description": "x",
		"tags":        `["mcp"]`,
	})
	require.False(t, isErr)

	// Duplicate single tag: ["torque", "torque"] should match same as ["torque"]
	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
		"tags": `["torque", "torque"]`,
	})
	require.False(t, isErr, "task_list with duplicate tag should not error: %s", text)

	var envelope struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &envelope)
	require.Len(t, envelope.Items, 2, "duplicate single tag should match both tasks with 'torque' tag")

	titles := []string{}
	for _, item := range envelope.Items {
		titles = append(titles, item["title"].(string))
	}
	assert.ElementsMatch(t, []string{"Torque task", "Torque + API task"}, titles)

	// Duplicate with distinct tags: ["torque", "api", "torque"] should AND-match correctly
	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{
		"tags": `["torque", "api", "torque"]`,
	})
	require.False(t, isErr, "task_list with duplicate mixed tags should not error: %s", text)

	parseData(t, text, &envelope)
	require.Len(t, envelope.Items, 1, "duplicate with distinct tags should apply AND-match correctly")
	require.Equal(t, "Torque + API task", envelope.Items[0]["title"])
}

// TestFullStack_TaskList_TagPredicates_BoundaryCases verifies edge cases for tag
// filtering through the MCP adapter: mixed valid+missing tags, case-sensitive
// mismatches, whitespace/empty handling (CW-20260911-0081).
func TestFullStack_TaskList_TagPredicates_BoundaryCases(t *testing.T) {
	a := setupAdapter(t)

	_, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Torque task",
		"description": "x",
		"tags":        `["torque"]`,
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Torque+API task",
		"description": "x",
		"tags":        `["torque","api"]`,
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "MCP task",
		"description": "x",
		"tags":        `["mcp"]`,
	})
	require.False(t, isErr)

	var envelope struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}

	tests := []struct {
		name     string
		tags     string
		expected []string
	}{
		{
			name:     "valid tag + missing tag: AND-match requires both, so empty result",
			tags:     `["torque","nonexistent"]`,
			expected: []string{},
		},
		{
			name:     "case-sensitive mismatch: 'Torque' != 'torque', no match",
			tags:     `["Torque"]`,
			expected: []string{},
		},
		{
			name:     "case-sensitive mismatch with valid tag: ['Torque','api'] no match",
			tags:     `["Torque","api"]`,
			expected: []string{},
		},
		{
			name:     "whitespace-only elements: normalized out",
			tags:     `["   ","  ","    "]`,
			expected: []string{"Torque task", "Torque+API task", "MCP task"},
		},
		{
			name:     "duplicate + missing: ['torque','torque','missing'] returns empty",
			tags:     `["torque","torque","missing"]`,
			expected: []string{},
		},
		{
			name:     "whitespace + duplicate + valid: [' torque ','torque','api']",
			tags:     `[" torque ","torque","api"]`,
			expected: []string{"Torque+API task"},
		},
		{
			name:     "empty strings mixed: ['torque','','api']",
			tags:     `["torque","","api"]`,
			expected: []string{"Torque+API task"},
		},
		{
			name:     "all empty strings: ['','','']",
			tags:     `["","",""]`,
			expected: []string{"Torque task", "Torque+API task", "MCP task"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
				"tags": tc.tags,
			})
			require.False(t, isErr, "task_list should not error for %s: %s", tc.name, text)

			parseData(t, text, &envelope)
			titles := []string{}
			for _, item := range envelope.Items {
				titles = append(titles, item["title"].(string))
			}
			assert.ElementsMatch(t, tc.expected, titles, tc.name)
		})
	}
}

// TestFullStack_TaskList_FilterByManual verifies the manual filter vocabularies.
func TestFullStack_TaskList_FilterByManual(t *testing.T) {
	a := setupAdapter(t)

	// Create one manual and one non-manual task (update bypasses safety override).
	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Manual task",
		"description": "x",
	})
	var created1 map[string]interface{}
	parseData(t, text, &created1)
	id1 := created1["ID"].(string)
	// Keep manual=true (already forced by safety override).

	text, _ = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Auto task",
		"description": "x",
	})
	var created2 map[string]interface{}
	parseData(t, text, &created2)
	id2 := created2["ID"].(string)

	// Flip task2 to manual=false via update.
	_, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":     id2,
		"manual": false,
	})
	require.False(t, isErr)

	_ = id1 // id1 stays manual=true

	t.Run("manual filter returns only manual tasks", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
			"manual": "manual",
		})
		require.False(t, isErr, "should not error: %s", text)
		var env struct {
			Items []map[string]interface{} `json:"items"`
		}
		parseData(t, text, &env)
		require.Len(t, env.Items, 1)
		require.Equal(t, "Manual task", env.Items[0]["title"])
	})

	t.Run("auto filter returns only non-manual tasks", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
			"manual": "auto",
		})
		require.False(t, isErr, "should not error: %s", text)
		var env struct {
			Items []map[string]interface{} `json:"items"`
		}
		parseData(t, text, &env)
		require.Len(t, env.Items, 1)
		require.Equal(t, "Auto task", env.Items[0]["title"])
	})

	t.Run("both/omit returns all tasks", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
			"manual": "both",
		})
		require.False(t, isErr, "should not error: %s", text)
		var env struct {
			Items []map[string]interface{} `json:"items"`
		}
		parseData(t, text, &env)
		require.Len(t, env.Items, 2)
	})
}

func TestFullStack_TaskList_StrictPriorityQueries(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "zero", "description": "x", "priority": "1",
	})
	require.False(t, isErr, text)
	var zero map[string]interface{}
	parseData(t, text, &zero)
	zeroID := zero["ID"].(string)
	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{"id": zeroID, "priority": 0})
	require.False(t, isErr, text)

	text, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "one", "description": "x", "priority": "1",
	})
	require.False(t, isErr, text)
	var one map[string]interface{}
	parseData(t, text, &one)
	oneID := one["ID"].(string)

	text, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "two", "description": "x", "priority": "2",
	})
	require.False(t, isErr, text)
	var two map[string]interface{}
	parseData(t, text, &two)
	twoID := two["ID"].(string)
	largeAID := createTaskWithPriority(t, a, "large-a", "9007199254740992")
	largeBID := createTaskWithPriority(t, a, "large-b", "9007199254740993")
	minID := createTaskWithPriority(t, a, "min-int64", "-9223372036854775808")
	maxID := createTaskWithPriority(t, a, "max-int64", "9223372036854775807")

	assert.ElementsMatch(t, []string{zeroID, oneID, twoID, largeAID, largeBID, minID, maxID}, taskListIDs(t, a, map[string]interface{}{}))
	assert.Equal(t, []string{zeroID}, taskListIDs(t, a, map[string]interface{}{"priority": "0"}))
	assert.ElementsMatch(t, []string{zeroID, twoID}, taskListIDs(t, a, map[string]interface{}{"priorities": `[2,0,2]`}))
	assert.Equal(t, []string{largeBID}, taskListIDs(t, a, map[string]interface{}{"priorities": `[9007199254740993]`}),
		"JSON-number array parsing must not round >2^53 to the adjacent priority")
	assert.Equal(t, []string{minID}, taskListIDs(t, a, map[string]interface{}{"priorities": `[-9223372036854775808]`}))
	assert.Equal(t, []string{maxID}, taskListIDs(t, a, map[string]interface{}{"priorities": `[9223372036854775807]`}))

	for _, tc := range []struct {
		name  string
		args  map[string]interface{}
		field string
	}{
		{name: "bad scalar", args: map[string]interface{}{"priority": "not_an_integer"}, field: "priority"},
		{name: "fraction scalar", args: map[string]interface{}{"priority": "1.2"}, field: "priority"},
		{name: "empty priorities", args: map[string]interface{}{"priorities": `[]`}, field: "priorities"},
		{name: "fraction member", args: map[string]interface{}{"priorities": `[1.5]`}, field: "priorities"},
		{name: "overflow member", args: map[string]interface{}{"priorities": `["9223372036854775808"]`}, field: "priorities"},
		{name: "trailing garbage", args: map[string]interface{}{"priorities": `[1] garbage`}, field: "priorities"},
		{name: "two arrays", args: map[string]interface{}{"priorities": `[1] [2]`}, field: "priorities"},
		{name: "scalar conflict", args: map[string]interface{}{"priority": "1", "priorities": `[2]`}, field: "priority"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := callTool(t, a, "torque_task_list", tc.args)
			require.True(t, isErr, "task_list should reject %s: %s", tc.name, text)
			code, _, field := parseError(t, text)
			assert.Equal(t, "arg_invalid", code)
			assert.Equal(t, tc.field, field)
		})
	}
}

func TestFullStack_TaskList_StrictMalformedInputs(t *testing.T) {
	a := setupAdapter(t)

	for _, tc := range []struct {
		name  string
		args  map[string]interface{}
		field string
	}{
		{name: "malformed tags json", args: map[string]interface{}{"tags": "not-json"}, field: "tags"},
		{name: "non-string tag member", args: map[string]interface{}{"tags": "[1]"}, field: "tags"},
		{name: "bad manual", args: map[string]interface{}{"manual": "sometimes"}, field: "manual"},
		{name: "bad include internal", args: map[string]interface{}{"include_internal": "maybe"}, field: "include_internal"},
		{name: "bad verbose", args: map[string]interface{}{"verbose": "maybe"}, field: "verbose"},
		{name: "bad limit", args: map[string]interface{}{"limit": "not_an_integer"}, field: "limit"},
		{name: "unsafe native float limit", args: map[string]interface{}{"limit": float64(9007199254740993)}, field: "limit"},
		{name: "bad token bound", args: map[string]interface{}{"token_budget_gte": "1.2"}, field: "token_budget_gte"},
		{name: "non-finite float string", args: map[string]interface{}{"cost_budget_gte": "NaN"}, field: "cost_budget_gte"},
		{name: "wrong sort type", args: map[string]interface{}{"sort_dir": true}, field: "sort_dir"},
		{name: "wrong date type", args: map[string]interface{}{"created_after": 12}, field: "created_after"},
		{name: "wrong cursor type", args: map[string]interface{}{"cursor": 12}, field: "cursor"},
		{name: "wrong scalar filter type", args: map[string]interface{}{"status": []interface{}{"todo"}}, field: "status"},
		{name: "bad priority gte", args: map[string]interface{}{"priority_gte": "1.2"}, field: "priority_gte"},
		{name: "bad priority lte", args: map[string]interface{}{"priority_lte": "9223372036854775808"}, field: "priority_lte"},
		{name: "blank missing field", args: map[string]interface{}{"missing": `[" "]`}, field: "missing"},
		{name: "unknown present field", args: map[string]interface{}{"present": `["metadata"]`}, field: "present"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := callTool(t, a, "torque_task_list", tc.args)
			require.True(t, isErr, "task_list should reject %s: %s", tc.name, text)
			code, _, field := parseError(t, text)
			assert.Equal(t, "arg_invalid", code)
			assert.Equal(t, tc.field, field)
		})
	}

	for _, key := range []string{"tags_any", "tags_none", "missing", "present"} {
		for _, raw := range []interface{}{
			nil,
			"",
			" ",
			[]interface{}{nil},
			[]interface{}{"a", 1},
			`[null]`,
			`["a",1]`,
			`null`,
		} {
			for _, tool := range []string{"torque_task_list", "torque_task_facets"} {
				t.Run(fmt.Sprintf("%s bad %s %#v", tool, key, raw), func(t *testing.T) {
					text, isErr := callTool(t, a, tool, map[string]interface{}{key: raw})
					require.True(t, isErr, "%s should reject %s=%#v: %s", tool, key, raw, text)
					code, _, field := parseError(t, text)
					assert.Equal(t, "arg_invalid", code)
					assert.Equal(t, key, field)
				})
			}
		}
	}

	for _, args := range []map[string]interface{}{
		{"manual": "both"},
		{"manual": ""},
		{"parent_id": ""},
		{"parent_id": "null"},
		{"created_after": ""},
		{"updated_before": ""},
		{"include_internal": "false"},
		{"include_internal": "0"},
		{"include_internal": "yes"},
		{"verbose": "false"},
		{"tags_any": `[]`},
		{"tags_none": []interface{}{}},
		{"missing": `[]`},
		{"present": []interface{}{}},
	} {
		text, isErr := callTool(t, a, "torque_task_list", args)
		require.False(t, isErr, "valid alias/sentinel should work for args=%v: %s", args, text)
	}
}

func TestFullStack_TaskList_QueryOperatorsAndFacets(t *testing.T) {
	a := setupAdapter(t)

	zeroID := createTaskWithPriority(t, a, "zero bug ui", "0")
	_, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{"id": zeroID, "tags": `["bug","ui"]`, "cost_budget": "0"})
	require.False(t, isErr)
	twoID := createTaskWithPriority(t, a, "two backend", "2")
	_, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{"id": twoID, "tags": `["backend"]`})
	require.False(t, isErr)
	_ = createTaskWithPriority(t, a, "five no tags", "5")
	sevenID := createTaskWithPriority(t, a, "seven bug", "7")
	_, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{"id": sevenID, "tags": `["bug"]`})
	require.False(t, isErr)
	largeID := createTaskWithPriority(t, a, "large precise", "9007199254740993")
	_ = createTaskWithPriority(t, a, "adjacent large", "9007199254740992")

	args := map[string]interface{}{
		"tags_any":      []interface{}{"bug", "bug", " "},
		"tags_none":     `["backend"]`,
		"priority_gte":  "0",
		"priority_lte":  "7",
		"include_total": "true",
	}
	assert.ElementsMatch(t, []string{zeroID, sevenID}, taskListIDs(t, a, args))
	assert.Equal(t, []string{zeroID}, taskListIDs(t, a, map[string]interface{}{"present": `["cost_budget"]`, "tags_any": `["bug"]`}))
	assert.Equal(t, []string{twoID}, taskListIDs(t, a, map[string]interface{}{"priorities": `[0,2,7]`, "priority_gte": "2", "priority_lte": "5"}))
	assert.Empty(t, taskListIDs(t, a, map[string]interface{}{"missing": `["tags"]`, "tags_any": `["bug"]`}))
	assert.Equal(t, []string{largeID}, taskListIDs(t, a, map[string]interface{}{"priority_gte": "9007199254740993", "priority_lte": "9007199254740993"}))
	assert.Equal(t, []string{zeroID}, taskListIDs(t, a, map[string]interface{}{"tags": `["bug"]`, "tags_any": `["ui","backend"]`, "tags_none": `["backend"]`}))

	args["limit"] = "1"
	text, isErr := callTool(t, a, "torque_task_list", args)
	require.False(t, isErr, text)
	var first taskListCursorEnvelope
	parseData(t, text, &first)
	require.Len(t, first.Items, 1)
	assert.Equal(t, zeroID, first.Items[0]["id"])
	require.True(t, first.Meta.HasMore)
	require.NotNil(t, first.Meta.NextCursor)
	args["cursor"] = *first.Meta.NextCursor
	text, isErr = callTool(t, a, "torque_task_list", args)
	require.False(t, isErr, text)
	var second taskListCursorEnvelope
	parseData(t, text, &second)
	require.Len(t, second.Items, 1)
	assert.Equal(t, sevenID, second.Items[0]["id"])
	assert.False(t, second.Meta.HasMore)

	text, isErr = callTool(t, a, "torque_task_facets", map[string]interface{}{
		"tags_any":     `["bug"]`,
		"tags_none":    `["backend"]`,
		"priority_gte": "0",
		"priority_lte": "7",
		"dimensions":   `["tags","priority"]`,
	})
	require.False(t, isErr, text)
	var facets struct {
		MatchingCount int `json:"matching_count"`
		Facets        []struct {
			Dimension string `json:"dimension"`
			Buckets   []struct {
				Value interface{} `json:"value"`
				Count int         `json:"count"`
			} `json:"buckets"`
		} `json:"facets"`
	}
	parseData(t, text, &facets)
	assert.Equal(t, 2, facets.MatchingCount)
	require.Len(t, facets.Facets, 2)
	assert.Equal(t, "tags", facets.Facets[0].Dimension)
	assert.Equal(t, "priority", facets.Facets[1].Dimension)
	tagBuckets := map[interface{}]int{}
	for _, b := range facets.Facets[0].Buckets {
		tagBuckets[b.Value] = b.Count
	}
	assert.Equal(t, map[interface{}]int{"bug": 2, "ui": 1}, tagBuckets)
	priorityBuckets := map[interface{}]int{}
	for _, b := range facets.Facets[1].Buckets {
		priorityBuckets[b.Value] = b.Count
	}
	assert.Equal(t, map[interface{}]int{float64(0): 1, float64(7): 1}, priorityBuckets)
}

func taskListIDs(t *testing.T, a *mcpadapter.Adapter, args map[string]interface{}) []string {
	t.Helper()
	text, isErr := callTool(t, a, "torque_task_list", args)
	require.False(t, isErr, "task_list should not error: %s", text)
	var env taskListCursorEnvelope
	parseData(t, text, &env)
	out := make([]string, 0, len(env.Items))
	for _, item := range env.Items {
		out = append(out, item["id"].(string))
	}
	return out
}

func createTaskWithPriority(t *testing.T, a *mcpadapter.Adapter, title, priority string) string {
	t.Helper()
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": title, "description": "x", "priority": "1",
	})
	require.False(t, isErr, text)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)
	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{"id": id, "priority": priority})
	require.False(t, isErr, "priority update should accept %s: %s", priority, text)
	return id
}

// TestFullStack_TaskList_CombinedSearchAndProjectID verifies query+project_id AND combination.
func TestFullStack_TaskList_CombinedSearchAndProjectID(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	// Create projects. repo_path supplied because project_create requires it for real-shape
	// projects; the test exercises combined search+project_id filtering, not validation edges.
	text, isErr := callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Alpha",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var projAlpha map[string]interface{}
	parseData(t, text, &projAlpha)
	alphaID := projAlpha["ID"].(string)

	text, isErr = callTool(t, a, "torque_project_create", map[string]interface{}{
		"name":      "Beta",
		"repo_path": t.TempDir(),
	})
	require.False(t, isErr)
	var projBeta map[string]interface{}
	parseData(t, text, &projBeta)
	betaID := projBeta["ID"].(string)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Auth bug in Alpha",
		"description": "login broken",
		"project_id":  alphaID,
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Auth bug in Beta",
		"description": "login broken",
		"project_id":  betaID,
	})
	require.False(t, isErr)

	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{
		"search":     "login",
		"project_id": alphaID,
	})
	require.False(t, isErr, "combined filter should not error: %s", text)

	var envelope struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, text, &envelope)
	require.Len(t, envelope.Items, 1)
	require.Equal(t, "Auth bug in Alpha", envelope.Items[0]["title"])
}

// TestFullStack_TaskList_SearchWithSprintID is FIX-006's port of the former
// torque_task_search's TestFullStack_TaskSearch_WithSprintID, verifying
// torque_task_list's search+sprint_id combination.
func TestFullStack_TaskList_SearchWithSprintID(t *testing.T) {
	a := setupAdapterWithFeatures(t)

	// Create sprints.
	text, isErr := callTool(t, a, "torque_sprint_create", map[string]interface{}{"name": "Sprint 01"})
	require.False(t, isErr, "create sprint 01: %s", text)
	var sprint01 map[string]interface{}
	parseData(t, text, &sprint01)
	sp01ID := sprint01["ID"].(string)

	text, isErr = callTool(t, a, "torque_sprint_create", map[string]interface{}{"name": "Sprint 02"})
	require.False(t, isErr, "create sprint 02: %s", text)
	var sprint02 map[string]interface{}
	parseData(t, text, &sprint02)
	sp02ID := sprint02["ID"].(string)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Sprint task refactor",
		"description": "refactoring the auth module",
		"sprint_id":   sp01ID,
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Other sprint refactor",
		"description": "refactoring something else",
		"sprint_id":   sp02ID,
	})
	require.False(t, isErr)

	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{
		"search":    "refactor",
		"sprint_id": sp01ID,
	})
	require.False(t, isErr, "task_list search+sprint_id should not error: %s", text)

	var envelope struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, text, &envelope)
	require.Len(t, envelope.Items, 1)
	require.Equal(t, "Sprint task refactor", envelope.Items[0]["title"])
}

// TestFullStack_TaskList_EmptySearchIsNoOpFilter is FIX-006's repurposing of
// the former torque_task_search's TestFullStack_TaskSearch_EmptyQueryReturnsError.
// torque_task_search required a non-empty query and errored otherwise;
// torque_task_list's search param has no such requirement — an empty (or
// omitted) search is correct list semantics ("no substring filter"), not an
// error, so this confirms the new, correct no-op-filter behavior rather than
// porting the old error assertion 1:1.
func TestFullStack_TaskList_EmptySearchIsNoOpFilter(t *testing.T) {
	a := setupAdapter(t)

	_, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "Fix login bug",
		"description": "Login is broken",
	})
	require.False(t, isErr)

	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
		"search": "",
	})
	require.False(t, isErr, "empty search should not error: %s", text)

	var envelope struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, text, &envelope)
	require.Len(t, envelope.Items, 1, "empty search is a no-op filter — all tasks returned")
}

// TestFullStack_TaskSearchToolRemoved confirms torque_task_search no longer
// exists as a separate tool — the merge into torque_task_list (ADR-0004 §3)
// removes the old dual-tool redundancy entirely rather than keeping a
// deprecated alias, matching Issue's already-merged pattern
// (TestFullStack_IssueSearchToolRemoved in issue_tools_test.go, ENT-ISSUE).
func TestFullStack_TaskSearchToolRemoved(t *testing.T) {
	a := setupAdapter(t)
	require.False(t, toolIsRegistered(t, a, "torque_task_search"), "torque_task_search must be removed, merged into torque_task_list")
	require.True(t, toolIsRegistered(t, a, "torque_task_list"))
}

// TestFullStack_CommentSearch covers 7 sub-tests for torque_comment_search.
func TestFullStack_CommentSearch(t *testing.T) {
	a := setupAdapter(t)

	// Create two tasks.
	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "Task A", "description": "x",
	})
	var taskA map[string]interface{}
	parseData(t, text, &taskA)
	taskAID := taskA["ID"].(string)

	text, _ = callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "Task B", "description": "x",
	})
	var taskB map[string]interface{}
	parseData(t, text, &taskB)
	taskBID := taskB["ID"].(string)

	// Add 3 comments.
	_, isErr := callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task",
		"entity_id":   taskAID,
		"author":      "alice",
		"content":     "Fix the login handler please",
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task",
		"entity_id":   taskAID,
		"author":      "bob",
		"content":     "Agreed login is broken and needs attention",
	})
	require.False(t, isErr)

	_, isErr = callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task",
		"entity_id":   taskBID,
		"author":      "alice",
		"content":     "Unrelated work item on task B",
	})
	require.False(t, isErr)

	parseEnvelope := func(t *testing.T, text string) ([]map[string]interface{}, map[string]interface{}) {
		t.Helper()
		var env struct {
			Items []map[string]interface{} `json:"items"`
			Meta  map[string]interface{}   `json:"meta"`
		}
		parseData(t, text, &env)
		return env.Items, env.Meta
	}

	t.Run("content match", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_search", map[string]interface{}{
			"query": "login",
		})
		require.False(t, isErr, "should not error: %s", text)
		items, meta := parseEnvelope(t, text)
		require.Len(t, items, 2)
		require.Equal(t, false, meta["truncated"])
		// Both login comments must be present.
		contents := []string{items[0]["content"].(string), items[1]["content"].(string)}
		require.Contains(t, contents, "Fix the login handler please")
		require.Contains(t, contents, "Agreed login is broken and needs attention")
	})

	t.Run("entity_id scope", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_search", map[string]interface{}{
			"query":       "task",
			"entity_type": "task",
			"entity_id":   taskBID,
		})
		require.False(t, isErr, "should not error: %s", text)
		items, _ := parseEnvelope(t, text)
		require.Len(t, items, 1)
		require.Equal(t, taskBID, items[0]["entity_id"])
		require.Equal(t, "task", items[0]["entity_type"])
	})

	t.Run("author filter", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_search", map[string]interface{}{
			"query":  "login",
			"author": "alice",
		})
		require.False(t, isErr, "should not error: %s", text)
		items, _ := parseEnvelope(t, text)
		require.Len(t, items, 1)
		require.Equal(t, "alice", items[0]["author"])
	})

	t.Run("author not in content still matches when query matches", func(t *testing.T) {
		// "alice" is an author; query "Unrelated" doesn't mention alice by name.
		text, isErr := callTool(t, a, "torque_comment_search", map[string]interface{}{
			"query":  "Unrelated",
			"author": "alice",
		})
		require.False(t, isErr, "should not error: %s", text)
		items, _ := parseEnvelope(t, text)
		require.Len(t, items, 1)
		require.Equal(t, "alice", items[0]["author"])
		require.Equal(t, taskBID, items[0]["entity_id"])
	})

	t.Run("combined filters", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_search", map[string]interface{}{
			"query":       "login",
			"entity_type": "task",
			"entity_id":   taskAID,
			"author":      "bob",
		})
		require.False(t, isErr, "should not error: %s", text)
		items, _ := parseEnvelope(t, text)
		require.Len(t, items, 1)
		require.Equal(t, "bob", items[0]["author"])
		require.Equal(t, taskAID, items[0]["entity_id"])
	})

	t.Run("limit and truncated flag", func(t *testing.T) {
		// query "login" matches 2 comments; limit to 1.
		text, isErr := callTool(t, a, "torque_comment_search", map[string]interface{}{
			"query": "login",
			"limit": "1",
		})
		require.False(t, isErr, "should not error: %s", text)
		items, meta := parseEnvelope(t, text)
		require.Len(t, items, 1)
		// meta.truncated reflects whether cappedJSONResult trimmed; at this scale
		// it won't — the Limit is enforced at the store level.
		require.Equal(t, float64(1), meta["returned"])
	})

	t.Run("empty query returns error", func(t *testing.T) {
		text, isErr := callTool(t, a, "torque_comment_search", map[string]interface{}{
			"query": "",
		})
		require.True(t, isErr, "empty query should error; got: %s", text)
		code, _, field := parseError(t, text)
		require.Equal(t, "arg_invalid", code)
		require.Equal(t, "query", field)
	})
}

// TestFullStack_TaskCreate_ForcesManualTrue_ExplicitFalse verifies the
// CW-20260417-0133 safety override at the MCP surface: torque_task_create
// with an explicit manual=false still persists manual=true.
func TestFullStack_TaskCreate_ForcesManualTrue_ExplicitFalse(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "mcp-force-explicit",
		"description": "x",
		"manual":      false,
	})
	require.False(t, isErr, "create should not error: %s", text)

	var created map[string]interface{}
	parseData(t, text, &created)
	require.Equal(t, true, created["Manual"], "manual=false must be coerced to true per CW-20260417-0133")
}

// TestFullStack_TaskCreate_ManualOmitted_CoercedToTrue verifies that a
// torque_task_create call with no manual arg (historical default-false)
// is coerced to manual=true.
func TestFullStack_TaskCreate_ManualOmitted_CoercedToTrue(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "mcp-force-omitted",
		"description": "x",
	})
	require.False(t, isErr, "create should not error: %s", text)

	var created map[string]interface{}
	parseData(t, text, &created)
	require.Equal(t, true, created["Manual"], "omitted manual must default to true per CW-20260417-0133")
}

// TestFullStack_TaskUpdate_ManualFalse_Unchanged verifies the Update path is
// NOT subject to the CW-20260417-0133 override — operators still need to
// promote reviewed tasks to manual=false.
func TestFullStack_TaskUpdate_ManualFalse_Unchanged(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "mcp-promote-me",
		"description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)
	require.Equal(t, true, created["Manual"])

	text, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":     id,
		"manual": false,
	})
	require.False(t, isErr, "update should not error: %s", text)

	var updated map[string]interface{}
	parseData(t, text, &updated)
	require.Equal(t, false, updated["Manual"], "Update path must NOT coerce manual=false")
}

// TestFullStack_TaskUpdate_ManualStringCoercion reproduces CW-20260418-0019
// Instance 1: torque_task_update manual=true returned success but the
// DB column didn't flip. Root cause was the local reqBool helper silently
// returning false for any non-bool JSON type, including strings. If a
// caller (or a middle layer) shipped "manual": "true" as a JSON string,
// presence-detection fired, reqBool returned false, and manual was
// overwritten with 0. The mcp-go library's own GetBool helper coerces
// strings via strconv.ParseBool — our local reqBool now matches that
// behavior so the silent-drop class is closed.
func TestFullStack_TaskUpdate_ManualStringCoercion(t *testing.T) {
	a := setupAdapter(t)

	// Create a task (forced manual=true by CW-20260417-0133 override) and
	// flip it to manual=false with a properly-typed bool so we have a
	// known baseline to flip back.
	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "coerce-me",
		"description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":     id,
		"manual": false,
	})
	require.False(t, isErr, "baseline flip to false should succeed: %s", text)
	var flipped map[string]interface{}
	parseData(t, text, &flipped)
	require.Equal(t, false, flipped["Manual"])

	// The silent-drop repro: caller sends "true" as a JSON string.
	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":     id,
		"manual": "true",
	})
	require.False(t, isErr, "string-typed manual should be accepted: %s", text)
	var promoted map[string]interface{}
	parseData(t, text, &promoted)
	assert.Equal(t, true, promoted["Manual"],
		`torque_task_update manual="true" (string) must coerce to bool true; silent drop was CW-20260418-0019 Instance 1`)

	// Confirm the DB row agrees (not just the response echo).
	text, _ = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id})
	var got map[string]interface{}
	parseData(t, text, &got)
	assert.Equal(t, true, got["Manual"], "task_get must reflect the coerced manual=true in DB")
}

// TestFullStack_TaskUpdate_BoolCoercionVariants exercises every JSON shape
// a poorly-behaved client might send for a Boolean-typed MCP arg. Each
// variant must round-trip deterministically instead of silently dropping
// to the zero value. Mirrors mcp-go's own CallToolRequest.GetBool accepted
// type set.
func TestFullStack_TaskUpdate_BoolCoercionVariants(t *testing.T) {
	cases := []struct {
		label string
		input interface{}
		want  bool
	}{
		{"bool_true", true, true},
		{"bool_false", false, false},
		{"string_true", "true", true},
		{"string_false", "false", false},
		{"string_1", "1", true},
		{"string_0", "0", false},
		{"float64_1", float64(1), true},
		{"float64_0", float64(0), false},
		{"int_1", 1, true},
		{"int_0", 0, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.label, func(t *testing.T) {
			a := setupAdapter(t)
			text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
				"title":       "bool-variant",
				"description": "x",
			})
			var created map[string]interface{}
			parseData(t, text, &created)
			id := created["ID"].(string)

			// Baseline: flip to the opposite of tc.want so we can detect an
			// actual change.
			text, _ = callTool(t, a, "torque_task_update", map[string]interface{}{
				"id":     id,
				"manual": !tc.want,
			})
			var baseline map[string]interface{}
			parseData(t, text, &baseline)
			require.Equal(t, !tc.want, baseline["Manual"])

			text, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{
				"id":     id,
				"manual": tc.input,
			})
			require.False(t, isErr, "update should succeed for %v: %s", tc.input, text)
			var got map[string]interface{}
			parseData(t, text, &got)
			assert.Equal(t, tc.want, got["Manual"],
				"manual=%v (%T) should coerce to %v", tc.input, tc.input, tc.want)
		})
	}
}

// taskListCursorEnvelope mirrors torque_task_list's PRIM-001/PRIM-002
// {items, meta} response shape for test parsing.
type taskListCursorEnvelope struct {
	Items []map[string]interface{} `json:"items"`
	Meta  struct {
		Truncated  bool    `json:"truncated"`
		Returned   int     `json:"returned"`
		Limit      int     `json:"limit"`
		Total      *int    `json:"total"`
		HasMore    bool    `json:"has_more"`
		NextCursor *string `json:"next_cursor"`
	} `json:"meta"`
}

func TestFullStack_TaskList_HTTPMCPParity_ComposedQuery(t *testing.T) {
	a, ts, db := setupTaskQueryParitySurfaces(t)
	defer ts.Close()

	parentText, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "parent", "description": "x"})
	require.False(t, isErr, "parent create: %s", parentText)
	var parent map[string]interface{}
	parseData(t, parentText, &parent)
	parentID := parent["ID"].(string)

	makeTask := func(title string, args map[string]interface{}) string {
		t.Helper()
		args["title"] = title
		args["description"] = "query parity needle"
		text, isErr := callTool(t, a, "torque_task_create", args)
		require.False(t, isErr, "create %s: %s", title, text)
		var rec map[string]interface{}
		parseData(t, text, &rec)
		return rec["ID"].(string)
	}
	matchA := makeTask("match-a", map[string]interface{}{
		"priority": "0", "tags": `["alpha","beta"]`, "agent_profile": "codex", "launch_profile": "cli-fast",
		"parent_id": parentID, "cost_budget": "75.5", "token_budget": "9000", "max_duration_ms": "60000", "max_retries": "2",
	})
	matchB := makeTask("match-b", map[string]interface{}{
		"priority": "2", "tags": `["alpha","beta"]`, "agent_profile": "codex", "launch_profile": "cli-fast",
		"parent_id": parentID, "cost_budget": "125", "token_budget": "12000", "max_duration_ms": "90000", "max_retries": "4",
	})
	wrongTag := makeTask("wrong-tag", map[string]interface{}{"priority": "0", "tags": `["alpha"]`, "agent_profile": "codex", "launch_profile": "cli-fast", "parent_id": parentID, "cost_budget": "90", "token_budget": "9000", "max_duration_ms": "60000", "max_retries": "2"})
	wrongProfile := makeTask("wrong-profile", map[string]interface{}{"priority": "0", "tags": `["alpha","beta"]`, "agent_profile": "other", "launch_profile": "cli-fast", "parent_id": parentID, "cost_budget": "90", "token_budget": "9000", "max_duration_ms": "60000", "max_retries": "2"})
	wrongCreated := makeTask("wrong-created", map[string]interface{}{"priority": "0", "tags": `["alpha","beta"]`, "agent_profile": "codex", "launch_profile": "cli-fast", "parent_id": parentID, "cost_budget": "90", "token_budget": "9000", "max_duration_ms": "60000", "max_retries": "2"})
	wrongCost := makeTask("wrong-cost", map[string]interface{}{"priority": "0", "tags": `["alpha","beta"]`, "agent_profile": "codex", "launch_profile": "cli-fast", "parent_id": parentID, "cost_budget": "10", "token_budget": "9000", "max_duration_ms": "60000", "max_retries": "2"})
	wrongToken := makeTask("wrong-token", map[string]interface{}{"priority": "0", "tags": `["alpha","beta"]`, "agent_profile": "codex", "launch_profile": "cli-fast", "parent_id": parentID, "cost_budget": "90", "token_budget": "100", "max_duration_ms": "60000", "max_retries": "2"})
	wrongDuration := makeTask("wrong-duration", map[string]interface{}{"priority": "0", "tags": `["alpha","beta"]`, "agent_profile": "codex", "launch_profile": "cli-fast", "parent_id": parentID, "cost_budget": "90", "token_budget": "9000", "max_duration_ms": "1000", "max_retries": "2"})
	wrongRetries := makeTask("wrong-retries", map[string]interface{}{"priority": "0", "tags": `["alpha","beta"]`, "agent_profile": "codex", "launch_profile": "cli-fast", "parent_id": parentID, "cost_budget": "90", "token_budget": "9000", "max_duration_ms": "60000", "max_retries": "9"})
	internalID := makeTask("internal-match-hidden", map[string]interface{}{
		"kind": "internal", "executor": "cli", "priority": "0", "tags": `["alpha","beta"]`, "agent_profile": "codex", "launch_profile": "cli-fast",
		"parent_id": parentID, "cost_budget": "90", "token_budget": "9000", "max_duration_ms": "60000", "max_retries": "2",
	})

	for _, id := range []string{matchA, matchB, internalID, wrongTag, wrongProfile, wrongCreated, wrongCost, wrongToken, wrongDuration, wrongRetries} {
		_, isErr = callTool(t, a, "torque_task_transition", map[string]interface{}{"id": id, "status": "doing"})
		require.False(t, isErr)
		_, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{"id": id, "manual": false})
		require.False(t, isErr)
	}
	for _, id := range []string{matchA, internalID, wrongTag, wrongProfile, wrongCreated, wrongCost, wrongToken, wrongDuration, wrongRetries} {
		_, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{"id": id, "priority": "0"})
		require.False(t, isErr)
	}
	require.NoError(t, setTaskTimes(db, matchA, "2026-09-11 01:00:00.000000123", "2026-09-11 04:00:00.000000123"))
	require.NoError(t, setTaskTimes(db, matchB, "2026-09-11 02:00:00.000000456", "2026-09-11 05:00:00.000000456"))
	require.NoError(t, setTaskTimes(db, internalID, "2026-09-11 03:00:00.000000789", "2026-09-11 06:00:00.000000789"))
	for _, id := range []string{wrongTag, wrongProfile, wrongCost, wrongToken, wrongDuration, wrongRetries} {
		require.NoError(t, setTaskTimes(db, id, "2026-09-11 01:30:00.000000333", "2026-09-11 04:30:00.000000333"))
	}
	require.NoError(t, setTaskTimes(db, wrongCreated, "2026-09-11 03:30:00.000000333", "2026-09-11 04:30:00.000000333"))

	httpIDs := httpTaskIDs(t, ts.URL+"/api/v1/tasks?"+url.Values{
		"status":              {"doing"},
		"priority":            {"0,2,0"},
		"tags":                {"alpha,beta"},
		"manual":              {"auto"},
		"parent_id":           {parentID},
		"agent_profile":       {"codex"},
		"launch_profile":      {"cli-fast"},
		"created_after":       {"2026-09-11T00:00:00Z"},
		"created_before":      {"2026-09-11T03:00:00.000000500Z"},
		"updated_after":       {"2026-09-11T03:00:00Z"},
		"cost_budget_gte":     {"50"},
		"cost_budget_lte":     {"150"},
		"token_budget_gte":    {"8000"},
		"token_budget_lte":    {"13000"},
		"max_duration_ms_gte": {"50000"},
		"max_duration_ms_lte": {"100000"},
		"max_retries_gte":     {"1"},
		"max_retries_lte":     {"4"},
		"sort_by":             {"updated_at"},
		"sort_dir":            {"desc"},
	}.Encode())
	mcpIDs := mcpTaskIDs(t, a, map[string]interface{}{
		"status": "doing", "priorities": `[0,2,0]`, "tags": `["alpha","beta"]`, "manual": "auto", "parent_id": parentID,
		"agent_profile": "codex", "launch_profile": "cli-fast", "created_after": "2026-09-11T00:00:00Z",
		"created_before": "2026-09-11T03:00:00.000000500Z", "updated_after": "2026-09-11T03:00:00Z",
		"cost_budget_gte": "50", "cost_budget_lte": "150", "token_budget_gte": "8000", "token_budget_lte": "13000",
		"max_duration_ms_gte": "50000", "max_duration_ms_lte": "100000", "max_retries_gte": "1", "max_retries_lte": "4",
		"sort_by": "updated_at", "sort_dir": "desc", "include_total": "true",
	})
	require.Equal(t, []string{matchB, matchA}, httpIDs)
	require.Equal(t, httpIDs, mcpIDs)
}

type decodedTaskFacets struct {
	MatchingCount int      `json:"matching_count"`
	BucketLimit   int      `json:"bucket_limit"`
	Dimensions    []string `json:"dimensions"`
	Facets        []struct {
		Dimension     string `json:"dimension"`
		TotalDistinct int    `json:"total_distinct"`
		Returned      int    `json:"returned"`
		Truncated     bool   `json:"truncated"`
		Buckets       []struct {
			Value any `json:"value"`
			Count int `json:"count"`
		} `json:"buckets"`
	} `json:"facets"`
}

func decodeHTTPTaskFacets(t *testing.T, rawURL string) decodedTaskFacets {
	t.Helper()
	resp, err := http.Get(rawURL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	defer resp.Body.Close()
	var out decodedTaskFacets
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return out
}

func taskFacetByDimension(t *testing.T, got decodedTaskFacets, dim string) []struct {
	Value any `json:"value"`
	Count int `json:"count"`
} {
	t.Helper()
	for _, f := range got.Facets {
		if f.Dimension == dim {
			return f.Buckets
		}
	}
	t.Fatalf("missing facet dimension %s in %+v", dim, got.Dimensions)
	return nil
}

func TestFullStack_TaskFacets_HTTPMCPParityAndValidation(t *testing.T) {
	a, ts, _ := setupTaskQueryParitySurfaces(t)
	defer ts.Close()
	create := func(title string, args map[string]any) string {
		t.Helper()
		body := map[string]any{"title": title, "description": "facet parity"}
		for k, v := range args {
			body[k] = v
		}
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewReader(raw))
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		defer resp.Body.Close()
		var created map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
		return created["id"].(string)
	}
	update := func(id string, patch map[string]any) {
		t.Helper()
		raw, err := json.Marshal(patch)
		require.NoError(t, err)
		req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewReader(raw))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}
	id1 := create("facet parity a", map[string]any{"tags": []string{"api", "torque"}, "agent_profile": "codex"})
	id2 := create("facet parity b", map[string]any{"tags": []string{"api"}, "agent_profile": "codex"})
	_ = create("facet parity c", map[string]any{"agent_profile": "other"})
	update(id1, map[string]any{"priority": 0, "manual": false})
	update(id2, map[string]any{"priority": 0, "manual": false})

	httpFacets := decodeHTTPTaskFacets(t, ts.URL+"/api/v1/tasks/facets?search=facet+parity&priority=0,1&dimensions=priority,manual,tags,tags&bucket_limit=10")
	require.Equal(t, 2, httpFacets.MatchingCount)
	assert.Equal(t, []string{"priority", "manual", "tags"}, httpFacets.Dimensions)
	assert.Equal(t, float64(0), taskFacetByDimension(t, httpFacets, "priority")[0].Value)
	assert.Equal(t, 2, taskFacetByDimension(t, httpFacets, "priority")[0].Count)
	assert.Equal(t, false, taskFacetByDimension(t, httpFacets, "manual")[0].Value)
	assert.Equal(t, 2, taskFacetByDimension(t, httpFacets, "manual")[0].Count)
	assert.Equal(t, "api", taskFacetByDimension(t, httpFacets, "tags")[0].Value)
	assert.Equal(t, 2, taskFacetByDimension(t, httpFacets, "tags")[0].Count)

	defaultHTTP := decodeHTTPTaskFacets(t, ts.URL+"/api/v1/tasks/facets?search=facet+parity")
	assert.Equal(t, 3, defaultHTTP.MatchingCount)
	assert.Equal(t, 50, defaultHTTP.BucketLimit)
	assert.Len(t, defaultHTTP.Dimensions, 12)
	projectBuckets := taskFacetByDimension(t, defaultHTTP, "project_id")
	require.Len(t, projectBuckets, 1)
	assert.Nil(t, projectBuckets[0].Value)
	assert.Equal(t, 3, projectBuckets[0].Count)

	noMatch := decodeHTTPTaskFacets(t, ts.URL+"/api/v1/tasks/facets?search=definitely-no-match")
	assert.Equal(t, 0, noMatch.MatchingCount)
	assert.Equal(t, 50, noMatch.BucketLimit)
	assert.Len(t, noMatch.Dimensions, 12)
	for _, f := range noMatch.Facets {
		assert.Empty(t, f.Buckets)
		assert.Equal(t, 0, f.TotalDistinct)
		assert.Equal(t, 0, f.Returned)
		assert.False(t, f.Truncated)
	}

	text, isErr := callTool(t, a, "torque_task_facets", map[string]interface{}{
		"search":       "facet parity",
		"priorities":   []interface{}{0, 1, 0},
		"dimensions":   []interface{}{"priority", "manual", "tags", "tags"},
		"bucket_limit": "10",
	})
	require.False(t, isErr, "mcp facets should not error: %s", text)
	var mcpFacets decodedTaskFacets
	parseData(t, text, &mcpFacets)
	assert.Equal(t, httpFacets.MatchingCount, mcpFacets.MatchingCount)
	assert.Equal(t, httpFacets.Dimensions, mcpFacets.Dimensions)
	assert.Equal(t, taskFacetByDimension(t, httpFacets, "tags"), taskFacetByDimension(t, mcpFacets, "tags"))

	for _, rawQuery := range []string{
		"dimensions=", "limit=0", "offset=0", "cursor=", "sort_by=",
		"dimensions=missing", "bucket_limit=-1", "bucket_limit=9223372036854775808",
		"dimensions=status&dimensions=manual",
	} {
		resp, err := http.Get(ts.URL + "/api/v1/tasks/facets?" + rawQuery)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, rawQuery)
		resp.Body.Close()
	}
	for _, args := range []map[string]interface{}{
		{"dimensions": "[]"},
		{"dimensions": ""},
		{"limit": "0"},
		{"offset": "0"},
		{"cursor": ""},
		{"sort_by": ""},
		{"dimensions": `[null]`},
		{"tags": `[null]`},
		{"tags": `[1]`},
		{"dimensions": `["missing"]`},
		{"bucket_limit": "-1"},
		{"bucket_limit": "9223372036854775808"},
	} {
		text, isErr := callTool(t, a, "torque_task_facets", args)
		require.True(t, isErr, "expected facet arg rejection for %#v: %s", args, text)
		code, _, field := parseError(t, text)
		assert.Equal(t, "arg_invalid", code)
		assert.NotEmpty(t, field)
	}
}

func TestFullStack_TaskFacets_MCPByteCapTrimsBucketsOnly(t *testing.T) {
	a, ts, db := setupTaskQueryParitySurfaces(t)
	defer ts.Close()
	long := strings.Repeat("x", 900)
	for i := 0; i < 220; i++ {
		id := fmt.Sprintf("CW-20260911-7%03d", i)
		launch := fmt.Sprintf("launch-%03d-%s", i, long)
		_, err := db.Exec(`INSERT INTO tasks (id, title, description, status, priority, manual, kind, launch_profile) VALUES (?, ?, 'cap fixture', 'todo', 2, 0, 'agent', ?)`, id, "facet cap", launch)
		require.NoError(t, err)
	}
	httpFacets := decodeHTTPTaskFacets(t, ts.URL+"/api/v1/tasks/facets?search=facet+cap&dimensions=launch_profile&bucket_limit=200")
	require.Equal(t, 220, httpFacets.MatchingCount)
	require.Len(t, httpFacets.Facets, 1)
	assert.Equal(t, 220, httpFacets.Facets[0].TotalDistinct)
	assert.Equal(t, 200, httpFacets.Facets[0].Returned)
	assert.Len(t, httpFacets.Facets[0].Buckets, httpFacets.Facets[0].Returned)
	assert.True(t, httpFacets.Facets[0].Truncated)
	assert.Contains(t, httpFacets.Facets[0].Buckets[0].Value.(string), "launch-000-")
	assert.Contains(t, httpFacets.Facets[0].Buckets[199].Value.(string), "launch-199-")
	assert.Equal(t, 1, httpFacets.Facets[0].Buckets[0].Count)

	text, isErr := callTool(t, a, "torque_task_facets", map[string]interface{}{
		"search":       "facet cap",
		"dimensions":   []interface{}{"launch_profile"},
		"bucket_limit": "200",
	})
	require.False(t, isErr, "mcp facets should trim, not fail: %s", text)
	require.LessOrEqual(t, len([]byte(text)), 100*1024)
	var got decodedTaskFacets
	parseData(t, text, &got)
	require.Equal(t, 220, got.MatchingCount)
	require.Len(t, got.Facets, 1)
	assert.Equal(t, 220, got.Facets[0].TotalDistinct)
	assert.True(t, got.Facets[0].Truncated)
	assert.Len(t, got.Facets[0].Buckets, got.Facets[0].Returned)
	assert.Less(t, got.Facets[0].Returned, 200)
	for _, b := range got.Facets[0].Buckets {
		require.IsType(t, "", b.Value)
		assert.Contains(t, b.Value.(string), long)
	}

	giant := strings.Repeat("y", 110*1024)
	_, err := db.Exec(`INSERT INTO tasks (id, title, description, status, priority, manual, kind, launch_profile) VALUES ('CW-20260911-7999', 'facet giant', 'giant fixture', 'todo', 2, 0, 'agent', ?)`, giant)
	require.NoError(t, err)
	text, isErr = callTool(t, a, "torque_task_facets", map[string]interface{}{
		"search":       "facet giant",
		"dimensions":   []interface{}{"launch_profile"},
		"bucket_limit": "1",
	})
	require.False(t, isErr, "oversized single bucket should be omitted, not fail: %s", text)
	require.LessOrEqual(t, len([]byte(text)), 100*1024)
	var giantGot decodedTaskFacets
	parseData(t, text, &giantGot)
	require.Equal(t, 1, giantGot.MatchingCount)
	require.Len(t, giantGot.Facets, 1)
	assert.Equal(t, 1, giantGot.Facets[0].TotalDistinct)
	assert.Equal(t, 0, giantGot.Facets[0].Returned)
	assert.Empty(t, giantGot.Facets[0].Buckets)
	assert.True(t, giantGot.Facets[0].Truncated)
}

func TestFullStack_TaskList_HTTPMCPParity_CursorTraversal(t *testing.T) {
	a, ts, db := setupTaskQueryParitySurfaces(t)
	defer ts.Close()

	var seededIDs []string
	for i := 0; i < 5; i++ {
		text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": fmt.Sprintf("page-%d", i), "description": "cursor parity"})
		require.False(t, isErr, "create: %s", text)
		var rec map[string]interface{}
		parseData(t, text, &rec)
		id := rec["ID"].(string)
		seededIDs = append(seededIDs, id)
		require.NoError(t, setTaskTimes(db, id, fmt.Sprintf("2026-09-11 00:00:00.%09d", i+1), fmt.Sprintf("2026-09-11 00:00:01.%09d", i+1)))
	}

	var httpIDs []string
	var cursor string
	for pages := 0; pages < 4; pages++ {
		cursorMode := cursor != ""
		q := url.Values{"limit": {"2"}, "sort_by": {"created_at"}, "sort_dir": {"asc"}}
		if cursorMode {
			q.Set("cursor", cursor)
		}
		page := httpTaskPage(t, ts.URL+"/api/v1/tasks?"+q.Encode())
		require.Equal(t, 5, int(page["total"].(float64)))
		for _, item := range page["tasks"].([]interface{}) {
			httpIDs = append(httpIDs, item.(map[string]interface{})["id"].(string))
		}
		if !page["has_more"].(bool) {
			require.Nil(t, page["next_cursor"])
			require.Nil(t, page["next_offset"])
			require.Equal(t, seededIDs, httpIDs)
			descIDs := httpTaskIDs(t, ts.URL+"/api/v1/tasks?limit=5&sort_by=created_at&sort_dir=desc")
			require.Equal(t, reverseStrings(seededIDs), descIDs)
			mcpIDs := mcpTaskIDsAllPages(t, a, map[string]interface{}{"limit": "2", "sort_by": "created_at", "sort_dir": "asc", "include_total": "true"})
			require.Equal(t, mcpIDs, httpIDs)
			require.Len(t, httpIDs, 5)
			return
		}
		next, ok := page["next_cursor"].(string)
		require.True(t, ok)
		continuation := page["continuation"].(map[string]interface{})
		if cursorMode {
			require.Equal(t, next, continuation["cursor"])
			require.Nil(t, page["next_offset"])
		} else {
			require.NotContains(t, continuation, "cursor")
			require.NotNil(t, page["next_offset"])
		}
		cursor = next
	}
	t.Fatal("cursor traversal did not terminate within expected page count")
}

func TestFullStack_TaskList_IncludeTotalAndVerboseByteCapTraversal(t *testing.T) {
	a := setupAdapter(t)
	for i := 0; i < 70; i++ {
		text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
			"title":       fmt.Sprintf("fat-%02d", i),
			"description": strings.Repeat(fmt.Sprintf("payload-%02d ", i), 200),
			"metadata":    fmt.Sprintf(`{"blob":%q}`, strings.Repeat("x", 800)),
		})
		require.False(t, isErr, "create: %s", text)
	}

	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{"limit": "5"})
	require.False(t, isErr, "task_list default total omitted: %s", text)
	var first taskListCursorEnvelope
	parseData(t, text, &first)
	require.Nil(t, first.Meta.Total)

	var seen []string
	args := map[string]interface{}{"limit": "70", "verbose": "true", "include_total": "true"}
	for pages := 0; pages < 80; pages++ {
		text, isErr = callTool(t, a, "torque_task_list", args)
		require.False(t, isErr, "verbose page should not error: %s", text)
		require.Less(t, len(text), 100*1024)
		var env taskListCursorEnvelope
		parseData(t, text, &env)
		require.NotNil(t, env.Meta.Total)
		require.Equal(t, 70, *env.Meta.Total)
		for _, item := range env.Items {
			seen = append(seen, item["ID"].(string))
		}
		if !env.Meta.HasMore {
			require.Nil(t, env.Meta.NextCursor)
			require.Len(t, seen, 70)
			require.Len(t, uniqueStrings(seen), 70)
			return
		}
		require.NotNil(t, env.Meta.NextCursor)
		args["cursor"] = *env.Meta.NextCursor
	}
	t.Fatal("verbose byte-cap traversal did not terminate")
}

func TestFullStack_TaskList_RejectsNullTagMemberBeforeSanitizeBroadens(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{"tags": `[null]`})
	require.True(t, isErr, "task_list should reject null tag member: %s", text)
	code, _, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "tags", field)

	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{"statuses": `["todo",null]`})
	require.True(t, isErr, "task_list should reject null status member: %s", text)
	code, _, field = parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "statuses", field)
}

func setTaskTimes(db *sql.DB, id, createdAt, updatedAt string) error {
	_, err := db.Exec(`UPDATE tasks SET created_at = ?, updated_at = ? WHERE id = ?`, createdAt, updatedAt, id)
	return err
}

func httpTaskPage(t *testing.T, rawURL string) map[string]interface{} {
	t.Helper()
	resp, err := http.Get(rawURL)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	defer resp.Body.Close()
	var page map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&page))
	return page
}

func httpTaskIDs(t *testing.T, rawURL string) []string {
	t.Helper()
	page := httpTaskPage(t, rawURL)
	rawTasks := page["tasks"].([]interface{})
	ids := make([]string, 0, len(rawTasks))
	for _, raw := range rawTasks {
		ids = append(ids, raw.(map[string]interface{})["id"].(string))
	}
	return ids
}

func mcpTaskIDs(t *testing.T, a *mcpadapter.Adapter, args map[string]interface{}) []string {
	t.Helper()
	text, isErr := callTool(t, a, "torque_task_list", args)
	require.False(t, isErr, "task_list should not error: %s", text)
	var env taskListCursorEnvelope
	parseData(t, text, &env)
	ids := make([]string, 0, len(env.Items))
	for _, item := range env.Items {
		ids = append(ids, item["id"].(string))
	}
	if _, ok := args["include_total"]; ok {
		require.NotNil(t, env.Meta.Total)
	}
	return ids
}

func mcpTaskIDsAllPages(t *testing.T, a *mcpadapter.Adapter, args map[string]interface{}) []string {
	t.Helper()
	pageArgs := map[string]interface{}{}
	for k, v := range args {
		pageArgs[k] = v
	}
	var ids []string
	for {
		text, isErr := callTool(t, a, "torque_task_list", pageArgs)
		require.False(t, isErr, "task_list should not error: %s", text)
		var env taskListCursorEnvelope
		parseData(t, text, &env)
		if _, ok := pageArgs["include_total"]; ok {
			require.NotNil(t, env.Meta.Total)
			require.Equal(t, 5, *env.Meta.Total)
		}
		for _, item := range env.Items {
			ids = append(ids, item["id"].(string))
		}
		if !env.Meta.HasMore {
			require.Nil(t, env.Meta.NextCursor)
			break
		}
		require.NotNil(t, env.Meta.NextCursor)
		pageArgs["cursor"] = *env.Meta.NextCursor
	}
	return ids
}

func reverseStrings(in []string) []string {
	out := make([]string, len(in))
	for i := range in {
		out[len(in)-1-i] = in[i]
	}
	return out
}

func uniqueStrings(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, v := range in {
		out[v] = true
	}
	return out
}

// TestFullStack_TaskList_CursorPagination_NoDuplicatesOrSkips is PRIM-001's
// acceptance criterion: paging through torque_task_list via meta.next_cursor
// must visit every row exactly once, even when many rows share the same
// default sort_by (priority) value — the id tiebreak (DEC-001) is what makes
// that true; without it, ties in the sort column would make page boundaries
// nondeterministic and duplicate/skip rows.
func TestFullStack_TaskList_CursorPagination_NoDuplicatesOrSkips(t *testing.T) {
	a := setupAdapter(t)

	const total = 9
	created := make(map[string]bool, total)
	for i := 0; i < total; i++ {
		text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
			"title":       fmt.Sprintf("task %d", i),
			"description": "x",
			// Priority omitted so every task defaults to priority=2 —
			// forces every page boundary to rely on the id tiebreak rather
			// than a naturally-distinct sort value.
		})
		require.False(t, isErr, "create should not error: %s", text)
		var rec map[string]interface{}
		parseData(t, text, &rec)
		created[rec["ID"].(string)] = true
	}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; ; pages++ {
		require.LessOrEqual(t, pages, total, "too many pages — likely an infinite loop from a broken cursor")

		args := map[string]interface{}{"limit": "4"}
		if cursor != "" {
			args["cursor"] = cursor
		}
		text, isErr := callTool(t, a, "torque_task_list", args)
		require.False(t, isErr, "list should not error: %s", text)

		var env taskListCursorEnvelope
		parseData(t, text, &env)

		for _, item := range env.Items {
			id := item["id"].(string)
			require.False(t, seen[id], "duplicate id %s seen across pages", id)
			seen[id] = true
		}

		if !env.Meta.HasMore {
			require.Nil(t, env.Meta.NextCursor, "next_cursor must be null once exhausted")
			break
		}
		require.NotNil(t, env.Meta.NextCursor, "next_cursor must be set when has_more=true")
		cursor = *env.Meta.NextCursor
	}

	require.Len(t, seen, total, "expected every created task to appear exactly once across pages")
	for id := range created {
		require.True(t, seen[id], "task %s missing from paged results", id)
	}
}

// TestFullStack_TaskList_HasMoreAndNextCursorAccuracy is PRIM-001's
// acceptance criterion: meta.has_more/next_cursor must accurately reflect
// whether more rows exist, verified against a dataset large enough to need
// a second page (total > limit).
func TestFullStack_TaskList_HasMoreAndNextCursorAccuracy(t *testing.T) {
	a := setupAdapter(t)

	const total = 7
	const pageSize = 5
	for i := 0; i < total; i++ {
		_, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
			"title":       fmt.Sprintf("task %d", i),
			"description": "x",
		})
		require.False(t, isErr)
	}

	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
		"limit": fmt.Sprintf("%d", pageSize),
	})
	require.False(t, isErr, "list should not error: %s", text)
	var page1 taskListCursorEnvelope
	parseData(t, text, &page1)
	require.Equal(t, pageSize, page1.Meta.Returned)
	require.Equal(t, pageSize, page1.Meta.Limit)
	require.True(t, page1.Meta.HasMore, "7 rows over a limit of 5 must report has_more=true")
	require.NotNil(t, page1.Meta.NextCursor)
	require.Len(t, page1.Items, pageSize)

	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{
		"limit":  fmt.Sprintf("%d", pageSize),
		"cursor": *page1.Meta.NextCursor,
	})
	require.False(t, isErr, "list should not error: %s", text)
	var page2 taskListCursorEnvelope
	parseData(t, text, &page2)
	require.Equal(t, total-pageSize, page2.Meta.Returned)
	require.False(t, page2.Meta.HasMore, "remaining 2 rows exactly fill the last page")
	require.Nil(t, page2.Meta.NextCursor, "next_cursor must be null once exhausted")
	require.Len(t, page2.Items, total-pageSize)

	seen := map[string]bool{}
	for _, it := range page1.Items {
		seen[it["id"].(string)] = true
	}
	for _, it := range page2.Items {
		id := it["id"].(string)
		require.False(t, seen[id], "task %s appeared on both pages", id)
	}
}

// TestFullStack_TaskList_InvalidSortBy is PRIM-002's acceptance criterion:
// an unrecognized sort_by must return a clean error.code=arg_invalid rather
// than silently ignoring the value or erroring at the SQL layer.
func TestFullStack_TaskList_InvalidSortBy(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
		"sort_by": "not_a_real_field",
	})
	require.True(t, isErr, "list should error on invalid sort_by: %s", text)

	code, _, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "sort_by", field)
}

// TestFullStack_TaskList_InvalidSortDir mirrors
// TestFullStack_TaskList_InvalidSortBy for sort_dir.
func TestFullStack_TaskList_InvalidSortDir(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
		"sort_dir": "sideways",
	})
	require.True(t, isErr, "list should error on invalid sort_dir: %s", text)

	code, _, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "sort_dir", field)
}

// TestFullStack_TaskList_CursorSortMismatchRejected verifies DEC-001's
// cursor/sort binding: a cursor issued under one sort_by/sort_dir is
// rejected with arg_invalid if replayed against a different sort_by or
// sort_dir, since the WHERE-clause tuple comparison it encodes is only
// meaningful for the exact order it was built against.
func TestFullStack_TaskList_CursorSortMismatchRejected(t *testing.T) {
	a := setupAdapter(t)

	for i := 0; i < 2; i++ {
		_, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
			"title":       fmt.Sprintf("task %d", i),
			"description": "x",
		})
		require.False(t, isErr)
	}

	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
		"limit":    "1",
		"sort_by":  "priority",
		"sort_dir": "asc",
	})
	require.False(t, isErr, "list should not error: %s", text)
	var env taskListCursorEnvelope
	parseData(t, text, &env)
	require.NotNil(t, env.Meta.NextCursor)

	// Same cursor, different sort_dir — must be rejected.
	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{
		"limit":    "1",
		"sort_by":  "priority",
		"sort_dir": "desc",
		"cursor":   *env.Meta.NextCursor,
	})
	require.True(t, isErr, "list should error on cursor/sort_dir mismatch: %s", text)
	code, _, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "cursor", field)

	// Same cursor, different sort_by — must also be rejected.
	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{
		"limit":    "1",
		"sort_by":  "status",
		"sort_dir": "asc",
		"cursor":   *env.Meta.NextCursor,
	})
	require.True(t, isErr, "list should error on cursor/sort_by mismatch: %s", text)
	code, _, field = parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "cursor", field)
}

// TestFullStack_TaskList_SortByUpdatedAtDesc exercises a non-default sort
// column (a timestamp) end to end, guarding against the
// sqlstore.SQLiteDatetimeLayout encode/decode round-trip
// (mcpadapter.taskSortValue <-> sqlstore.taskCursorArg) silently
// mis-comparing against what's actually stored in the column.
func TestFullStack_TaskList_SortByUpdatedAtDesc(t *testing.T) {
	a := setupAdapter(t)

	var ids []string
	for i := 0; i < 5; i++ {
		text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
			"title":       fmt.Sprintf("task %d", i),
			"description": "x",
		})
		require.False(t, isErr)
		var rec map[string]interface{}
		parseData(t, text, &rec)
		ids = append(ids, rec["ID"].(string))
	}

	// updated_at is whole-second precision (see sqlstore.updatedAtNow's doc
	// comment) — sleep past a full second before the update so it lands in
	// a strictly later second than the batch of creates above, rather than
	// relying on the id tiebreak to (accidentally) produce the expected
	// order.
	time.Sleep(1100 * time.Millisecond)

	// Touch the first-created task last so its updated_at becomes the
	// newest — desc order should surface it first despite being created
	// first.
	_, isErr := callTool(t, a, "torque_task_update", map[string]interface{}{
		"id":    ids[0],
		"title": "touched",
	})
	require.False(t, isErr)

	text, isErr := callTool(t, a, "torque_task_list", map[string]interface{}{
		"sort_by":  "updated_at",
		"sort_dir": "desc",
		"limit":    "10",
	})
	require.False(t, isErr, "list should not error: %s", text)
	var env taskListCursorEnvelope
	parseData(t, text, &env)
	require.Len(t, env.Items, 5)
	require.Equal(t, ids[0], env.Items[0]["id"], "most recently updated task should sort first under updated_at desc")
	require.False(t, env.Meta.HasMore)
	require.Nil(t, env.Meta.NextCursor)
}

// TestFullStack_TaskList_FilterByStatuses verifies torque_task_list's
// ENT-TASK statuses[] MCP wiring on top of the pre-existing store-layer OR
// filter (TaskFilter.Statuses).
func TestFullStack_TaskList_FilterByStatuses(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "s1", "description": "x"})
	var t1 map[string]interface{}
	parseData(t, text, &t1)
	id1 := t1["ID"].(string)

	text, _ = callTool(t, a, "torque_task_create", map[string]interface{}{"title": "s2", "description": "x"})
	var t2 map[string]interface{}
	parseData(t, text, &t2)
	id2 := t2["ID"].(string)

	text, _ = callTool(t, a, "torque_task_create", map[string]interface{}{"title": "s3", "description": "x"})
	var t3 map[string]interface{}
	parseData(t, text, &t3)
	id3 := t3["ID"].(string)

	_, isErr := callTool(t, a, "torque_task_transition", map[string]interface{}{"id": id1, "status": "doing"})
	require.False(t, isErr)

	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{
		"statuses": `["doing","todo"]`,
	})
	require.False(t, isErr, "list should not error: %s", text)
	var env taskListCursorEnvelope
	parseData(t, text, &env)
	var gotIDs []string
	for _, item := range env.Items {
		gotIDs = append(gotIDs, item["id"].(string))
	}
	require.ElementsMatch(t, []string{id1, id2, id3}, gotIDs)
}

// TestFullStack_TaskList_FilterByAgentAndLaunchProfile verifies the
// ENT-TASK agent_profile/launch_profile MCP filters.
func TestFullStack_TaskList_FilterByAgentAndLaunchProfile(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "profiled", "description": "x", "agent_profile": "reviewer", "launch_profile": "claude-code",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	_, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "other-profile", "description": "x", "agent_profile": "builder",
	})
	require.False(t, isErr)

	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{"agent_profile": "reviewer"})
	require.False(t, isErr, "list should not error: %s", text)
	var env taskListCursorEnvelope
	parseData(t, text, &env)
	require.Len(t, env.Items, 1)
	require.Equal(t, id, env.Items[0]["id"])

	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{"launch_profile": "claude-code"})
	require.False(t, isErr, "list should not error: %s", text)
	parseData(t, text, &env)
	require.Len(t, env.Items, 1)
	require.Equal(t, id, env.Items[0]["id"])
}

// TestFullStack_TaskList_FilterByBudgetRange verifies the ENT-TASK
// cost_budget_gte/lte MCP filters — "show me over-budget tasks" style
// queries. A task that never set cost_budget must never match either bound.
func TestFullStack_TaskList_FilterByBudgetRange(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "pricey", "description": "x", "cost_budget": "500",
	})
	var pricey map[string]interface{}
	parseData(t, text, &pricey)
	priceyID := pricey["ID"].(string)

	_, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "no-budget", "description": "x",
	})
	require.False(t, isErr)

	text, isErr = callTool(t, a, "torque_task_list", map[string]interface{}{"cost_budget_gte": "100"})
	require.False(t, isErr, "list should not error: %s", text)
	var env taskListCursorEnvelope
	parseData(t, text, &env)
	require.Len(t, env.Items, 1, "the never-budgeted task must not match a numeric bound")
	require.Equal(t, priceyID, env.Items[0]["id"])
}

// TestFullStack_TaskCreate_FieldExpansion verifies the ENT-TASK create-field
// expansion: fields TaskCreateInput already accepted at the service layer
// but torque_task_create's MCP schema didn't expose.
func TestFullStack_TaskCreate_FieldExpansion(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":            "expanded fields",
		"tools":            `["bash","read"]`,
		"on_review":        "auto-approve",
		"files":            `["a.go","b.go"]`,
		"cost_budget":      "42.5",
		"max_retries":      "7",
		"permissions":      `{"network":"deny"}`,
		"environment":      `{"FOO":"bar"}`,
		"max_duration_ms":  "60000",
		"token_budget":     "100000",
		"escalation_chain": `["lead","manager"]`,
		"quality_gates":    `["lint","tests"]`,
		"deliverables":     `[{"type":"diff","required":true}]`,
		"blocked_reason":   "waiting on legal",
	})
	require.False(t, isErr, "create should not error: %s", text)

	var rec map[string]interface{}
	parseData(t, text, &rec)
	require.Equal(t, "auto-approve", rec["OnReview"])
	require.Equal(t, "waiting on legal", rec["BlockedReason"])
	require.Equal(t, float64(7), rec["MaxRetries"])

	id := rec["ID"].(string)
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id})
	require.False(t, isErr)
	var got map[string]interface{}
	parseData(t, text, &got)
	require.Equal(t, `["bash","read"]`, got["Tools"].(map[string]interface{})["String"])
	require.Equal(t, `["a.go","b.go"]`, got["Files"].(map[string]interface{})["String"])
	require.Equal(t, 42.5, got["CostBudget"].(map[string]interface{})["Float64"])
	require.Equal(t, float64(60000), got["MaxDurationMs"].(map[string]interface{})["Int64"])
	require.Equal(t, float64(100000), got["TokenBudget"].(map[string]interface{})["Int64"])
}

// TestFullStack_TaskCreate_SubtodosSeed verifies torque_task_create's
// subtodos[] seed list creates the task and its checklist atomically, with
// missing ids auto-generated (same guarantee torque_task_subtodo_add gives
// a single append).
func TestFullStack_TaskCreate_SubtodosSeed(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":    "seeded",
		"subtodos": `[{"id":"check-1","text":"write test","required":true},{"text":"no id item"}]`,
	})
	require.False(t, isErr, "create should not error: %s", text)
	var rec map[string]interface{}
	parseData(t, text, &rec)
	id := rec["ID"].(string)

	text, isErr = callTool(t, a, "torque_task_subtodo_list", map[string]interface{}{"task_id": id, "verbose": "true"})
	require.False(t, isErr, "subtodo_list should not error: %s", text)
	var env struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, text, &env)
	items := env.Items
	require.Len(t, items, 2)
	require.Equal(t, "check-1", items[0]["id"])
	require.Equal(t, "write test", items[0]["text"])
	require.Equal(t, true, items[0]["required"])
	require.NotEmpty(t, items[1]["id"])
	require.NotEqual(t, "check-1", items[1]["id"])
	require.Equal(t, "no id item", items[1]["text"])
}

// TestFullStack_TaskCreate_SubtodosSeedDuplicateID verifies a caller-
// supplied duplicate id in the seed list is rejected as arg-invalid-shaped
// domain validation, not silently accepted.
func TestFullStack_TaskCreate_SubtodosSeedDuplicateID(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":    "dup seed",
		"subtodos": `[{"id":"dup","text":"first"},{"id":"dup","text":"second"}]`,
	})
	require.True(t, isErr, "duplicate subtodo id should error: %s", text)
}

// TestFullStack_TaskTransition_WithComment verifies torque_task_transition's
// ENT-TASK comment param posts atomically with the status change: the
// status changes AND the comment appears on the task's thread from one call.
func TestFullStack_TaskTransition_WithComment(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "transition with comment", "description": "x",
	})
	require.False(t, isErr)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id":             id,
		"status":         "doing",
		"comment":        "kicking this off",
		"comment_author": "alice",
	})
	require.False(t, isErr, "transition with comment should not error: %s", text)
	var got map[string]interface{}
	parseData(t, text, &got)
	require.Equal(t, "doing", got["Status"])

	text, isErr = callTool(t, a, "torque_comment_list", map[string]interface{}{
		"entity_type": "task", "entity_id": id, "verbose": "true",
	})
	require.False(t, isErr, "comment_list should not error: %s", text)
	var env struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, text, &env)
	require.Len(t, env.Items, 1)
	require.Equal(t, "kicking this off", env.Items[0]["content"])
	require.Equal(t, "alice", env.Items[0]["author"])
}

// TestFullStack_TaskTransition_WithComment_InvalidTransitionPostsNoComment
// mirrors the service-layer invariant: an FSM-invalid transition must not
// leave the comment behind either.
func TestFullStack_TaskTransition_WithComment_InvalidTransitionPostsNoComment(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "invalid transition with comment", "description": "x",
	})
	require.False(t, isErr)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	// "in_progress" is not in the vocabulary — the exact name agents guessed
	// before the tool description carried the real list (CW-20260907-0059).
	text, isErr = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id":      id,
		"status":  "in_progress",
		"comment": "should not stick",
	})
	require.True(t, isErr, "unrecognized status should error: %s", text)
	require.Contains(t, text, "doing", "the refusal must name the vocabulary so the caller can self-correct")

	text, isErr = callTool(t, a, "torque_comment_list", map[string]interface{}{
		"entity_type": "task", "entity_id": id,
	})
	require.False(t, isErr)
	var env struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, text, &env)
	require.Empty(t, env.Items)
}

// TestFullStack_TaskUpdate_StatusApplies pins the CW-20260909-0011 fix for
// the defect the friction log called the sharper of the pair: torque_task_update
// had no status param, the adapter dropped the arg, and the call still
// answered ok:true — reporting a status change that never happened. It must
// now actually apply, and route through the transition path so the terminal
// guard still holds.
func TestFullStack_TaskUpdate_StatusApplies(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title": "update-status", "description": "x",
	})
	require.False(t, isErr)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	// The move that silently no-opped before.
	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{
		"id": id, "status": "done",
	})
	require.False(t, isErr, "update with status should succeed: %s", text)
	var updated map[string]interface{}
	parseData(t, text, &updated)
	require.Equal(t, "done", updated["Status"], "status must actually change, not report success and do nothing")

	// It goes through the transition path, so the terminal guard applies
	// rather than update being a back door around it.
	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{
		"id": id, "status": "doing",
	})
	require.True(t, isErr, "leaving a terminal status via update must be refused: %s", text)
	require.Contains(t, text, "force=true")

	// A field edit alongside a status change still applies both.
	_, isErr = callTool(t, a, "torque_task_transition", map[string]interface{}{
		"id": id, "status": "todo", "force": true,
	})
	require.False(t, isErr)
	text, isErr = callTool(t, a, "torque_task_update", map[string]interface{}{
		"id": id, "status": "doing", "priority": "1",
	})
	require.False(t, isErr, "%s", text)
	parseData(t, text, &updated)
	require.Equal(t, "doing", updated["Status"])
	require.Equal(t, float64(1), updated["Priority"])
}
