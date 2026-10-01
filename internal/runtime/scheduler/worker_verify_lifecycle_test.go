package scheduler

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/queue"
	"github.com/hollis-labs/torque/internal/worktree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// TestEditsWithoutCommits_BlocksAndPreservesWorktree is CW-20261001-0028
// end to end, from the verdict to the next scheduler tick. A long-lived
// worker reached review with an uncommitted diff and no commits. The task
// runs under on_fail=retry with retries left, which used to send it
// straight back to `todo` and re-dispatch it into a fresh worktree off
// origin/main, stranding the diff. Now it must:
//
//   - land in `blocked`, with a reason naming the preserved worktree, how
//     many paths are uncommitted, and the remedy;
//   - burn no retry and not be re-dispatched by the next tick, while an
//     eligible task beside it is, so the tick was live;
//   - keep its worktree through per-run cleanup.
func TestEditsWithoutCommits_BlocksAndPreservesWorktree(t *testing.T) {
	worktreePath := makeGitRepoWithCommits(t, 0)
	dirtyWorktree(t, worktreePath)
	logDir := writeStreamJSONL(t, []toolUseEntry{{Tool: "Edit"}, {Tool: "Write"}})

	verdict := VerifyWorkerCompletion(context.Background(), "", worktreePath, logDir, "", 0)
	require.Equal(t, VerdictFailedNoCommitsWithEdits, verdict.Kind)
	status, reason := verdict.ApplyTo("review", "")
	require.Equal(t, "blocked", status)

	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	// The worker self-transitioned to review before the engine graded it.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-EDITS-NOCOMMIT", Title: "forgot to commit", Status: "review",
		Executor: "mock", AgentProfile: "mock", OnFail: "retry", MaxRetries: 3,
	}))
	runID, err := store.CreateRun(&sqlstore.RunRecord{
		TaskID: "CW-EDITS-NOCOMMIT", Executor: "mock", Status: "running",
	})
	require.NoError(t, err)

	bus := NewEventBus()
	t.Cleanup(bus.Close)
	lm := NewLifecycleManager(store, bus)
	require.NoError(t, lm.HandleResult("CW-EDITS-NOCOMMIT", runID, &executor.ExecutionResult{
		Status: status, Reason: reason,
	}))

	task, err := store.GetTask("CW-EDITS-NOCOMMIT")
	require.NoError(t, err)
	assert.Equal(t, "blocked", task.Status, "edits without commits must park the task, not retry it")
	assert.Contains(t, task.BlockedReason, worktreePath, "the reason must name the preserved worktree")
	assert.Contains(t, task.BlockedReason, "2 uncommitted path(s)")
	assert.Contains(t, task.BlockedReason, "commit or discard them there, then re-queue the task")

	var retryCount int
	require.NoError(t, store.DB().QueryRow(
		`SELECT retry_count FROM tasks WHERE id = ?`, "CW-EDITS-NOCOMMIT").Scan(&retryCount))
	assert.Equal(t, 0, retryCount, "blocking is not a retry; on_fail's budget must be untouched")

	// The next tick must not re-dispatch it. The eligible control proves the
	// tick would have dispatched work that was there to dispatch.
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-ELIGIBLE", Title: "eligible control", Status: "todo",
		Executor: "mock", AgentProfile: "mock",
	}))
	reg := executor.NewRegistry()
	reg.Register(executor.NewMockExecutor())
	q, err := queue.Open(context.Background(), filepath.Join(t.TempDir(), "queue.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = q.Close() })
	sched := New(store, q, reg, nil, &config.SchedulerConfig{Workers: 1, StaleSeconds: 300, Enabled: true})
	t.Cleanup(func() { sched.Stop(context.Background()) })
	require.NoError(t, sched.Tick(context.Background()))

	runs, err := store.ListRuns("CW-EDITS-NOCOMMIT")
	require.NoError(t, err)
	assert.Len(t, runs, 1, "a blocked task must not be re-dispatched")
	control, err := store.ListRuns("CW-ELIGIBLE")
	require.NoError(t, err)
	assert.Len(t, control, 1, "the tick dispatched the eligible task, so it was live")

	// Per-run cleanup keeps a worktree with uncommitted work, so the path in
	// the reason still holds the diff.
	removed, err := worktree.CleanupPerRun(worktreePath, worktreePath)
	require.NoError(t, err)
	assert.False(t, removed, "a dirty worktree must survive per-run cleanup")
	_, statErr := os.Stat(filepath.Join(worktreePath, "new.txt"))
	assert.NoError(t, statErr, "the uncommitted file must still be in the preserved worktree")
}
