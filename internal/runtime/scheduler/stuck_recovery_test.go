package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/healthscan"
)

// backdateTaskUpdatedAt pushes tasks.updated_at into the past by `age`
// so the session-recovery grace window can be exercised without
// time.Sleep. Mirrors the plantHeartbeat helper's style — direct SQL
// rather than a Store method because Store deliberately doesn't expose
// a "set my updated_at" surface (state writes own that column).
func backdateTaskUpdatedAt(t *testing.T, store *sqlstore.Store, taskID string, age time.Duration) {
	t.Helper()
	ageSec := int(age.Seconds())
	if ageSec < 0 {
		ageSec = 0
	}
	_, err := store.DB().Exec(
		`UPDATE tasks SET updated_at = datetime('now', '-' || ? || ' seconds') WHERE id = ?`,
		ageSec, taskID,
	)
	require.NoError(t, err)
}

// backdateRunStartedAt pushes runs.started_at into the past by `age`.
// Same shape as backdateTaskUpdatedAt; used to age a run past the
// stuck-recovery grace window.
func backdateRunStartedAt(t *testing.T, store *sqlstore.Store, runID int64, age time.Duration) {
	t.Helper()
	ageSec := int(age.Seconds())
	if ageSec < 0 {
		ageSec = 0
	}
	_, err := store.DB().Exec(
		`UPDATE runs SET started_at = datetime('now', '-' || ? || ' seconds') WHERE id = ?`,
		ageSec, runID,
	)
	require.NoError(t, err)
}

// setupRecoverySchedulerWithGrace is a thin variant of
// setupRecoveryScheduler that lets the test pick the stuck-recovery
// grace window. Tests that exercise the grace boundary need a tight
// window (e.g. 1s) so backdating a row by 2s reliably puts it past the
// grace; tests that exercise the protection arm leave the default in
// place.
func setupRecoverySchedulerWithGrace(t *testing.T, graceSeconds int) (*Scheduler, *sqlstore.Store) {
	sched, store := setupRecoveryScheduler(t)
	sched.cfg.StuckGraceSeconds = graceSeconds
	return sched, store
}

// TestRecoverStuckTask_Boot_BackdatedTaskRecovered is the
// CW-20260519-0084 RECOVER half locked in. A task in `doing` with no
// worker_heartbeats row at all, whose tasks.updated_at is older than
// StuckGraceSeconds, must be re-queued by the boot scan. The DETECT
// half (CW-20260519-0083) surfaces the anomaly; this test proves the
// loop is closed.
func TestRecoverStuckTask_Boot_BackdatedTaskRecovered(t *testing.T) {
	sched, store := setupRecoverySchedulerWithGrace(t, 1)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-STUCK-BOOT", Title: "stuck doing, no hb", Status: "doing",
		Executor: "mock", AgentProfile: "mock",
	}))
	backdateTaskUpdatedAt(t, store, "CW-STUCK-BOOT", 10*time.Second)

	sched.runHealthScan(context.Background(), healthscan.ModeBoot)

	task, err := store.GetTask("CW-STUCK-BOOT")
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status,
		"boot recovery must re-queue a stuck `doing` task with no heartbeat row, once tasks.updated_at has aged past the grace")
}

// TestRecoverStuckTask_GraceWindow_ProtectsFreshTasks is the negative
// complement. A freshly-created `doing` task with no heartbeat row
// looks identical on the wire to a long-zombied one — the only signal
// is the age. The grace window MUST hold off recovery for ages below
// the threshold so a mid-dispatch race between TransitionTask(doing)
// and HeartbeatMonitor.Register does not see the task get spuriously
// re-queued.
func TestRecoverStuckTask_GraceWindow_ProtectsFreshTasks(t *testing.T) {
	// Default 60s grace; the test creates the task at "now" so its age
	// is at most a few milliseconds — well inside grace.
	sched, store := setupRecoveryScheduler(t)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-FRESH-DOING", Title: "freshly transitioned to doing", Status: "doing",
		Executor: "mock", AgentProfile: "mock",
	}))

	sched.runHealthScan(context.Background(), healthscan.ModeTick)

	task, err := store.GetTask("CW-FRESH-DOING")
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status,
		"task within the grace window must be left alone — dispatch-vs-register race is the dominant false-positive source")
}

// TestRecoverStuckTask_AlsoFailsRunningRuns covers the cross-table
// invariant: a stuck task may have a `running` run row lingering from
// the lost dispatch (its heartbeat row deregistered but the run never
// reached CompleteRun). Recovery must fail any still-running run for
// the same task so observers see a terminal run row, not a perpetual
// `running` zombie.
func TestRecoverStuckTask_AlsoFailsRunningRuns(t *testing.T) {
	sched, store := setupRecoverySchedulerWithGrace(t, 1)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-STUCK-RUN", Title: "stuck with abandoned run", Status: "doing",
		Executor: "mock", AgentProfile: "mock",
	}))
	abandoned, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-STUCK-RUN", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)
	backdateTaskUpdatedAt(t, store, "CW-STUCK-RUN", 10*time.Second)
	backdateRunStartedAt(t, store, abandoned, 10*time.Second)

	sched.runHealthScan(context.Background(), healthscan.ModeBoot)

	task, err := store.GetTask("CW-STUCK-RUN")
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status, "task must be re-queued")

	run, err := store.GetRun(abandoned)
	require.NoError(t, err)
	assert.Equal(t, "failed", run.Status,
		"recovery must also mark the abandoned `running` run failed so it stops surfacing as a live run")
	assert.Contains(t, run.ErrorMessage, "orphaned",
		"run error_message must carry the structured `orphaned:` prefix so log search can group recoveries")
}

// TestRecoverOrphanRun_Tick_BackdatedRunFailedAndTaskRequeued covers
// the runs-table arm: a `running` run with no heartbeat row keyed by
// run_id, where the run's started_at has aged past the grace. The run
// must be marked failed; if the parent task is still `doing` it must
// also be re-queued.
func TestRecoverOrphanRun_Tick_BackdatedRunFailedAndTaskRequeued(t *testing.T) {
	sched, store := setupRecoverySchedulerWithGrace(t, 1)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-ORPH-RUN", Title: "doing with orphan run", Status: "doing",
		Executor: "mock", AgentProfile: "mock",
	}))
	abandoned, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-ORPH-RUN", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)
	// Task is fresh (within grace) but run is aged — exercises the
	// orphan-run arm independent of the stuck-task arm. Once recovery
	// fires for the run, the task should also be re-queued because the
	// task path checks task.Status without re-applying the grace (the
	// run's age IS the recovery signal).
	backdateRunStartedAt(t, store, abandoned, 10*time.Second)

	sched.runHealthScan(context.Background(), healthscan.ModeTick)

	run, err := store.GetRun(abandoned)
	require.NoError(t, err)
	assert.Equal(t, "failed", run.Status, "orphan run must be marked failed")

	task, err := store.GetTask("CW-ORPH-RUN")
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status,
		"parent task in `doing` must be re-queued when its only run is reclaimed by orphan-run recovery")
}

// TestRecoverOrphanRun_TaskAlreadyMovedOn_RunFailedOnly is the
// partial-state case. The task transitioned out of `doing` by some
// other path (manual operator transition, sibling recovery) but the
// run row was never failed. Recovery must mark the run failed without
// touching the task — clobbering the task would overwrite operator
// intent.
func TestRecoverOrphanRun_TaskAlreadyMovedOn_RunFailedOnly(t *testing.T) {
	sched, store := setupRecoverySchedulerWithGrace(t, 1)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-ORPH-RUN-MOVED", Title: "task moved on", Status: "todo",
		Executor: "mock", AgentProfile: "mock",
	}))
	abandoned, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-ORPH-RUN-MOVED", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)
	backdateRunStartedAt(t, store, abandoned, 10*time.Second)

	sched.runHealthScan(context.Background(), healthscan.ModeBoot)

	run, err := store.GetRun(abandoned)
	require.NoError(t, err)
	assert.Equal(t, "failed", run.Status, "abandoned run must still be reclaimed")

	task, err := store.GetTask("CW-ORPH-RUN-MOVED")
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status,
		"task already out of `doing` must not be touched — operator intent wins over auto-recovery")
}

// TestRecoverOrphanRun_GraceWindow_ProtectsFreshRuns is the negative
// arm for the runs-table case. A `running` run created seconds ago
// without a heartbeat row is still inside the dispatch-vs-register
// race; the grace MUST keep it alive.
func TestRecoverOrphanRun_GraceWindow_ProtectsFreshRuns(t *testing.T) {
	sched, store := setupRecoveryScheduler(t)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-FRESH-RUN", Title: "fresh run, no hb yet", Status: "doing",
		Executor: "mock", AgentProfile: "mock",
	}))
	fresh, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-FRESH-RUN", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)

	sched.runHealthScan(context.Background(), healthscan.ModeTick)

	run, err := store.GetRun(fresh)
	require.NoError(t, err)
	assert.Equal(t, "running", run.Status,
		"fresh run within grace must not be failed — covers the dispatch-vs-register race")
}

// TestStuckRecovery_Idempotent locks in that a second scan over a
// just-recovered task does nothing — the task is already `todo`, the
// run is already `failed`. The scheduler's recovery primitives MUST
// re-read state and short-circuit; otherwise a slow recovery overlap
// with a manual re-dispatch could clobber the new run.
func TestStuckRecovery_Idempotent(t *testing.T) {
	sched, store := setupRecoverySchedulerWithGrace(t, 1)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-IDEM", Title: "idempotent recover", Status: "doing",
		Executor: "mock", AgentProfile: "mock",
	}))
	r, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-IDEM", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)
	backdateTaskUpdatedAt(t, store, "CW-IDEM", 10*time.Second)
	backdateRunStartedAt(t, store, r, 10*time.Second)

	// First pass — should recover.
	sched.runHealthScan(context.Background(), healthscan.ModeBoot)
	task, err := store.GetTask("CW-IDEM")
	require.NoError(t, err)
	require.Equal(t, "todo", task.Status)
	run, err := store.GetRun(r)
	require.NoError(t, err)
	require.Equal(t, "failed", run.Status)

	// Second pass — must be a no-op. Re-running the scan over the same
	// state should not move the task back to doing or rewrite the run's
	// error_message.
	priorErr := run.ErrorMessage
	sched.runHealthScan(context.Background(), healthscan.ModeBoot)

	task2, err := store.GetTask("CW-IDEM")
	require.NoError(t, err)
	assert.Equal(t, "todo", task2.Status,
		"second pass over already-recovered state must not move task back to doing")
	run2, err := store.GetRun(r)
	require.NoError(t, err)
	assert.Equal(t, "failed", run2.Status, "second pass must not rewrite run status")
	assert.Equal(t, priorErr, run2.ErrorMessage,
		"second pass must not rewrite the run's error_message — the first recovery's message wins")
}
