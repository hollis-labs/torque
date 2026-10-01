package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtectionEnabled(t *testing.T) {
	for v, want := range map[string]bool{"": true, "1": true, "on": true, "0": false, "false": false, "OFF": false, "no": false} {
		t.Setenv(ProtectEnv, v)
		assert.Equal(t, want, ProtectionEnabled(), "%s=%q", ProtectEnv, v)
	}
}

// Only existing directories, by their real path, deduplicated, with a
// directory inside another kept one dropped: go-sandbox refuses files,
// missing paths and symlinked entries.
func TestResolveProtectedPaths(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	data := filepath.Join(root, "data")
	inner := filepath.Join(data, "workspaces")
	state := filepath.Join(root, "state")
	require.NoError(t, os.MkdirAll(inner, 0o700))
	require.NoError(t, os.MkdirAll(state, 0o700))
	link := filepath.Join(root, "state-link")
	require.NoError(t, os.Symlink(state, link))
	file := filepath.Join(root, "main.db")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	got := ResolveProtectedPaths([]string{inner, data, link, state, file, filepath.Join(root, "missing"), "relative/dir", ""})
	assert.Equal(t, []string{data, state}, got)
	assert.Empty(t, ResolveProtectedPaths(nil))
}
