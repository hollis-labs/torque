package worktree_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hollis-labs/torque/internal/worktree"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindRepoRootRejectsEmptyGitDirectory(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	child := filepath.Join(root, "child")
	require.NoError(t, os.Mkdir(child, 0o755))
	_, err := worktree.FindRepoRoot(child)
	require.Error(t, err, "an empty .git directory must not make its children repositories")

	// An invalid nested marker must not hide a genuine repository above it.
	gitInWt(t, root, "init", "-b", "main")
	require.NoError(t, os.Mkdir(filepath.Join(child, ".git"), 0o755))
	got, err := worktree.FindRepoRoot(child)
	require.NoError(t, err)
	assert.Equal(t, root, got)
}

func TestFindRepoRootFindsInitializedRepository(t *testing.T) {
	root := t.TempDir()
	gitInWt(t, root, "init", "-b", "main")
	child := filepath.Join(root, "src", "nested")
	require.NoError(t, os.MkdirAll(child, 0o755))

	for _, start := range []string{root, child} {
		got, err := worktree.FindRepoRoot(start)
		require.NoError(t, err)
		assert.Equal(t, root, got)
	}
}

func TestFindRepoRootFindsLinkedWorktree(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.Mkdir(root, 0o755))
	gitInWt(t, root, "init", "-b", "main")
	gitInWt(t, root, "commit", "--allow-empty", "-m", "initial")
	checkout := filepath.Join(filepath.Dir(root), "checkout")
	gitInWt(t, root, "worktree", "add", "--detach", checkout, "HEAD")
	info, err := os.Stat(filepath.Join(checkout, ".git"))
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular(), "linked worktrees use a gitdir pointer file")
	child := filepath.Join(checkout, "nested")
	require.NoError(t, os.Mkdir(child, 0o755))
	got, err := worktree.FindRepoRoot(child)
	require.NoError(t, err)
	assert.Equal(t, checkout, got)
}

func TestFindRepoRootResolvesRelativeGitdirPointer(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "checkout")
	gitDir := filepath.Join(parent, "gitdir")
	require.NoError(t, os.Mkdir(root, 0o755))
	gitInWt(t, root, "init", "--separate-git-dir", gitDir, "-b", "main")
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: ../gitdir\n"), 0o644))
	got, err := worktree.FindRepoRoot(root)
	require.NoError(t, err)
	assert.Equal(t, root, got)
}

func TestFindRepoRootSkipsInvalidGitdirPointers(t *testing.T) {
	root := t.TempDir()
	gitInWt(t, root, "init", "-b", "main")
	require.NoError(t, os.Mkdir(filepath.Join(root, "empty-dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "regular-file"), nil, 0o644))

	for name, contents := range map[string]string{
		"empty":          "",
		"malformed":      "not a gitdir pointer\n",
		"empty target":   "gitdir: \n",
		"missing target": "gitdir: ../missing\n",
		"non-git target": "gitdir: ../empty-dir\n",
		"file target":    "gitdir: ../regular-file\n",
	} {
		t.Run(name, func(t *testing.T) {
			child := filepath.Join(root, name)
			require.NoError(t, os.Mkdir(child, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(child, ".git"), []byte(contents), 0o644))
			got, err := worktree.FindRepoRoot(child)
			require.NoError(t, err)
			assert.Equal(t, root, got, "invalid pointers must not stop the upward walk")
		})
	}
}
