package worktree_test

import (
	"context"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/worktree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMergedSetHas covers the nil/empty/positive paths of the merged-set
// lookup. The Has helper is the hot path SweepMergedPerRun and
// SweepMergedBranches gate on, so the nil-safe contract is worth pinning.
func TestMergedSetHas(t *testing.T) {
	var nilSet worktree.MergedSet
	assert.False(t, nilSet.Has("anything"), "nil set must not panic and must return false")

	empty := worktree.MergedSet{}
	assert.False(t, empty.Has("fix/CW-1"))

	set := worktree.MergedSet{"fix/CW-1": true, "feat/CW-2": true}
	assert.True(t, set.Has("fix/CW-1"))
	assert.False(t, set.Has("fix/CW-2"))
	assert.False(t, set.Has(""), "empty branch name must always be false")
}

// TestWorktreeBranchNameDetached: a worktree detached at a SHA returns "".
// The SetupPerRun harness creates a detached worktree by design (workers
// create their own branch from the clean checkout), so this is the
// default state and the PR-merged sweeper must skip it.
func TestWorktreeBranchNameDetached(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 1)
	require.NoError(t, err)

	assert.Equal(t, "", worktree.WorktreeBranchName(wtPath),
		"detached worktree must report empty branch name")
}

// TestWorktreeBranchNameOnBranch: when the worker checks out a named branch
// in the worktree, WorktreeBranchName returns it verbatim (no refs/heads/
// prefix), matching the key shape gh's headRefName uses.
func TestWorktreeBranchNameOnBranch(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 1)
	require.NoError(t, err)
	gitInWt(t, wtPath, "checkout", "-b", "fix/CW-20260519-0086-test")

	assert.Equal(t, "fix/CW-20260519-0086-test", worktree.WorktreeBranchName(wtPath))
}

// TestCleanupMergedPerRun_RemovesMergedSquashedWorktree pins the headline
// bug fix: a worktree whose branch has a merged PR but whose local SHA
// shows commits ahead of origin/main (the squash-merge case) must be
// force-removed and its branch deleted.
func TestCleanupMergedPerRun_RemovesMergedSquashedWorktree(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 42)
	require.NoError(t, err)

	// Simulate the worker flow: branch + commit. CleanupPerRun alone would
	// preserve this because the branch is "ahead of origin/main".
	gitInWt(t, wtPath, "checkout", "-b", "fix/CW-20260519-0086-test")
	require.NoError(t, os.WriteFile(filepath.Join(wtPath, "code.go"), []byte("package x"), 0644))
	gitInWt(t, wtPath, "add", "code.go")
	gitInWt(t, wtPath, "commit", "-m", "agent work")

	// Sanity: plain cleanup preserves (the bug we're fixing).
	preserved, err := worktree.CleanupPerRun(repoRoot, wtPath)
	require.NoError(t, err)
	require.False(t, preserved, "plain CleanupPerRun must preserve this worktree")
	_, statErr := os.Stat(wtPath)
	require.NoError(t, statErr)

	// PR-aware cleanup with the branch in mergedSet removes it.
	mergedSet := worktree.MergedSet{"fix/CW-20260519-0086-test": true}
	removed, err := worktree.CleanupMergedPerRun(repoRoot, wtPath, mergedSet)
	require.NoError(t, err)
	assert.True(t, removed, "merged-PR worktree must be force-removed")

	_, statErr = os.Stat(wtPath)
	assert.True(t, os.IsNotExist(statErr), "worktree dir should be gone")

	// Local branch is deleted too.
	out, err := gitOut(repoRoot, "branch", "--list", "fix/CW-20260519-0086-test")
	require.NoError(t, err)
	assert.Empty(t, out, "local branch must be deleted after merged-PR cleanup")
}

// TestCleanupMergedPerRun_PreservesUnmergedWorktree: a worktree whose
// branch is NOT in mergedSet (PR open, closed-without-merge, or no PR at
// all) must be preserved by the merged path — workers in flight must not
// have their worktree pulled.
func TestCleanupMergedPerRun_PreservesUnmergedWorktree(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 1)
	require.NoError(t, err)
	gitInWt(t, wtPath, "checkout", "-b", "fix/CW-test-open")
	require.NoError(t, os.WriteFile(filepath.Join(wtPath, "code.go"), []byte("package x"), 0644))
	gitInWt(t, wtPath, "add", "code.go")
	gitInWt(t, wtPath, "commit", "-m", "agent work")

	// mergedSet does NOT include this branch.
	mergedSet := worktree.MergedSet{"some/other-branch": true}
	removed, err := worktree.CleanupMergedPerRun(repoRoot, wtPath, mergedSet)
	require.NoError(t, err)
	assert.False(t, removed, "worktree on un-merged branch must be preserved")

	_, statErr := os.Stat(wtPath)
	require.NoError(t, statErr, "worktree dir must still exist")
}

// TestCleanupMergedPerRun_NilSetCollapsesToPlainCleanup: gh-unavailable
// flows (mergedSet=nil) must collapse to the existing CleanupPerRun
// behaviour — clean worktrees still removed, dirty/ahead worktrees still
// preserved.
func TestCleanupMergedPerRun_NilSetCollapsesToPlainCleanup(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	// Clean worktree: nil set → still removed.
	wtClean, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 1)
	require.NoError(t, err)
	removed, err := worktree.CleanupMergedPerRun(repoRoot, wtClean, nil)
	require.NoError(t, err)
	assert.True(t, removed, "clean worktree must be removed even with nil mergedSet")

	// Dirty worktree: nil set → preserved.
	wtDirty, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 2)
	require.NoError(t, err)
	gitInWt(t, wtDirty, "checkout", "-b", "feat/CW-x")
	require.NoError(t, os.WriteFile(filepath.Join(wtDirty, "f.go"), []byte("package x"), 0644))
	gitInWt(t, wtDirty, "add", "f.go")
	gitInWt(t, wtDirty, "commit", "-m", "x")
	removed, err = worktree.CleanupMergedPerRun(repoRoot, wtDirty, nil)
	require.NoError(t, err)
	assert.False(t, removed, "ahead-of-main worktree must be preserved with nil mergedSet")
}

// TestSweepMergedPerRun_ForceRemovesMergedWorktreesOnly: the startup
// sweep targets per-run worktrees whose branch is merged; un-merged and
// detached siblings are left alone.
func TestSweepMergedPerRun_ForceRemovesMergedWorktreesOnly(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	// Three worktrees: one on a merged branch, one on an unmerged branch,
	// one detached (the SetupPerRun default — no branch yet).
	wtMerged, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 100)
	require.NoError(t, err)
	gitInWt(t, wtMerged, "checkout", "-b", "fix/CW-merged")
	require.NoError(t, os.WriteFile(filepath.Join(wtMerged, "a.go"), []byte("package x"), 0644))
	gitInWt(t, wtMerged, "add", "a.go")
	gitInWt(t, wtMerged, "commit", "-m", "merged work")

	wtOpen, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 101)
	require.NoError(t, err)
	gitInWt(t, wtOpen, "checkout", "-b", "fix/CW-open")
	require.NoError(t, os.WriteFile(filepath.Join(wtOpen, "b.go"), []byte("package x"), 0644))
	gitInWt(t, wtOpen, "add", "b.go")
	gitInWt(t, wtOpen, "commit", "-m", "still open")

	wtDetached, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 102)
	require.NoError(t, err)

	mergedSet := worktree.MergedSet{"fix/CW-merged": true}
	removed, errs := worktree.SweepMergedPerRun(repoRoot, root, mergedSet)
	require.Empty(t, errs)
	require.Len(t, removed, 1)
	assert.Equal(t, wtMerged, removed[0])

	_, statErr := os.Stat(wtMerged)
	assert.True(t, os.IsNotExist(statErr))
	_, statErr = os.Stat(wtOpen)
	assert.NoError(t, statErr, "un-merged worktree must survive")
	_, statErr = os.Stat(wtDetached)
	assert.NoError(t, statErr, "detached worktree must survive")
}

// TestSweepMergedPerRun_EmptySetIsNoop pins the gh-unavailable contract:
// when the merged-set lookup fails (returning an empty set), the sweep
// must be a no-op — no worktrees are touched.
func TestSweepMergedPerRun_EmptySetIsNoop(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 1)
	require.NoError(t, err)
	gitInWt(t, wtPath, "checkout", "-b", "fix/CW-x")

	removed, errs := worktree.SweepMergedPerRun(repoRoot, root, worktree.MergedSet{})
	assert.Empty(t, removed)
	assert.Empty(t, errs)

	_, statErr := os.Stat(wtPath)
	assert.NoError(t, statErr, "worktree must survive when mergedSet is empty")
}

// TestSweepMergedBranches_DeletesMergedNonWorktreeBranches: the canonical
// post-merge "branch leak" — a branch whose PR shipped, no worktree holds
// it, and which matches an allowed prefix — gets deleted.
func TestSweepMergedBranches_DeletesMergedNonWorktreeBranches(t *testing.T) {
	repoRoot := setupOriginAndClone(t)

	// Create three local branches:
	//   fix/CW-merged-1  → in mergedSet, no worktree → DELETED
	//   feat/CW-open-1   → not in mergedSet → kept
	//   chore/CW-merged-2 → in mergedSet, no worktree → DELETED
	for _, br := range []string{"fix/CW-merged-1", "feat/CW-open-1", "chore/CW-merged-2"} {
		gitInWt(t, repoRoot, "branch", br)
	}

	mergedSet := worktree.MergedSet{
		"fix/CW-merged-1":   true,
		"chore/CW-merged-2": true,
	}
	deleted, errs := worktree.SweepMergedBranches(repoRoot, nil, mergedSet)
	require.Empty(t, errs)
	assert.ElementsMatch(t, []string{"fix/CW-merged-1", "chore/CW-merged-2"}, deleted)

	// Verify state — only the un-merged branch remains.
	out, err := gitOut(repoRoot, "branch", "--list")
	require.NoError(t, err)
	assert.Contains(t, out, "feat/CW-open-1")
	assert.NotContains(t, out, "fix/CW-merged-1")
	assert.NotContains(t, out, "chore/CW-merged-2")
}

// TestSweepMergedBranches_SkipsWorktreeHeldBranches: even when a branch is
// in mergedSet, it must NOT be deleted while a worktree holds it. The
// worktree sweep is the only path that handles those — pulling the branch
// out from under a worktree leaves the worktree in an undefined state.
func TestSweepMergedBranches_SkipsWorktreeHeldBranches(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	// Branch that is held by a worktree.
	wtPath, err := worktree.SetupPerRun(worktree.PerRunOptions{Root: root}, repoRoot, 1)
	require.NoError(t, err)
	gitInWt(t, wtPath, "checkout", "-b", "fix/CW-held")

	// Branch with no worktree.
	gitInWt(t, repoRoot, "branch", "fix/CW-bare")

	mergedSet := worktree.MergedSet{"fix/CW-held": true, "fix/CW-bare": true}
	deleted, errs := worktree.SweepMergedBranches(repoRoot, nil, mergedSet)
	require.Empty(t, errs)
	assert.Equal(t, []string{"fix/CW-bare"}, deleted,
		"worktree-held branch must NOT be deleted by the branch sweep")

	// Held branch still exists (the worktree still uses it).
	out, err := gitOut(repoRoot, "branch", "--list")
	require.NoError(t, err)
	assert.Contains(t, out, "fix/CW-held")
}

// TestSweepMergedBranches_IgnoresPrefixesOutsideAllowlist: a branch with a
// merged PR but with a name that doesn't match any allowlisted prefix
// (e.g., a hand-curated `wip/...`) is left alone — the prefix filter is
// the safety belt against deleting an operator's scratch branch.
func TestSweepMergedBranches_IgnoresPrefixesOutsideAllowlist(t *testing.T) {
	repoRoot := setupOriginAndClone(t)

	gitInWt(t, repoRoot, "branch", "wip/operator-scratch")
	gitInWt(t, repoRoot, "branch", "fix/CW-allowed")

	mergedSet := worktree.MergedSet{
		"wip/operator-scratch": true,
		"fix/CW-allowed":       true,
	}
	deleted, errs := worktree.SweepMergedBranches(repoRoot, nil, mergedSet)
	require.Empty(t, errs)
	assert.Equal(t, []string{"fix/CW-allowed"}, deleted)

	out, err := gitOut(repoRoot, "branch", "--list")
	require.NoError(t, err)
	assert.Contains(t, out, "wip/operator-scratch")
}

// TestSweepMergedBranches_NeverDeletesMain pins the main-protection
// belt-and-suspenders even if main somehow ended up in mergedSet.
func TestSweepMergedBranches_NeverDeletesMain(t *testing.T) {
	repoRoot := setupOriginAndClone(t)

	mergedSet := worktree.MergedSet{"main": true}
	deleted, errs := worktree.SweepMergedBranches(repoRoot, []string{""}, mergedSet)
	require.Empty(t, errs)
	assert.Empty(t, deleted, "main must never be deleted")
}

// TestMergedHeadRefs_NoGhBinaryReturnsEmptySet: the best-effort contract —
// no `gh` on PATH yields an empty set and no error, so callers fall
// through cleanly without surfacing a noisy log on every dispatch.
func TestMergedHeadRefs_NoGhBinaryReturnsEmptySet(t *testing.T) {
	t.Setenv("PATH", "/nonexistent-path-for-gh-shim")
	set, err := worktree.MergedHeadRefs(context.Background(), t.TempDir(), 10)
	require.NoError(t, err)
	assert.Empty(t, set)
}

// gitOut runs a git command in dir and returns combined output. The
// existing `gitInWt` helper in perrun_test.go is require.NoError-heavy
// for setup; gitOut is the listing-and-assertion variant that lets a test
// inspect output (e.g. confirming a branch was deleted) without failing on
// non-zero git exits.
func gitOut(dir string, args ...string) (string, error) {
	cmd := osexec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
