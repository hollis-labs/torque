package httpserver_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/httpserver"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
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
	handler := httpserver.New(svc)
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
