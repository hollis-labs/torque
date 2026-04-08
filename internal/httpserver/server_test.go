package httpserver_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/httpserver"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	svc := service.New(store)
	handler := httpserver.New(svc)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts
}

func TestCreateAndGetTask(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Test task","description":"A test task description","priority":1,"executor":"cli"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	require.NoError(t, err)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	assert.Equal(t, "Test task", created["title"])
	taskID := created["id"].(string)

	resp, err = http.Get(ts.URL + "/api/v1/tasks/" + taskID)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var got map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&got)
	resp.Body.Close()
	assert.Equal(t, "Test task", got["title"])
}

func TestListTasks(t *testing.T) {
	ts := setupTestServer(t)

	for _, title := range []string{"Task A", "Task B"} {
		body := `{"title":"` + title + `","description":"desc","executor":"cli"}`
		http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	}

	resp, err := http.Get(ts.URL + "/api/v1/tasks")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	tasks := result["tasks"].([]interface{})
	assert.Len(t, tasks, 2)
}

func TestTransitionTask(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"title":"Task","description":"desc","executor":"cli"}`
	resp, _ := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
	var created map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	taskID := created["id"].(string)

	transBody := `{"status":"doing"}`
	resp, err := http.Post(ts.URL+"/api/v1/tasks/"+taskID+"/transition", "application/json", bytes.NewBufferString(transBody))
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var transitioned map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&transitioned)
	resp.Body.Close()
	assert.Equal(t, "doing", transitioned["status"])
}

func TestSettingsGetSet(t *testing.T) {
	ts := setupTestServer(t)

	body := `{"value":"true"}`
	req, _ := http.NewRequest("PUT", ts.URL+"/api/v1/settings/features.sprints", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	resp, err = http.Get(ts.URL + "/api/v1/settings/features.sprints")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	assert.Equal(t, "true", result["value"])
}
