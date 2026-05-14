package scheduler_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hollis-labs/torque/internal/concurrency"
	"github.com/hollis-labs/torque/internal/config"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore"
	"github.com/hollis-labs/torque/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/torque/internal/worktree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

func setupIntegration(t *testing.T) (*sqlstore.Store, *worktree.Manager, string) {
	t.Helper()

	// Database
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)

	// Git repo
	repoDir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("# Test"), 0644))
	run("add", ".")
	run("commit", "-m", "initial")

	// Worktree manager
	wtCfg := worktree.DefaultWorktreeConfig()
	mgr := worktree.NewManager(store, wtCfg)

	t.Cleanup(func() { store.Close() })
	return store, mgr, repoDir
}

func TestSchedulerWorktreeLifecycle(t *testing.T) {
	store, mgr, repoDir := setupIntegration(t)
	ctx := context.Background()

	// 1. Scheduler creates task
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:          "CW-20260407-0001",
		Title:       "Fix the bug",
		Description: "Fix the login bug",
		Executor:    "cli",
		Status:      "todo",
		OnDoneMerge: "auto",
		WorkingDir:  repoDir,
		ProjectID:   sql.NullString{String: "proj-1", Valid: true},
	}))

	// 2. Scheduler transitions to doing — creates worktree
	require.NoError(t, store.TransitionTask("CW-20260407-0001", "doing"))
	wt, err := mgr.Create(ctx, worktree.CreateRequest{
		TaskID:    "CW-20260407-0001",
		RunID:     1,
		ProjectID: "proj-1",
		RepoPath:  repoDir,
	})
	require.NoError(t, err)

	// Verify worktree exists
	_, err = os.Stat(wt.Path)
	require.NoError(t, err)

	// 3. Executor works in the worktree (simulate a commit)
	require.NoError(t, os.WriteFile(filepath.Join(wt.Path, "fix.go"), []byte("package fix"), 0644))
	run := func(dir string, args ...string) {
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
	run(wt.Path, "add", "fix.go")
	run(wt.Path, "commit", "-m", "fix the bug")

	// 4. Task done — scheduler triggers merge
	require.NoError(t, store.TransitionTask("CW-20260407-0001", "review"))
	require.NoError(t, store.TransitionTask("CW-20260407-0001", "done"))

	merger := worktree.NewMerger()
	result, err := merger.Execute(ctx, worktree.MergeRequest{
		Policy:       worktree.MergePolicyAuto,
		RepoPath:     repoDir,
		SourceBranch: "torque/CW-20260407-0001",
		TargetBranch: "main",
		WorktreePath: wt.Path,
	})
	require.NoError(t, err)
	assert.True(t, result.Success)

	// 5. Cleanup
	err = mgr.Cleanup(ctx, "CW-20260407-0001", repoDir)
	require.NoError(t, err)

	_, err = os.Stat(wt.Path)
	assert.True(t, os.IsNotExist(err))
}

func TestSchedulerResolutionTaskCreation(t *testing.T) {
	store, _, _ := setupIntegration(t)
	ctx := context.Background()

	// Set up a task
	require.NoError(t, store.CreateTask(&sqlstore.TaskRecord{
		ID:          "CW-20260407-0001",
		Title:       "Add feature",
		Description: "Add user registration feature",
		Executor:    "cli",
		Status:      "done",
		ProjectID:   sql.NullString{String: "proj-1", Valid: true},
	}))

	mergeCfg := config.MergeConfig{
		ResolutionExecutor:    "cli",
		ResolutionAgent:       "default",
		ConfidenceThreshold:   0.8,
		MaxResolutionAttempts: 1,
		NotifyOnConflict:      true,
	}

	resolver := worktree.NewResolver(store, mergeCfg)

	// Simulate conflict detection
	req, err := resolver.BuildResolutionRequest(ctx, worktree.ResolutionInput{
		SourceTaskID:  "CW-20260407-0001",
		SourceBranch:  "torque/CW-20260407-0001",
		TargetBranch:  "main",
		WorktreePath:  "/tmp/worktree",
		ConflictFiles: []string{"main.go", "handler.go"},
		ProjectID:     "proj-1",
	})
	require.NoError(t, err)

	task, err := resolver.CreateResolutionTask(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, 1, task.Priority)
	assert.Equal(t, "block", task.OnFail)
}

func TestWriteBufferIntegrationWithSerializer(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`CREATE TABLE run_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id INTEGER NOT NULL,
		task_id TEXT NOT NULL,
		type TEXT NOT NULL,
		payload TEXT NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	require.NoError(t, err)

	ws := concurrency.NewWriteSerializer(db, 64)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ws.Start(ctx) //nolint:errcheck
	time.Sleep(10 * time.Millisecond)

	// Simulate high-frequency writes through the serializer
	for i := 0; i < 20; i++ {
		err := ws.Submit(ctx, func(db *sql.DB) error {
			_, err := db.Exec(
				"INSERT INTO run_events (run_id, task_id, type, payload) VALUES (?, ?, ?, ?)",
				1, "CW-20260407-0001", "heartbeat", `{"ts": "now"}`,
			)
			return err
		})
		require.NoError(t, err)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM run_events").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 20, count)
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	_, err = db.Exec("PRAGMA journal_mode=WAL")
	require.NoError(t, err)
	_, err = db.Exec("PRAGMA busy_timeout=5000")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	return db
}
