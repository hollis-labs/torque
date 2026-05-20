package healthscan_test

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/healthscan"
)

// fakeLiveness is the in-memory cancelRegistry stand-in used by tests.
// scheduler.cancelRegistry satisfies the same Liveness interface in
// production; the fake lets the healthscan tests run without dragging
// in the whole scheduler.
type fakeLiveness struct {
	mu     sync.Mutex
	living map[string]struct{}
}

func newFakeLiveness(taskIDs ...string) *fakeLiveness {
	f := &fakeLiveness{living: make(map[string]struct{})}
	for _, id := range taskIDs {
		f.living[id] = struct{}{}
	}
	return f
}

func (f *fakeLiveness) Has(taskID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.living[taskID]
	return ok
}

func newStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// plantHeartbeat inserts a worker_heartbeats row backdated by `age`.
// Mirrors the helper pattern used in stale_recovery_test.go but lives
// here so the healthscan tests don't take a scheduler dependency.
func plantHeartbeat(t *testing.T, store *sqlstore.Store, workerID, taskID string, runID int64, executor string, age time.Duration) {
	t.Helper()
	ageSec := int(age.Seconds())
	if ageSec < 0 {
		ageSec = 0
	}
	_, err := store.DB().Exec(
		`INSERT INTO worker_heartbeats (worker_id, task_id, run_id, executor, started_at, last_heartbeat)
		 VALUES (?, ?, ?, ?, datetime('now', '-' || ? || ' seconds'), datetime('now', '-' || ? || ' seconds'))`,
		workerID, taskID, runID, executor, ageSec, ageSec,
	)
	require.NoError(t, err)
}

func mustCreateTask(t *testing.T, store *sqlstore.Store, id, status string) {
	t.Helper()
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:           id,
		Title:        id,
		Status:       status,
		Executor:     "mock",
		AgentProfile: "mock",
	}))
}

func mustCreateRun(t *testing.T, store *sqlstore.Store, taskID, status string) int64 {
	t.Helper()
	id, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID:   taskID,
		Executor: "mock",
		Status:   status,
	})
	require.NoError(t, err)
	return id
}

// TestScan_HealthyState_NoAnomalies asserts the steady-state happy
// path: a task in `doing` with a fresh heartbeat row and a `running`
// run row produces zero anomalies in both modes. Locks in that the
// scanner doesn't false-positive on the normal in-flight shape.
func TestScan_HealthyState_NoAnomalies(t *testing.T) {
	store := newStore(t)
	mustCreateTask(t, store, "CW-OK", "doing")
	runID := mustCreateRun(t, store, "CW-OK", "running")
	plantHeartbeat(t, store, "worker-ok", "CW-OK", runID, "mock", 0)
	live := newFakeLiveness("CW-OK")

	for _, mode := range []healthscan.Mode{healthscan.ModeBoot, healthscan.ModeTick} {
		t.Run(string(mode), func(t *testing.T) {
			s := healthscan.New(store, live, healthscan.Config{StaleHeartbeat: 900 * time.Second})
			res, err := s.Scan(context.Background(), mode)
			require.NoError(t, err)
			assert.Empty(t, res.Anomalies, "%s: live worker + fresh heartbeat should produce no anomalies", mode)
		})
	}
}

// TestScan_Boot_FlagsAllHeartbeatRowsAsOrphans is the run 864 /
// CW-20260515-0133 fix in test form. At boot the cancelRegistry is
// empty, so every surviving heartbeat row (no matter how fresh) is by
// definition an orphan from the previous serve. The scanner must
// surface them so the scheduler can route through recoverOrphanedWorker
// before the staleness timer would have eventually triggered the same
// recovery from the periodic tick path.
func TestScan_Boot_FlagsAllHeartbeatRowsAsOrphans(t *testing.T) {
	store := newStore(t)
	mustCreateTask(t, store, "CW-ORPH-FRESH", "doing")
	runFresh := mustCreateRun(t, store, "CW-ORPH-FRESH", "running")
	plantHeartbeat(t, store, "worker-fresh", "CW-ORPH-FRESH", runFresh, "mock", 5*time.Second)

	mustCreateTask(t, store, "CW-ORPH-STALE", "doing")
	runStale := mustCreateRun(t, store, "CW-ORPH-STALE", "running")
	plantHeartbeat(t, store, "worker-stale", "CW-ORPH-STALE", runStale, "mock", 30*time.Minute)

	// Empty registry — mirrors a fresh daemon process.
	live := newFakeLiveness()

	s := healthscan.New(store, live, healthscan.Config{StaleHeartbeat: 900 * time.Second})
	res, err := s.Scan(context.Background(), healthscan.ModeBoot)
	require.NoError(t, err)

	orphans := filterByKind(res.Anomalies, healthscan.AnomalyOrphanWorker)
	assert.Len(t, orphans, 2,
		"both fresh and stale heartbeat rows must be flagged at boot — every survivor is an orphan when cancelRegistry is empty")
	assertHasTask(t, orphans, "CW-ORPH-FRESH")
	assertHasTask(t, orphans, "CW-ORPH-STALE")
}

// TestScan_Tick_OnlyStaleHeartbeatsAreOrphans pins the boot-vs-tick
// split: ModeTick honors StaleHeartbeat so a fresh heartbeat row whose
// task happens to be missing from the registry (because Liveness is a
// fake here) does NOT fire. Only the stale row does. This guards
// against the scanner turning into a tick-time false-positive engine
// the moment a real worker's heartbeat slows down between Beat calls.
func TestScan_Tick_OnlyStaleHeartbeatsAreOrphans(t *testing.T) {
	store := newStore(t)
	mustCreateTask(t, store, "CW-FRESH", "doing")
	runFresh := mustCreateRun(t, store, "CW-FRESH", "running")
	plantHeartbeat(t, store, "worker-fresh", "CW-FRESH", runFresh, "mock", 5*time.Second)

	mustCreateTask(t, store, "CW-STALE", "doing")
	runStale := mustCreateRun(t, store, "CW-STALE", "running")
	plantHeartbeat(t, store, "worker-stale", "CW-STALE", runStale, "mock", 30*time.Minute)

	// Empty registry — both rows would look orphan-by-cancel; the
	// staleness threshold is what scopes the tick-mode scan.
	live := newFakeLiveness()

	s := healthscan.New(store, live, healthscan.Config{StaleHeartbeat: 900 * time.Second})
	res, err := s.Scan(context.Background(), healthscan.ModeTick)
	require.NoError(t, err)

	orphans := filterByKind(res.Anomalies, healthscan.AnomalyOrphanWorker)
	require.Len(t, orphans, 1,
		"tick mode must only flag heartbeat rows past StaleHeartbeat — fresh rows are reserved for the boot sweep")
	assert.Equal(t, "CW-STALE", orphans[0].TaskID)
	assert.NotZero(t, orphans[0].LastHeartbeat, "anomaly carries last_heartbeat for log lines")
}

// TestScan_Tick_LiveWorkerNotFlagged locks in the cancelRegistry
// false-positive guard from CW-20260519-0079: a stale heartbeat row
// whose task IS in Liveness must NOT be classified as an orphan. The
// scheduler refreshes the row in place; healthscan must agree.
func TestScan_Tick_LiveWorkerNotFlagged(t *testing.T) {
	store := newStore(t)
	mustCreateTask(t, store, "CW-LIVE-BUT-QUIET", "doing")
	runID := mustCreateRun(t, store, "CW-LIVE-BUT-QUIET", "running")
	plantHeartbeat(t, store, "worker-quiet", "CW-LIVE-BUT-QUIET", runID, "mock", 30*time.Minute)

	live := newFakeLiveness("CW-LIVE-BUT-QUIET")

	s := healthscan.New(store, live, healthscan.Config{StaleHeartbeat: 900 * time.Second})
	res, err := s.Scan(context.Background(), healthscan.ModeTick)
	require.NoError(t, err)
	assert.Empty(t, filterByKind(res.Anomalies, healthscan.AnomalyOrphanWorker),
		"a stale heartbeat for a task still alive in this process must not be classified as orphan")
}

// TestScan_TaskDoingWithoutHeartbeat is the gap that the existing
// FindStale-based sweep cannot see: a task pinned at `doing` with NO
// matching heartbeat row at all. The scanner must surface it in both
// modes — the lifecycle invariant is the same regardless of when the
// scheduler looks.
func TestScan_TaskDoingWithoutHeartbeat(t *testing.T) {
	store := newStore(t)
	// `doing` task with NO heartbeat row.
	mustCreateTask(t, store, "CW-DOING-NOHB", "doing")
	// Healthy control — should not be flagged.
	mustCreateTask(t, store, "CW-DOING-OK", "doing")
	runOK := mustCreateRun(t, store, "CW-DOING-OK", "running")
	plantHeartbeat(t, store, "worker-ok", "CW-DOING-OK", runOK, "mock", 5*time.Second)
	// `done` task with no heartbeat — normal terminal state, must NOT
	// be flagged.
	mustCreateTask(t, store, "CW-DONE", "done")

	live := newFakeLiveness("CW-DOING-OK")

	for _, mode := range []healthscan.Mode{healthscan.ModeBoot, healthscan.ModeTick} {
		t.Run(string(mode), func(t *testing.T) {
			s := healthscan.New(store, live, healthscan.Config{StaleHeartbeat: 900 * time.Second})
			res, err := s.Scan(context.Background(), mode)
			require.NoError(t, err)

			dnw := filterByKind(res.Anomalies, healthscan.AnomalyTaskDoingNoWorker)
			require.Len(t, dnw, 1)
			assert.Equal(t, "CW-DOING-NOHB", dnw[0].TaskID,
				"only the task-in-doing with no heartbeat row should be flagged; done tasks and healthy ones must pass through")
		})
	}
}

// TestScan_RunRunningWithoutHeartbeat covers the runs-table version of
// the previous case. A run can be abandoned while its task moves on
// (e.g. a manual transition cleared the task but never failed the
// run); the scanner surfaces the runs row so observability isn't blind
// to it.
func TestScan_RunRunningWithoutHeartbeat(t *testing.T) {
	store := newStore(t)
	mustCreateTask(t, store, "CW-RUN-NOHB", "todo")
	abandoned := mustCreateRun(t, store, "CW-RUN-NOHB", "running")

	// Control: a `running` run WITH heartbeat — must not be flagged.
	mustCreateTask(t, store, "CW-RUN-OK", "doing")
	okRun := mustCreateRun(t, store, "CW-RUN-OK", "running")
	plantHeartbeat(t, store, "worker-ok", "CW-RUN-OK", okRun, "mock", 5*time.Second)

	// Control: a `failed` run with no heartbeat — terminal, must not
	// be flagged.
	mustCreateTask(t, store, "CW-FAILED", "blocked")
	_ = mustCreateRun(t, store, "CW-FAILED", "failed")

	live := newFakeLiveness("CW-RUN-OK")
	s := healthscan.New(store, live, healthscan.Config{StaleHeartbeat: 900 * time.Second})
	res, err := s.Scan(context.Background(), healthscan.ModeBoot)
	require.NoError(t, err)

	rnw := filterByKind(res.Anomalies, healthscan.AnomalyRunRunningNoWorker)
	require.Len(t, rnw, 1)
	assert.Equal(t, abandoned, rnw[0].RunID,
		"only the abandoned `running` run should be flagged; healthy and terminal runs must pass through")
	assert.Equal(t, "CW-RUN-NOHB", rnw[0].TaskID)
}

// TestScan_EmptyStore is the trivial null case: no tasks, no runs, no
// heartbeats. The scanner returns an empty result without error.
// Guards against a scan-on-fresh-database boot crash.
func TestScan_EmptyStore(t *testing.T) {
	store := newStore(t)
	live := newFakeLiveness()
	s := healthscan.New(store, live, healthscan.Config{StaleHeartbeat: 900 * time.Second})

	for _, mode := range []healthscan.Mode{healthscan.ModeBoot, healthscan.ModeTick} {
		res, err := s.Scan(context.Background(), mode)
		require.NoError(t, err)
		assert.Empty(t, res.Anomalies)
		assert.Equal(t, mode, res.Mode)
		assert.False(t, res.StartedAt.IsZero())
		assert.False(t, res.CompletedAt.IsZero())
	}
}

// TestScan_UnknownMode guards the typed-mode switch. An unknown mode
// must be a hard error, not a silent no-op — silent no-ops on the
// scanner's primary entry point would leave operators unable to tell a
// healthy daemon from a broken one.
func TestScan_UnknownMode(t *testing.T) {
	store := newStore(t)
	live := newFakeLiveness()
	s := healthscan.New(store, live, healthscan.Config{StaleHeartbeat: 900 * time.Second})
	_, err := s.Scan(context.Background(), healthscan.Mode("garbage"))
	require.Error(t, err)
}

// filterByKind is a small test helper. Keeping it local avoids
// exporting predicates from the healthscan package itself.
func filterByKind(in []healthscan.Anomaly, kind healthscan.AnomalyKind) []healthscan.Anomaly {
	out := make([]healthscan.Anomaly, 0, len(in))
	for _, a := range in {
		if a.Kind == kind {
			out = append(out, a)
		}
	}
	return out
}

func assertHasTask(t *testing.T, in []healthscan.Anomaly, taskID string) {
	t.Helper()
	for _, a := range in {
		if a.TaskID == taskID {
			return
		}
	}
	t.Fatalf("expected anomaly for task %s, got %+v", taskID, in)
}
