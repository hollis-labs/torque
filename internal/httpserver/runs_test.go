package httpserver_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/httpserver"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runListResp mirrors the envelope the aggregate endpoint returns. Fields
// are PascalCase because the backend marshals sqlstore.RunRecord directly.
type runListResp struct {
	Runs []struct {
		ID       int64
		TaskID   string
		Executor string
		Status   string
		Provider string
		Model    string
	} `json:"runs"`
}

// setupRunsTestServer returns an httptest server plus the underlying store
// so tests can seed runs directly (the store is the path real executors
// use — the HTTP surface is read-only for runs).
func setupRunsTestServer(t *testing.T) (*httptest.Server, *sqlstore.Store) {
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
	return ts, store
}

func TestListRuns_Aggregate(t *testing.T) {
	ts, store := setupRunsTestServer(t)

	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "proj-a", Name: "A"}))
	require.NoError(t, store.CreateProject(&sqlstore.ProjectRecord{ID: "proj-b", Name: "B"}))

	taskA := &sqlstore.TaskRecord{
		ID:        "CW-A-0001",
		Title:     "A1",
		Priority:  2,
		ProjectID: sql.NullString{String: "proj-a", Valid: true},
	}
	taskB := &sqlstore.TaskRecord{
		ID:        "CW-B-0001",
		Title:     "B1",
		Priority:  2,
		ProjectID: sql.NullString{String: "proj-b", Valid: true},
	}
	require.NoError(t, store.CreateTask(taskA))
	require.NoError(t, store.CreateTask(taskB))

	mkRun := func(taskID, status, executor, metadata string) int64 {
		r := &sqlstore.RunRecord{TaskID: taskID, Executor: executor, Status: status}
		if metadata != "" {
			r.Metadata = sql.NullString{String: metadata, Valid: true}
		}
		id, err := store.CreateRun(r)
		require.NoError(t, err)
		// Space inserts by >1ms so ORDER BY started_at DESC is deterministic.
		time.Sleep(2 * time.Millisecond)
		return id
	}

	rA1 := mkRun(taskA.ID, "running", "cli", "")
	rA2 := mkRun(taskA.ID, "done", "agent", `{"provider":"anthropic","model":"claude-opus-4"}`)
	rB1 := mkRun(taskB.ID, "failed", "cli", "")
	rB2 := mkRun(taskB.ID, "done", "cli", "")

	doGet := func(t *testing.T, params url.Values) runListResp {
		t.Helper()
		u := ts.URL + "/api/v1/runs"
		if len(params) > 0 {
			u += "?" + params.Encode()
		}
		resp, err := http.Get(u)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var out runListResp
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
		return out
	}

	t.Run("no task_id returns all runs capped by limit", func(t *testing.T) {
		got := doGet(t, url.Values{"limit": {"50"}})
		require.Len(t, got.Runs, 4)
		assert.Equal(t, rB2, got.Runs[0].ID)
		assert.Equal(t, rA1, got.Runs[3].ID)
		// Provider falls back to executor when metadata is missing.
		assert.Equal(t, "cli", got.Runs[0].Provider)
		// Metadata-backed provider/model wins when present.
		for _, r := range got.Runs {
			if r.ID == rA2 {
				assert.Equal(t, "anthropic", r.Provider)
				assert.Equal(t, "claude-opus-4", r.Model)
			}
		}
	})

	t.Run("since filter drops older runs", func(t *testing.T) {
		a2, err := store.GetRun(rA2)
		require.NoError(t, err)
		cutoff := a2.StartedAt.Add(500 * time.Microsecond).UTC().Format(time.RFC3339Nano)
		got := doGet(t, url.Values{"since": {cutoff}})
		require.Len(t, got.Runs, 2)
		assert.Equal(t, rB2, got.Runs[0].ID)
		assert.Equal(t, rB1, got.Runs[1].ID)
	})

	t.Run("task_id pins to a single task (regression)", func(t *testing.T) {
		got := doGet(t, url.Values{"task_id": {taskA.ID}})
		require.Len(t, got.Runs, 2)
		for _, r := range got.Runs {
			assert.Equal(t, taskA.ID, r.TaskID)
		}
	})

	t.Run("status filter accepts CSV", func(t *testing.T) {
		got := doGet(t, url.Values{"status": {"done,failed"}})
		require.Len(t, got.Runs, 3)
		for _, r := range got.Runs {
			assert.Contains(t, []string{"done", "failed"}, r.Status)
		}
	})

	t.Run("project_id joins via tasks", func(t *testing.T) {
		got := doGet(t, url.Values{"project_id": {"proj-a"}})
		require.Len(t, got.Runs, 2)
		for _, r := range got.Runs {
			assert.Equal(t, taskA.ID, r.TaskID)
		}
	})

	t.Run("default limit returns envelope with all runs under cap", func(t *testing.T) {
		got := doGet(t, nil)
		require.Len(t, got.Runs, 4)
	})
}
