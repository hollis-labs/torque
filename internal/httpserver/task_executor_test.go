package httpserver_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/httpserver"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/service"
)

// CW-20260910-0087: the HTTP write path refuses an executor the process has
// not registered, with the same 422 the other task facets use.
func TestHTTP_TaskExecutorMustBeRegistered(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	svc := service.New(store)
	svc.Task.SetRegisteredExecutors(func() []string { return []string{"api", "cli", "mock"} })
	ts := httptest.NewServer(httpserver.New(svc, nil))
	t.Cleanup(ts.Close)

	post := func(body string) (*http.Response, map[string]any) {
		resp, err := http.Post(ts.URL+"/api/v1/tasks", "application/json", bytes.NewBufferString(body))
		require.NoError(t, err)
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}

	resp, out := post(`{"title":"t","description":"x","executor":"opencode"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	assert.Contains(t, out["error"], "invalid executor: got 'opencode', expected one of: api, cli, mock")

	resp, out = post(`{"title":"t","description":"x","executor":"cli"}`)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	id, _ := out["id"].(string)
	require.NotEmpty(t, id)

	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/v1/tasks/"+id, bytes.NewBufferString(`{"executor":"codex"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	put, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	put.Body.Close()
	assert.Equal(t, http.StatusUnprocessableEntity, put.StatusCode)
}
