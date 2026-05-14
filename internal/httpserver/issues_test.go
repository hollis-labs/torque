package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func setupIssueProject(t *testing.T, baseURL string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, baseURL+"/api/v1/settings/features.projects", bytes.NewBufferString(`{"value":"true"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp, err = http.Post(baseURL+"/api/v1/projects", "application/json", bytes.NewBufferString(`{"name":"Issue Project","repo_path":"/tmp/issues"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var project map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&project))
	resp.Body.Close()
	return project["id"].(string)
}

func TestHTTP_IssueCreateAndList(t *testing.T) {
	ts := setupTestServer(t)
	projectID := setupIssueProject(t, ts.URL)

	resp, err := http.Post(ts.URL+"/api/v1/issues", "application/json", bytes.NewBufferString(
		`{"title":"Login callback fails","details":"Users see HTTP 500.","project_id":"`+projectID+`"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	require.Equal(t, "issue", created["kind"])
	require.Equal(t, "backlog", created["status"])
	require.Equal(t, true, created["manual"])
	require.Equal(t, "Users see HTTP 500.", created["body"])

	resp, err = http.Get(ts.URL + "/api/v1/issues?project_id=" + projectID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var listed map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listed))
	resp.Body.Close()
	require.Len(t, listed["issues"].([]interface{}), 1)
}

func TestHTTP_IssueCreateRequiresProjectID(t *testing.T) {
	ts := setupTestServer(t)

	resp, err := http.Post(ts.URL+"/api/v1/issues", "application/json", bytes.NewBufferString(
		`{"title":"Missing project","body":"Details"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	resp.Body.Close()
}
