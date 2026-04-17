package httpserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createCommentsTestTask is a small helper that creates a task via HTTP and
// returns its id, so comments endpoints have a valid task_id to target.
func createCommentsTestTask(t *testing.T, baseURL string) string {
	t.Helper()
	body := `{"title":"comments test","description":"x","executor":"cli"}`
	resp, err := http.Post(baseURL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	id, _ := created["id"].(string)
	require.NotEmpty(t, id)
	return id
}

func TestHTTP_NestedComments_PostAndList(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createCommentsTestTask(t, ts.URL)

	// POST nested with body {content} only. author defaults to "user".
	postResp, err := http.Post(
		ts.URL+"/api/v1/tasks/"+taskID+"/comments",
		"application/json",
		bytes.NewBufferString(`{"content":"hello from composer"}`),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, postResp.StatusCode)

	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(postResp.Body).Decode(&created))
	postResp.Body.Close()
	assert.Equal(t, "hello from composer", created["content"])
	assert.Equal(t, taskID, created["task_id"])
	assert.Equal(t, "user", created["author"])
	assert.NotEmpty(t, created["created_at"])

	// GET nested returns a Comment[] directly (no wrapper).
	getResp, err := http.Get(ts.URL + "/api/v1/tasks/" + taskID + "/comments")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getResp.StatusCode)

	var listed []map[string]interface{}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&listed))
	getResp.Body.Close()
	require.Len(t, listed, 1)
	assert.Equal(t, "hello from composer", listed[0]["content"])
	assert.Equal(t, "user", listed[0]["author"])
	assert.Equal(t, taskID, listed[0]["task_id"])
}

func TestHTTP_NestedComments_XUserHeaderOverridesAuthor(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createCommentsTestTask(t, ts.URL)

	req, err := http.NewRequest(
		http.MethodPost,
		ts.URL+"/api/v1/tasks/"+taskID+"/comments",
		bytes.NewBufferString(`{"content":"hi"}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User", "alice")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	assert.Equal(t, "alice", created["author"])
}

func TestHTTP_NestedComments_BodyAuthorWinsOverDefault(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createCommentsTestTask(t, ts.URL)

	resp, err := http.Post(
		ts.URL+"/api/v1/tasks/"+taskID+"/comments",
		"application/json",
		bytes.NewBufferString(`{"content":"hi","author":"bob"}`),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()
	assert.Equal(t, "bob", created["author"])
}

func TestHTTP_NestedComments_EmptyContentIs400(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createCommentsTestTask(t, ts.URL)

	resp, err := http.Post(
		ts.URL+"/api/v1/tasks/"+taskID+"/comments",
		"application/json",
		bytes.NewBufferString(`{"content":""}`),
	)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	resp.Body.Close()
}

// Regression guard: the flat /api/v1/comments endpoints must still accept the
// original shape used by existing callers (MCP-equivalent HTTP clients, curl).
func TestHTTP_FlatComments_RegressionGuard(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createCommentsTestTask(t, ts.URL)

	body := `{"task_id":"` + taskID + `","author":"carol","content":"flat style"}`
	postResp, err := http.Post(
		ts.URL+"/api/v1/comments",
		"application/json",
		bytes.NewBufferString(body),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, postResp.StatusCode)
	postResp.Body.Close()

	getResp, err := http.Get(ts.URL + "/api/v1/comments?task_id=" + taskID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getResp.StatusCode)
	raw, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	// Accept either the legacy {"comments":[...]} wrapper or a direct array,
	// but must include the author we just posted.
	assert.True(t, strings.Contains(string(raw), `"carol"`), "response should include posted author; got %s", string(raw))
}

func TestHTTP_NestedComments_MissingTaskReturns500OrError(t *testing.T) {
	ts := setupTestServer(t)

	// task_id that doesn't exist — FK violation surfaces as 500.
	resp, err := http.Post(
		ts.URL+"/api/v1/tasks/CW-NOPE/comments",
		"application/json",
		bytes.NewBufferString(`{"content":"orphan"}`),
	)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, resp.StatusCode, 400)
	resp.Body.Close()
}
