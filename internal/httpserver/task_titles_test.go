package httpserver_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// taskReferenceCall sends a JSON request under /api/v1 and decodes the JSON
// reply (object or array) into out, which may be nil.
func taskReferenceCall(t *testing.T, method, base, path string, body any, headers map[string]string) (int, any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, base+"/api/v1"+path, rdr)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NotEqual(t, byte('<'), referenceFirstByte(raw), "got HTML (SPA fallback?) for %s %s: %.120s", method, path, raw)
	var out any
	require.NoError(t, json.Unmarshal(raw, &out), "body: %s", raw)
	return resp.StatusCode, out
}

func referenceFirstByte(b []byte) byte {
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

func referenceObj(v any) map[string]any { m, _ := v.(map[string]any); return m }

func TestHTTP_TaskTitlesWithoutPlanner(t *testing.T) {
	ts := setupTestServer(t)

	taskResp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(`{"title":"Referenced task","description":"x","executor":"cli","tags":["ws-demo"]}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, taskResp.StatusCode)
	var task map[string]any
	require.NoError(t, json.NewDecoder(taskResp.Body).Decode(&task))
	taskResp.Body.Close()
	id := task["id"].(string)

	status, body := taskReferenceCall(t, http.MethodGet, ts.URL, "/tasks/titles?ids="+id+",CW-20200101-9999", nil, nil)
	require.Equal(t, http.StatusOK, status, "%v", body)
	titles := referenceObj(body)
	assert.Equal(t, "Referenced task", referenceObj(titles[id])["title"])
	assert.Nil(t, titles["CW-20200101-9999"])
	assert.Contains(t, titles, "CW-20200101-9999")
	status, body = taskReferenceCall(t, http.MethodGet, ts.URL, "/tasks/titles?ids=nope", nil, nil)
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "invalid", referenceObj(body)["code"])
	status, body = taskReferenceCall(t, http.MethodGet, ts.URL, "/tasks/titles", nil, nil)
	require.Equal(t, http.StatusOK, status, "no ids is an empty answer, not a task id called titles")
	assert.Empty(t, referenceObj(body))
}

func TestHTTP_RetiredPlannerRoutesAreNotSPA(t *testing.T) {
	ts := setupTestServer(t)
	for _, path := range []string{"/planner/databases", "/planner/board", "/planner/workstreams"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
			status, body := taskReferenceCall(t, method, ts.URL, path, nil, nil)
			assert.Equal(t, http.StatusNotFound, status, "%s %s", method, path)
			assert.Equal(t, "API route not found", referenceObj(body)["error"])
		}
	}
}
