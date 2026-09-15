package httpserver_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
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

func setupCommentsTestServerWithService(t *testing.T) (*httptest.Server, *service.Service) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	svc := service.New(store)
	handler := httpserver.New(svc, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, svc
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
	assert.Equal(t, "task", created["entity_type"])
	assert.Equal(t, taskID, created["entity_id"])
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
	assert.Equal(t, "task", listed[0]["entity_type"])
	assert.Equal(t, taskID, listed[0]["entity_id"])
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

// CW-20260503-0007: the legacy task_id alias on the flat /comments endpoint
// has been removed. Both POST body and GET query forms must now be rejected
// with a clear 400 that names the new entity_type + entity_id shape.
func TestHTTP_FlatComments_LegacyTaskIDShapeRejected(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createCommentsTestTask(t, ts.URL)

	// POST body {"task_id": ...} must 400.
	body := `{"task_id":"` + taskID + `","author":"carol","content":"flat style"}`
	postResp, err := http.Post(
		ts.URL+"/api/v1/comments",
		"application/json",
		bytes.NewBufferString(body),
	)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, postResp.StatusCode)
	postRaw, _ := io.ReadAll(postResp.Body)
	postResp.Body.Close()
	assert.Contains(t, string(postRaw), "task_id is no longer accepted",
		"error should explain the alias removal; got %s", string(postRaw))
	assert.Contains(t, string(postRaw), "entity_type",
		"error should name the new shape; got %s", string(postRaw))

	// GET ?task_id=... must 400 too.
	getResp, err := http.Get(ts.URL + "/api/v1/comments?task_id=" + taskID)
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, getResp.StatusCode)
	getRaw, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	assert.Contains(t, string(getRaw), "task_id is no longer accepted",
		"error should explain the alias removal; got %s", string(getRaw))
}

// TestHTTP_FlatComments_PolymorphicShape exercises the new entity_type +
// entity_id query/body shape on the flat endpoint, validating that the
// polymorphism is fully usable from HTTP without going through the
// task-nested route.
func TestHTTP_FlatComments_PolymorphicShape(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createCommentsTestTask(t, ts.URL)

	body := `{"entity_type":"task","entity_id":"` + taskID + `","author":"dan","content":"polymorphic style"}`
	postResp, err := http.Post(
		ts.URL+"/api/v1/comments",
		"application/json",
		bytes.NewBufferString(body),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, postResp.StatusCode)
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(postResp.Body).Decode(&created))
	postResp.Body.Close()
	assert.Equal(t, "task", created["entity_type"])
	assert.Equal(t, taskID, created["entity_id"])

	getResp, err := http.Get(ts.URL + "/api/v1/comments?entity_type=task&entity_id=" + taskID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getResp.StatusCode)
	raw, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	assert.True(t, strings.Contains(string(raw), `"dan"`), "response should include posted author; got %s", string(raw))
}

func TestHTTP_DeleteComment_ForceGuardMatrix(t *testing.T) {
	ts, svc := setupCommentsTestServerWithService(t)
	taskID := createCommentsTestTask(t, ts.URL)

	add := func(author string, includeAuthor bool) int64 {
		t.Helper()
		if !includeAuthor {
			c, err := svc.Comment.Add("task", taskID, "", "delete me")
			require.NoError(t, err)
			return c.ID
		}
		body := map[string]string{
			"entity_type": "task",
			"entity_id":   taskID,
			"content":     "delete me",
		}
		body["author"] = author
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		resp, err := http.Post(ts.URL+"/api/v1/comments", "application/json", bytes.NewReader(raw))
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		var created map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
		resp.Body.Close()
		return int64(created["id"].(float64))
	}
	del := func(id int64, author string, includeForce bool, force string) int {
		t.Helper()
		q := url.Values{"author": {author}}
		if includeForce {
			q.Set("force", force)
		}
		req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/comments/"+strconv.FormatInt(id, 10)+"?"+q.Encode(), nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	exists := func(id int64) bool {
		t.Helper()
		resp, err := http.Get(ts.URL + "/api/v1/comments?entity_type=task&entity_id=" + taskID)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var listed []map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&listed))
		resp.Body.Close()
		for _, item := range listed {
			if int64(item["id"].(float64)) == id {
				return true
			}
		}
		return false
	}
	assertDelete := func(name string, id int64, author string, includeForce bool, force string, wantStatus int, wantExists bool) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, wantStatus, del(id, author, includeForce, force))
			assert.Equal(t, wantExists, exists(id))
		})
	}

	assertDelete("matching author omitted force succeeds", add("alice", true), "alice", false, "", http.StatusOK, false)
	assertDelete("mismatching author omitted force rejects", add("alice", true), "bob", false, "", http.StatusForbidden, true)
	assertDelete("mismatching author force false rejects", add("alice", true), "bob", true, "false", http.StatusForbidden, true)
	assertDelete("mismatching author force true succeeds", add("alice", true), "bob", true, "true", http.StatusOK, false)
	assertDelete("explicit empty author omitted force succeeds", add("", false), "", false, "", http.StatusOK, false)
	assertDelete("empty author force false succeeds", add("", false), "", true, "false", http.StatusOK, false)
	assertDelete("empty author mismatched force true succeeds", add("", false), "bob", true, "true", http.StatusOK, false)
}

func TestHTTP_DeleteComment_ExactParsingAndMissingRemainGuarded(t *testing.T) {
	ts := setupTestServer(t)
	taskID := createCommentsTestTask(t, ts.URL)
	body := `{"entity_type":"task","entity_id":"` + taskID + `","author":"alice","content":"survives bad delete"}`
	postResp, err := http.Post(ts.URL+"/api/v1/comments", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, postResp.StatusCode)
	var created map[string]interface{}
	require.NoError(t, json.NewDecoder(postResp.Body).Decode(&created))
	postResp.Body.Close()
	id := int64(created["id"].(float64))

	badURLs := []string{
		ts.URL + "/api/v1/comments/" + strconv.FormatInt(id, 10) + "?author=alice&force=1",
		ts.URL + "/api/v1/comments/" + strconv.FormatInt(id, 10) + "?author=alice&force=yes",
		ts.URL + "/api/v1/comments/" + strconv.FormatInt(id, 10) + "?author=alice&force=True",
		ts.URL + "/api/v1/comments/" + strconv.FormatInt(id, 10) + "?author=alice&force=",
		ts.URL + "/api/v1/comments/" + strconv.FormatInt(id, 10) + "?author=alice&force=true&force=false",
		ts.URL + "/api/v1/comments/" + strconv.FormatInt(id, 10) + "?author=alice&bogus=true",
		ts.URL + "/api/v1/comments/" + strconv.FormatInt(id, 10) + "?force=true",
		ts.URL + "/api/v1/comments/1.5?author=alice&force=true",
	}
	for _, u := range badURLs {
		req, err := http.NewRequest(http.MethodDelete, u, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, u)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/comments/999999?author=alice&force=true", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	getResp, err := http.Get(ts.URL + "/api/v1/comments?entity_type=task&entity_id=" + taskID)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, getResp.StatusCode)
	var listed []map[string]interface{}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&listed))
	getResp.Body.Close()
	require.Len(t, listed, 1)
	assert.Equal(t, "survives bad delete", listed[0]["content"])
}

func TestHTTP_NestedComments_MissingTaskReturns500OrError(t *testing.T) {
	ts := setupTestServer(t)

	// FK is dropped in migration 019, so an "orphan" task_id (no matching
	// task row) is now allowed at the persistence layer — comments are
	// polymorphic and don't need their target entity to exist as a FK.
	resp, err := http.Post(
		ts.URL+"/api/v1/tasks/CW-NOPE/comments",
		"application/json",
		bytes.NewBufferString(`{"content":"orphan"}`),
	)
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode,
		"comments are no longer FK-bound to tasks; orphan inserts succeed")
	resp.Body.Close()
}
