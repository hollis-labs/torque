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

// enableCollectionsFeature flips features.collections=true via the settings
// API so the service-layer feature gate doesn't 404 every collection route.
func enableCollectionsFeature(t *testing.T, baseURL string) {
	t.Helper()
	req, err := http.NewRequest(
		http.MethodPut,
		baseURL+"/api/v1/settings/features.collections",
		bytes.NewBufferString(`{"value":"true"}`),
	)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
}

// assertJSONResponse fails the test if the response body looks like HTML
// (the SPA fallback) instead of JSON. This is the specific regression we
// want to guard against — a missing or shadowed route falls through to
// chi's NotFound handler which serves index.html.
func assertJSONResponse(t *testing.T, resp *http.Response, wantStatus int) map[string]interface{} {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()

	bodyStr := string(body)
	require.Falsef(t, strings.HasPrefix(strings.TrimSpace(bodyStr), "<"),
		"expected JSON response, got HTML (SPA fallback?): %.200s", bodyStr)

	contentType := resp.Header.Get("Content-Type")
	require.Containsf(t, contentType, "application/json",
		"expected application/json content-type, got %q (body: %.200s)", contentType, bodyStr)

	require.Equal(t, wantStatus, resp.StatusCode, "body: %s", bodyStr)

	var got map[string]interface{}
	require.NoErrorf(t, json.Unmarshal(body, &got), "body: %s", bodyStr)
	return got
}

// TestHTTP_InboxRoute_NotShadowedByCollectionID guards against chi route
// ordering regression. The literal /collections/inbox/tasks endpoint must
// resolve before /collections/{id}/tasks; otherwise chi treats "inbox" as
// a collection ID and the handler 404s on lookup, falling through to the
// SPA fallback (HTML response), which is exactly what the GUI saw and
// surfaced as "API returned HTML instead of JSON".
func TestHTTP_InboxRoute_NotShadowedByCollectionID(t *testing.T) {
	ts := setupTestServer(t)
	enableCollectionsFeature(t, ts.URL)

	resp, err := http.Get(ts.URL + "/api/v1/collections/inbox/tasks")
	require.NoError(t, err)
	got := assertJSONResponse(t, resp, http.StatusOK)

	// Empty inbox is OK; we just need the shape to match the list response,
	// not the SPA HTML.
	tasks, ok := got["tasks"].([]interface{})
	require.True(t, ok, "expected tasks array, got: %v", got)
	assert.Empty(t, tasks)
}

// TestHTTP_AddTaskToInbox_NotShadowedByCollectionID is the POST counterpart
// to the inbox route-shadowing guard. POST /collections/inbox/tasks must
// route to addTaskToInbox, not to addTaskToCollection({id="inbox"}).
func TestHTTP_AddTaskToInbox_NotShadowedByCollectionID(t *testing.T) {
	ts := setupTestServer(t)
	enableCollectionsFeature(t, ts.URL)

	// Need a real task to send to the inbox.
	taskResp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json",
		bytes.NewBufferString(`{"title":"inbox test","description":"x","executor":"cli"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, taskResp.StatusCode)
	var task map[string]interface{}
	require.NoError(t, json.NewDecoder(taskResp.Body).Decode(&task))
	taskResp.Body.Close()
	taskID := task["id"].(string)

	resp, err := http.Post(ts.URL+"/api/v1/collections/inbox/tasks",
		"application/json",
		bytes.NewBufferString(`{"task_id":"`+taskID+`"}`))
	require.NoError(t, err)
	got := assertJSONResponse(t, resp, http.StatusOK)
	assert.Equal(t, taskID, got["task_id"])
	assert.Equal(t, true, got["in_inbox"])
}

// TestHTTP_MoveTaskRoute_NotShadowedByCollectionID guards POST
// /collections/tasks/move against being matched as
// /collections/{id}/... where {id}="tasks". The literal `/tasks/move` path
// segment must resolve to moveTaskToCollection, not to addTaskToCollection.
func TestHTTP_MoveTaskRoute_NotShadowedByCollectionID(t *testing.T) {
	ts := setupTestServer(t)
	enableCollectionsFeature(t, ts.URL)

	// Create a task and a target collection so MoveTask has real IDs to
	// work with — otherwise we'd be testing an error path and couldn't
	// distinguish a 404 from a route miss.
	taskResp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json",
		bytes.NewBufferString(`{"title":"move test","description":"x","executor":"cli"}`))
	require.NoError(t, err)
	var task map[string]interface{}
	require.NoError(t, json.NewDecoder(taskResp.Body).Decode(&task))
	taskResp.Body.Close()
	taskID := task["id"].(string)

	colResp, err := http.Post(ts.URL+"/api/v1/collections", "application/json",
		bytes.NewBufferString(`{"name":"target","description":"x"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, colResp.StatusCode)
	var col map[string]interface{}
	require.NoError(t, json.NewDecoder(colResp.Body).Decode(&col))
	colResp.Body.Close()
	colID := col["id"].(string)

	body := `{"task_id":"` + taskID + `","target_collection_id":"` + colID + `"}`
	resp, err := http.Post(ts.URL+"/api/v1/collections/tasks/move",
		"application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	got := assertJSONResponse(t, resp, http.StatusOK)
	assert.Equal(t, taskID, got["task_id"])
	assert.Equal(t, colID, got["target_collection_id"])
	assert.Equal(t, true, got["moved"])
}

// TestHTTP_CollectionByID_StillWorksAfterLiteralRoutes is the inverse
// guard: confirming the literal routes don't accidentally swallow the
// genuine /collections/{id} parameterized lookup.
func TestHTTP_CollectionByID_StillWorksAfterLiteralRoutes(t *testing.T) {
	ts := setupTestServer(t)
	enableCollectionsFeature(t, ts.URL)

	colResp, err := http.Post(ts.URL+"/api/v1/collections", "application/json",
		bytes.NewBufferString(`{"name":"by-id-test","description":"x"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, colResp.StatusCode)
	var col map[string]interface{}
	require.NoError(t, json.NewDecoder(colResp.Body).Decode(&col))
	colResp.Body.Close()
	colID := col["id"].(string)

	// GET /collections/{id} → still resolves to getCollection.
	resp, err := http.Get(ts.URL + "/api/v1/collections/" + colID)
	require.NoError(t, err)
	got := assertJSONResponse(t, resp, http.StatusOK)
	assert.Equal(t, colID, got["id"])
	assert.Equal(t, "by-id-test", got["name"])

	// GET /collections/{id}/tasks → still resolves to listCollectionTasks.
	resp2, err := http.Get(ts.URL + "/api/v1/collections/" + colID + "/tasks")
	require.NoError(t, err)
	got2 := assertJSONResponse(t, resp2, http.StatusOK)
	tasks, ok := got2["tasks"].([]interface{})
	require.True(t, ok)
	assert.Empty(t, tasks)
}

// TestHTTP_DeleteTaskFromCollection_ScopeMismatchReturns409 guards the
// scoped DELETE /collections/{id}/tasks/{task_id} route. Calling it with
// a collection_id that doesn't match the task's actual membership must
// return 409 (not silently detach + emit a wrong-collection SSE event).
func TestHTTP_DeleteTaskFromCollection_ScopeMismatchReturns409(t *testing.T) {
	ts := setupTestServer(t)
	enableCollectionsFeature(t, ts.URL)

	// Create two collections and a task assigned to the first one.
	colA := postCollection(t, ts.URL, "scope-A")
	colB := postCollection(t, ts.URL, "scope-B")
	taskID := postTask(t, ts.URL, "scope mismatch task")

	addBody := `{"task_id":"` + taskID + `"}`
	addResp, err := http.Post(ts.URL+"/api/v1/collections/"+colA+"/tasks",
		"application/json", bytes.NewBufferString(addBody))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, addResp.StatusCode)
	addResp.Body.Close()

	// Try to remove via the WRONG collection's scoped DELETE — must 409.
	delReq, err := http.NewRequest(http.MethodDelete,
		ts.URL+"/api/v1/collections/"+colB+"/tasks/"+taskID, nil)
	require.NoError(t, err)
	delResp, err := http.DefaultClient.Do(delReq)
	require.NoError(t, err)
	assertJSONResponse(t, delResp, http.StatusConflict)

	// Confirm the task is still in colA — no silent detach.
	listResp, err := http.Get(ts.URL + "/api/v1/collections/" + colA + "/tasks")
	require.NoError(t, err)
	listBody := assertJSONResponse(t, listResp, http.StatusOK)
	tasks2, ok := listBody["tasks"].([]interface{})
	require.True(t, ok)
	require.Len(t, tasks2, 1, "task must still be in colA after the rejected scope-mismatch DELETE")

	// And the correct scoped DELETE succeeds.
	delReqOK, err := http.NewRequest(http.MethodDelete,
		ts.URL+"/api/v1/collections/"+colA+"/tasks/"+taskID, nil)
	require.NoError(t, err)
	delRespOK, err := http.DefaultClient.Do(delReqOK)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, delRespOK.StatusCode)
	delRespOK.Body.Close()
}

// postCollection is a small helper for tests that need a real collection ID.
func postCollection(t *testing.T, baseURL, name string) string {
	t.Helper()
	resp, err := http.Post(baseURL+"/api/v1/collections", "application/json",
		bytes.NewBufferString(`{"name":"`+name+`","description":""}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	return got["id"].(string)
}

// postTask creates a minimal task and returns its ID.
func postTask(t *testing.T, baseURL, title string) string {
	t.Helper()
	resp, err := http.Post(baseURL+"/api/v1/tasks", "application/json",
		bytes.NewBufferString(`{"title":"`+title+`","description":"x","executor":"cli"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	return got["id"].(string)
}
