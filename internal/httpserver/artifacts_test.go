package httpserver_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// helper: create a task via HTTP and return its ID.
func createTestTask(t *testing.T, baseURL string) string {
	t.Helper()
	body := `{"title":"artifact api test","description":"x","executor":"cli"}`
	resp, err := http.Post(baseURL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	return got["id"].(string)
}

// helper: POST a file artifact for the task, return its id.
func postArtifact(t *testing.T, baseURL, taskID, filePath string) int64 {
	t.Helper()
	body := `{"task_id":"` + taskID + `","type":"file","file_path":"` + filePath + `"}`
	resp, err := http.Post(baseURL+"/api/v1/artifacts", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	// id comes back as a JSON number → float64.
	switch v := got["id"].(type) {
	case float64:
		return int64(v)
	case string:
		i, _ := strconv.ParseInt(v, 10, 64)
		return i
	}
	t.Fatalf("no id in create artifact response: %v", got)
	return 0
}

func TestHTTP_TaskArtifactsAlias(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createTestTask(t, ts.URL)

	_ = postArtifact(t, ts.URL, taskID, "/tmp/one.log")
	_ = postArtifact(t, ts.URL, taskID, "/tmp/two.log")

	// Legacy query-string form.
	resp, err := http.Get(ts.URL + "/api/v1/artifacts?task_id=" + taskID)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var legacy map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&legacy))
	legacyList := legacy["artifacts"].([]interface{})
	require.Len(t, legacyList, 2)

	// Alias form — same shape.
	resp2, err := http.Get(ts.URL + "/api/v1/tasks/" + taskID + "/artifacts")
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	var alias map[string]interface{}
	require.NoError(t, json.NewDecoder(resp2.Body).Decode(&alias))
	aliasList := alias["artifacts"].([]interface{})
	require.Len(t, aliasList, 2)
	assert.Equal(t, legacyList[0], aliasList[0])
	assert.Equal(t, legacyList[1], aliasList[1])
}

func TestHTTP_GetArtifactByID(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createTestTask(t, ts.URL)
	id := postArtifact(t, ts.URL, taskID, "/tmp/report.log")

	resp, err := http.Get(ts.URL + "/api/v1/artifacts/" + strconv.FormatInt(id, 10))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, float64(id), got["ID"])
	assert.Equal(t, taskID, got["TaskID"])
	assert.Equal(t, "/tmp/report.log", got["FilePath"])
}

func TestHTTP_GetArtifactByID_NotFound(t *testing.T) {
	ts := setupTestServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/artifacts/999999")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHTTP_DeleteArtifact(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createTestTask(t, ts.URL)
	id := postArtifact(t, ts.URL, taskID, "/tmp/tmp.log")

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/artifacts/"+strconv.FormatInt(id, 10), nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Subsequent GET returns 404.
	resp2, err := http.Get(ts.URL + "/api/v1/artifacts/" + strconv.FormatInt(id, 10))
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestHTTP_DeleteArtifact_NotFound(t *testing.T) {
	ts := setupTestServer(t)

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/artifacts/777777", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHTTP_ArtifactCreateAndPatchRoundTrip(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createTestTask(t, ts.URL)
	tmpFile := filepath.Join(t.TempDir(), "artifact.txt")
	require.NoError(t, os.WriteFile(tmpFile, []byte("still here"), 0o600))

	createBody := `{"task_id":` + strconv.Quote(taskID) + `,"type":"inline","content":"old","file_path":` + strconv.Quote(tmpFile) + `,"metadata":{"nested":{"big":9007199254740993123}}}`
	resp, err := http.Post(ts.URL+"/api/v1/artifacts", "application/json", bytes.NewBufferString(createBody))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	id := int64(created["id"].(float64))
	resp, err = http.Get(ts.URL + "/api/v1/artifacts/" + strconv.FormatInt(id, 10))
	require.NoError(t, err)
	var before map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&before))
	resp.Body.Close()

	req, err := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/artifacts/"+strconv.FormatInt(id, 10), bytes.NewBufferString(`{"content":"new","url":"https://example.test/a","file_path":""}`))
	require.NoError(t, err)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var patched map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&patched))
	assert.Equal(t, float64(id), patched["ID"])
	assert.Equal(t, taskID, patched["TaskID"])
	assert.Equal(t, before["CreatedAt"], patched["CreatedAt"])
	assert.Equal(t, "inline", patched["Type"])
	assert.Equal(t, "new", patched["Content"])
	assert.Equal(t, "https://example.test/a", patched["URL"])
	assert.Equal(t, "", patched["FilePath"])
	assert.Contains(t, patched["Metadata"].(map[string]interface{})["String"], `9007199254740993123`)
	_, statErr := os.Stat(tmpFile)
	require.NoError(t, statErr, "updating/clearing file_path must not delete referenced files")

	req, err = http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/artifacts/"+strconv.FormatInt(id, 10), bytes.NewBufferString(`{"metadata":{},"content":""}`))
	require.NoError(t, err)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var emptyMeta map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&emptyMeta))
	assert.Equal(t, true, emptyMeta["Metadata"].(map[string]interface{})["Valid"])
	assert.Equal(t, "{}", emptyMeta["Metadata"].(map[string]interface{})["String"])

	req, err = http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/artifacts/"+strconv.FormatInt(id, 10), bytes.NewBufferString(`{"metadata":null,"run_id":null}`))
	require.NoError(t, err)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var cleared map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&cleared))
	assert.Equal(t, false, cleared["Metadata"].(map[string]interface{})["Valid"])
	assert.Equal(t, false, cleared["RunID"].(map[string]interface{})["Valid"])
}

func TestHTTP_ArtifactPatchRejectsInvalidWithoutMutation(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createTestTask(t, ts.URL)
	resp, err := http.Post(ts.URL+"/api/v1/artifacts", "application/json", bytes.NewBufferString(`{"task_id":`+strconv.Quote(taskID)+`,"type":"inline","content":{"wrong":true}}`))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	id := postArtifact(t, ts.URL, taskID, "/tmp/original.log")

	req, err := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/artifacts/"+strconv.FormatInt(id, 10), bytes.NewBufferString(`{"file_path":null,"content":"mutated"}`))
	require.NoError(t, err)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp, err = http.Get(ts.URL + "/api/v1/artifacts/" + strconv.FormatInt(id, 10))
	require.NoError(t, err)
	defer resp.Body.Close()
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "/tmp/original.log", got["FilePath"])
	assert.Equal(t, "", got["Content"])

	req, err = http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/artifacts/"+strconv.FormatInt(id, 10), bytes.NewBufferString(`{"metadata":"{} {}","content":"mutated"}`))
	require.NoError(t, err)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp, err = http.Get(ts.URL + "/api/v1/artifacts/" + strconv.FormatInt(id, 10))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "", got["Content"])
}

func TestHTTP_ArtifactRunLinkValidation(t *testing.T) {
	ts, store := setupRunsTestServer(t)
	taskA := createTestTask(t, ts.URL)
	taskB := createTestTask(t, ts.URL)
	runA, err := store.CreateRun(&sqlstore.RunRecord{TaskID: taskA, Executor: "cli"})
	require.NoError(t, err)
	runB, err := store.CreateRun(&sqlstore.RunRecord{TaskID: taskB, Executor: "cli"})
	require.NoError(t, err)

	resp, err := http.Post(ts.URL+"/api/v1/artifacts", "application/json", bytes.NewBufferString(`{"task_id":`+strconv.Quote(taskA)+`,"type":"inline","run_id":999999}`))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	body := `{"task_id":` + strconv.Quote(taskA) + `,"type":"inline","run_id":` + strconv.FormatInt(runA, 10) + `}`
	resp, err = http.Post(ts.URL+"/api/v1/artifacts", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	id := int64(created["id"].(float64))

	req, err := http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/artifacts/"+strconv.FormatInt(id, 10), bytes.NewBufferString(`{"run_id":`+strconv.FormatInt(runB, 10)+`,"url":"https://bad.example"}`))
	require.NoError(t, err)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	got, err := store.GetArtifact(id)
	require.NoError(t, err)
	assert.Equal(t, sql.NullInt64{Int64: runA, Valid: true}, got.RunID)
	assert.Equal(t, "", got.URL)

	req, err = http.NewRequest(http.MethodPatch, ts.URL+"/api/v1/artifacts/999999", bytes.NewBufferString(`{"url":"https://missing.example"}`))
	require.NoError(t, err)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}
