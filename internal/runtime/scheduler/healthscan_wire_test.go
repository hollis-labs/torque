package scheduler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/healthscan"
)

// TestHealthScanBoot_ReclaimsFreshOrphanHeartbeat is the run 864 /
// CW-20260515-0133 boot-sweep fix in test form (CW-20260519-0083). A
// previous serve's heartbeat row whose last_heartbeat is FRESH (well
// inside the staleness threshold) survives into a new process whose
// cancelRegistry is empty. The tick-time FindStale path can't see it —
// it's not stale yet — so the task would have zombied for up to
// StaleSeconds before recovery (the original 1h47m incident). The boot
// scan must reclaim it immediately on startup.
func TestHealthScanBoot_ReclaimsFreshOrphanHeartbeat(t *testing.T) {
	sched, store := setupRecoveryScheduler(t)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-BOOT-ORPHAN",
		Title:    "previous serve died with this in flight",
		Status:   "doing",
		Executor: "mock", AgentProfile: "mock",
	}))
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-BOOT-ORPHAN", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)

	// Plant a FRESH heartbeat row (no backdating) — the previous
	// serve was just updating it before crashing, so a tick-time
	// FindStale would NOT match it. The cancelRegistry on this fresh
	// scheduler is empty, which is the boot-time orphan signal.
	_, err = store.DB().Exec(
		`INSERT INTO worker_heartbeats (worker_id, task_id, run_id, executor, started_at, last_heartbeat)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"worker-boot-orphan", "CW-BOOT-ORPHAN", runID, "mock",
	)
	require.NoError(t, err)

	// Invoke the boot scan directly — Run() would do the same and
	// then block on the ticker. Calling the helper keeps the test
	// focused on the boot-sweep behavior.
	sched.runHealthScan(context.Background(), healthscan.ModeBoot)

	task, err := store.GetTask("CW-BOOT-ORPHAN")
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status,
		"boot sweep must re-queue the orphaned task immediately, without waiting for the staleness timer")

	run, err := store.GetRun(runID)
	require.NoError(t, err)
	assert.Equal(t, "failed", run.Status,
		"abandoned run row must be marked failed by the boot recovery")

	workers, err := sched.heartbeat.ActiveWorkers()
	require.NoError(t, err)
	assert.Empty(t, workers, "boot recovery must delete the orphan heartbeat row so it doesn't re-fire next tick")
}

// TestHealthScanBoot_LeavesLiveTaskAlone is the negative complement: a
// task in `doing` whose worker IS registered in cancelRegistry must
// NOT be reclaimed. In production the boot scan runs from Run() so the
// cancels registry is empty at that moment; the test exercises the
// defensive path that protects a healthy running worker if the call
// sequence ever changed.
func TestHealthScanBoot_LeavesLiveTaskAlone(t *testing.T) {
	sched, store := setupRecoveryScheduler(t)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-BOOT-LIVE", Title: "live", Status: "doing",
		Executor: "mock", AgentProfile: "mock",
	}))
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-BOOT-LIVE", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)
	_, err = store.DB().Exec(
		`INSERT INTO worker_heartbeats (worker_id, task_id, run_id, executor, started_at, last_heartbeat)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"worker-boot-live", "CW-BOOT-LIVE", runID, "mock",
	)
	require.NoError(t, err)

	_, cancel := context.WithCancelCause(context.Background())
	t.Cleanup(func() { cancel(nil) })
	sched.cancels.register("CW-BOOT-LIVE", cancel)

	sched.runHealthScan(context.Background(), healthscan.ModeBoot)

	task, err := store.GetTask("CW-BOOT-LIVE")
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status,
		"a task whose worker is alive in this process must not be re-queued by the boot scan")

	run, err := store.GetRun(runID)
	require.NoError(t, err)
	assert.Equal(t, "running", run.Status,
		"live run must not be failed by the boot scan")
}

// TestHealthScanTick_SurfacesTaskDoingWithoutHeartbeat covers the
// blind-spot the staleness sweep cannot see: a task pinned at `doing`
// with NO heartbeat row at all. The tick scan must surface it on the
// bus so observability isn't blind. The freshly-created task is
// protected from auto-recovery by the StuckGraceSeconds grace window
// (CW-20260519-0084) — recovery only fires once tasks.updated_at has
// aged past the grace. Mid-dispatch races between TransitionTask(doing)
// and HeartbeatMonitor.Register are exactly what this grace exists to
// suppress.
func TestHealthScanTick_SurfacesTaskDoingWithoutHeartbeat(t *testing.T) {
	sched, store := setupRecoveryScheduler(t)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-DOING-GHOST", Title: "ghost doing", Status: "doing",
		Executor: "mock", AgentProfile: "mock",
	}))

	sub := sched.bus.Subscribe()
	t.Cleanup(func() { sched.bus.Unsubscribe(sub) })

	require.NoError(t, sched.Tick(context.Background()))

	found := false
	for done := false; !done; {
		select {
		case ev := <-sub:
			if ev.Type != "health.anomaly" {
				continue
			}
			data, ok := ev.Data.(map[string]interface{})
			if !ok {
				continue
			}
			if data["kind"] == "task_doing_no_worker" && ev.TaskID == "CW-DOING-GHOST" {
				found = true
				done = true
			}
		default:
			done = true
		}
	}
	assert.True(t, found,
		"tick scan must publish health.anomaly task_doing_no_worker for a task in doing with no heartbeat row")

	// Grace-protected: a freshly-created task's updated_at is well inside
	// the default 60s grace window, so the session-recovery primitive
	// defers and leaves the task in `doing`. The backdated-recovery test
	// below proves the same primitive DOES reclaim the same shape once
	// tasks.updated_at has aged past the grace.
	task, err := store.GetTask("CW-DOING-GHOST")
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status,
		"fresh task within StuckGraceSeconds must be left alone — grace covers the dispatch-vs-register race")
}
