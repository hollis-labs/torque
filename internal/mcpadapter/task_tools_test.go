package mcpadapter_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
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
	return mcpadapter.New(svc, nil)
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
	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
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
	text, isErr = callTool(t, a, "clockwork_task_get", map[string]interface{}{
		"id": id,
	})
	require.False(t, isErr, "get should not error: %s", text)

	var fetched map[string]interface{}
	parseData(t, text, &fetched)
	require.Equal(t, "Fix auth bug", fetched["Title"])
}

func TestFullStack_TaskCreate_Facets(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
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

	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
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

	_, _ = callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "agent one",
		"description": "x",
	})
	_, _ = callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "external one",
		"description": "x",
		"kind":        "external",
		"manual":      true,
	})

	text, isErr := callTool(t, a, "clockwork_task_list", map[string]interface{}{
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

func TestFullStack_TaskUpdate_Facets(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "u",
		"description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr := callTool(t, a, "clockwork_task_update", map[string]interface{}{
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
// the clockwork_task_update MCP tool for HTTP PUT parity (CW-20260417-0007).
// Each field gets a round-trip check via the returned TaskRecord JSON.
func TestFullStack_TaskUpdate_WritableFields(t *testing.T) {
	a := setupAdapter(t)

	text, _ := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "writable-fields",
		"description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	// Scalars / simple strings / bools / numbers.
	text, isErr := callTool(t, a, "clockwork_task_update", map[string]interface{}{
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
	text, _ = callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "dep",
		"description": "x",
	})
	var dep map[string]interface{}
	parseData(t, text, &dep)
	depID := dep["ID"].(string)

	text, isErr = callTool(t, a, "clockwork_task_update", map[string]interface{}{
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
	expectNullStringJSON("DependsOn", `["`+depID+`"]`)
	expectNullStringJSON("Metadata", `{"meta_key":"meta_val"}`)
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
	parseData(t, text, &created)
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

	var envelope struct {
		Items []map[string]interface{} `json:"items"`
		Meta  map[string]interface{}   `json:"meta"`
	}
	parseData(t, text, &envelope)
	require.Len(t, envelope.Items, 1, "search for 'login' should return exactly 1 result")
	require.Equal(t, false, envelope.Meta["truncated"])
}

// TestFullStack_TaskCreate_ForcesManualTrue_ExplicitFalse verifies the
// CW-20260417-0133 safety override at the MCP surface: clockwork_task_create
// with an explicit manual=false still persists manual=true.
func TestFullStack_TaskCreate_ForcesManualTrue_ExplicitFalse(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
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
// clockwork_task_create call with no manual arg (historical default-false)
// is coerced to manual=true.
func TestFullStack_TaskCreate_ManualOmitted_CoercedToTrue(t *testing.T) {
	a := setupAdapter(t)

	text, isErr := callTool(t, a, "clockwork_task_create", map[string]interface{}{
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

	text, _ := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "mcp-promote-me",
		"description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)
	require.Equal(t, true, created["Manual"])

	text, isErr := callTool(t, a, "clockwork_task_update", map[string]interface{}{
		"id":     id,
		"manual": false,
	})
	require.False(t, isErr, "update should not error: %s", text)

	var updated map[string]interface{}
	parseData(t, text, &updated)
	require.Equal(t, false, updated["Manual"], "Update path must NOT coerce manual=false")
}

// TestFullStack_TaskUpdate_ManualStringCoercion reproduces CW-20260418-0019
// Instance 1: clockwork_task_update manual=true returned success but the
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
	text, _ := callTool(t, a, "clockwork_task_create", map[string]interface{}{
		"title":       "coerce-me",
		"description": "x",
	})
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr := callTool(t, a, "clockwork_task_update", map[string]interface{}{
		"id":     id,
		"manual": false,
	})
	require.False(t, isErr, "baseline flip to false should succeed: %s", text)
	var flipped map[string]interface{}
	parseData(t, text, &flipped)
	require.Equal(t, false, flipped["Manual"])

	// The silent-drop repro: caller sends "true" as a JSON string.
	text, isErr = callTool(t, a, "clockwork_task_update", map[string]interface{}{
		"id":     id,
		"manual": "true",
	})
	require.False(t, isErr, "string-typed manual should be accepted: %s", text)
	var promoted map[string]interface{}
	parseData(t, text, &promoted)
	assert.Equal(t, true, promoted["Manual"],
		`clockwork_task_update manual="true" (string) must coerce to bool true; silent drop was CW-20260418-0019 Instance 1`)

	// Confirm the DB row agrees (not just the response echo).
	text, _ = callTool(t, a, "clockwork_task_get", map[string]interface{}{"id": id})
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
			text, _ := callTool(t, a, "clockwork_task_create", map[string]interface{}{
				"title":       "bool-variant",
				"description": "x",
			})
			var created map[string]interface{}
			parseData(t, text, &created)
			id := created["ID"].(string)

			// Baseline: flip to the opposite of tc.want so we can detect an
			// actual change.
			text, _ = callTool(t, a, "clockwork_task_update", map[string]interface{}{
				"id":     id,
				"manual": !tc.want,
			})
			var baseline map[string]interface{}
			parseData(t, text, &baseline)
			require.Equal(t, !tc.want, baseline["Manual"])

			text, isErr := callTool(t, a, "clockwork_task_update", map[string]interface{}{
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
