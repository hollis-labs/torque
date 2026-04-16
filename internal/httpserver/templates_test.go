package httpserver_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func httpCreateTemplate(t *testing.T, ts, body string) map[string]interface{} {
	t.Helper()
	resp, err := http.Post(ts+"/api/v1/templates", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var tpl map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tpl))
	resp.Body.Close()
	return tpl
}

func TestHTTP_Template_CreateAndGet(t *testing.T) {
	ts := setupTestServer(t)

	body := `{
        "id": "backend-fix",
        "name": "Backend Fix",
        "description": "Fix {{issue}}",
        "kind": "agent",
        "executor": "cli"
    }`
	tpl := httpCreateTemplate(t, ts.URL, body)
	assert.Equal(t, "backend-fix", tpl["id"])
	assert.Equal(t, float64(1), tpl["version"])
	assert.Equal(t, true, tpl["auto_execute"])

	resp, err := http.Get(ts.URL + "/api/v1/templates/backend-fix")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Equal(t, "Backend Fix", got["name"])
}

func TestHTTP_Template_Update_AppendsVersion(t *testing.T) {
	ts := setupTestServer(t)
	httpCreateTemplate(t, ts.URL, `{"id":"t","name":"v1","description":"x","kind":"agent","executor":"cli"}`)

	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/templates/t",
		bytes.NewBufferString(`{"name":"v2"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Equal(t, float64(2), got["version"])
	assert.Equal(t, "v2", got["name"])
}

func TestHTTP_Template_Delete_Conflict(t *testing.T) {
	ts := setupTestServer(t)
	httpCreateTemplate(t, ts.URL, `{"id":"t","name":"t","description":"x","kind":"agent","executor":"cli"}`)

	// Instantiate once so a task references the template.
	resp, err := http.Post(ts.URL+"/api/v1/templates/t/instantiate", "application/json",
		bytes.NewBufferString(`{"title":"ref","description":"x"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp.Body.Close()

	req, err := http.NewRequest(http.MethodDelete, ts.URL+"/api/v1/templates/t", nil)
	require.NoError(t, err)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	resp.Body.Close()
}

func TestHTTP_Template_Archive(t *testing.T) {
	ts := setupTestServer(t)
	httpCreateTemplate(t, ts.URL, `{"id":"t","name":"t","description":"x","kind":"agent","executor":"cli"}`)

	resp, err := http.Post(ts.URL+"/api/v1/templates/t/archive/1", "application/json", nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	resp, err = http.Get(ts.URL + "/api/v1/templates/t?version=1")
	require.NoError(t, err)
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Equal(t, true, got["is_archived"])
}

// GET /templates/{id} without a version resolves to the latest non-archived
// row; if every version is archived, it 404s. Callers who want the archived
// data must pass ?version=N. Documented on the handler; covered here so the
// contract doesn't silently drift.
func TestHTTP_Template_GetLatest_404_WhenAllArchived(t *testing.T) {
	ts := setupTestServer(t)
	httpCreateTemplate(t, ts.URL, `{"id":"t","name":"t","description":"x","kind":"agent","executor":"cli"}`)

	resp, err := http.Post(ts.URL+"/api/v1/templates/t/archive/1", "application/json", nil)
	require.NoError(t, err)
	resp.Body.Close()

	// GET without version → 404 (no non-archived rows).
	resp, err = http.Get(ts.URL + "/api/v1/templates/t")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	// GET with explicit version → 200, archived row still retrievable.
	resp, err = http.Get(ts.URL + "/api/v1/templates/t?version=1")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	var got map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	resp.Body.Close()
	assert.Equal(t, true, got["is_archived"])
}

func TestHTTP_Template_List(t *testing.T) {
	ts := setupTestServer(t)
	httpCreateTemplate(t, ts.URL, `{"id":"a","name":"a","description":"x","kind":"agent","executor":"cli"}`)
	httpCreateTemplate(t, ts.URL, `{"id":"b","name":"b","description":"x","kind":"wait"}`)

	resp, err := http.Get(ts.URL + "/api/v1/templates")
	require.NoError(t, err)
	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	resp.Body.Close()
	list := result["templates"].([]interface{})
	assert.Len(t, list, 2)
}

func TestHTTP_Template_Instantiate(t *testing.T) {
	ts := setupTestServer(t)
	body := `{
        "id": "backend-fix",
        "name": "Backend Fix",
        "description": "Fix {{issue}}",
        "kind": "agent",
        "executor": "cli",
        "required_vars": ["issue"]
    }`
	httpCreateTemplate(t, ts.URL, body)

	resp, err := http.Post(ts.URL+"/api/v1/templates/backend-fix/instantiate", "application/json",
		bytes.NewBufferString(`{"title":"Fix auth","vars":{"issue":"auth-42"}}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var task map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&task))
	resp.Body.Close()
	assert.Equal(t, "Fix auth", task["title"])
	assert.Equal(t, "Fix auth-42", task["description"])
	assert.Equal(t, "agent", task["kind"])
	// template_ref stamped in metadata
	md := task["metadata"].(map[string]interface{})
	ref := md["template_ref"].(map[string]interface{})
	assert.Equal(t, "backend-fix", ref["id"])
}

func TestHTTP_Template_Instantiate_MissingVar_422(t *testing.T) {
	ts := setupTestServer(t)
	httpCreateTemplate(t, ts.URL, `{"id":"x","name":"x","description":"x","kind":"agent","executor":"cli","required_vars":["a"]}`)

	resp, err := http.Post(ts.URL+"/api/v1/templates/x/instantiate", "application/json",
		bytes.NewBufferString(`{"title":"t"}`))
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	resp.Body.Close()
}

func TestHTTP_Template_Get_NotFound(t *testing.T) {
	ts := setupTestServer(t)
	resp, err := http.Get(ts.URL + "/api/v1/templates/missing")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}
