package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// httpCreateDecisionTask creates a decision+blocking task and transitions it
// to doing so checkpoint emits have a meaningful predecessor state.
func httpCreateDecisionTask(t *testing.T, ts string) string {
	t.Helper()
	body := `{"title":"decision","description":"x","kind":"decision","manual":true,"checkpoint_mode":"blocking"}`
	resp, err := http.Post(ts+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	id := created["id"].(string)

	resp, err = http.Post(ts+"/api/v1/tasks/"+id+"/transition", "application/json",
		bytes.NewBufferString(`{"status":"doing"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	return id
}

func TestHTTP_Checkpoint_Emit_and_Get(t *testing.T) {
	ts := setupTestServer(t)
	taskID := httpCreateDecisionTask(t, ts.URL)

	body := `{
        "task_id":"` + taskID + `",
        "type":"collect_data",
        "payload_json":"{\"q\":\"?\"}",
        "emitter_source_type":"system"
    }`
	resp, err := http.Post(ts.URL+"/api/v1/checkpoints", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var emitted map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&emitted))
	resp.Body.Close()
	corr := emitted["correlation_id"].(string)
	assert.Equal(t, "pending", emitted["status"])

	resp, err = http.Get(ts.URL + "/api/v1/checkpoints/" + corr)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Equal(t, taskID, got["task_id"])
	assert.Equal(t, "collect_data", got["type"])
	assert.Equal(t, "pending", got["status"])
}

func TestHTTP_Checkpoint_Respond(t *testing.T) {
	ts := setupTestServer(t)
	taskID := httpCreateDecisionTask(t, ts.URL)

	emitBody := `{"task_id":"` + taskID + `","type":"x","payload_json":"{}","emitter_source_type":"system"}`
	resp, _ := http.Post(ts.URL+"/api/v1/checkpoints", "application/json", bytes.NewBufferString(emitBody))
	var emitted map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&emitted))
	resp.Body.Close()
	corr := emitted["correlation_id"].(string)

	respondBody := `{"response_json":"{\"a\":1}","responder_source_type":"user","responder_source_ref":"chrispian"}`
	resp, err := http.Post(ts.URL+"/api/v1/checkpoints/"+corr+"/respond", "application/json",
		bytes.NewBufferString(respondBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var responded map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&responded))
	resp.Body.Close()
	assert.Equal(t, "responded", responded["status"])
	assert.Equal(t, "user", responded["responder_source_type"])
}

func TestHTTP_Checkpoint_RespondConflict(t *testing.T) {
	ts := setupTestServer(t)
	taskID := httpCreateDecisionTask(t, ts.URL)

	emitBody := `{"task_id":"` + taskID + `","type":"x","payload_json":"{}","emitter_source_type":"system"}`
	resp, _ := http.Post(ts.URL+"/api/v1/checkpoints", "application/json", bytes.NewBufferString(emitBody))
	var emitted map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&emitted))
	resp.Body.Close()
	corr := emitted["correlation_id"].(string)

	respondBody := `{"response_json":"{}","responder_source_type":"user"}`
	resp, _ = http.Post(ts.URL+"/api/v1/checkpoints/"+corr+"/respond", "application/json",
		bytes.NewBufferString(respondBody))
	resp.Body.Close()

	// Second respond → 409
	resp, err := http.Post(ts.URL+"/api/v1/checkpoints/"+corr+"/respond", "application/json",
		bytes.NewBufferString(respondBody))
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()
}

func TestHTTP_Checkpoint_Cancel(t *testing.T) {
	ts := setupTestServer(t)
	taskID := httpCreateDecisionTask(t, ts.URL)

	emitBody := `{"task_id":"` + taskID + `","type":"x","payload_json":"{}","emitter_source_type":"system"}`
	resp, _ := http.Post(ts.URL+"/api/v1/checkpoints", "application/json", bytes.NewBufferString(emitBody))
	var emitted map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&emitted))
	resp.Body.Close()
	corr := emitted["correlation_id"].(string)

	cancelBody := `{"reason":"no longer relevant","canceler_source_type":"user","canceler_source_ref":"chrispian"}`
	resp, err := http.Post(ts.URL+"/api/v1/checkpoints/"+corr+"/cancel", "application/json",
		bytes.NewBufferString(cancelBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var canceled map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&canceled))
	resp.Body.Close()
	assert.Equal(t, "canceled", canceled["status"])
}

func TestHTTP_Checkpoint_NotFound(t *testing.T) {
	ts := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/api/v1/checkpoints/nonexistent")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestHTTP_Checkpoint_ListForTask(t *testing.T) {
	ts := setupTestServer(t)
	taskID := httpCreateDecisionTask(t, ts.URL)

	for i := 0; i < 2; i++ {
		emitBody := `{"task_id":"` + taskID + `","type":"x","payload_json":"{}","emitter_source_type":"system"}`
		resp, _ := http.Post(ts.URL+"/api/v1/checkpoints", "application/json", bytes.NewBufferString(emitBody))
		resp.Body.Close()
	}

	resp, err := http.Get(ts.URL + "/api/v1/tasks/" + taskID + "/checkpoints")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	resp.Body.Close()
	list := result["checkpoints"].([]interface{})
	assert.Len(t, list, 2)
}

func TestHTTP_Checkpoint_Pending(t *testing.T) {
	ts := setupTestServer(t)
	taskID := httpCreateDecisionTask(t, ts.URL)

	emitBody := `{"task_id":"` + taskID + `","type":"x","payload_json":"{}","emitter_source_type":"system"}`
	resp, _ := http.Post(ts.URL+"/api/v1/checkpoints", "application/json", bytes.NewBufferString(emitBody))
	resp.Body.Close()

	resp, err := http.Get(ts.URL + "/api/v1/checkpoints/pending")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	resp.Body.Close()
	list := result["checkpoints"].([]interface{})
	assert.Len(t, list, 1)
}

func TestHTTP_Checkpoint_EmitWithTimeoutAt(t *testing.T) {
	ts := setupTestServer(t)
	taskID := httpCreateDecisionTask(t, ts.URL)

	deadline := time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339)
	body := `{
        "task_id":"` + taskID + `",
        "type":"collect_data",
        "payload_json":"{}",
        "emitter_source_type":"system",
        "timeout_at":"` + deadline + `"
    }`
	resp, err := http.Post(ts.URL+"/api/v1/checkpoints", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var emitted map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&emitted))
	resp.Body.Close()
	assert.NotNil(t, emitted["timeout_at"])
}
