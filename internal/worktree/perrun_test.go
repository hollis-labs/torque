package worktree_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/worktree"
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

	// No Root supplied — the per-run worktree must be a TRUE SIBLING of the
	// repo root at the SAME directory depth: <repoParent>/<repoName>-worktrees-run-<id>.
	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{}, repoRoot, 7)
	require.NoError(t, err)
	defer os.RemoveAll(wtPath)

	repoParent := filepath.Dir(repoRoot)
	repoName := filepath.Base(repoRoot)
	want := filepath.Join(repoParent, repoName+"-worktrees-run-7")
	assert.Equal(t, want, wtPath)

	// Sibling-depth invariant: the worktree's parent dir == the repo's parent
	// dir, so a relative "../" go.mod replace resolves identically from both.
	assert.Equal(t, filepath.Dir(repoRoot), filepath.Dir(wtPath),
		"per-run worktree must sit at the same directory depth as the repo root")
}

// TestPerRunPathSiblingDepth: PerRunPath with an empty root places the
// worktree as a true sibling of the repo (same depth).
func TestPerRunPathSiblingDepth(t *testing.T) {
	got := worktree.PerRunPath("/home/dev/myrepo", "", 42)
	assert.Equal(t, "/home/dev/myrepo-worktrees-run-42", got)
	assert.Equal(t, "/home/dev", filepath.Dir(got),
		"worktree parent must equal repo parent")
}

// TestPerRunPathExplicitRootHonored: PerRunPath with an explicit root
// (TORQUE_WORKTREE_ROOT override) places the leaf under that root as-is.
func TestPerRunPathExplicitRootHonored(t *testing.T) {
	got := worktree.PerRunPath("/home/dev/myrepo", "/var/torque/wt", 9)
	assert.Equal(t, "/var/torque/wt/run-9", got)
}

// TestSetupPerRunSiblingDepthPreservesRelativeReplace: a repo whose go.mod
// has a relative "../" replace passes the guard cleanly under the default
// sibling-depth placement, and the worktree is created.
func TestSetupPerRunSiblingDepthPreservesRelativeReplace(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "go.mod"),
		[]byte("module example.com/x\n\ngo 1.26\n\nreplace example.com/lib => ../lib\n"), 0644))

	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{}, repoRoot, 5)
	require.NoError(t, err, "default sibling-depth placement must pass the relative-replace guard")
	defer os.RemoveAll(wtPath)

	assert.Equal(t, filepath.Dir(repoRoot), filepath.Dir(wtPath))
}

// TestSetupPerRunBlocksRelativeReplaceAtWrongDepth: when the repo's go.mod
// has a relative replace AND an explicit root puts the worktree at a
// different depth, SetupPerRun returns a blocking error instead of silently
// mis-resolving the replace.
func TestSetupPerRunBlocksRelativeReplaceAtWrongDepth(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "go.mod"),
		[]byte("module example.com/x\n\ngo 1.26\n\nreplace example.com/lib => ../../lib\n"), 0644))

	// Explicit root at an unrelated location — worktree parent != repo parent.
	badRoot := filepath.Join(t.TempDir(), "elsewhere", "deeper")
	_, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: badRoot}, repoRoot, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "relative go.mod replace")
}

// TestCheckRelativeReplaceSafe covers the guard decision directly.
func TestCheckRelativeReplaceSafe(t *testing.T) {
	// No go.mod → safe regardless of placement.
	repoNoMod := t.TempDir()
	assert.NoError(t, worktree.CheckRelativeReplaceSafe(repoNoMod, "/anywhere/run-1"))

	// go.mod with no replace → safe.
	repoPlain := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoPlain, "go.mod"),
		[]byte("module m\n\ngo 1.26\n"), 0644))
	assert.NoError(t, worktree.CheckRelativeReplaceSafe(repoPlain, "/anywhere/run-1"))

	// go.mod with an ABSOLUTE replace → not depth-sensitive → safe.
	repoAbs := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(repoAbs, "go.mod"),
		[]byte("module m\n\ngo 1.26\n\nreplace m/lib => /opt/lib\n"), 0644))
	assert.NoError(t, worktree.CheckRelativeReplaceSafe(repoAbs, "/anywhere/run-1"))

	// go.mod with a relative replace + sibling placement → safe.
	repoRel := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(repoRel, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(repoRel, "go.mod"),
		[]byte("module m\n\ngo 1.26\n\nreplace m/lib => ../lib\n"), 0644))
	sibling := worktree.PerRunPath(repoRel, "", 1)
	assert.NoError(t, worktree.CheckRelativeReplaceSafe(repoRel, sibling))

	// go.mod with a relative replace + wrong-depth placement → blocking error.
	err := worktree.CheckRelativeReplaceSafe(repoRel, "/somewhere/else/run-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "relative go.mod replace")
}

func TestSetupPerRunRejectsNonRepo(t *testing.T) {
	notARepo := t.TempDir()
	_, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: t.TempDir()}, notARepo, 1)
	assert.Error(t, err)
}

// TestSetupPerRunLocalRepoNoOrigin pins the no-origin fallback: a git repo
// with NO `origin` remote must still get a per-run worktree (branched from
// local HEAD) instead of failing `git fetch origin` and silently degrading
// to shared mode.
func TestSetupPerRunLocalRepoNoOrigin(t *testing.T) {
	repoRoot := t.TempDir()
	gitInWt(t, repoRoot, "init", "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("# Local"), 0644))
	gitInWt(t, repoRoot, "add", ".")
	gitInWt(t, repoRoot, "commit", "-m", "initial")

	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{}, repoRoot, 1)
	require.NoError(t, err, "local-only repo (no origin) must still get a worktree")

	info, statErr := os.Stat(wtPath)
	require.NoError(t, statErr, "worktree dir must exist")
	assert.True(t, info.IsDir())

	// Worktree HEAD matches the repo's local HEAD — the fallback base ref.
	repoHEAD := gitInWt(t, repoRoot, "rev-parse", "HEAD")
	wtHEAD := gitInWt(t, wtPath, "rev-parse", "HEAD")
	assert.Equal(t, repoHEAD, wtHEAD, "worktree should be detached at local HEAD")
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
