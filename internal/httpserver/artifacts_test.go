package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

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
