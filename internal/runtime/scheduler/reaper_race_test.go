package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/healthscan"
	"github.com/hollis-labs/torque/internal/runtime/writeq"
)

// finishBeforeWriter is a writeq.Writer that, just before the submit named op
// runs, lets the run finish for real: the state of a run whose worker was
// only slow and completed between the reaper's read and its write.
type finishBeforeWriter struct {
	writeq.Writer
	op     string
	finish func()
}

func (w *finishBeforeWriter) Submit(ctx context.Context, name string, fn func(*sqlstore.WriteTx) error) error {
	if name == w.op {
		w.finish()
	}
	return w.Writer.Submit(ctx, name, fn)
}

// raceFixture is a `doing` task with one `running` run, aged past the grace
// window, whose run finishes (done, with a cost and its ledger row) at the
// moment the reaper's write for op begins.
func raceFixture(t *testing.T, op string) (sched *Scheduler, store *sqlstore.Store, taskID string, runID int64) {
	t.Helper()
	sched, store = setupRecoverySchedulerWithGrace(t, 1)
	taskID = "CW-REAP-RACE-" + op
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: taskID, Title: "reaper race", Status: "doing", Executor: "mock", AgentProfile: "mock"}))
	id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: taskID, Executor: "mock", Status: "running"})
	require.NoError(t, err)
	backdateRunStartedAt(t, store, id, 10*time.Second)
	backdateTaskUpdatedAt(t, store, taskID, 10*time.Second)
	sched.stateWriter = &finishBeforeWriter{Writer: sched.stateWriter, op: op, finish: func() {
		require.NoError(t, store.CompleteRun(id, sqlstore.RunCompletion{Status: "done", PromptTokens: 10, Cost: 0.25, CostSource: "provider"}))
		_, err := store.AppendCostLedger(&sqlstore.CostLedgerRecord{TaskID: taskID, RunID: id, Cost: 0.25, CostSource: "provider", ProviderCost: 0.25})
		require.NoError(t, err)
	}}
	return sched, store, taskID, id
}

// A run that finished between the reaper's check and its write is left as it
// finished (not reset to failed at cost 0 with its ledger row orphaned), gets
// no run_orphan_recovered event, and its task is neither requeued nor has its
// worktree cleaned up as if the run had been lost.
func requireFinishedRunLeftAlone(t *testing.T, store *sqlstore.Store, taskID string, runID int64) {
	t.Helper()
	run, err := store.GetRun(runID)
	require.NoError(t, err)
	assert.Equal(t, "done", run.Status, "the run that finished keeps its status")
	assert.InDelta(t, 0.25, run.Cost, 1e-9, "and its cost, which its ledger row still matches")
	assert.Equal(t, 10, run.PromptTokens)
	events, err := store.ListRunEvents(sqlstore.RunEventFilter{TaskID: taskID, Types: []string{"run_orphan_recovered"}})
	require.NoError(t, err)
	assert.Empty(t, events, "no orphan-recovered event for a run that was not reclaimed")
	task, err := store.GetTask(taskID)
	require.NoError(t, err)
	assert.Equal(t, "doing", task.Status, "the task is not requeued over a run that finished")
}

func TestRecoverStuckTask_RunFinishingFirstIsLeftAlone(t *testing.T) {
	sched, store, taskID, runID := raceFixture(t, "scheduler_stuck_task_recovery")
	sched.recoverStuckTask(context.Background(), healthscan.ModeTick, healthscan.Anomaly{
		Kind: healthscan.AnomalyTaskDoingNoWorker, TaskID: taskID, ObservedAt: time.Now().Add(-10 * time.Second),
	})
	requireFinishedRunLeftAlone(t, store, taskID, runID)
}

func TestRecoverOrphanRun_RunFinishingFirstIsLeftAlone(t *testing.T) {
	sched, store, taskID, runID := raceFixture(t, "scheduler_orphan_run_recovery")
	sched.recoverOrphanRun(context.Background(), healthscan.ModeTick, healthscan.Anomaly{
		Kind: healthscan.AnomalyRunRunningNoWorker, TaskID: taskID, RunID: runID, ObservedAt: time.Now().Add(-10 * time.Second),
	})
	requireFinishedRunLeftAlone(t, store, taskID, runID)
}

func TestRecoverOrphanedWorker_RunFinishingFirstIsLeftAlone(t *testing.T) {
	sched, store, taskID, runID := raceFixture(t, "scheduler_orphan_recovery")
	sched.recoverOrphanedWorker(context.Background(), StaleWorker{WorkerID: "w-" + taskID, TaskID: taskID, RunID: runID, Executor: "mock"})
	requireFinishedRunLeftAlone(t, store, taskID, runID)
}

// The control: with no race the same reapers reclaim the run, record the
// event and requeue the task, so the guard changes nothing for a lost run.
func TestRecoverOrphanRun_ALostRunIsStillReclaimed(t *testing.T) {
	sched, store := setupRecoverySchedulerWithGrace(t, 1)
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{ID: "CW-REAP-LOST", Title: "lost", Status: "doing", Executor: "mock", AgentProfile: "mock"}))
	id, err := store.CreateRun(&sqlstore.RunRecord{TaskID: "CW-REAP-LOST", Executor: "mock", Status: "running"})
	require.NoError(t, err)
	backdateRunStartedAt(t, store, id, 10*time.Second)

	sched.recoverOrphanRun(context.Background(), healthscan.ModeTick, healthscan.Anomaly{
		Kind: healthscan.AnomalyRunRunningNoWorker, TaskID: "CW-REAP-LOST", RunID: id, ObservedAt: time.Now().Add(-10 * time.Second),
	})

	run, err := store.GetRun(id)
	require.NoError(t, err)
	assert.Equal(t, "failed", run.Status)
	events, err := store.ListRunEvents(sqlstore.RunEventFilter{TaskID: "CW-REAP-LOST", Types: []string{"run_orphan_recovered"}})
	require.NoError(t, err)
	assert.Len(t, events, 1)
	task, err := store.GetTask("CW-REAP-LOST")
	require.NoError(t, err)
	assert.Equal(t, "todo", task.Status)
}
