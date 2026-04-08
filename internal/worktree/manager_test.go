package worktree_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore"
	"github.com/hollis-labs/clockwork-manifold/internal/persistence/sqlstore/migrations"
	"github.com/hollis-labs/clockwork-manifold/internal/worktree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// setupTestRepo creates a temporary git repo with an initial commit.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, out)
	}
	run("init", "-b", "main")
	// Create an initial file and commit
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Test"), 0644))
	run("add", ".")
	run("commit", "-m", "initial")
	return dir
}

func setupTestStore2(t *testing.T) *sqlstore.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1) // in-memory SQLite is per-connection; force single conn
	require.NoError(t, migrations.Run(db))
	store, err := sqlstore.New(db, "sqlite")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return store
}

func TestWorktreeCreate(t *testing.T) {
	repoDir := setupTestRepo(t)
	store := setupTestStore2(t)

	// Create a task and project for the worktree
	store.CreateTask(&sqlstore.TaskRecord{
		ID:       "CW-20260407-0001",
		Title:    "Fix bug",
		Executor: "cli",
	})

	cfg := worktree.DefaultWorktreeConfig()
	mgr := worktree.NewManager(store, cfg)

	ctx := context.Background()
	wt, err := mgr.Create(ctx, worktree.CreateRequest{
		TaskID:    "CW-20260407-0001",
		RunID:     1,
		ProjectID: "proj-1",
		RepoPath:  repoDir,
	})
	require.NoError(t, err)

	assert.Equal(t, "clockwork/CW-20260407-0001", wt.Branch)
	assert.Contains(t, wt.Path, "CW-20260407-0001")
	assert.Equal(t, "active", wt.Status)

	// Verify the worktree directory exists
	_, err = os.Stat(wt.Path)
	require.NoError(t, err)

	// Verify the branch was created
	cmd := exec.Command("git", "branch", "--list", "clockwork/CW-20260407-0001")
	cmd.Dir = repoDir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(out), "clockwork/CW-20260407-0001")
}

func TestWorktreeMaxPerProject(t *testing.T) {
	repoDir := setupTestRepo(t)
	store := setupTestStore2(t)

	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("CW-20260407-%04d", i)
		store.CreateTask(&sqlstore.TaskRecord{ID: id, Title: "Task", Executor: "cli"})
	}

	cfg := worktree.DefaultWorktreeConfig()
	cfg.MaxPerProject = 2

	mgr := worktree.NewManager(store, cfg)
	ctx := context.Background()

	// Create 2 worktrees — should succeed
	_, err := mgr.Create(ctx, worktree.CreateRequest{TaskID: "CW-20260407-0001", RunID: 1, ProjectID: "proj-1", RepoPath: repoDir})
	require.NoError(t, err)

	_, err = mgr.Create(ctx, worktree.CreateRequest{TaskID: "CW-20260407-0002", RunID: 2, ProjectID: "proj-1", RepoPath: repoDir})
	require.NoError(t, err)

	// 3rd worktree for same project — should fail
	_, err = mgr.Create(ctx, worktree.CreateRequest{TaskID: "CW-20260407-0003", RunID: 3, ProjectID: "proj-1", RepoPath: repoDir})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "max concurrent worktrees")

	// Different project — should succeed
	_, err = mgr.Create(ctx, worktree.CreateRequest{TaskID: "CW-20260407-0004", RunID: 4, ProjectID: "proj-2", RepoPath: repoDir})
	require.NoError(t, err)
}

func TestWorktreeCleanup(t *testing.T) {
	repoDir := setupTestRepo(t)
	store := setupTestStore2(t)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-20260407-0001", Title: "Task", Executor: "cli"})

	cfg := worktree.DefaultWorktreeConfig()
	mgr := worktree.NewManager(store, cfg)
	ctx := context.Background()

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

	// Clean up the worktree
	err = mgr.Cleanup(ctx, wt.TaskID, repoDir)
	require.NoError(t, err)

	// Verify worktree directory is removed
	_, err = os.Stat(wt.Path)
	assert.True(t, os.IsNotExist(err))

	// Verify database record updated
	record, err := mgr.Get(ctx, wt.TaskID)
	require.NoError(t, err)
	assert.Equal(t, "cleaned", record.Status)
	assert.NotNil(t, record.CleanedAt)
}

func TestWorktreeList(t *testing.T) {
	repoDir := setupTestRepo(t)
	store := setupTestStore2(t)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-20260407-0001", Title: "Task 1", Executor: "cli"})
	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-20260407-0002", Title: "Task 2", Executor: "cli"})

	cfg := worktree.DefaultWorktreeConfig()
	mgr := worktree.NewManager(store, cfg)
	ctx := context.Background()

	mgr.Create(ctx, worktree.CreateRequest{TaskID: "CW-20260407-0001", RunID: 1, ProjectID: "proj-1", RepoPath: repoDir})
	mgr.Create(ctx, worktree.CreateRequest{TaskID: "CW-20260407-0002", RunID: 2, ProjectID: "proj-1", RepoPath: repoDir})

	// List active worktrees
	worktrees, err := mgr.ListActive(ctx, "proj-1")
	require.NoError(t, err)
	assert.Len(t, worktrees, 2)
}

func TestWorktreeBranchNaming(t *testing.T) {
	repoDir := setupTestRepo(t)
	store := setupTestStore2(t)

	store.CreateTask(&sqlstore.TaskRecord{ID: "CW-20260407-0042", Title: "Task", Executor: "cli"})

	cfg := worktree.DefaultWorktreeConfig()
	mgr := worktree.NewManager(store, cfg)
	ctx := context.Background()

	wt, err := mgr.Create(ctx, worktree.CreateRequest{
		TaskID:    "CW-20260407-0042",
		RunID:     1,
		ProjectID: "proj-1",
		RepoPath:  repoDir,
	})
	require.NoError(t, err)

	assert.Equal(t, "clockwork/CW-20260407-0042", wt.Branch)
}
