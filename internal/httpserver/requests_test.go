package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPRequestLifecycle(t *testing.T) {
	ts := setupTestServer(t)
	call := func(method, path, body string, status int) map[string]any {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		var out map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		require.Equal(t, status, res.StatusCode, out)
		return out
	}
	task := call("POST", "/api/v1/tasks", `{"title":"Please check reports","manual":true}`, 201)
	path := "/api/v1/tasks/" + task["id"].(string)
	call("GET", path+"/request", "", 404)
	reg := `{"requester_urn":"msg://user/local/owner","recipient_urn":"msg://agent/local/worker","mail_message_id":"request-message","due_ack_at":"2026-01-01T00:00:00Z"}`
	rec := call("PUT", path+"/request", reg, 200)
	assert.Equal(t, "submitted", rec["state"])
	assert.Nil(t, rec["ack_at"])
	assert.Equal(t, rec, call("PUT", path+"/request", reg, 200))
	pending := call("GET", "/api/v1/requests?awaiting_ack=true&due_before=2026-11-20T00:00:00Z&limit=1", "", 200)
	require.Len(t, pending["items"], 1)
	assert.Equal(t, task["id"], pending["next_after_task_id"])
	call("GET", "/api/v1/requests?awaiting_ack=wat", "", 400)
	call("GET", "/api/v1/requests?due_before=wat", "", 400)
	call("GET", "/api/v1/requests?limit=0", "", 400)
	call("PUT", path+"/request", `{"requester_urn":"msg://user/local/owner","recipient_urn":"msg://agent/local/other","mail_message_id":"request-message"}`, 409)
	call("POST", path+"/request/transition", `{"state":"unknown"}`, 400)
	call("POST", path+"/comments", `{"content":"STATUS started · working","author":"msg://agent/local/worker"}`, 201)
	rec = call("GET", path+"/request", "", 200)
	assert.Equal(t, "acknowledged", rec["state"])
	assert.NotNil(t, rec["ack_at"])
	task = call("GET", path, "", 200)
	metadata := task["metadata"].(map[string]any)
	assert.Equal(t, rec, metadata["request"])
	call("POST", path+"/request/transition", `{"state":"in_progress"}`, 200)
	call("POST", path+"/request/transition", `{"state":"done"}`, 400)
	rec = call("POST", path+"/request/transition", `{"state":"declined","summary":"DECLINED: unavailable"}`, 200)
	assert.Equal(t, "declined", rec["state"])
	assert.NotNil(t, rec["closed_at"])
	call("POST", path+"/request/transition", `{"state":"in_progress"}`, 409)
}

func TestHTTPRequestSubmitAndAttach(t *testing.T) {
	ts := setupTestServer(t)
	call := func(method, path, body string, status int) map[string]any {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		var out map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		require.Equal(t, status, res.StatusCode, out)
		return out
	}
	body := `{"requester_urn":"msg://user/local/owner","recipient_urn":"msg://agent/local/worker","send_key":"stable","subject":"Check reports","body":"Yesterday","deliverable":"Summary","recipient_online":false}`
	r := call("POST", "/api/v1/requests/submit", body, 200)
	assert.Empty(t, r["mail_message_id"])
	assert.Equal(t, r["submitted_at"], r["due_ack_at"])
	assert.Equal(t, r, call("POST", "/api/v1/requests/submit", body, 200))
	path := "/api/v1/tasks/" + r["task_id"].(string) + "/request/mail"
	attached := call("PUT", path, `{"mail_message_id":"transport-id"}`, 200)
	assert.Equal(t, "transport-id", attached["mail_message_id"])
	assert.Equal(t, attached, call("PUT", path, `{"mail_message_id":"transport-id"}`, 200))
	call("PUT", path, `{"mail_message_id":"other-id"}`, 409)
	call("POST", "/api/v1/requests/submit", `{"requester_urn":"msg://user/local/owner","recipient_urn":"msg://agent/local/worker","send_key":"stable","subject":"Changed"}`, 409)
}

func TestHTTPRequestCanonicalContract(t *testing.T) {
	ts := setupTestServer(t)
	call := func(method, path, body string, status int) map[string]any {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, bytes.NewBufferString(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer res.Body.Close()
		var out map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&out))
		require.Equal(t, status, res.StatusCode, out)
		return out
	}
	body := `{"requester_urn":"msg://user/local/owner","recipient_urn":"msg://agent/local/worker","idempotency_key":"canonical-key","subject":"Work","body":"Check reports","deliverable":"Summary","recipient_online":false}`
	r := call("POST", "/api/v1/requests", body, 200)
	assert.Equal(t, "canonical-key", r["idempotency_key"])
	assert.Equal(t, r, call("POST", "/api/v1/requests", body, 200))
	path := "/api/v1/tasks/" + r["task_id"].(string)
	attached := call("PATCH", path+"/request", `{"mail_message_id":"actual-id"}`, 200)
	assert.Equal(t, "actual-id", attached["mail_message_id"])
	call("POST", path+"/request/transition", `{"actor_urn":"msg://agent/local/worker","state":"done","evidence":"Tests pass"}`, 400)
	call("POST", path+"/request/transition", `{"actor_urn":"msg://agent/local/intruder","state":"done","summary":"Wrong recipient"}`, 403)
	closed := call("POST", path+"/request/transition", `{"actor_urn":"msg://agent/local/worker","state":"done","summary":"DONE: checked","evidence":["tests pass","ci green"]}`, 200)
	assert.Equal(t, "done", closed["state"])
	call("POST", path+"/request/transition", `{"actor_urn":"msg://agent/local/intruder","state":"done","summary":"Unauthorized retry"}`, 403)
	comments := call("GET", path, "", 200)
	assert.Equal(t, "done", comments["status"])
	// Read comments to verify actor/evidence.
	res, err := http.Get(ts.URL + path + "/comments")
	require.NoError(t, err)
	defer res.Body.Close()
	var envelope struct {
		Items []map[string]any `json:"items"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&envelope))
	rows := envelope.Items
	require.Len(t, rows, 1)
	assert.Equal(t, "msg://agent/local/worker", rows[0]["author"])
	assert.Contains(t, rows[0]["content"], "Evidence: \n- tests pass\n- ci green")
}
