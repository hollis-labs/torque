package mcpadapter_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

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

	// torque_task_search mirrors the same default-exclude.
	text, _ = callTool(t, a, "torque_task_search", map[string]interface{}{
		"query": "x",
	})
	require.Len(t, parseList(text), 1, "search default-excludes internal")

	text, _ = callTool(t, a, "torque_task_search", map[string]interface{}{
		"query":            "x",
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

func TestFullStack_SearchTasks(t *testing.T) {
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
	text, isErr := callTool(t, a, "torque_task_search", map[string]interface{}{
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

// TestFullStack_TaskSearch_WithSprintID verifies task_search with sprint_id filter.
func TestFullStack_TaskSearch_WithSprintID(t *testing.T) {
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

	text, isErr = callTool(t, a, "torque_task_search", map[string]interface{}{
		"query":     "refactor",
		"sprint_id": sp01ID,
	})
	require.False(t, isErr, "task_search with sprint_id should not error: %s", text)

	var envelope struct {
		Items []map[string]interface{} `json:"items"`
	}
	parseData(t, text, &envelope)
	require.Len(t, envelope.Items, 1)
	require.Equal(t, "Sprint task refactor", envelope.Items[0]["title"])
}

// TestFullStack_TaskSearch_EmptyQueryReturnsError verifies that task_search
// rejects an empty query with a structured error.
func TestFullStack_TaskSearch_EmptyQueryReturnsError(t *testing.T) {
	a := setupAdapter(t)

	// task_search declares query as Required() so the MCP framework may reject
	// it before the handler runs. Passing an explicit empty string instead.
	text, isErr := callTool(t, a, "torque_task_search", map[string]interface{}{
		"query": "",
	})
	require.True(t, isErr, "empty query should return error; got: %s", text)
	code, _, field := parseError(t, text)
	require.Equal(t, "arg_invalid", code)
	require.Equal(t, "query", field)
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
