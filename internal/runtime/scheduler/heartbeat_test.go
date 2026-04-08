package scheduler_test

import (
	"database/sql"
	"testing"
	"time"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/runtime/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupHeartbeatStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestHeartbeatRegisterAndBeat(t *testing.T) {
	store := setupHeartbeatStore(t)
	hb := scheduler.NewHeartbeatMonitor(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	err := hb.Register("worker-1", "CW-0001", 1, "cli")
	require.NoError(t, err)

	err = hb.Beat("worker-1")
	require.NoError(t, err)
}

func TestHeartbeatDeregister(t *testing.T) {
	store := setupHeartbeatStore(t)
	hb := scheduler.NewHeartbeatMonitor(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	hb.Register("worker-1", "CW-0001", 1, "cli")
	err := hb.Deregister("worker-1")
	require.NoError(t, err)

	stale, err := hb.FindStale(1 * time.Minute)
	require.NoError(t, err)
	assert.Len(t, stale, 0)
}

func TestHeartbeatFindStale(t *testing.T) {
	store := setupHeartbeatStore(t)
	hb := scheduler.NewHeartbeatMonitor(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	hb.Register("worker-1", "CW-0001", 1, "cli")

	// Manually backdate the heartbeat to simulate staleness
	_, err := store.DB().Exec(
		`UPDATE worker_heartbeats SET last_heartbeat = datetime('now', '-10 minutes') WHERE worker_id = ?`,
		"worker-1",
	)
	require.NoError(t, err)

	stale, err := hb.FindStale(5 * time.Minute)
	require.NoError(t, err)
	assert.Len(t, stale, 1)
	assert.Equal(t, "worker-1", stale[0].WorkerID)
	assert.Equal(t, "CW-0001", stale[0].TaskID)
}

func TestHeartbeatFindStaleNoneStale(t *testing.T) {
	store := setupHeartbeatStore(t)
	hb := scheduler.NewHeartbeatMonitor(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "Task", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	hb.Register("worker-1", "CW-0001", 1, "cli")

	stale, err := hb.FindStale(5 * time.Minute)
	require.NoError(t, err)
	assert.Len(t, stale, 0, "freshly registered worker should not be stale")
}

func TestHeartbeatActiveWorkers(t *testing.T) {
	store := setupHeartbeatStore(t)
	hb := scheduler.NewHeartbeatMonitor(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "A", Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "B", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0002", Executor: "cli", Status: "running"})

	hb.Register("worker-1", "CW-0001", 1, "cli")
	hb.Register("worker-2", "CW-0002", 2, "api")

	workers, err := hb.ActiveWorkers()
	require.NoError(t, err)
	assert.Len(t, workers, 2)
}
