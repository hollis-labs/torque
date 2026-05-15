package worktree_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/worktree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpecResolveSharedMode: a shared-mode spec resolves work_root to repo_root
// and creates no worktree.
func TestSpecResolveSharedMode(t *testing.T) {
	repoRoot := setupOriginAndClone(t)

	spec := worktree.Spec{Mode: worktree.ModeShared}
	workRoot, wtPath, err := spec.Resolve(repoRoot, 1)
	require.NoError(t, err)

	assert.Equal(t, repoRoot, workRoot, "shared mode: work_root == repo_root")
	assert.Empty(t, wtPath, "shared mode: no worktree created")
	assert.False(t, spec.WorktreeEnabled())
}

// TestSpecResolveZeroValueIsShared: the zero-value Spec behaves as shared mode.
func TestSpecResolveZeroValueIsShared(t *testing.T) {
	repoRoot := setupOriginAndClone(t)

	var spec worktree.Spec
	workRoot, wtPath, err := spec.Resolve(repoRoot, 1)
	require.NoError(t, err)

	assert.Equal(t, repoRoot, workRoot)
	assert.Empty(t, wtPath)
}

// TestSpecResolveWorktreeMode: a worktree-mode spec resolves work_root to a
// fresh per-run worktree checked out at origin/main.
func TestSpecResolveWorktreeMode(t *testing.T) {
	repoRoot := setupOriginAndClone(t)
	root := filepath.Join(t.TempDir(), "wt-root")

	spec := worktree.Spec{Mode: worktree.ModeWorktree, Root: root}
	require.True(t, spec.WorktreeEnabled())

	workRoot, wtPath, err := spec.Resolve(repoRoot, 99)
	require.NoError(t, err)

	expected := filepath.Join(root, "run-99")
	assert.Equal(t, expected, workRoot, "worktree mode: work_root is the per-run worktree")
	assert.Equal(t, expected, wtPath, "worktree mode: wtPath is the cleanup handle")

	_, statErr := os.Stat(workRoot)
	require.NoError(t, statErr, "worktree dir should exist")

	// Resolve -> CleanupPerRun round-trip: a clean worktree is removed.
	removed, err := worktree.CleanupPerRun(repoRoot, wtPath)
	require.NoError(t, err)
	assert.True(t, removed)
}

// TestSpecResolveWorktreeModeFallsBackOnGitFailure: when the repo isn't a git
// repo, Resolve returns the error AND repoRoot so callers can fall back.
func TestSpecResolveWorktreeModeFallsBackOnGitFailure(t *testing.T) {
	notARepo := t.TempDir()

	spec := worktree.Spec{Mode: worktree.ModeWorktree, Root: t.TempDir()}
	workRoot, wtPath, err := spec.Resolve(notARepo, 1)

	assert.Error(t, err, "non-repo should surface an error")
	assert.Equal(t, notARepo, workRoot, "on failure work_root falls back to repo_root")
	assert.Empty(t, wtPath, "on failure no cleanup handle is produced")
}

// TestSpecPerRunOptionsProjection: PerRunOptions mirrors the spec fields.
func TestSpecPerRunOptionsProjection(t *testing.T) {
	spec := worktree.Spec{Mode: worktree.ModeWorktree, Root: "/tmp/wt", KeepDays: 5}
	opts := spec.PerRunOptions()

	assert.True(t, opts.Enabled)
	assert.Equal(t, "/tmp/wt", opts.Root)
	assert.Equal(t, 5, opts.KeepDays)

	shared := worktree.Spec{Mode: worktree.ModeShared}
	assert.False(t, shared.PerRunOptions().Enabled)
}

// TestSpecFromEnv: the TORQUE_WORKTREE_PER_RUN env var still drives the spec
// as a default/fallback.
func TestSpecFromEnv(t *testing.T) {
	t.Setenv("TORQUE_WORKTREE_PER_RUN", "")
	t.Setenv("TORQUE_WORKTREE_ROOT", "")
	assert.Equal(t, worktree.ModeShared, worktree.SpecFromEnv().Mode)

	t.Setenv("TORQUE_WORKTREE_PER_RUN", "1")
	t.Setenv("TORQUE_WORKTREE_ROOT", "/custom/wt")
	spec := worktree.SpecFromEnv()
	assert.Equal(t, worktree.ModeWorktree, spec.Mode)
	assert.True(t, spec.WorktreeEnabled())
	assert.Equal(t, "/custom/wt", spec.Root)

	t.Setenv("TORQUE_WORKTREE_PER_RUN", "true")
	assert.Equal(t, worktree.ModeWorktree, worktree.SpecFromEnv().Mode)
}

// TestSpecFromEnvKeepDays: SpecFromEnv populates Spec.KeepDays from
// TORQUE_WORKTREE_KEEP_DAYS, defaulting to 7 (matching config.Load's
// envInt("TORQUE_WORKTREE_KEEP_DAYS", 7)) when unset or unparseable.
func TestSpecFromEnvKeepDays(t *testing.T) {
	t.Setenv("TORQUE_WORKTREE_PER_RUN", "")
	t.Setenv("TORQUE_WORKTREE_ROOT", "")

	// Unset → default 7.
	t.Setenv("TORQUE_WORKTREE_KEEP_DAYS", "")
	assert.Equal(t, 7, worktree.SpecFromEnv().KeepDays)

	// Explicit value parsed.
	t.Setenv("TORQUE_WORKTREE_KEEP_DAYS", "21")
	assert.Equal(t, 21, worktree.SpecFromEnv().KeepDays)

	// Zero is honored (keep forever).
	t.Setenv("TORQUE_WORKTREE_KEEP_DAYS", "0")
	assert.Equal(t, 0, worktree.SpecFromEnv().KeepDays)

	// Unparseable → default 7.
	t.Setenv("TORQUE_WORKTREE_KEEP_DAYS", "not-a-number")
	assert.Equal(t, 7, worktree.SpecFromEnv().KeepDays)
}
