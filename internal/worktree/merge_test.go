package worktree_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/worktree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeCommitInWorktree(t *testing.T, wtPath, filename, content, msg string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(wtPath, filename), []byte(content), 0644))
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = wtPath
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=Test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, out)
	}
	run("add", filename)
	run("commit", "-m", msg)
}

func TestMergeAutoSuccess(t *testing.T) {
	repoDir := setupTestRepo(t)

	// Create a branch and worktree manually
	gitCmdDir(t, repoDir, "branch", "torque/CW-20260407-0001")
	wtPath := filepath.Join(repoDir, ".torque", "worktrees", "CW-20260407-0001")
	gitCmdDir(t, repoDir, "worktree", "add", wtPath, "torque/CW-20260407-0001")

	// Make a non-conflicting change in the worktree
	makeCommitInWorktree(t, wtPath, "newfile.txt", "new content", "add newfile")

	merger := worktree.NewMerger()
	ctx := context.Background()

	result, err := merger.Execute(ctx, worktree.MergeRequest{
		Policy:       worktree.MergePolicyAuto,
		RepoPath:     repoDir,
		SourceBranch: "torque/CW-20260407-0001",
		TargetBranch: "main",
		WorktreePath: wtPath,
	})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Empty(t, result.ConflictFiles)
	assert.NotEmpty(t, result.MergedCommit)
}

func TestMergeAutoConflict(t *testing.T) {
	repoDir := setupTestRepo(t)

	// Make a change on main
	makeCommitInWorktree(t, repoDir, "README.md", "# Changed on main", "main change")

	// Create branch and worktree
	gitCmdDir(t, repoDir, "branch", "torque/CW-20260407-0001", "HEAD~1")
	wtPath := filepath.Join(repoDir, ".torque", "worktrees", "CW-20260407-0001")
	gitCmdDir(t, repoDir, "worktree", "add", wtPath, "torque/CW-20260407-0001")

	// Make a conflicting change in worktree
	makeCommitInWorktree(t, wtPath, "README.md", "# Changed in worktree", "wt change")

	merger := worktree.NewMerger()
	ctx := context.Background()

	result, err := merger.Execute(ctx, worktree.MergeRequest{
		Policy:       worktree.MergePolicyAuto,
		RepoPath:     repoDir,
		SourceBranch: "torque/CW-20260407-0001",
		TargetBranch: "main",
		WorktreePath: wtPath,
	})
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.True(t, result.NeedsResolution)
	assert.Contains(t, result.ConflictFiles, "README.md")
}

func TestMergePolicyNone(t *testing.T) {
	repoDir := setupTestRepo(t)

	merger := worktree.NewMerger()
	ctx := context.Background()

	result, err := merger.Execute(ctx, worktree.MergeRequest{
		Policy:       worktree.MergePolicyNone,
		RepoPath:     repoDir,
		SourceBranch: "torque/CW-20260407-0001",
		TargetBranch: "main",
	})
	require.NoError(t, err)
	assert.True(t, result.Success) // "none" means no merge needed — always succeeds
	assert.False(t, result.NeedsResolution)
}

func TestMergeAutoResolveTriggersResolution(t *testing.T) {
	repoDir := setupTestRepo(t)

	// Set up conflict
	makeCommitInWorktree(t, repoDir, "README.md", "# Main version", "main edit")
	gitCmdDir(t, repoDir, "branch", "torque/CW-20260407-0001", "HEAD~1")
	wtPath := filepath.Join(repoDir, ".torque", "worktrees", "CW-20260407-0001")
	gitCmdDir(t, repoDir, "worktree", "add", wtPath, "torque/CW-20260407-0001")
	makeCommitInWorktree(t, wtPath, "README.md", "# Worktree version", "wt edit")

	merger := worktree.NewMerger()
	ctx := context.Background()

	result, err := merger.Execute(ctx, worktree.MergeRequest{
		Policy:       worktree.MergePolicyAutoResolve,
		RepoPath:     repoDir,
		SourceBranch: "torque/CW-20260407-0001",
		TargetBranch: "main",
		WorktreePath: wtPath,
	})
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.True(t, result.NeedsResolution)
	assert.Contains(t, result.ConflictFiles, "README.md")
}

// Helper to run git in a directory
func gitCmdDir(t *testing.T, dir string, args ...string) {
	t.Helper()
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
