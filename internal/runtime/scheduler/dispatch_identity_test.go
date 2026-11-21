package scheduler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerDispatchStampsRegistryIdentityBeforeRunCreation(t *testing.T) {
	sched, store, _ := setupScheduler(t)
	const canonical = "workstream:01M7VQE9DMDXCS0Z1MVA7045D6"
	file := filepath.Join(t.TempDir(), "dispatcher-token")
	require.NoError(t, os.WriteFile(file, []byte("fake"), 0600))
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/workstreams/ensure", r.URL.Path)
		require.Equal(t, "Bearer fake", r.Header.Get("Authorization"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "CW-ID-1", body["key"])
		_ = json.NewEncoder(w).Encode(map[string]string{"id": canonical})
	}))
	defer registry.Close()
	identity, e := service.NewDispatchSync(store, service.DispatchSyncConfig{RegistryURL: registry.URL, RegistryTokenFile: file})
	require.NoError(t, e)
	sched.SetDispatchIdentity(identity.Prepare)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-ID-1", Title: "identity", Status: "todo", Priority: 1, Executor: "mock", AgentProfile: "mock"}))
	require.NoError(t, sched.Tick(context.Background()))
	var got string
	require.NoError(t, store.ReadDB().QueryRow(`SELECT workstream_id FROM runs WHERE task_id=?`, "CW-ID-1").Scan(&got))
	require.Equal(t, canonical, got)
	require.NoError(t, store.ReadDB().QueryRow(`SELECT json_extract(metadata,'$.workstream_id') FROM tasks WHERE id=?`, "CW-ID-1").Scan(&got))
	require.Equal(t, canonical, got)
}
