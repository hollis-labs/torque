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

// DeleteStale removes zombie heartbeat rows whose last_heartbeat is older than
// the threshold. The scheduler calls this after logging stale workers so they
// don't accumulate across sessions (CW-20260418-0003 secondary fix).
func TestHeartbeatDeleteStale(t *testing.T) {
	store := setupHeartbeatStore(t)
	hb := scheduler.NewHeartbeatMonitor(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "A", Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "B", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0002", Executor: "cli", Status: "running"})

	hb.Register("worker-stale", "CW-0001", 1, "cli")
	hb.Register("worker-fresh", "CW-0002", 2, "cli")

	// Backdate only worker-stale past the threshold.
	_, err := store.DB().Exec(
		`UPDATE worker_heartbeats SET last_heartbeat = datetime('now', '-10 minutes') WHERE worker_id = ?`,
		"worker-stale",
	)
	require.NoError(t, err)

	deleted, err := hb.DeleteStale(5 * time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "exactly the one stale row is removed")

	workers, err := hb.ActiveWorkers()
	require.NoError(t, err)
	require.Len(t, workers, 1)
	assert.Equal(t, "worker-fresh", workers[0].WorkerID, "fresh heartbeat survives cleanup")

	stale, err := hb.FindStale(5 * time.Minute)
	require.NoError(t, err)
	assert.Len(t, stale, 0, "no stale rows remain after DeleteStale")
}

// TestHeartbeatDeleteStaleTwoRowsSinglePass covers the concurrency sharp
// edge from CW-20260418-0018: two stale rows for different workers must
// both disappear in a single DeleteStale call, not one-per-tick.
func TestHeartbeatDeleteStaleTwoRowsSinglePass(t *testing.T) {
	store := setupHeartbeatStore(t)
	hb := scheduler.NewHeartbeatMonitor(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "A", Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "B", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0002", Executor: "cli", Status: "running"})

	hb.Register("worker-zombie-1", "CW-0001", 1, "cli")
	hb.Register("worker-zombie-2", "CW-0002", 2, "cli")

	// Backdate BOTH rows past the threshold. A single DeleteStale call
	// should remove both; if the DELETE were ever accidentally scoped to
	// one row (e.g. LIMIT 1 or per-worker loop) this assertion catches it.
	_, err := store.DB().Exec(
		`UPDATE worker_heartbeats SET last_heartbeat = datetime('now', '-10 minutes')`,
	)
	require.NoError(t, err)

	deleted, err := hb.DeleteStale(5 * time.Minute)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted, "both stale rows are removed in a single pass")

	workers, err := hb.ActiveWorkers()
	require.NoError(t, err)
	assert.Len(t, workers, 0, "no rows remain")
}

// TestHeartbeatCountHeartbeatsSplit validates the live/stale split used by
// the per-tick gauge log (CW-20260418-0018). Empty table → zeros. Mix of
// fresh + backdated → correct split.
func TestHeartbeatCountHeartbeatsSplit(t *testing.T) {
	store := setupHeartbeatStore(t)
	hb := scheduler.NewHeartbeatMonitor(store)

	// Empty table — SUM over zero rows is NULL; must coalesce to zero.
	counts, err := hb.CountHeartbeats(5 * time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 0, counts.Live)
	assert.Equal(t, 0, counts.Stale)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "A", Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0002", Title: "B", Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0003", Title: "C", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0002", Executor: "cli", Status: "running"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0003", Executor: "cli", Status: "running"})

	hb.Register("worker-fresh-1", "CW-0001", 1, "cli")
	hb.Register("worker-fresh-2", "CW-0002", 2, "cli")
	hb.Register("worker-stale-1", "CW-0003", 3, "cli")

	// Backdate one to stale.
	_, err = store.DB().Exec(
		`UPDATE worker_heartbeats SET last_heartbeat = datetime('now', '-10 minutes') WHERE worker_id = ?`,
		"worker-stale-1",
	)
	require.NoError(t, err)

	counts, err = hb.CountHeartbeats(5 * time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 2, counts.Live, "two fresh heartbeats")
	assert.Equal(t, 1, counts.Stale, "one backdated heartbeat is stale")
}

// TestHeartbeatCountHeartbeatsNullLastHeartbeat covers the defensive NULL
// handling documented on HeartbeatCounts: a row whose last_heartbeat is
// NULL (shouldn't occur in practice but defensible against future bad
// inserts) must be counted as stale so it surfaces instead of silently
// dropping from both counters.
func TestHeartbeatCountHeartbeatsNullLastHeartbeat(t *testing.T) {
	store := setupHeartbeatStore(t)
	hb := scheduler.NewHeartbeatMonitor(store)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-0001", Title: "A", Executor: "cli"})
	store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-0001", Executor: "cli", Status: "running"})

	// Insert a row with explicit NULL last_heartbeat — bypasses Register
	// (which writes CURRENT_TIMESTAMP) to simulate a corrupt/partial
	// insert. The schema permits NULL (no NOT NULL constraint on
	// last_heartbeat).
	_, err := store.DB().Exec(
		`INSERT INTO worker_heartbeats (worker_id, task_id, run_id, executor, started_at, last_heartbeat)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, NULL)`,
		"worker-null", "CW-0001", 1, "cli",
	)
	require.NoError(t, err)

	counts, err := hb.CountHeartbeats(5 * time.Minute)
	require.NoError(t, err)
	assert.Equal(t, 0, counts.Live)
	assert.Equal(t, 1, counts.Stale, "NULL last_heartbeat counts as stale")
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
