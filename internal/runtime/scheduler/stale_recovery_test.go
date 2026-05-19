package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"log"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/queue"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupRecoveryScheduler(t *testing.T) (*Scheduler, *sqlstore.Store) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	reg := executor.NewRegistry()
	q, err := queue.Open(context.Background(), filepath.Join(t.TempDir(), "queue.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })

	// StaleSeconds: 1 so a row backdated by minutes is firmly past it.
	sched := New(store, q, reg, nil, &config.SchedulerConfig{
		Workers:      2,
		StaleSeconds: 1,
		Enabled:      true,
	})
	t.Cleanup(func() { sched.Stop(context.Background()) })
	return sched, store
}

// TestStaleHeartbeat_LiveWorker_RefreshedNotRecovered locks in the
// false-positive guard from CW-20260519-0079: a stale heartbeat row for a
// task that's still in the in-process cancelRegistry (the actual liveness
// signal) must NOT be auto-recovered. The row is refreshed in place; the
// task stays in `doing`; no run row is failed. Mirrors the run 904
// scenario (mid-`go test`, executor quiet, heartbeat aged past threshold).
func TestStaleHeartbeat_LiveWorker_RefreshedNotRecovered(t *testing.T) {
	sched, store := setupRecoveryScheduler(t)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-LIVE",
		Title:    "live but slow",
		Status:   "doing",
		Executor: "mock", AgentProfile: "mock",
	}))
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-LIVE", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)

	// Plant a heartbeat row backdated past StaleSeconds.
	_, err = store.DB().Exec(
		`INSERT INTO worker_heartbeats (worker_id, task_id, run_id, executor, started_at, last_heartbeat)
		 VALUES (?, ?, ?, ?, datetime('now', '-2 minutes'), datetime('now', '-2 minutes'))`,
		"worker-live", "CW-LIVE", runID, "mock",
	)
	require.NoError(t, err)

	// Inject a cancel into the registry so the worker LOOKS alive to the
	// stale sweep. Use a no-op cancel — the test never triggers it.
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sched.cancels.register("CW-LIVE", cancel)

	require.NoError(t, sched.Tick(context.Background()))

	// Task stays `doing` — the worker is still active.
	task, err := store.GetTask("CW-LIVE")
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status,
		"live worker must NOT be re-queued just because its DB heartbeat aged out")

	// Run stays `running` — no orphan recovery happened.
	run, err := store.GetRun(runID)
	require.NoError(t, err)
	assert.Equal(t, "running", run.Status,
		"live worker's run must not be marked failed by the stale sweep")

	// Heartbeat row was refreshed in place (not deleted): the row still
	// exists and its last_heartbeat is no longer past the threshold.
	stale, err := sched.heartbeat.FindStale(time.Duration(sched.cfg.StaleSeconds) * time.Second)
	require.NoError(t, err)
	assert.Empty(t, stale, "stale row should be refreshed in place")
	workers, err := sched.heartbeat.ActiveWorkers()
	require.NoError(t, err)
	require.Len(t, workers, 1, "row not deleted; just refreshed")
	assert.Equal(t, "worker-live", workers[0].WorkerID)
}

// TestStaleHeartbeat_OrphanedWorker_AutoRecovered locks in the bug-fix
// from CW-20260519-0079 case #1 (run 864 / CW-20260515-0133 zombie): a
// stale heartbeat row with NO cancelRegistry entry is a confirmed-dead
// orphan and must be reclaimed end-to-end without operator intervention.
// Task goes `doing → todo` so the next picker tick re-dispatches it; the
// orphaned run row is marked `failed`; the heartbeat row is deleted.
func TestStaleHeartbeat_OrphanedWorker_AutoRecovered(t *testing.T) {
	sched, store := setupRecoveryScheduler(t)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-ORPHAN",
		Title:    "orphaned by restart",
		Status:   "doing",
		Executor: "mock", AgentProfile: "mock",
	}))
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-ORPHAN", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)

	// Plant a stale heartbeat WITHOUT registering a cancel — simulates a
	// restart-orphaned worker (previous serve crashed; the heartbeat row
	// survived but this scheduler has no in-flight worker for the task).
	_, err = store.DB().Exec(
		`INSERT INTO worker_heartbeats (worker_id, task_id, run_id, executor, started_at, last_heartbeat)
		 VALUES (?, ?, ?, ?, datetime('now', '-2 minutes'), datetime('now', '-2 minutes'))`,
		"worker-orphan", "CW-ORPHAN", runID, "mock",
	)
	require.NoError(t, err)

	require.NoError(t, sched.Tick(context.Background()))

	// Task forced back to `todo` so the picker re-dispatches it next tick.
	task, err := store.GetTask("CW-ORPHAN")
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status,
		"orphaned doing-state task must be re-queued so the picker re-dispatches")

	// Run row marked failed with the recovery reason — visible to log
	// search and the runs UI.
	run, err := store.GetRun(runID)
	require.NoError(t, err)
	assert.Equal(t, "failed", run.Status,
		"orphaned run must be marked failed by the recovery path")
	assert.Contains(t, run.ErrorMessage, "orphaned",
		"run error_message should carry the structured 'orphaned' reason for log search")

	// Heartbeat row is gone — won't re-fire next tick.
	workers, err := sched.heartbeat.ActiveWorkers()
	require.NoError(t, err)
	assert.Empty(t, workers, "orphan recovery must delete the heartbeat row")
}

// TestStaleHeartbeat_OrphanedWorker_NonDoingTaskNotReQueued protects
// operator intent: if a stale heartbeat names a task that has ALREADY
// been transitioned out of `doing` (e.g. an operator manually flipped it
// to `blocked` while the orphan was being processed), the recovery path
// must NOT clobber that status. Only the run row + heartbeat are
// reclaimed.
func TestStaleHeartbeat_OrphanedWorker_NonDoingTaskNotReQueued(t *testing.T) {
	sched, store := setupRecoveryScheduler(t)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-MANUAL",
		Title:    "operator-flipped to blocked",
		Status:   "blocked",
		Executor: "mock", AgentProfile: "mock",
	}))
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-MANUAL", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)
	_, err = store.DB().Exec(
		`INSERT INTO worker_heartbeats (worker_id, task_id, run_id, executor, started_at, last_heartbeat)
		 VALUES (?, ?, ?, ?, datetime('now', '-2 minutes'), datetime('now', '-2 minutes'))`,
		"worker-manual", "CW-MANUAL", runID, "mock",
	)
	require.NoError(t, err)

	require.NoError(t, sched.Tick(context.Background()))

	task, err := store.GetTask("CW-MANUAL")
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status,
		"recovery must respect operator's manual status change")

	// Heartbeat row still cleaned up.
	workers, err := sched.heartbeat.ActiveWorkers()
	require.NoError(t, err)
	assert.Empty(t, workers)
}

// TestStaleHeartbeat_LiveWorker_LogsFalsePositiveClassifier checks the
// observability contract from CW-20260519-0079: the live-worker path
// must log a distinguishable line so operators can tell a refresh apart
// from an orphan recovery in tail-the-log diagnoses. The orphan path
// logs `orphaned worker detected`; the live-worker path logs `stale
// heartbeat for live worker`. Both lines are load-bearing for grep, so
// they're locked in as test contracts.
func TestStaleHeartbeat_LiveWorker_LogsFalsePositiveClassifier(t *testing.T) {
	sched, store := setupRecoveryScheduler(t)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-LIVE-LOG", Title: "live", Status: "doing",
		Executor: "mock", AgentProfile: "mock",
	}))
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-LIVE-LOG", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)
	_, err = store.DB().Exec(
		`INSERT INTO worker_heartbeats (worker_id, task_id, run_id, executor, started_at, last_heartbeat)
		 VALUES (?, ?, ?, ?, datetime('now', '-2 minutes'), datetime('now', '-2 minutes'))`,
		"worker-live-log", "CW-LIVE-LOG", runID, "mock",
	)
	require.NoError(t, err)

	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sched.cancels.register("CW-LIVE-LOG", cancel)

	var buf bytes.Buffer
	origOut := log.Writer()
	origFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origOut)
		log.SetFlags(origFlags)
	}()

	require.NoError(t, sched.Tick(context.Background()))

	assert.Contains(t, buf.String(), "stale heartbeat for live worker",
		"live-worker false-positive must produce the distinguishable log line for grep diagnoses")
	assert.NotContains(t, buf.String(), "orphaned worker detected",
		"live worker must NOT take the orphan-recovery path")
}
