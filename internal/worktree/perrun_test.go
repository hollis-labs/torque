package worktree_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/clockwork-manifold/internal/worktree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupOriginAndClone creates a bare "origin" repo with one commit on main,
// then clones it into a working repo so that origin/main is a real remote ref.
// Returns the working repo path.
func setupOriginAndClone(t *testing.T) string {
	t.Helper()

	originDir := t.TempDir()
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

	// Build a non-bare seed repo, then clone --bare into origin so we can clone again.
	seedDir := t.TempDir()
	runIn(seedDir, "init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(seedDir, "README.md"), []byte("# Seed"), 0644))
	runIn(seedDir, "add", ".")
	runIn(seedDir, "commit", "-m", "initial")

	// Make origin a bare clone of seed.
	cmd := exec.Command("git", "clone", "--bare", seedDir, originDir)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "clone --bare: %s", out)

	// Clone origin into the working repo.
	workDir := t.TempDir()
	cmd = exec.Command("git", "clone", originDir, workDir)
	out, err = cmd.CombinedOutput()
	require.NoError(t, err, "clone: %s", out)

	return workDir
}

func gitInWt(t *testing.T, dir string, args ...string) string {
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
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

func TestSetupPerRunCreatesCleanWorktreeAtOriginMain(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{
		Root: root,
	}, repoRoot, 42)
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(root, "run-42"), wtPath)

	// Worktree directory exists.
	_, err = os.Stat(wtPath)
	require.NoError(t, err)

	// Worktree HEAD matches origin/main.
	headSHA := gitInWt(t, wtPath, "rev-parse", "HEAD")
	originMainSHA := gitInWt(t, repoRoot, "rev-parse", "origin/main")
	assert.Equal(t, originMainSHA, headSHA)
}

func TestSetupPerRunDefaultRoot(t *testing.T) {
	repoRoot := setupOriginAndClone(t)

	// No Root supplied — should default to ${repoRoot}-worktrees.
	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{}, repoRoot, 7)
	require.NoError(t, err)
	defer os.RemoveAll(filepath.Dir(wtPath))

	assert.Equal(t, filepath.Join(repoRoot+"-worktrees", "run-7"), wtPath)
}

func TestSetupPerRunRejectsNonRepo(t *testing.T) {
	notARepo := t.TempDir()
	_, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: t.TempDir()}, notARepo, 1)
	assert.Error(t, err)
}

func TestCleanupPerRunRemovesCleanWorktree(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 1)
	require.NoError(t, err)

	removed, err := worktree.CleanupPerRun(repoRoot, wtPath)
	require.NoError(t, err)
	assert.True(t, removed, "clean worktree should be removed")

	_, statErr := os.Stat(wtPath)
	assert.True(t, os.IsNotExist(statErr), "worktree dir should be gone")
}

func TestCleanupPerRunPreservesUncommittedWork(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 1)
	require.NoError(t, err)

	// Create an untracked file — uncommitted work.
	require.NoError(t, os.WriteFile(filepath.Join(wtPath, "scratch.txt"), []byte("wip"), 0644))

	removed, err := worktree.CleanupPerRun(repoRoot, wtPath)
	require.NoError(t, err)
	assert.False(t, removed, "worktree with uncommitted work should be preserved")

	_, statErr := os.Stat(wtPath)
	require.NoError(t, statErr, "worktree dir should still exist")
}

func TestCleanupPerRunPreservesCommitsAheadOfMain(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 1)
	require.NoError(t, err)

	// Create a branch and commit on it — agent's typical flow.
	gitInWt(t, wtPath, "checkout", "-b", "task/CW-test")
	require.NoError(t, os.WriteFile(filepath.Join(wtPath, "agent.go"), []byte("package x"), 0644))
	gitInWt(t, wtPath, "add", "agent.go")
	gitInWt(t, wtPath, "commit", "-m", "agent work")

	removed, err := worktree.CleanupPerRun(repoRoot, wtPath)
	require.NoError(t, err)
	assert.False(t, removed, "worktree with commits ahead of origin/main should be preserved")

	_, statErr := os.Stat(wtPath)
	require.NoError(t, statErr)
}

func TestCleanupPerRunIdempotentWhenMissing(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	missing := filepath.Join(t.TempDir(), "never-existed")

	removed, err := worktree.CleanupPerRun(repoRoot, missing)
	require.NoError(t, err)
	assert.False(t, removed, "missing worktree should not be reported as removed")
}
