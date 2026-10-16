package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHTTPSelfReport(t *testing.T) {
	ts := setupTestServer(t)
	post := func(path, body string, status int) map[string]any {
		t.Helper()
		resp, err := http.Post(ts.URL+"/api/v1"+path, "application/json", bytes.NewBufferString(body))
		require.NoError(t, err)
		defer resp.Body.Close()
		var out map[string]any
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		require.Equal(t, status, resp.StatusCode, out)
		return out
	}
	task := post("/tasks", `{"title":"reports","progress_detail":"step","checkpoint_mode":"blocking"}`, 201)
	path := "/tasks/" + task["id"].(string)
	first := post(path+"/progress", `{"label":"ready","idempotency_key":"p"}`, 200)
	require.Equal(t, true, first["emitted"])
	duplicate := post(path+"/progress", `{"label":"ready","idempotency_key":"p"}`, 200)
	require.Equal(t, "duplicate", duplicate["suppressed_reason"])
	require.Equal(t, first["event_id"], duplicate["event_id"])
	conflict := post(path+"/progress", `{"label":"different","idempotency_key":"p"}`, 409)
	require.Equal(t, "idempotency_conflict", conflict["code"])
	post(path+"/progress", `{"label":"ready","task_id":"another"}`, 400)
	data := post(path+"/data", `{"schema":"example@1","data":{"value":1},"idempotency_key":"d"}`, 200)
	require.NotEmpty(t, data["event_id"])
	repeat := post(path+"/data", `{"schema":"example@1","data":{"value":1},"idempotency_key":"d"}`, 200)
	require.Equal(t, true, repeat["duplicate"])
	post(path+"/data", `{"schema":"example@1","data":null}`, 400)
}
