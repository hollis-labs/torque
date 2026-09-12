package mcpadapter_test

import (
	"bytes"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseDataUseNumber(t *testing.T, text string, out any) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(dataBytes(t, text)))
	dec.UseNumber()
	require.NoError(t, dec.Decode(out), "data unmarshal: %s", text)
}

func typedDecodeErrors(rec map[string]interface{}) map[string]string {
	out := map[string]string{}
	raw, ok := rec["decode_errors"].([]interface{})
	if !ok {
		return out
	}
	for _, item := range raw {
		obj := item.(map[string]interface{})
		out[obj["field"].(string)] = obj["error"].(string)
	}
	return out
}

func assertMapsEqualExcept(t *testing.T, want, got map[string]interface{}, except map[string]bool) {
	t.Helper()
	for k, wantValue := range want {
		if except[k] {
			continue
		}
		gotValue, ok := got[k]
		require.True(t, ok, "typed response missing HTTP key %s", k)
		assert.Equal(t, wantValue, gotValue, "typed MCP should match HTTP field %s", k)
	}
	for k := range got {
		if except[k] {
			continue
		}
		require.Contains(t, want, k, "typed response has key absent from HTTP: %s", k)
	}
}

func TestFullStack_TaskGetTyped_DefaultCommentsFlattensNativeFields(t *testing.T) {
	a, _, db := setupTaskQueryParitySurfaces(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":       "typed comments",
		"description": "x",
		"tools":       `["bash"]`,
	})
	require.False(t, isErr, "create: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)
	_, err := db.Exec(`UPDATE tasks SET metadata = ? WHERE id = ?`, `{"nested":{"token":9007199254740993123}}`, id)
	require.NoError(t, err)
	_, isErr = callTool(t, a, "torque_comment_add", map[string]interface{}{
		"entity_type": "task",
		"entity_id":   id,
		"author":      "reviewer",
		"content":     "typed comment",
	})
	require.False(t, isErr)

	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id, "format": "typed"})
	require.False(t, isErr, "typed get: %s", text)
	var got map[string]interface{}
	parseDataUseNumber(t, text, &got)
	assert.Equal(t, id, got["id"])
	assert.Equal(t, "typed comments", got["title"])
	assert.Equal(t, []interface{}{"bash"}, got["tools"])
	require.Contains(t, got, "comments")
	require.Contains(t, got, "comments_meta")
	assert.NotContains(t, got, "typedTaskRecord", "embedded map must be flattened")
	assert.NotContains(t, got, "ID", "typed format must not leak legacy PascalCase")
	assert.Nil(t, got["cost_budget"])
	meta := got["metadata"].(map[string]interface{})
	nested := meta["nested"].(map[string]interface{})
	token, ok := nested["token"].(json.Number)
	require.True(t, ok, "UseNumber should preserve large JSON integers")
	assert.Equal(t, "9007199254740993123", token.String())
	_, ok = new(big.Int).SetString(token.String(), 10)
	assert.True(t, ok)
}

func TestFullStack_TaskGetTyped_CommentsOptOutOmitsLowercaseKeys(t *testing.T) {
	a := setupAdapter(t)
	taskID := seedTaskWithComments(t, a, 1, numberedBody)
	text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{
		"id":       taskID,
		"format":   "typed",
		"comments": "false",
	})
	require.False(t, isErr, "typed get opt-out: %s", text)
	var got map[string]interface{}
	parseData(t, text, &got)
	assert.Equal(t, taskID, got["id"])
	assert.NotContains(t, got, "comments")
	assert.NotContains(t, got, "comments_meta")
}

func TestFullStack_TaskGetTyped_ByteCapDropsOldestAndRetainsTaskFields(t *testing.T) {
	a := setupAdapter(t)
	taskID := seedTaskWithComments(t, a, 12, func(i int) string {
		return "TYPED-MARK-" + string(rune('A'+i-1)) + "-" + strings.Repeat("x", 12*1024)
	})

	text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{
		"id":             taskID,
		"format":         "typed",
		"comments_limit": "12",
	})
	require.False(t, isErr, "typed task_get: %s", text)
	require.LessOrEqual(t, len(text), 100*1024)

	var got map[string]interface{}
	parseData(t, text, &got)
	assert.Equal(t, taskID, got["id"])
	assert.Equal(t, "comment-tail-target", got["title"])
	window := got["comments"].([]interface{})
	require.NotEmpty(t, window)
	assert.Less(t, len(window), 12, "typed cap should trim comments")
	assert.True(t, strings.HasPrefix(window[len(window)-1].(map[string]interface{})["content"].(string), "TYPED-MARK-L-"))
	assert.False(t, strings.HasPrefix(window[0].(map[string]interface{})["content"].(string), "TYPED-MARK-A-"))
	meta := got["comments_meta"].(map[string]interface{})
	assert.Equal(t, float64(len(window)), meta["returned"])
	assert.Equal(t, float64(12), meta["total"])
	assert.Equal(t, float64(12-len(window)), meta["omitted"])
	assert.Equal(t, true, meta["truncated"])
	assert.Contains(t, meta["hint"], "torque_comment_list")
}

func TestFullStack_TaskListTyped_BriefIncludesScopeRefsAndCursorContinuity(t *testing.T) {
	a, _, db := setupTaskQueryParitySurfaces(t)
	parentText, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "parent", "description": "x"})
	require.False(t, isErr)
	var parent map[string]interface{}
	parseData(t, parentText, &parent)
	parentID := parent["ID"].(string)

	var ids []string
	for _, title := range []string{"typed-a", "typed-b", "typed-c"} {
		text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
			"title":       title,
			"description": "x",
			"parent_id":   parentID,
			"tags":        `["typed"]`,
		})
		require.False(t, isErr, "create %s: %s", title, text)
		var rec map[string]interface{}
		parseData(t, text, &rec)
		ids = append(ids, rec["ID"].(string))
	}
	require.NoError(t, setTaskTimes(db, ids[0], "2026-09-11 00:00:00.000000001", "2026-09-11 00:00:00.000000001"))
	require.NoError(t, setTaskTimes(db, ids[1], "2026-09-11 00:00:00.000000002", "2026-09-11 00:00:00.000000002"))
	require.NoError(t, setTaskTimes(db, ids[2], "2026-09-11 00:00:00.000000003", "2026-09-11 00:00:00.000000003"))

	pageArgs := map[string]interface{}{"format": "typed", "limit": "2", "sort_by": "created_at", "sort_dir": "asc", "include_total": "true", "parent_id": parentID}
	var seen []string
	for pages := 0; pages < 10; pages++ {
		text, isErr := callTool(t, a, "torque_task_list", pageArgs)
		require.False(t, isErr, "typed list: %s", text)
		var env taskListCursorEnvelope
		parseData(t, text, &env)
		require.NotNil(t, env.Meta.Total)
		assert.Equal(t, 3, *env.Meta.Total)
		for _, item := range env.Items {
			seen = append(seen, item["id"].(string))
			assert.Equal(t, parentID, item["parent_id"])
			assert.Contains(t, item, "project_id")
			assert.Contains(t, item, "sprint_id")
			assert.Contains(t, item, "epic_id")
			assert.Equal(t, []interface{}{"typed"}, item["tags"])
			assert.NotContains(t, item, "Title")
		}
		if !env.Meta.HasMore {
			require.Nil(t, env.Meta.NextCursor)
			assert.Equal(t, ids, seen)
			assert.Len(t, seen, len(ids))
			assert.Len(t, uniqueStrings(seen), len(ids))
			return
		}
		require.NotNil(t, env.Meta.NextCursor)
		pageArgs["cursor"] = *env.Meta.NextCursor
	}
	t.Fatal("typed brief cursor traversal did not terminate")
}

func TestFullStack_TaskTyped_FullParityWithHTTPValidRecord(t *testing.T) {
	a, ts, db := setupTaskQueryParitySurfaces(t)
	defer ts.Close()

	depText, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "dep", "description": "x"})
	require.False(t, isErr)
	var dep map[string]interface{}
	parseData(t, depText, &dep)
	depID := dep["ID"].(string)
	parentText, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "parent", "description": "x"})
	require.False(t, isErr)
	var parent map[string]interface{}
	parseData(t, parentText, &parent)
	parentID := parent["ID"].(string)

	taskText, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":                  "typed parity",
		"description":            "x",
		"tags":                   `["api","mcp"]`,
		"tools":                  `["bash","rg"]`,
		"permissions":            `{"mode":"read"}`,
		"environment":            `{"A":"B"}`,
		"files":                  `["README.md"]`,
		"cost_budget":            "0",
		"max_duration_ms":        "60000",
		"token_budget":           "12345",
		"escalation_chain":       `["ops"]`,
		"quality_gates":          `["tests"]`,
		"deliverables":           `[{"type":"note","required":false}]`,
		"metadata":               `{"hitl":{"required_workflow":{"workflow_type":"approval","enforcement_mode":"required","requirements":{"response_required":true},"reason":"typed parity"}}}`,
		"depends_on":             `["` + depID + `"]`,
		"parent_id":              parentID,
		"source_ref":             "src",
		"on_done":                "review",
		"on_fail":                "retry",
		"on_review":              "pause",
		"on_done_merge":          "none",
		"checkpoint_mode":        "none",
		"on_checkpoint_response": "resume",
	})
	require.False(t, isErr, "create: %s", taskText)
	var created map[string]interface{}
	parseData(t, taskText, &created)
	id := created["ID"].(string)
	_, err := db.Exec(`INSERT INTO projects (id, name) VALUES ('PRJ-TYPED', 'Typed Project')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO sprints (id, name) VALUES ('SP-TYPED', 'Typed Sprint')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO epics (id, name) VALUES ('EP-TYPED', 'Typed Epic')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO collections (id, name) VALUES ('COL-TYPED', 'Typed Collection')`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE tasks SET project_id = 'PRJ-TYPED', sprint_id = 'SP-TYPED', epic_id = 'EP-TYPED',
		collection_id = 'COL-TYPED', collection_position = 7, added_to_collections_at = '2026-09-11 00:00:00',
		subtodos = '[{"id":"check","text":"Check typed parity","required":true,"done":false}]'
		WHERE id = ?`, id)
	require.NoError(t, err)
	res, err := db.Exec(`INSERT INTO runs (task_id, executor, status, prompt_tokens, completion_tokens) VALUES (?, 'mock', 'done', 11, 13)`, id)
	require.NoError(t, err)
	runID, err := res.LastInsertId()
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO cost_ledger (task_id, run_id, cost, prompt_tokens, completion_tokens, cost_source) VALUES (?, ?, 1.25, 11, 13, 'executor')`, id, runID)
	require.NoError(t, err)

	text, isErr := callTool(t, a, "torque_task_get", map[string]interface{}{"id": id, "format": "typed", "comments": "false"})
	require.False(t, isErr, "typed get: %s", text)
	var typed map[string]interface{}
	parseDataUseNumber(t, text, &typed)

	resp, err := http.Get(ts.URL + "/api/v1/tasks/" + id)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	defer resp.Body.Close()
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var httpTask map[string]interface{}
	require.NoError(t, dec.Decode(&httpTask))

	assertMapsEqualExcept(t, httpTask, typed, map[string]bool{"decode_errors": true, "comments": true, "comments_meta": true})
	assert.Equal(t, []interface{}{"bash", "rg"}, typed["tools"])
	assert.Len(t, typed["tags"], 2)
	assert.Equal(t, []interface{}{depID}, typed["depends_on"])
	assert.Equal(t, parentID, typed["parent_id"])
	assert.Equal(t, "PRJ-TYPED", typed["project_id"])
	assert.Equal(t, "SP-TYPED", typed["sprint_id"])
	assert.Equal(t, "EP-TYPED", typed["epic_id"])
	assert.Equal(t, "COL-TYPED", typed["collection_id"])
	assert.Equal(t, "Typed Collection", typed["collection_name"])
	assert.Equal(t, json.Number("7"), typed["collection_position"])
	stats := typed["stats"].(map[string]interface{})
	assert.Equal(t, json.Number("1"), stats["run_count"])
	assert.Equal(t, json.Number("11"), stats["prompt_tokens"])
	assert.Equal(t, json.Number("13"), stats["completion_tokens"])
	assert.Equal(t, json.Number("1.25"), stats["cost"])
	assert.Equal(t, "measured", stats["cost_source"])
	assert.Len(t, typed["subtodos"], 1)
	policy := typed["required_workflow_policy"].(map[string]interface{})
	assert.Equal(t, "approval", policy["workflow_type"])
	assert.Equal(t, "required", policy["enforcement_mode"])
	assert.NotContains(t, typed, "decode_errors")
	assert.NotContains(t, typed, "comments")
}

func TestFullStack_TaskTyped_DecodeErrorsForMalformedStoredJSON(t *testing.T) {
	a, _, db := setupTaskQueryParitySurfaces(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "bad blobs", "description": "x"})
	require.False(t, isErr)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	_, err := db.Exec(`UPDATE tasks SET
		tools = ?,
		permissions = ?,
		environment = ?,
		files = ?,
		escalation_chain = ?,
		deliverables = ?,
		metadata = ?,
		quality_gates = ?
		WHERE id = ?`,
		`["ok"] trailing`, `[]`, `{"A":2}`, `["ok",3]`, `"scalar"`,
		`[{"type":"note"}]`, `{"hitl":{"required_workflow":{"workflow_type":"approval","enforcement_mode":"required","requirements":{"response_required":true}}}} trailing`, `null {}`, id)
	require.NoError(t, err)

	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id, "format": "typed", "comments": "false"})
	require.False(t, isErr, "typed get malformed: %s", text)
	var got map[string]interface{}
	parseData(t, text, &got)
	errs := typedDecodeErrors(got)
	for _, field := range []string{"tools", "permissions", "environment", "files", "escalation_chain", "metadata", "quality_gates"} {
		assert.Contains(t, errs, field, "expected decode error for %s", field)
	}
	assert.NotContains(t, got, "required_workflow_policy", "malformed metadata must not emit policy")
	assert.NotContains(t, errs, "deliverables", "omitted optional required/description fields are HTTP-compatible")
	assert.Equal(t, []interface{}{}, got["tools"])
	assert.Equal(t, map[string]interface{}{}, got["permissions"])
	assert.Equal(t, map[string]interface{}{}, got["environment"])
	assert.Equal(t, []interface{}{}, got["files"])
	assert.Equal(t, []interface{}{}, got["quality_gates"])
	deliverables := got["deliverables"].([]interface{})
	require.Len(t, deliverables, 1)
	assert.Equal(t, "note", deliverables[0].(map[string]interface{})["type"])
	assert.Equal(t, false, deliverables[0].(map[string]interface{})["required"])
}

func TestFullStack_TaskTyped_StoredNullsAndAbsentNumericNullables(t *testing.T) {
	a, _, db := setupTaskQueryParitySurfaces(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "nulls", "description": "x"})
	require.False(t, isErr)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)
	_, err := db.Exec(`UPDATE tasks SET tools = 'null', permissions = 'null', environment = 'null', deliverables = 'null' WHERE id = ?`, id)
	require.NoError(t, err)

	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id, "format": "typed", "comments": "false"})
	require.False(t, isErr)
	var got map[string]interface{}
	parseDataUseNumber(t, text, &got)
	assert.Equal(t, []interface{}{}, got["tools"])
	assert.Equal(t, map[string]interface{}{}, got["permissions"])
	assert.Equal(t, map[string]interface{}{}, got["environment"])
	assert.Equal(t, []interface{}{}, got["deliverables"])
	assert.Nil(t, got["cost_budget"])
	assert.Nil(t, got["max_duration_ms"])
	assert.Nil(t, got["token_budget"])
	assert.NotContains(t, got, "decode_errors")

	zeroText, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "zero", "description": "x", "cost_budget": "0"})
	require.False(t, isErr)
	var zero map[string]interface{}
	parseData(t, zeroText, &zero)
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": zero["ID"].(string), "format": "typed", "comments": "false"})
	require.False(t, isErr)
	parseDataUseNumber(t, text, &got)
	assert.Equal(t, json.Number("0"), got["cost_budget"])
	assert.Nil(t, got["max_duration_ms"])
}

func TestFullStack_TaskTyped_MetadataPolicyRequiresStrictDecode(t *testing.T) {
	a, _, db := setupTaskQueryParitySurfaces(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
		"title":    "policy",
		"metadata": `{"hitl":{"required_workflow":{"workflow_type":"approval","requirements":{"response_required":true}}}}`,
	})
	require.False(t, isErr, "create: %s", text)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id, "format": "typed", "comments": "false"})
	require.False(t, isErr)
	var got map[string]interface{}
	parseDataUseNumber(t, text, &got)
	require.Contains(t, got, "required_workflow_policy")
	assert.NotContains(t, got, "decode_errors")

	_, err := db.Exec(`UPDATE tasks SET metadata = ? WHERE id = ?`,
		`{"hitl":{"required_workflow":{"workflow_type":"approval","requirements":{"response_required":true}}}} trailing`, id)
	require.NoError(t, err)
	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id, "format": "typed", "comments": "false"})
	require.False(t, isErr)
	got = map[string]interface{}{}
	parseDataUseNumber(t, text, &got)
	assert.Contains(t, typedDecodeErrors(got), "metadata")
	assert.NotContains(t, got, "required_workflow_policy")
}

func TestFullStack_TaskTyped_StrictFormatArgument(t *testing.T) {
	a := setupAdapter(t)
	text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{"title": "format", "description": "x"})
	require.False(t, isErr)
	var created map[string]interface{}
	parseData(t, text, &created)
	id := created["ID"].(string)

	for _, tc := range []struct {
		name string
		tool string
		args map[string]interface{}
	}{
		{"get numeric", "torque_task_get", map[string]interface{}{"id": id, "format": 1}},
		{"list numeric", "torque_task_list", map[string]interface{}{"format": 1}},
		{"get unknown", "torque_task_get", map[string]interface{}{"id": id, "format": "snake"}},
		{"list map", "torque_task_list", map[string]interface{}{"format": map[string]interface{}{"mode": "typed"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := callTool(t, a, tc.tool, tc.args)
			require.True(t, isErr, "expected format error: %s", text)
			code, _, field := parseError(t, text)
			assert.Equal(t, "arg_invalid", code)
			assert.Equal(t, "format", field)
		})
	}

	text, isErr = callTool(t, a, "torque_task_get", map[string]interface{}{"id": id, "format": "legacy"})
	require.False(t, isErr, "legacy format should work: %s", text)
	var legacy map[string]interface{}
	parseData(t, text, &legacy)
	assert.Equal(t, id, legacy["ID"])
}

func TestFullStack_TaskListTyped_VerboseByteCapTraversal(t *testing.T) {
	a := setupAdapter(t)
	for i := 0; i < 30; i++ {
		text, isErr := callTool(t, a, "torque_task_create", map[string]interface{}{
			"title":       strings.Repeat("typed-fat-", 20) + string(rune('a'+i%26)),
			"description": strings.Repeat("payload ", 1400),
			"metadata":    `{"blob":"` + strings.Repeat("x", 1200) + `"}`,
		})
		require.False(t, isErr, "create: %s", text)
	}
	var seen []string
	args := map[string]interface{}{"format": "typed", "verbose": "true", "limit": "30", "include_total": "true"}
	for pages := 0; pages < 40; pages++ {
		text, isErr := callTool(t, a, "torque_task_list", args)
		require.False(t, isErr, "typed verbose page: %s", text)
		require.LessOrEqual(t, len(text), 100*1024)
		var env taskListCursorEnvelope
		parseData(t, text, &env)
		require.NotNil(t, env.Meta.Total)
		assert.Equal(t, 30, *env.Meta.Total)
		if pages == 0 {
			assert.True(t, env.Meta.Truncated, "first typed verbose page should hit the byte cap")
			assert.Less(t, env.Meta.Returned, 30)
		}
		for _, item := range env.Items {
			seen = append(seen, item["id"].(string))
		}
		if !env.Meta.HasMore {
			require.Nil(t, env.Meta.NextCursor)
			assert.Len(t, seen, 30)
			assert.Len(t, uniqueStrings(seen), 30)
			return
		}
		require.NotNil(t, env.Meta.NextCursor)
		args["cursor"] = *env.Meta.NextCursor
	}
	t.Fatal("typed verbose traversal did not terminate")
}
