package httpserver_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	svc := service.New(store)
	handler := httpserver.New(svc, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestCreateAndGetTask(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Test task","description":"A test task description","priority":1,"executor":"cli"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	assert.Equal(t, "Test task", created["title"])
	taskID := created["id"].(string)

	resp, err = http.Get(ts.URL + "/api/v1/tasks/" + taskID)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var got map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	assert.Equal(t, "Test task", got["title"])
}

func TestHTTP_TaskCreate_RoundTripWithFacets(t *testing.T) {
	ts := setupTestServer(t)

	body := `{
        "title": "http facets",
        "description": "x",
        "kind": "agent",
        "executor": "cli",
        "source_type": "agent",
        "source_ref": "claude-code",
        "trust": "trusted",
        "checkpoint_mode": "blocking",
        "on_checkpoint_response": "review"
    }`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Equal(t, "agent", got["kind"])
	assert.Equal(t, "agent", got["source_type"])
	assert.Equal(t, "claude-code", got["source_ref"])
	assert.Equal(t, "trusted", got["trust"])
	assert.Equal(t, "blocking", got["checkpoint_mode"])
	assert.Equal(t, "review", got["on_checkpoint_response"])
}

func TestHTTP_TaskCreate_FacetDefaults(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"defaults","description":"x"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Equal(t, "agent", got["kind"])
	assert.Equal(t, "user", got["source_type"])
	assert.Nil(t, got["source_ref"])
	assert.Equal(t, "normal", got["trust"])
	assert.Equal(t, "none", got["checkpoint_mode"])
	assert.Equal(t, "resume", got["on_checkpoint_response"])
}

func TestHTTP_TaskList_FilterByManual(t *testing.T) {
	ts := setupTestServer(t)

	// Create two tasks. Both land as manual=true due to the CW-0133 safety
	// override; flip one to manual=false via the update endpoint so we can
	// verify both arms of the ?manual= filter.
	createTask := func(title string) string {
		resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(
			`{"title":"`+title+`","description":"x"}`))
		require.NoError(t, err)
		var created map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
		resp.Body.Close()
		return created["id"].(string)
	}
	autoID := createTask("auto-eligible")
	manualID := createTask("manual-hold")

	flipReq, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+autoID,
		bytes.NewBufferString(`{"manual":false}`))
	require.NoError(t, err)
	flipReq.Header.Set("Content-Type", "application/json")
	flipResp, err := http.DefaultClient.Do(flipReq)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, flipResp.StatusCode)
	flipResp.Body.Close()

	idsFrom := func(url string) []string {
		resp, err := http.Get(url)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		resp.Body.Close()
		raw := result["tasks"].([]interface{})
		out := make([]string, 0, len(raw))
		for _, t := range raw {
			out = append(out, t.(map[string]interface{})["id"].(string))
		}
		return out
	}

	assert.ElementsMatch(t, []string{autoID, manualID}, idsFrom(ts.URL+"/api/v1/tasks"))
	assert.Equal(t, []string{autoID}, idsFrom(ts.URL+"/api/v1/tasks?manual=false"))
	assert.Equal(t, []string{manualID}, idsFrom(ts.URL+"/api/v1/tasks?manual=true"))
	// UI aliases — `auto` / `manual` — must resolve identically.
	assert.Equal(t, []string{autoID}, idsFrom(ts.URL+"/api/v1/tasks?manual=auto"))
	assert.Equal(t, []string{manualID}, idsFrom(ts.URL+"/api/v1/tasks?manual=manual"))
}

func TestHTTP_TaskList_FilterByKind(t *testing.T) {
	ts := setupTestServer(t)

	http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(
		`{"title":"a","kind":"agent","executor":"cli","description":"x"}`))
	http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(
		`{"title":"e","kind":"external","manual":true,"description":"x"}`))

	resp, err := http.Get(ts.URL + "/api/v1/tasks?kind=external")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	resp.Body.Close()
	tasks := result["tasks"].([]interface{})
	require.Len(t, tasks, 1)
	assert.Equal(t, "external", tasks[0].(map[string]interface{})["kind"])
}

// CW-20260503-0011 (S1.1): list endpoints default-exclude kind=internal so
// automation/system tasks (Reviewer end-agents etc.) don't pollute the
// user-facing task list. Three opt-in shapes surface them: ?include_internal=1,
// ?include_internal=true, or an explicit ?kind=internal filter.
func TestHTTP_TaskList_DefaultExcludesInternal(t *testing.T) {
	ts := setupTestServer(t)

	http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(
		`{"title":"agent task","kind":"agent","executor":"cli","agent_profile":"cli","description":"x"}`))
	http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(
		`{"title":"reviewer","kind":"internal","executor":"cli","agent_profile":"reviewer","description":"x"}`))

	idsFrom := func(url string) []string {
		resp, err := http.Get(url)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var result map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
		resp.Body.Close()
		raw := result["tasks"].([]interface{})
		out := make([]string, 0, len(raw))
		for _, t := range raw {
			out = append(out, t.(map[string]interface{})["id"].(string))
		}
		return out
	}

	defaultIDs := idsFrom(ts.URL + "/api/v1/tasks")
	require.Len(t, defaultIDs, 1, "internal task hidden from default list")

	// Opt-in via include_internal=true surfaces both rows.
	allIDs := idsFrom(ts.URL + "/api/v1/tasks?include_internal=true")
	assert.Len(t, allIDs, 2)

	// Numeric truthy alias.
	allIDsNumeric := idsFrom(ts.URL + "/api/v1/tasks?include_internal=1")
	assert.Len(t, allIDsNumeric, 2)

	// Explicit kind=internal filter wins regardless of include_internal absence.
	internalOnly := idsFrom(ts.URL + "/api/v1/tasks?kind=internal")
	require.Len(t, internalOnly, 1)
}

// TestIntegration_TaskWithFacets_ThroughAllLayers is the Phase A exit-gate
// integration check: create → get → update → list filter, all via HTTP,
// exercising the facet pipeline through service + store layers.
func TestIntegration_TaskWithFacets_ThroughAllLayers(t *testing.T) {
	ts := setupTestServer(t)

	// Create via HTTP (external/user/trusted).
	body := `{
        "title": "e2e facets",
        "description": "x",
        "kind": "external",
        "manual": true,
        "source_type": "user",
        "source_ref": "chrispian",
        "trust": "trusted"
    }`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	id := created["id"].(string)

	// Get via HTTP — verify persistence.
	resp, err = http.Get(ts.URL + "/api/v1/tasks/" + id)
	require.NoError(t, err)
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Equal(t, "external", got["kind"])
	assert.Equal(t, "user", got["source_type"])
	assert.Equal(t, "chrispian", got["source_ref"])
	assert.Equal(t, "trusted", got["trust"])

	// Update trust via HTTP.
	patchBody := `{"trust":"normal"}`
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(patchBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var updated map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&updated))
	resp.Body.Close()
	assert.Equal(t, "normal", updated["trust"])

	// List filtered by source_ref.
	resp, err = http.Get(ts.URL + "/api/v1/tasks?source_ref=chrispian")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var listResult map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listResult))
	resp.Body.Close()
	tasks := listResult["tasks"].([]interface{})
	require.Len(t, tasks, 1)
	assert.Equal(t, id, tasks[0].(map[string]interface{})["id"])
	assert.Equal(t, "normal", tasks[0].(map[string]interface{})["trust"])
}

func TestHTTP_TaskUpdate_Facets(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"t","description":"x","kind":"agent","executor":"cli"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	id := created["id"].(string)

	upd := `{"checkpoint_mode":"blocking","on_checkpoint_response":"review","source_ref":"ctx-123"}`
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(upd))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Equal(t, "blocking", got["checkpoint_mode"])
	assert.Equal(t, "review", got["on_checkpoint_response"])
	assert.Equal(t, "ctx-123", got["source_ref"])
}

func TestListTasks(t *testing.T) {
	ts := setupTestServer(t)

	for _, title := range []string{"Task A", "Task B"} {
		body := `{"title":"` + title + `","description":"desc","executor":"cli"}`
		http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	}

	resp, err := http.Get(ts.URL + "/api/v1/tasks")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	tasks := result["tasks"].([]interface{})
	assert.Len(t, tasks, 2)
}

func TestHTTP_TaskList_StrictPriorityQuery(t *testing.T) {
	ts := setupTestServer(t)

	create := func(title string, priority int) string {
		resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(
			`{"title":"`+title+`","description":"x","priority":1}`))
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var created map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
		resp.Body.Close()
		id := created["id"].(string)
		req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id,
			bytes.NewBufferString(`{"priority":`+strconv.Itoa(priority)+`}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err = http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
		return id
	}
	zeroID := create("zero", 0)
	oneID := create("one", 1)
	twoID := create("two", 2)

	assert.ElementsMatch(t, []string{zeroID, oneID, twoID}, httpTaskListIDs(t, ts.URL+"/api/v1/tasks"))
	assert.Equal(t, []string{zeroID}, httpTaskListIDs(t, ts.URL+"/api/v1/tasks?priority=0"))
	assert.ElementsMatch(t, []string{zeroID, oneID}, httpTaskListIDs(t, ts.URL+"/api/v1/tasks?priority=1,0,1"))

	resp, err := http.Get(ts.URL + "/api/v1/tasks?priority=not_an_integer")
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	errBody := decodeHTTPError(t, resp)
	assert.Equal(t, "priority", errBody["field"])
	assert.Contains(t, errBody["error"], "integer")
}

func TestHTTP_TaskList_StrictQueryValidation(t *testing.T) {
	ts := setupTestServer(t)

	for _, tc := range []struct {
		query string
		field string
	}{
		{query: "limit=abc", field: "limit"},
		{query: "offset=1.2", field: "offset"},
		{query: "manual=sometimes", field: "manual"},
		{query: "include_internal=maybe", field: "include_internal"},
		{query: "kind=internal&include_internal=maybe", field: "include_internal"},
		{query: "sort_by=updated_at", field: "sort_by"},
		{query: "created_after=2026-09-11T00%3A00%3A00Z", field: "created_after"},
		{query: "unknown_filter=x", field: "unknown_filter"},
		{query: "priority=1,garbage", field: "priority"},
		{query: "priority=1,,2", field: "priority"},
		{query: "priority=", field: "priority"},
		{query: "priority=9223372036854775808", field: "priority"},
		{query: "priority=1&priority=garbage", field: "priority"},
		{query: "%zz", field: "query"},
	} {
		resp, err := http.Get(ts.URL + "/api/v1/tasks?" + tc.query)
		require.NoError(t, err, tc.query)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode, tc.query)
		errBody := decodeHTTPError(t, resp)
		assert.Equal(t, tc.field, errBody["field"], tc.query)
		assert.NotEmpty(t, errBody["error"], tc.query)
	}
}

func TestHTTP_TaskList_ManualAliasesStillWork(t *testing.T) {
	ts := setupTestServer(t)

	autoID := httpCreateTask(t, ts.URL, "auto-eligible")
	manualID := httpCreateTask(t, ts.URL, "manual-hold")
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+autoID,
		bytes.NewBufferString(`{"manual":false}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	assert.Equal(t, []string{manualID}, httpTaskListIDs(t, ts.URL+"/api/v1/tasks?manual=manual"))
	assert.Equal(t, []string{autoID}, httpTaskListIDs(t, ts.URL+"/api/v1/tasks?manual=auto"))
	assert.ElementsMatch(t, []string{autoID, manualID}, httpTaskListIDs(t, ts.URL+"/api/v1/tasks?manual=both"))
}

func httpCreateTask(t *testing.T, baseURL, title string) string {
	t.Helper()
	resp, err := http.Post(baseURL+"/api/v1/tasks", "application/json", bytes.NewBufferString(
		`{"title":"`+title+`","description":"x"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	return created["id"].(string)
}

func httpTaskListIDs(t *testing.T, url string) []string {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	resp.Body.Close()
	raw := result["tasks"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, task := range raw {
		out = append(out, task.(map[string]interface{})["id"].(string))
	}
	return out
}

func decodeHTTPError(t *testing.T, resp *http.Response) map[string]string {
	t.Helper()
	defer resp.Body.Close()
	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return body
}

func TestTransitionTask(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Task","description":"desc","executor":"cli"}`
	resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	taskID := created["id"].(string)

	transBody := `{"status":"doing"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks/"+taskID+"/transition", "application/json", bytes.NewBufferString(transBody))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var transitioned map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&transitioned)
	resp.Body.Close()
	assert.Equal(t, "doing", transitioned["status"])
}

func TestSettingsGetSet(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"value":"true"}`
	req, _ := http.NewRequest("PUT", ts.URL+"/api/v1/settings/features.sprints", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp, err = http.Get(ts.URL + "/api/v1/settings/features.sprints")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	assert.Equal(t, "true", result["value"])
}

func TestSettingsFeatureFlagsAlias(t *testing.T) {
	ts := setupTestServer(t)

	for _, key := range []string{"features.projects", "features.epics", "features.sprints"} {
		req, err := http.NewRequest("PUT", ts.URL+"/api/v1/settings/"+key, bytes.NewBufferString(`{"value":"true"}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	resp, err := http.Get(ts.URL + "/api/v1/settings/feature-flags")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var flags map[string]bool
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&flags))
	resp.Body.Close()
	assert.Equal(t, map[string]bool{"projects": true, "epics": true, "sprints": true, "collections": false}, flags)
}

func TestCreateAndGetTag(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"name":"Frontend Bug","color":"red","description":"UI issues"}`
	resp, err := http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	assert.Equal(t, "frontend-bug", created["slug"])
	assert.Equal(t, "Frontend Bug", created["name"])
	assert.Equal(t, "red", created["color"])

	// GET it back
	resp2, err := http.Get(ts.URL + "/api/v1/tags/frontend-bug")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
}

func TestCreateTagValidationErrors(t *testing.T) {
	ts := setupTestServer(t)

	// Missing name
	resp, _ := http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{}`))
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	// Invalid color
	resp, _ = http.Post(ts.URL+"/api/v1/tags", "application/json",
		bytes.NewBufferString(`{"name":"Bug","color":"turquoise"}`))
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	// Malformed JSON
	resp, _ = http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{not json`))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestGetTagNotFound(t *testing.T) {
	ts := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/tags/nonexistent")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestListTags(t *testing.T) {
	ts := setupTestServer(t)

	http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"Bug","color":"red"}`))
	http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"UI","color":"blue"}`))

	resp, err := http.Get(ts.URL + "/api/v1/tags")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var out map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	tags, ok := out["tags"].([]interface{})
	require.True(t, ok)
	assert.Len(t, tags, 2)
}

func TestPatchTag(t *testing.T) {
	ts := setupTestServer(t)
	http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"Bug","color":"red"}`))

	req, _ := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/tags/bug", bytes.NewBufferString(`{"color":"orange"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var updated map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&updated))
	assert.Equal(t, "orange", updated["color"])
}

func TestDeleteTag(t *testing.T) {
	ts := setupTestServer(t)
	http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"Bug","color":"red"}`))

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/tags/bug", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// GET returns 404 now
	resp2, _ := http.Get(ts.URL + "/api/v1/tags/bug")
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestCreateTaskWithTags(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Test","description":"x","priority":1,"executor":"cli","tags":["Bug","UI"]}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))

	tags, ok := created["tags"].([]interface{})
	require.True(t, ok, "tags field should be an array")
	require.Len(t, tags, 2)

	tag0 := tags[0].(map[string]interface{})
	assert.Equal(t, "bug", tag0["slug"])
	assert.Equal(t, "Bug", tag0["name"])
	assert.Equal(t, "zinc", tag0["color"])

	tag1 := tags[1].(map[string]interface{})
	assert.Equal(t, "ui", tag1["slug"])
}

func TestUpdateTaskTags(t *testing.T) {
	ts := setupTestServer(t)

	// Create with one tag
	body := `{"title":"Test","description":"x","priority":1,"executor":"cli","tags":["bug"]}`
	resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	id := created["id"].(string)

	// Replace tags
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(`{"tags":["ui","frontend"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var updated map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&updated))
	tags := updated["tags"].([]interface{})
	require.Len(t, tags, 2)
	assert.Equal(t, "ui", tags[0].(map[string]interface{})["slug"])
	assert.Equal(t, "frontend", tags[1].(map[string]interface{})["slug"])
}

func TestGetTaskIncludesTags(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Test","description":"x","priority":1,"executor":"cli","tags":["bug"]}`
	resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	id := created["id"].(string)

	resp2, err := http.Get(ts.URL + "/api/v1/tasks/" + id)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var fetched map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&fetched))
	tags := fetched["tags"].([]interface{})
	require.Len(t, tags, 1)
}

func TestMergeTags(t *testing.T) {
	ts := setupTestServer(t)
	http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"Bug","color":"red"}`))
	http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(`{"name":"Defect","color":"red"}`))

	resp, err := http.Post(ts.URL+"/api/v1/tags/bug/merge", "application/json",
		bytes.NewBufferString(`{"into":"defect"}`))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, "defect", result["slug"])

	// Source tag is gone
	resp2, _ := http.Get(ts.URL + "/api/v1/tags/bug")
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestCreateTagDuplicateReturns409(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"name":"Bug","color":"red"}`
	resp1, _ := http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(body))
	assert.Equal(t, http.StatusCreated, resp1.StatusCode)

	// Second create with the same slug should return 409 Conflict, not 500.
	resp2, _ := http.Post(ts.URL+"/api/v1/tags", "application/json", bytes.NewBufferString(body))
	assert.Equal(t, http.StatusConflict, resp2.StatusCode)
}

func TestUpdateTaskTagsInvalidPayloadReturns400(t *testing.T) {
	ts := setupTestServer(t)

	// Create a task with one tag
	body := `{"title":"Test","description":"x","priority":1,"executor":"cli","tags":["bug"]}`
	resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	id := created["id"].(string)

	// Non-array tags payload (string instead of array) must fail with 400
	req1, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(`{"tags":"bug,ui"}`))
	req1.Header.Set("Content-Type", "application/json")
	resp1, err := http.DefaultClient.Do(req1)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp1.StatusCode)

	// Array of non-strings must also fail with 400
	req2, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(`{"tags":[1,2,3]}`))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode)
}

func TestCreateTaskWithAllFields(t *testing.T) {
	ts := setupTestServer(t)

	body := `{
		"title": "Full field task",
		"description": "All the fields",
		"priority": 1,
		"tags": ["bug","ui"],
		"manual": true,
		"executor": "api",
		"agent_profile": "claude-opus",
		"working_dir": "/repos/test",
		"tools": ["bash","edit"],
		"permissions": {"network": true},
		"environment": {"NODE_ENV": "test"},
		"system_prompt": "test agent",
		"files": ["src/main.go"],
		"cost_budget": 50.0,
		"max_retries": 5,
		"max_duration_ms": 60000,
		"token_budget": 100000,
		"on_done": "close",
		"on_fail": "block",
		"on_review": "auto-approve",
		"on_done_merge": "auto",
		"escalation_chain": ["senior","human"],
		"quality_gates": ["go test ./..."],
		"deliverables": [{"type":"diff","required":true}],
		"deliverable_preset": "backend-fix",
		"blocked_reason": "",
		"metadata": {"source":"test"}
	}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))

	assert.Equal(t, "Full field task", got["title"])
	assert.Equal(t, true, got["manual"])
	assert.Equal(t, "api", got["executor"])
	assert.Equal(t, "claude-opus", got["agent_profile"])
	assert.Equal(t, "close", got["on_done"])
	assert.Equal(t, "block", got["on_fail"])
	assert.Equal(t, "auto-approve", got["on_review"])
	assert.Equal(t, "auto", got["on_done_merge"])
	assert.Equal(t, "backend-fix", got["deliverable_preset"])
	assert.Equal(t, float64(5), got["max_retries"])
	assert.Equal(t, float64(50), got["cost_budget"])
	assert.Equal(t, float64(60000), got["max_duration_ms"])
	assert.Equal(t, float64(100000), got["token_budget"])

	// Structured fields
	tools, ok := got["tools"].([]interface{})
	require.True(t, ok)
	assert.Equal(t, []interface{}{"bash", "edit"}, tools)

	perms, ok := got["permissions"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, perms["network"])

	env, ok := got["environment"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "test", env["NODE_ENV"])

	delivs, ok := got["deliverables"].([]interface{})
	require.True(t, ok)
	require.Len(t, delivs, 1)
	d0 := delivs[0].(map[string]interface{})
	assert.Equal(t, "diff", d0["type"])
	assert.Equal(t, true, d0["required"])
}

func TestUpdateTaskAllNewFields(t *testing.T) {
	ts := setupTestServer(t)

	// Create a task with minimal fields
	createBody := `{"title":"Test","executor":"cli"}`
	resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(createBody))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	id := created["id"].(string)

	// Update each new field type
	updateBody := `{
		"agent_profile": "updated-profile",
		"working_dir": "/new/dir",
		"tools": ["bash","grep"],
		"permissions": {"network": false},
		"environment": {"DEBUG": "1"},
		"system_prompt": "updated",
		"files": ["new.go"],
		"cost_budget": 25.5,
		"max_retries": 10,
		"max_duration_ms": 45000,
		"token_budget": 200000,
		"on_done": "review",
		"on_fail": "retry",
		"escalation_chain": ["senior"],
		"quality_gates": ["lint"],
		"deliverables": [{"type":"log","required":false}],
		"deliverable_preset": "research",
		"blocked_reason": "waiting on data",
		"metadata": {"updated":true}
	}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var updated map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&updated))

	assert.Equal(t, "updated-profile", updated["agent_profile"])
	assert.Equal(t, "/new/dir", updated["working_dir"])
	assert.Equal(t, float64(25.5), updated["cost_budget"])
	assert.Equal(t, float64(10), updated["max_retries"])
	assert.Equal(t, float64(45000), updated["max_duration_ms"])
	assert.Equal(t, float64(200000), updated["token_budget"])
	assert.Equal(t, "review", updated["on_done"])
	assert.Equal(t, "research", updated["deliverable_preset"])
	assert.Equal(t, "waiting on data", updated["blocked_reason"])
}

func TestUpdateTaskRejectsStatusField(t *testing.T) {
	ts := setupTestServer(t)

	createBody := `{"title":"Test","executor":"cli"}`
	resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(createBody))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	id := created["id"].(string)

	updateBody := `{"status":"done"}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode)

	var errBody map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&errBody))
	assert.Contains(t, errBody["error"].(string), "transition")
}

func TestCreateTaskInvalidEnumReturns422(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Test","executor":"cli","on_done":"purge"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var errBody map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&errBody))
	assert.Contains(t, errBody["error"].(string), "on_done")
}

func TestCreateTaskInvalidNumericSentinelReturns422(t *testing.T) {
	ts := setupTestServer(t)

	// max_duration_ms = 0 is rejected
	body1 := `{"title":"Test","executor":"cli","max_duration_ms":0}`
	resp1, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body1))
	assert.Equal(t, http.StatusUnprocessableEntity, resp1.StatusCode)

	// max_duration_ms = -2 is rejected (only -1 is valid negative)
	body2 := `{"title":"Test","executor":"cli","max_duration_ms":-2}`
	resp2, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body2))
	assert.Equal(t, http.StatusUnprocessableEntity, resp2.StatusCode)

	// cost_budget = -5 is rejected
	body3 := `{"title":"Test","executor":"cli","cost_budget":-5}`
	resp3, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body3))
	assert.Equal(t, http.StatusUnprocessableEntity, resp3.StatusCode)
}

func TestCreateTaskUnknownDependsOnReturns422(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Test","executor":"cli","depends_on":["CW-99999999-9999"]}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var errBody map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&errBody)
	assert.Contains(t, errBody["error"].(string), "depends_on")
}

func TestCreateTaskInvalidDeliverableTypeReturns422(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Test","executor":"cli","deliverables":[{"type":"screencast","required":true}]}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var errBody map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&errBody)
	assert.Contains(t, errBody["error"].(string), "deliverables")
}

func TestCreateTaskSentinelUnlimitedRoundTrip(t *testing.T) {
	ts := setupTestServer(t)

	body := `{
		"title":"Sentinel task",
		"executor":"cli",
		"cost_budget":-1,
		"max_duration_ms":-1,
		"token_budget":-1
	}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, float64(-1), got["cost_budget"])
	assert.Equal(t, float64(-1), got["max_duration_ms"])
	assert.Equal(t, float64(-1), got["token_budget"])

	id := got["id"].(string)

	// Now PUT a partial update with cost_budget = 50 (resetting the sentinel to a real value)
	updateBody := `{"cost_budget":50.0}`
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var updated map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&updated))
	assert.Equal(t, float64(50), updated["cost_budget"])
	// The other two sentinel fields should be unchanged
	assert.Equal(t, float64(-1), updated["max_duration_ms"])
	assert.Equal(t, float64(-1), updated["token_budget"])
}

func TestUpdateTaskEmptyBodyIsNoOp(t *testing.T) {
	ts := setupTestServer(t)

	// Create a task
	createBody := `{"title":"Test","executor":"cli"}`
	resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(createBody))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	id := created["id"].(string)
	originalTitle := created["title"]

	// Empty body update should be 200 with the unchanged task
	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp2.StatusCode)

	var updated map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&updated))
	assert.Equal(t, originalTitle, updated["title"])
}

// TestCreateTask_ForcesManualTrue_ExplicitFalse verifies the
// CW-20260417-0133 safety override: POST /tasks with an explicit
// "manual": false still persists manual=true. The override prevents
// scheduler pickup of no-agent tasks from misbehaving portfolio callers.
func TestCreateTask_ForcesManualTrue_ExplicitFalse(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"force-test-explicit","description":"x","manual":false,"executor":"cli"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	assert.Equal(t, true, created["manual"], "manual=false must be coerced to true per CW-20260417-0133")
}

// TestCreateTask_ManualTrue_Unchanged verifies the override is a no-op when
// the caller already passed manual=true (no redundant warn, same result).
func TestCreateTask_ManualTrue_Unchanged(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"force-test-true","description":"x","manual":true,"executor":"cli"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	assert.Equal(t, true, created["manual"])
}

// TestCreateTask_ManualOmitted_CoercedToTrue verifies that a request with no
// manual field at all (the default-false historical behavior that caused the
// bug) is also coerced to manual=true.
func TestCreateTask_ManualOmitted_CoercedToTrue(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"force-test-omitted","description":"x","executor":"cli"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	assert.Equal(t, true, created["manual"], "omitted manual must default to true per CW-20260417-0133")
}

// TestUpdateTask_ManualFalse_Unchanged verifies the Update path is NOT
// affected by the CW-20260417-0133 override — operators need to promote
// reviewed tasks from manual=true to manual=false explicitly.
func TestUpdateTask_ManualFalse_Unchanged(t *testing.T) {
	ts := setupTestServer(t)

	// Create via HTTP (manual will be coerced to true on create).
	createBody := `{"title":"promote-me","description":"x","executor":"cli"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(createBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	id := created["id"].(string)
	assert.Equal(t, true, created["manual"])

	// PUT /tasks/:id with manual=false should persist manual=false.
	upd := `{"manual":false}`
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(upd))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	var updated map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&updated))
	resp2.Body.Close()
	assert.Equal(t, false, updated["manual"], "Update path must NOT coerce manual=false — operators need to promote tasks")
}
