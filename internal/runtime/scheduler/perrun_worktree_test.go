package scheduler_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/runtime/executor"
	"github.com/hollis-labs/torque/internal/runtime/queue"
	"github.com/hollis-labs/torque/internal/runtime/scheduler"
	"github.com/hollis-labs/torque/internal/testutil/sqlitetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeOriginAndClone seeds a bare origin repo with one commit on main and
// returns the path to a working clone of it. Mirrors the fixture used by
// internal/worktree/perrun_test.go but lives here to avoid a test-only
// dependency cycle through the worktree package.
func makeOriginAndClone(t *testing.T) string {
	t.Helper()
	runIn := func(dir string, args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}

	seedDir := t.TempDir()
	runIn(seedDir, "init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(seedDir, "README.md"), []byte("# Seed"), 0o644))
	runIn(seedDir, "add", ".")
	runIn(seedDir, "commit", "-m", "initial")

	originDir := t.TempDir()
	cmd := exec.Command("git", "clone", "--bare", seedDir, originDir)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "clone --bare: %s", out)

	workDir := t.TempDir()
	cmd = exec.Command("git", "clone", originDir, workDir)
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "clone: %s", out)

	return workDir
}

func setupSchedulerForWorktree(t *testing.T, cfg *config.SchedulerConfig) (*scheduler.Scheduler, *sqlstore.Store, *executor.MockExecutor) {
	t.Helper()

	store := sqlitetest.OpenStore(t)

	dir := t.TempDir()
	q, err := queue.Open(context.Background(), filepath.Join(dir, "queue.db"))
	require.NoError(t, err)

	mock := executor.NewMockExecutor()
	registry := executor.NewRegistry()
	registry.Register(mock)

	sched := scheduler.New(store, q, registry, nil, cfg)

	t.Cleanup(func() {
		sched.Stop(context.Background())
		q.Close()
		store.Close()
	})

	return sched, store, mock
}

func TestDispatchUsesPerRunWorktreeWhenEnabled(t *testing.T) {
	repo := makeOriginAndClone(t)
	wtRoot := filepath.Join(t.TempDir(), "wts")

	cfg := &config.SchedulerConfig{
		Workers:          1,
		IntervalSeconds:  1,
		Enabled:          true,
		StaleSeconds:     300,
		WorktreePerRun:   true,
		WorktreeRoot:     wtRoot,
		WorktreeKeepDays: 0,
	}

	sched, store, mock := setupSchedulerForWorktree(t, cfg)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-WT-0001", Title: "wt task", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnDone: "close", WorkingDir: repo,
	}))

	mock.SetResult(&executor.ExecutionResult{Status: "done"})

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(400 * time.Millisecond)
	sched.DrainResults()

	jobs := mock.RecordedJobs()
	require.Len(t, jobs, 1)
	expected := filepath.Join(wtRoot, "run-1")
	assert.Equal(t, expected, jobs[0].WorkingDir, "executor should run inside the per-run worktree")

	// Clean run with no commits/uncommitted work — cleanup should remove it.
	_, statErr := os.Stat(expected)
	assert.True(t, os.IsNotExist(statErr), "clean worktree should be removed after run")
}

func TestDispatchPreservesWorktreeWhenAgentLeavesWork(t *testing.T) {
	repo := makeOriginAndClone(t)
	wtRoot := filepath.Join(t.TempDir(), "wts")

	cfg := &config.SchedulerConfig{
		Workers: 1, IntervalSeconds: 1, Enabled: true, StaleSeconds: 300,
		WorktreePerRun: true, WorktreeRoot: wtRoot,
	}
	// Build a scheduler whose executor drops an uncommitted file in the
	// worktree before returning — simulating an agent that exited
	// mid-edit. We use a wrapper around MockExecutor so the existing mock
	// machinery still applies for results and validation.
	store := sqlitetest.OpenStore(t)
	defer store.Close()

	q, err := queue.Open(context.Background(), filepath.Join(t.TempDir(), "queue.db"))
	require.NoError(t, err)
	defer q.Close()

	mock := executor.NewMockExecutor()
	mock.SetResult(&executor.ExecutionResult{Status: "done"})
	expected := filepath.Join(wtRoot, "run-1")
	wrapper := &writeBeforeReturnExecutor{
		filePath: filepath.Join(expected, "wip.txt"),
		result:   mock,
	}
	registry := executor.NewRegistry()
	registry.Register(wrapper)

	sched := scheduler.New(store, q, registry, nil, cfg)
	defer sched.Stop(context.Background())

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-WT-0002", Title: "wt task w/ work", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnDone: "close", WorkingDir: repo,
	}))

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(500 * time.Millisecond)
	sched.DrainResults()

	// Worktree should be preserved because uncommitted work is present.
	_, statErr := os.Stat(expected)
	require.NoError(t, statErr, "worktree should be preserved when agent leaves work")
	_, statErr = os.Stat(filepath.Join(expected, "wip.txt"))
	require.NoError(t, statErr, "uncommitted file should still exist")
}

func TestPerRunWorktreeDisabledByDefault(t *testing.T) {
	repo := makeOriginAndClone(t)

	cfg := &config.SchedulerConfig{
		Workers: 1, IntervalSeconds: 1, Enabled: true, StaleSeconds: 300,
		// WorktreePerRun left false
	}
	sched, store, mock := setupSchedulerForWorktree(t, cfg)

	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID: "CW-WT-0003", Title: "no wt", Status: "todo", Priority: 1,
		Executor: "mock", AgentProfile: "mock", OnDone: "close", WorkingDir: repo,
	}))

	mock.SetResult(&executor.ExecutionResult{Status: "done"})

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(300 * time.Millisecond)
	sched.DrainResults()

	jobs := mock.RecordedJobs()
	require.Len(t, jobs, 1)
	assert.Equal(t, repo, jobs[0].WorkingDir, "with per-run disabled, executor should run in task.working_dir")
}

func TestConcurrentRunsGetSeparateWorktrees(t *testing.T) {
	repo := makeOriginAndClone(t)
	wtRoot := filepath.Join(t.TempDir(), "wts")

	cfg := &config.SchedulerConfig{
		Workers: 2, IntervalSeconds: 1, Enabled: true, StaleSeconds: 300,
		WorktreePerRun: true, WorktreeRoot: wtRoot,
	}
	sched, store, mock := setupSchedulerForWorktree(t, cfg)

	for i := 1; i <= 2; i++ {
		require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
			ID: fmt.Sprintf("CW-CONCUR-%04d", i), Title: "concur", Status: "todo",
			Priority: 1, Executor: "mock", AgentProfile: "mock", OnDone: "close", WorkingDir: repo,
		}))
	}

	// Slow the executor down enough that both runs overlap, so the test
	// would catch a worktree path collision (git worktree add refuses to
	// reuse a path).
	mock.SetDelay(150 * time.Millisecond)
	mock.SetResult(&executor.ExecutionResult{Status: "done"})

	require.NoError(t, sched.Tick(context.Background()))
	time.Sleep(800 * time.Millisecond)
	sched.DrainResults()

	jobs := mock.RecordedJobs()
	require.Len(t, jobs, 2)

	seen := map[string]bool{}
	for _, j := range jobs {
		rel, err := filepath.Rel(wtRoot, j.WorkingDir)
		require.NoError(t, err)
		assert.False(t, strings.HasPrefix(rel, ".."), "each run should be inside the worktree root, got %s", j.WorkingDir)
		assert.False(t, seen[j.WorkingDir], "concurrent runs must not collide on the same worktree path: %s", j.WorkingDir)
		seen[j.WorkingDir] = true
	}
}

// writeBeforeReturnExecutor is a tiny test executor that drops a file at
// filePath before delegating to the wrapped MockExecutor. Used to simulate
// an agent leaving uncommitted work in the worktree.
type writeBeforeReturnExecutor struct {
	filePath string
	result   *executor.MockExecutor
	once     sync.Once
}

func (w *writeBeforeReturnExecutor) Name() string { return "mock" }
func (w *writeBeforeReturnExecutor) Run(ctx context.Context, job *executor.ExecutionJob, cb executor.EventCallback) (*executor.ExecutionResult, error) {
	w.once.Do(func() {
		_ = os.MkdirAll(filepath.Dir(w.filePath), 0o755)
		_ = os.WriteFile(w.filePath, []byte("wip"), 0o644)
	})
	return w.result.Run(ctx, job, cb)
}
func (w *writeBeforeReturnExecutor) Validate(job *executor.ExecutionJob) error {
	return w.result.Validate(job)
}
func (w *writeBeforeReturnExecutor) Capabilities() executor.ExecutorCapabilities {
	return w.result.Capabilities()
}
